// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cache

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Status is what a cache did with one response.
type Status string

// The statuses a cache can report.
const (
	Hit     Status = "hit"     // served from a cache
	Miss    Status = "miss"    // cacheable yet fetched from the origin this time
	Bypass  Status = "bypass"  // the cache was told to skip this request (cookie, rule, no-cache)
	Expired Status = "expired" // the cached copy was too old and was refreshed
	Stale   Status = "stale"   // an old copy was served while the cache refreshes it
	Dynamic Status = "dynamic" // the cache doesn't consider this content cacheable (Cloudflare HTML)
	Unknown Status = "unknown" // no cache said anything about this response
)

// rank orders statuses when several layers disagree and none was a hit:
// the most informative answer wins.
func (s Status) rank() int {
	return slices.Index([]Status{Hit, Stale, Expired, Miss, Bypass, Dynamic, Unknown}, s)
}

// Kinds of cache layer, for deciding how loud "never cached" should be.
const (
	CDN    = "cdn"    // Cloudflare, CloudFront, Fastly…: often deliberately not caching HTML
	Server = "server" // Varnish, nginx, LiteSpeed, host caches: there to cache pages
	Plugin = "plugin" // a WordPress page cache plugin
)

// Signal is one header or page comment that says something about caching.
type Signal struct {
	Layer  string // Cloudflare, Varnish, nginx, WP Rocket…
	Kind   string // cdn, server or plugin
	Header string // where it came from: "cf-cache-status: HIT", "<!-- WP Rocket … -->"
	Status Status // Unknown when the signal only names the layer
}

// Reading is what the response headers and HTML say about caching.
type Reading struct {
	Status  Status
	Layer   string   // the layer that decided Status, or the first one seen
	Header  string   // the evidence for Status
	Layers  []string // every cache layer seen, edge first
	Signals []Signal
	// Age is how old the served copy is in seconds: the Age header, or
	// the time since a cache plugin stamped the HTML, whichever is older.
	// -1 when unknown.
	Age int
	// Stamp is when a cache plugin says it generated the HTML (zero when
	// there's no stamp). Local is true when the stamp is in the site's
	// local time zone rather than UTC.
	Stamp time.Time
	Local bool
	// HeaderAge is the Age header alone, -1 when absent.
	HeaderAge int
	Control   Control
	Vary      string
}

// Kinds returns the kinds of layer seen (cdn, server, plugin).
func (r Reading) Kinds() []string {
	var out []string
	for _, s := range r.Signals {
		if s.Kind != "" && !slices.Contains(out, s.Kind) {
			out = append(out, s.Kind)
		}
	}
	return out
}

// Control is the parsed Cache-Control (and Expires) of a response.
type Control struct {
	Raw                        string
	Public, Private            bool
	NoStore, NoCache           bool
	MaxAge, SMaxAge            int // -1 when absent
	ExpiresPast, ExpiresFuture bool
	seen                       bool
}

// Shared reports whether a shared cache (CDN, proxy) may store the
// response: public, or a max-age/s-maxage without private/no-store.
func (c Control) Shared() bool {
	if c.Private || c.NoStore || c.NoCache {
		return false
	}
	return c.Public || c.SMaxAge > 0 || c.MaxAge > 0 || (!c.seen && c.ExpiresFuture)
}

func parseControl(h http.Header) Control {
	c := Control{Raw: h.Get("Cache-Control"), MaxAge: -1, SMaxAge: -1}
	for d := range strings.SplitSeq(strings.ToLower(c.Raw), ",") {
		name, val, _ := strings.Cut(strings.TrimSpace(d), "=")
		val = strings.Trim(val, `"`)
		switch name {
		case "":
			continue
		case "public":
			c.Public = true
		case "private":
			c.Private = true
		case "no-store":
			c.NoStore = true
		case "no-cache":
			c.NoCache = true
		case "max-age":
			c.MaxAge, _ = strconv.Atoi(val)
		case "s-maxage":
			c.SMaxAge, _ = strconv.Atoi(val)
		}
		c.seen = true
	}
	if e := h.Get("Expires"); e != "" {
		t, err := http.ParseTime(e)
		now := responseTime(h)
		switch {
		case err != nil || !t.After(now): // "0", "-1" and past dates all mean expired
			c.ExpiresPast = true
		default:
			c.ExpiresFuture = true
		}
	}
	return c
}

