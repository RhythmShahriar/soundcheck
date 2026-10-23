// Package cache checks that a site's page cache works and is safe: which
// layer serves pages (Cloudflare, Varnish, nginx, LiteSpeed, a WordPress
// plugin…) and how often, that private pages (cart, checkout, account,
// login) are never cached, that logged-in content never leaks to
// visitors, that cached pages don't carry expired WordPress nonces, and
// that cached copies aren't out of date.
//
// Everything is fetched over plain HTTP (cc.Get: polite, rate-limited,
// no cookies), except the leak test, which logs in through a separate
// browser session.
//
// Facts recorded in model.Page.Cache:
//
//	status            hit|miss|bypass|expired|stale|dynamic|unknown, from the second anonymous fetch
//	first             the same, from the first fetch
//	layer             the cache layer that decided the status (Cloudflare, Varnish, nginx, WP Rocket…)
//	layers            every layer seen, edge first, comma separated
//	header            the evidence: "cf-cache-status: HIT", "<!-- WP Rocket cached@ -->", "age: 120"
//	age               seconds since the cached copy was made, when known
//	cache_control     the Cache-Control header
//	vary              the Vary header
//	ttfb_ms           time to first byte of the second fetch
//	ttfb_cached_ms    TTFB when served from cache (the second fetch, on a hit)
//	ttfb_uncached_ms  TTFB of a render the cache didn't serve (a miss, or the cache-busted fetch)
//	busted            "ignored" when the cache serves the same copy for a ?soundcheck= URL
//	private           "true" for cart, checkout, account and login pages
//
// model.Run.Cache holds the site summary: pages, hits, hit_rate (percent),
// layers, ttfb_cached_ms and ttfb_uncached_ms (medians).
package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Module is the cache check module.
type Module struct {
	// MaxPages caps how many public pages are fetched twice (private
	// pages are always checked); 0 means 40.
	MaxPages int
}

func (Module) Name() string { return "cache" }

const (
	nonceWarn  = 12 * time.Hour // WordPress nonces are valid for 12–24 hours
	nonceError = 24 * time.Hour
	maxBody    = 4 << 20
)

// fetched is one anonymous HTTP fetch.
type fetched struct {
	url    string
	code   int
	header http.Header
	body   string
	ttfb   time.Duration
	at     time.Time
	read   Reading
	err    error
}

func (f *fetched) html() bool {
	ct := strings.ToLower(f.header.Get("Content-Type"))
	return f.err == nil && f.code < 400 && (ct == "" || strings.Contains(ct, "html"))
}

// fetch gets a URL without cookies, measuring time to first byte.
func fetch(ctx context.Context, cc *check.Context, raw string) *fetched {
	f := &fetched{url: raw}
	var start, first atomic.Int64
	trace := &httptrace.ClientTrace{
		GetConn:              func(string) { start.CompareAndSwap(0, time.Now().UnixNano()) },
		GotFirstResponseByte: func() { first.CompareAndSwap(0, time.Now().UnixNano()) },
	}
	resp, err := cc.Get(httptrace.WithClientTrace(ctx, trace), http.MethodGet, raw)
	if err != nil {
		f.err = err
		return f
	}
	defer func() { _ = resp.Body.Close() }()
	f.at = time.Now()
	f.code, f.header = resp.StatusCode, resp.Header
	if s, e := start.Load(), first.Load(); s > 0 && e > s {
		f.ttfb = time.Duration(e - s)
	}
	if f.html() {
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		if err != nil {
			f.err = err
			return f
		}
		f.body = string(b)
	}
	f.read = Read(f.header, f.body)
	return f
}

// result is what the double fetch found for one URL.
type result struct {
	idx     int    // index in Run.Pages; -1 for a probed URL that wasn't visited
	path    string // page path
	private bool
	first   *fetched
	second  *fetched
	busted  *fetched
	facts   map[string]string
	issues  []model.Issue
}

// site holds what's shared across pages during one CheckSite.
type site struct {
	cc        *check.Context
	once      sync.Once
	offset    time.Duration
	hasOffset bool
}

// tzOffset reads the site's UTC offset from the WordPress REST index, so
// plugin stamps in local time can be turned into ages.
func (s *site) tzOffset(ctx context.Context) (time.Duration, bool) {
	s.once.Do(func() {
		for _, p := range []string{"wp-json/", "?rest_route=/"} {
			f := fetchJSON(ctx, s.cc, join(s.cc.SiteRoot(), p))
			if f == nil {
				continue
			}
			var idx struct {
				Offset json.RawMessage `json:"gmt_offset"`
			}
			if json.Unmarshal(f, &idx) != nil || len(idx.Offset) == 0 {
				continue
			}
			h, err := strconv.ParseFloat(strings.Trim(string(idx.Offset), `"`), 64)
			if err == nil && h >= -14 && h <= 14 {
				s.offset, s.hasOffset = time.Duration(h*float64(time.Hour)), true
				return
			}
		}
	})
	return s.offset, s.hasOffset
}

func fetchJSON(ctx context.Context, cc *check.Context, raw string) []byte {
	resp, err := cc.Get(ctx, http.MethodGet, raw)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return b
}

// double fetches a URL twice (the browser visit already warmed it), and,
// when the second fetch was a cache hit, twice more with a cache-busting
// query to compare the cached copy with a fresh render.
func (s *site) double(ctx context.Context, raw string) *result {
	r := &result{idx: -1, facts: map[string]string{}}
	r.first = fetch(ctx, s.cc, raw)
	if !r.first.html() {
		return r
	}
	s.localize(ctx, r.first)
	if r.first.read.Local {
		// A local stamp with no known time zone: equal stamps on two
		// fetches more than a second apart mean the copy was cached.
		_ = ratelimit.Sleep(ctx, time.Until(r.first.at.Add(1100*time.Millisecond)))
	}
	r.second = fetch(ctx, s.cc, raw)
	if !r.second.html() {
		return r
	}
	s.localize(ctx, r.second)
	if a, b := r.first.read, r.second.read; b.Local && !b.Stamp.IsZero() && a.Stamp.Equal(b.Stamp) {
		markPluginHit(&r.second.read)
	}
	if r.second.read.Status == Hit {
		r.busted = fetch(ctx, s.cc, bust(raw))
	}
	return r
}

func (s *site) localize(ctx context.Context, f *fetched) {
	if !f.read.Local || f.read.Stamp.IsZero() {
		return
	}
	if off, ok := s.tzOffset(ctx); ok {
		f.read.Localize(f.header, off)
	}
}

// markPluginHit records that a plugin served the copy from its cache.
func markPluginHit(r *Reading) {
	for i := len(r.Signals) - 1; i >= 0; i-- {
		if r.Signals[i].Kind == Plugin {
			r.Signals[i].Status = Hit
			if r.Status.rank() > Hit.rank() {
				r.Status, r.Layer, r.Header = Hit, r.Signals[i].Layer, r.Signals[i].Header
			}
			return
		}
	}
}

// private pages hold a visitor's own cart, order or account; a cached
// copy shows one visitor's page to the next, or breaks checkout.
var (
	privatePath  = regexp.MustCompile(`(?i)/(cart|basket|checkout|my-account|wp-login\.php)(/|$|\?)`)
	privateClass = []string{"woocommerce-cart", "woocommerce-checkout", "woocommerce-account"}
	wooURL       = regexp.MustCompile(`"(?:cart|checkout|myaccount)_url"\s*:\s*"(https?:[^"]+)"`)
	wpMarkers    = regexp.MustCompile(`(?i)/wp-content/|/wp-includes/|https://api\.w\.org/`)
)

func isPrivate(path, body string) bool {
	if privatePath.MatchString(path) {
		return true
	}
	classes := BodyClasses(body)
	return slices.ContainsFunc(privateClass, func(c string) bool { return slices.Contains(classes, c) })
}