// headerReaders map a response header to the layer that sends it.
var headerReaders = []struct {
	name, layer, kind string
}{
	{"cf-cache-status", "Cloudflare", CDN},
	{"x-qc-cache", "QUIC.cloud", CDN},
	{"cdn-cache", "Bunny CDN", CDN},
	{"x-sucuri-cache", "Sucuri", CDN},
	{"x-kinsta-cache", "Kinsta", Server},
	{"x-wpe-cache", "WP Engine", Server},
	{"x-ac", "WordPress.com", Server},
	{"x-hcdn-cache-status", "Hostinger", Server},
	{"x-litespeed-cache", "LiteSpeed", Server},
	{"x-proxy-cache", "nginx", Server},
	{"x-nginx-cache", "nginx", Server},
	{"x-fastcgi-cache", "nginx", Server},
	{"x-cache-status", "nginx", Server},
	{"x-rocket-nginx-serving-static", "WP Rocket", Plugin},
	{"x-wp-super-cache", "WP Super Cache", Plugin},
	{"wp-super-cache", "WP Super Cache", Plugin},
}

// Read classifies a response from its headers and, when given, its HTML.
func Read(h http.Header, body string) Reading {
	r := Reading{Age: -1, HeaderAge: -1, Control: parseControl(h), Vary: h.Get("Vary")}
	add := func(s Signal) { r.Signals = append(r.Signals, s) }

	for _, hr := range headerReaders {
		if v := h.Get(hr.name); v != "" {
			st := token(v)
			if hr.name == "wp-super-cache" || hr.name == "x-wp-super-cache" {
				if strings.Contains(strings.ToLower(v), "served") { // "Served supercache file from PHP"
					st = Hit
				}
			}
			add(Signal{Layer: hr.layer, Kind: hr.kind, Header: hr.name + ": " + v, Status: st})
		}
	}
	if v := strings.ToLower(h.Get("X-Litespeed-Cache-Control")); strings.Contains(v, "no-cache") && h.Get("X-Litespeed-Cache") == "" {
		add(Signal{Layer: "LiteSpeed", Kind: Server, Header: "x-litespeed-cache-control: " + v, Status: Bypass})
	}

	// X-Cache is sent by Varnish, CloudFront, Fastly, Squid and many hosts;
	// the neighboring headers say which.
	if v := h.Get("X-Cache"); v != "" {
		layer, kind := "proxy", Server
		lv := strings.ToLower(v)
		switch {
		case strings.Contains(lv, "cloudfront") || h.Get("X-Amz-Cf-Id") != "":
			layer, kind = "CloudFront", CDN
		case strings.Contains(strings.ToLower(h.Get("X-Served-By")), "cache-"):
			layer, kind = "Fastly", CDN
		case h.Get("X-Varnish") != "" || strings.Contains(strings.ToLower(h.Get("Via")), "varnish"):
			layer = "Varnish"
		case h.Get("X-Pantheon-Styx-Hostname") != "":
			layer = "Pantheon"
		}
		add(Signal{Layer: layer, Kind: kind, Header: "x-cache: " + v, Status: token(v)})
	}
	if v := h.Get("X-Varnish"); v != "" && h.Get("X-Cache") == "" {
		// One transaction ID is a miss; two (this request and the one that
		// stored the object) is a hit.
		st := Miss
		if len(strings.Fields(v)) >= 2 {
			st = Hit
		}
		add(Signal{Layer: "Varnish", Kind: Server, Header: "x-varnish: " + v, Status: st})
	}

	// Headers that name a layer without saying hit or miss.
	if !r.has("Cloudflare") && (h.Get("Cf-Ray") != "" || strings.EqualFold(h.Get("Server"), "cloudflare")) {
		add(Signal{Layer: "Cloudflare", Kind: CDN, Header: "cf-ray", Status: Unknown})
	}
	if via := strings.ToLower(h.Get("Via")); strings.Contains(via, "varnish") && !r.has("Varnish") {
		add(Signal{Layer: "Varnish", Kind: Server, Header: "via: " + h.Get("Via"), Status: Unknown})
	}
	if sb := strings.ToLower(h.Get("X-Served-By")); strings.Contains(sb, "cache-") && !r.has("Fastly") {
		add(Signal{Layer: "Fastly", Kind: CDN, Header: "x-served-by: " + h.Get("X-Served-By"), Status: Unknown})
	}
	if strings.Contains(strings.ToLower(h.Get("Server")), "litespeed") && !r.has("LiteSpeed") {
		add(Signal{Layer: "LiteSpeed", Kind: Server, Header: "server: " + h.Get("Server"), Status: Unknown})
	}

	if body != "" {
		readBody(&r, h, body)
	}

	if v := strings.TrimSpace(h.Get("Age")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			r.HeaderAge = n
		}
	}
	r.Age = r.HeaderAge
	if !r.Stamp.IsZero() && !r.Local {
		if a := int(responseTime(h).Sub(r.Stamp).Seconds()); a > r.Age {
			r.Age = a
		}
	}

	// Decide: a hit anywhere means visitors got a cached copy.
	r.Status = Unknown
	for _, s := range r.Signals {
		if !slices.Contains(r.Layers, s.Layer) {
			r.Layers = append(r.Layers, s.Layer)
		}
		if s.Status.rank() < r.Status.rank() {
			r.Status, r.Layer, r.Header = s.Status, s.Layer, s.Header
		}
	}
	if r.Status == Unknown && r.HeaderAge > 0 {
		// An Age header without any other signal still means a shared
		// cache served the copy.
		r.Status, r.Header = Hit, "age: "+strconv.Itoa(r.HeaderAge)
		if r.Layer == "" {
			r.Layer = "proxy"
			if len(r.Layers) > 0 {
				r.Layer = r.Layers[0]
			} else {
				r.Layers = []string{"proxy"}
			}
		}
	}
	if r.Layer == "" && len(r.Layers) > 0 {
		r.Layer = r.Layers[0]
	}
	return r
}

func (r *Reading) has(layer string) bool {
	return slices.ContainsFunc(r.Signals, func(s Signal) bool { return s.Layer == layer })
}

// token classifies a cache status value: "HIT", "Hit from cloudfront",
// "TCP_MEM_HIT", "hit,private", Fastly's "MISS, HIT" (any hit along the
// chain means the copy came from a cache).
func token(v string) Status {
	best := Unknown
	for part := range strings.SplitSeq(strings.ToLower(v), ",") {
		p := strings.TrimSpace(part)
		var s Status
		switch {
		case p == "":
			continue
		case strings.Contains(p, "pass"): // PASS, HIT-FOR-PASS, hitpass
			s = Bypass
		case strings.Contains(p, "stale") || strings.HasPrefix(p, "updating"):
			s = Stale
		case strings.Contains(p, "hit") || strings.HasPrefix(p, "revalidated"):
			s = Hit
		case strings.Contains(p, "expired"):
			s = Expired
		case strings.Contains(p, "miss"):
			s = Miss
		case strings.Contains(p, "dynamic"):
			s = Dynamic
		case strings.Contains(p, "no-cache") || p == "skip":
			s = Bypass
		default:
			continue // private, litemage, NONE, a numeric hit counter…
		}
		if s.rank() < best.rank() {
			best = s
		}
	}
	return best
}

// Cache plugin comments. Most stamp the time the HTML was generated in
// the site's local time; WP Rocket uses a Unix time and Cache Enabler
// GMT.
var (
	superCache   = regexp.MustCompile(`Cached page generated by WP-Super-Cache on (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`)
	rocket       = regexp.MustCompile(`(?i)Performance optimized by WP Rocket[^>]*?cached@(\d{9,11})`)
	rocketAny    = regexp.MustCompile(`(?i)optimized by WP Rocket`)
	litespeed    = regexp.MustCompile(`(?i)<!--[^>]*LiteSpeed Cache[^>]*?(?:@|\bon)\s*(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`)
	w3tc         = regexp.MustCompile(`(?i)Served from:[^>]*?@ (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) by W3 Total Cache`)
	w3tcAny      = regexp.MustCompile(`(?i)Performance optimized by W3 Total Cache[^>]*Page Caching using`)
	fastestCache = regexp.MustCompile(`(?i)(WP Fastest Cache|Hummingbird cache) file was created in [\d.]+ seconds, on (\d{2}-\d{2}-\d{2}) @ (\d{1,2}:\d{2})(?:\s*(am|pm))?`)
	cacheEnabler = regexp.MustCompile(`Cache Enabler by KeyCDN @ ([A-Z][a-z]{2}, \d{2} [A-Z][a-z]{2} \d{4} \d{2}:\d{2}:\d{2} GMT)`)
)