// CheckSite runs the double fetch, private page, expired nonce and stale
// cache checks, then the leak test.
func (m Module) CheckSite(ctx context.Context, cc *check.Context) {
	limit := m.MaxPages
	if limit <= 0 {
		limit = 40
	}
	type target struct {
		idx       int
		url, path string
	}
	var public, private []target
	seenGroup := map[string]bool{}
	var rest []target
	cc.Lock()
	for i, p := range cc.Run.Pages {
		if p.Error != "" || p.Status >= 400 || p.Status == 0 {
			continue
		}
		if ct := p.Headers["content-type"]; ct != "" && !strings.Contains(ct, "html") {
			continue
		}
		t := target{i, pageURL(p), p.Path}
		switch {
		case privatePath.MatchString(p.Path):
			private = append(private, t)
		case !seenGroup[p.Group] || p.Group == "":
			seenGroup[p.Group] = true
			public = append(public, t)
		default:
			rest = append(rest, t)
		}
	}
	cc.Unlock()
	public = append(public, rest...)
	public = public[:min(len(public), limit)]
	todo := append(private, public...)

	s := &site{cc: cc}
	results := make([]*result, len(todo))
	run := func(items []target, out []*result) {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for i, t := range items {
			sem <- struct{}{}
			wg.Go(func() {
				defer func() { <-sem }()
				r := s.double(ctx, t.url)
				r.idx, r.path = t.idx, t.path
				out[i] = r
			})
		}
		wg.Wait()
	}
	run(todo, results)
	if ctx.Err() != nil {
		return
	}

	// Probe private pages the crawl never visits: wp-login.php on
	// WordPress, and WooCommerce's cart/checkout/account URLs from its
	// script settings.
	visited := map[string]bool{}
	wp := false
	var extra []target
	for _, r := range results {
		visited[r.path] = true
	}
	for _, r := range results {
		if r.second == nil {
			continue
		}
		wp = wp || wpMarkers.MatchString(r.second.body)
		for _, m := range wooURL.FindAllStringSubmatch(r.second.body, -1) {
			u, err := url.Parse(strings.ReplaceAll(m[1], `\/`, "/"))
			if err == nil && strings.EqualFold(u.Host, cc.Base.Host) && !visited[u.RequestURI()] {
				visited[u.RequestURI()] = true
				extra = append(extra, target{-1, u.String(), u.RequestURI()})
			}
		}
	}
	if wp {
		login := join(cc.SiteRoot(), "wp-login.php")
		if u, err := url.Parse(login); err == nil && !visited[u.RequestURI()] {
			extra = append(extra, target{-1, login, u.RequestURI()})
		}
	}
	more := make([]*result, len(extra))
	run(extra, more)
	results = append(results, more...)

	for _, r := range results {
		analyze(r)
	}
	s.stale(ctx, results)
	apply(cc, results)
	summarize(cc, results)
	if ctx.Err() == nil {
		leak(ctx, cc, results)
	}
}

// pageURL is the URL to fetch for a visited page: where it ended up when
// that's on the same host.
func pageURL(p model.Page) string {
	if p.FinalURL != "" {
		a, errA := url.Parse(p.FinalURL)
		b, errB := url.Parse(p.URL)
		if errA == nil && errB == nil && a.Host == b.Host {
			return p.FinalURL
		}
	}
	return p.URL
}

// analyze records a page's cache facts and its private page and expired
// nonce issues.
func analyze(r *result) {
	f := r.second
	if f == nil || !f.html() {
		return
	}
	rd := f.read
	r.private = isPrivate(r.path, f.body)
	r.facts["status"] = string(rd.Status)
	r.facts["first"] = string(r.first.read.Status)
	set := func(k, v string) {
		if v != "" {
			r.facts[k] = v
		}
	}
	set("layer", rd.Layer)
	set("layers", strings.Join(rd.Layers, ", "))
	set("header", rd.Header)
	set("cache_control", rd.Control.Raw)
	set("vary", rd.Vary)
	if rd.Age >= 0 && !rd.Local {
		r.facts["age"] = strconv.Itoa(rd.Age)
	}
	r.facts["ttfb_ms"] = ms(f.ttfb)
	if rd.Status == Hit {
		r.facts["ttfb_cached_ms"] = ms(f.ttfb)
	} else {
		r.facts["ttfb_uncached_ms"] = ms(f.ttfb)
	}
	if r.private {
		r.facts["private"] = "true"
		if rd.Status == Hit || (rd.Control.Shared() && rd.HeaderAge > 0) {
			r.issues = append(r.issues, model.Issue{Check: "cache/private-cached", Level: model.Error,
				Title:  "Private page is served from cache",
				Detail: evidence(rd),
				Source: rd.Layer,
				Fix: "Exclude cart, checkout, account and login pages from every cache layer (" + orDefault(strings.Join(rd.Layers, ", "), "page cache") +
					"), and bypass the cache for requests with woocommerce_items_in_cart, wp_woocommerce_session_* or wordpress_logged_in_* cookies. " +
					"WooCommerce marks these pages no-cache; a cache rule is overriding that. Purge the cache afterwards."})
		}
	}
	if rd.Status == Hit {
		age := nonceAge(rd, f.header)
		if ns := Nonces(f.body); len(ns) > 0 && age > nonceWarn {
			level := model.Warning
			if age > nonceError {
				level = model.Error
			}
			r.issues = append(r.issues, model.Issue{Check: "cache/expired-nonce", Level: level,
				Title: "Cached page carries an expired security token",
				Detail: fmt.Sprintf("The cached copy is at least %s old (%s) and carries %s; WordPress rejects nonces after 12–24 hours, so forms, add to cart and AJAX on this copy fail.",
					hours(age), ageSource(rd), strings.Join(nonceKinds(ns), ", ")),
				Source: rd.Layer,
				Fix: "Lower the page cache lifetime below 10 hours (" + orDefault(rd.Layer, "page cache") + "), or load nonces with AJAX after the page loads " +
					"(e.g. your cache plugin's nonce/fragment option), so no visitor gets a page with a token WordPress no longer accepts."})
		}
	}
}

// nonceAge is how old the cached HTML is at least. A plugin stamp in an
// unknown time zone may be off by up to 14 hours, so it's discounted.
func nonceAge(rd Reading, h http.Header) time.Duration {
	age := time.Duration(max(rd.HeaderAge, 0)) * time.Second
	if rd.Local && !rd.Stamp.IsZero() {
		if a := responseTime(h).Sub(rd.Stamp) - 14*time.Hour; a > age {
			age = a
		}
	} else if rd.Age > 0 {
		age = max(age, time.Duration(rd.Age)*time.Second)
	}
	return age
}

func ageSource(rd Reading) string {
	if !rd.Stamp.IsZero() && (rd.HeaderAge < 0 || time.Duration(rd.HeaderAge)*time.Second < nonceWarn) {
		return rd.Layer + " timestamp"
	}
	return "Age header"
}

func evidence(rd Reading) string {
	parts := []string{rd.Header}
	if rd.Control.Raw != "" {
		parts = append(parts, "cache-control: "+rd.Control.Raw)
	}
	if rd.HeaderAge >= 0 {
		parts = append(parts, "age: "+strconv.Itoa(rd.HeaderAge))
	}
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), "; ")
}

// stale compares each cached hit with fresh renders. A difference only
// counts when two fresh renders agree with each other, so rotating
// widgets and random content don't look stale.
func (s *site) stale(ctx context.Context, results []*result) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, r := range results {
		if r.busted == nil || !r.busted.html() {
			continue
		}
		b := r.busted.read
		if b.Status == Hit && b.HeaderAge > 0 {
			r.facts["busted"] = "ignored" // the cache serves the same copy for any query string
			continue
		}
		r.facts["ttfb_uncached_ms"] = ms(r.busted.ttfb)
		cached := words(MainText(r.second.body))
		fresh := words(MainText(r.busted.body))
		gone, added := wordDiff(cached, fresh)
		if len(gone)+len(added) == 0 {
			continue
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			again := fetch(ctx, s.cc, bust(r.second.url))
			if !again.html() {
				return
			}
			if x, y := wordDiff(fresh, words(MainText(again.body))); len(x)+len(y) > 0 {
				return // the page differs on every render; can't tell stale from dynamic
			}
			detail := fmt.Sprintf("The cached copy (%s", r.second.read.Header)
			if r.second.read.Age > 0 && !r.second.read.Local {
				detail += ", " + hours(time.Duration(r.second.read.Age)*time.Second) + " old"
			}
			detail += ") differs from a fresh render"
			if len(gone) > 0 {
				detail += "; only in the cached copy: " + sample(gone)
			}
			if len(added) > 0 {
				detail += "; only in the fresh page: " + sample(added)
			}
			r.issues = append(r.issues, model.Issue{Check: "cache/stale", Level: model.Warning,
				Title: "Cached page is out of date", Detail: detail, Source: r.second.read.Layer,
				Fix: "Purge " + orDefault(r.second.read.Layer, "the page cache") + " when content changes: enable your cache plugin's purge-on-update for related pages " +
					"(home, archives, product and category pages), or connect it to your CDN so purges reach the edge."})
		})
	}
	wg.Wait()
}