// readBody looks for cache plugin comments. A stamp says when the HTML
// was generated, so a stamp older than a couple of seconds means the page
// came out of the plugin's cache.
func readBody(r *Reading, h http.Header, body string) {
	tail := body
	if len(tail) > 4096 { // plugins put their comment at the end
		tail = body[len(body)-4096:]
	}
	local := func(layer, header, stamp string) {
		if t, err := time.Parse(time.DateTime, stamp); err == nil {
			r.Stamp, r.Local = t, true
		}
		r.Signals = append(r.Signals, Signal{Layer: layer, Kind: Plugin, Header: header, Status: Unknown})
	}
	switch {
	case superCache.MatchString(tail):
		m := superCache.FindStringSubmatch(tail)
		local("WP Super Cache", "<!-- Cached page generated by WP-Super-Cache -->", m[1])
	case rocket.MatchString(tail):
		m := rocket.FindStringSubmatch(tail)
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			r.Stamp = time.Unix(n, 0).UTC()
		}
		r.Signals = append(r.Signals, Signal{Layer: "WP Rocket", Kind: Plugin, Header: "<!-- WP Rocket cached@ -->", Status: Unknown})
	case rocketAny.MatchString(tail):
		r.Signals = append(r.Signals, Signal{Layer: "WP Rocket", Kind: Plugin, Header: "<!-- WP Rocket -->", Status: Unknown})
	case litespeed.MatchString(tail):
		local("LiteSpeed", "<!-- LiteSpeed Cache -->", litespeed.FindStringSubmatch(tail)[1])
	case w3tc.MatchString(tail):
		local("W3 Total Cache", "<!-- W3 Total Cache -->", w3tc.FindStringSubmatch(tail)[1])
	case w3tcAny.MatchString(tail):
		r.Signals = append(r.Signals, Signal{Layer: "W3 Total Cache", Kind: Plugin, Header: "<!-- W3 Total Cache -->", Status: Unknown})
	case fastestCache.MatchString(tail):
		m := fastestCache.FindStringSubmatch(tail)
		layer := "WP Fastest Cache"
		if strings.HasPrefix(strings.ToLower(m[1]), "hummingbird") {
			layer = "Hummingbird"
		}
		clock := m[3]
		if m[4] != "" {
			if t, err := time.Parse("3:04pm", clock+strings.ToLower(m[4])); err == nil {
				clock = t.Format("15:04")
			}
		}
		if len(clock) == 4 {
			clock = "0" + clock
		}
		if t, err := time.Parse("02-01-06 15:04", m[2]+" "+clock); err == nil {
			r.Stamp, r.Local = t, true
		}
		r.Signals = append(r.Signals, Signal{Layer: layer, Kind: Plugin, Header: "<!-- " + layer + " -->", Status: Unknown})
	case cacheEnabler.MatchString(tail):
		if t, err := time.Parse(time.RFC1123, cacheEnabler.FindStringSubmatch(tail)[1]); err == nil {
			r.Stamp = t.UTC()
		}
		r.Signals = append(r.Signals, Signal{Layer: "Cache Enabler", Kind: Plugin, Header: "<!-- Cache Enabler -->", Status: Unknown})
	}
	// A plugin stamp more than a few seconds old means the HTML was not
	// generated for this request. Local stamps are only trusted once the
	// site's time zone is known (see Reading.Localize).
	if !r.Stamp.IsZero() && !r.Local {
		promoteStamp(r, h)
	}
}

// Localize applies the site's UTC offset to a local plugin stamp, so its
// age can be trusted, then re-decides the status.
func (r *Reading) Localize(h http.Header, offset time.Duration) {
	if r.Stamp.IsZero() || !r.Local {
		return
	}
	r.Stamp = r.Stamp.Add(-offset)
	r.Local = false
	if a := int(responseTime(h).Sub(r.Stamp).Seconds()); a > r.Age {
		r.Age = a
	}
	promoteStamp(r, h)
	if last := r.Signals[len(r.Signals)-1]; last.Status == Hit && r.Status.rank() > Hit.rank() {
		r.Status, r.Layer, r.Header = Hit, last.Layer, last.Header
	}
}

// staleStamp is how old a plugin stamp must be before it counts as a hit.
const staleStamp = 3 * time.Second

func promoteStamp(r *Reading, h http.Header) {
	if responseTime(h).Sub(r.Stamp) >= staleStamp {
		for i := len(r.Signals) - 1; i >= 0; i-- {
			if r.Signals[i].Kind == Plugin {
				r.Signals[i].Status = Hit
				return
			}
		}
	}
}

// responseTime is the server's clock from the Date header, so ages don't
// depend on the local clock; now when there's no Date.
func responseTime(h http.Header) time.Time {
	if t, err := http.ParseTime(h.Get("Date")); err == nil {
		return t
	}
	return now()
}

// now is replaceable in tests.
var now = time.Now