// apply writes facts and issues into the run.
func apply(cc *check.Context, results []*result) {
	cc.Lock()
	defer cc.Unlock()
	for _, r := range results {
		if len(r.facts) == 0 {
			continue
		}
		if r.idx >= 0 && r.idx < len(cc.Run.Pages) {
			p := &cc.Run.Pages[r.idx]
			if p.Cache == nil {
				p.Cache = map[string]string{}
			}
			for k, v := range r.facts {
				p.Cache[k] = v
			}
			for _, i := range r.issues {
				check.Page(p, i)
			}
			continue
		}
		for _, i := range r.issues {
			i.Pages = []string{r.path}
			i.Identify()
			cc.Run.Issues = append(cc.Run.Issues, i)
		}
	}
}

// summarize records the site hit rate and reports a page cache that never
// serves pages, or no cache at all.
func summarize(cc *check.Context, results []*result) {
	var pages, hits int
	var cached, uncached []time.Duration
	var layers []string
	kinds := map[string]bool{}
	answers := map[string]int{}
	for _, r := range results {
		if r.second == nil || !r.second.html() {
			continue
		}
		rd := r.second.read
		for _, l := range append(r.first.read.Layers, rd.Layers...) {
			if !slices.Contains(layers, l) {
				layers = append(layers, l)
			}
		}
		for _, k := range rd.Kinds() {
			kinds[k] = true
		}
		if r.private {
			continue
		}
		pages++
		if rd.Status == Hit {
			hits++
			cached = append(cached, r.second.ttfb)
			if r.busted != nil && r.busted.html() && r.facts["busted"] == "" {
				uncached = append(uncached, r.busted.ttfb)
			}
		} else {
			uncached = append(uncached, r.second.ttfb)
			if rd.Header != "" {
				answers[rd.Header]++
			}
		}
	}
	if pages == 0 {
		return
	}
	sum := map[string]string{"pages": strconv.Itoa(pages), "hits": strconv.Itoa(hits), "hit_rate": strconv.Itoa(hits * 100 / pages)}
	if len(layers) > 0 {
		sum["layers"] = strings.Join(layers, ", ")
	}
	if len(cached) > 0 {
		sum["ttfb_cached_ms"] = ms(median(cached))
	}
	if len(uncached) > 0 {
		sum["ttfb_uncached_ms"] = ms(median(uncached))
	}
	cc.Lock()
	cc.Run.Cache = sum
	cc.Unlock()

	switch {
	case len(layers) == 0:
		cc.AddIssue(model.Issue{Check: "cache/none", Level: model.Notice, Title: "No page cache detected",
			Detail: fmt.Sprintf("None of %d pages fetched twice carried a cache header or a cache plugin comment.", pages),
			Fix: "A page cache serves pages without running PHP for every visitor. On WordPress, use your host's cache, or a plugin such as " +
				"LiteSpeed Cache, WP Super Cache or WP Rocket; elsewhere, a reverse proxy (Varnish, nginx fastcgi_cache) or a CDN rule for HTML."})
	case hits == 0:
		level, fix := model.Notice, "Your CDN passes HTML through. If you want it to cache pages, add a cache rule for HTML that bypasses requests with login, cart and session cookies (on Cloudflare: a Cache Rule or APO for WordPress)."
		if kinds[Server] || kinds[Plugin] {
			level = model.Warning
			fix = "The page cache is installed but never answers with a cached page. Check that it's enabled, that its rules don't bypass every request " +
				"(cookies, query strings, a Cache-Control: no-cache/private from a plugin or theme), and that nothing purges it on every request."
		}
		var seen []string
		for h, n := range answers {
			seen = append(seen, fmt.Sprintf("%s (%d)", h, n))
		}
		slices.Sort(seen)
		cc.AddIssue(model.Issue{Check: "cache/never-cached", Level: level, Title: "Pages are never served from the page cache",
			Detail: fmt.Sprintf("%s present, but 0 of %d pages came from the cache on a second fetch. Seen: %s",
				strings.Join(layers, ", "), pages, orDefault(strings.Join(seen, ", "), "no status")),
			Source: strings.Join(layers, ", "), Fix: fix})
	}
}

// Compare reports a page cache that served pages in the snapshot and
// serves none now. Private pages that became cached are new issues, which
// the generic diff reports as BROKE.
func (Module) Compare(_ context.Context, cmp *check.CompareContext) []model.Change {
	var before, after, n int
	var pages []string
	for _, pair := range cmp.Pairs() {
		b, a := pair[0], pair[1]
		if b.Cache["status"] == "" || a.Cache["status"] == "" || b.Cache["private"] == "true" || a.Cache["private"] == "true" {
			continue
		}
		n++
		if b.Cache["status"] == string(Hit) {
			before++
			if a.Cache["status"] != string(Hit) {
				pages = append(pages, a.Path)
			}
		}
		if a.Cache["status"] == string(Hit) {
			after++
		}
	}
	if before == 0 || after > 0 {
		return nil
	}
	slices.Sort(pages)
	layer := ""
	for _, p := range cmp.Before.Pages {
		if p.Cache["status"] == string(Hit) && p.Cache["layer"] != "" {
			layer = p.Cache["layer"]
			break
		}
	}
	return []model.Change{{Class: model.Changed, Check: "cache/stopped", Title: "Page cache stopped working", Source: layer,
		Detail: "Pages that came from the cache in the snapshot are now rendered on every request.",
		Fix:    "Check the cache plugin or server cache is still enabled, and that the change didn't add a cookie, a no-cache header or a rule that bypasses it.",
		Pages:  pages, Before: fmt.Sprintf("%d of %d pages cached", before, n), After: fmt.Sprintf("0 of %d pages cached", n), Score: 100}}
}

// bust adds a random ?soundcheck= parameter so caches treat the URL as
// new and render it fresh.
func bust(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Set("soundcheck", randomHex(4))
	u.RawQuery = q.Encode()
	return u.String()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// join resolves a path against the site base, keeping a WordPress install
// in a subfolder (https://example.com/blog/).
func join(base *url.URL, p string) string {
	b := *base
	if !strings.HasSuffix(b.Path, "/") {
		b.Path += "/"
	}
	ref, err := url.Parse(p)
	if err != nil {
		return b.String()
	}
	return b.ResolveReference(ref).String()
}

func ms(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }

func median(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[len(s)/2]
}

func hours(d time.Duration) string {
	if d >= 48*time.Hour {
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}

func sample(ws []string) string {
	if len(ws) > 8 {
		return `"` + strings.Join(ws[:8], " ") + `" …`
	}
	return `"` + strings.Join(ws, " ") + `"`
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// FilterChanges drops "never served from cache" and "no page cache" when
// the comparison already says the page cache stopped working: one cause,
// one line.
func (Module) FilterChanges(changes []model.Change) []model.Change {
	if !slices.ContainsFunc(changes, func(c model.Change) bool { return c.Check == "cache/stopped" }) {
		return changes
	}
	return slices.DeleteFunc(changes, func(c model.Change) bool {
		return c.Class == model.New && (c.Check == "cache/never-cached" || c.Check == "cache/none")
	})
}
