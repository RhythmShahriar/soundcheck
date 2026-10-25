// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package speed

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Limits for site-wide suggestions.
const (
	oversizeRatio  = 2.0       // natural width more than 2× the displayed width × DPR (leaves room for retina screens)
	oversizeWaste  = 20 << 10  // and at least this many bytes wasted
	minLazyArea    = 50 * 50   // smaller images (icons, trackers) don't matter
	unusedShare    = 0.5       // more than half of a file never used
	minUnusedBytes = 2 << 10   // tiny files aren't worth the work
	minCompress    = 1 << 10   // text over 1 KB should be compressed
	shortCache     = 7 * 86400 // max-age below a week is short for static files
	heavyBytes     = 100 << 10 // per page, for third parties, plugins and themes
	heavyRequests  = 10
	heavyBlocking  = 100.0     // ms of render-blocking per page
	veryHeavy      = 500 << 10 // warning instead of notice
	veryBlocking   = 300.0
	topN           = 5 // files listed in a suggestion's detail
)

// CheckSite adds suggestions grouped by cause, each counted across pages:
// one issue for all oversized images, one per heavy plugin and so on.
// Titles are fixed per cause (or per plugin/host) so a comparison sees
// the same suggestion in both runs; the numbers go in Detail.
func (m *Module) CheckSite(_ context.Context, cc *check.Context) {
	f := m.pageFacts(cc.Run)
	cc.Lock()
	pages := make([]model.Page, 0, len(cc.Run.Pages))
	for _, p := range cc.Run.Pages {
		if p.Error == "" && p.Status < 400 {
			pages = append(pages, p)
		}
	}
	cc.Unlock()
	if len(pages) == 0 {
		return
	}
	s := site{base: cc.Base, pages: pages, facts: f}
	for _, i := range s.suggestions() {
		cc.AddIssue(i)
	}
}

// site holds one run's pages for the suggestion rules.
type site struct {
	base  *url.URL
	pages []model.Page
	facts map[string]*facts
}

func (s site) suggestions() []model.Issue {
	var out []model.Issue
	for _, rule := range []func() []model.Issue{
		s.oversizedImages, s.eagerImages, s.renderBlocking, s.unused, s.compression,
		s.httpVersion, s.caching, s.thirdParties, s.plugins,
	} {
		out = append(out, rule()...)
	}
	return out
}

// tally collects one file (or image) seen across pages.
type tally struct {
	key    string
	pages  []string
	bytes  int64   // per load
	extra  float64 // rule-specific: wasted bytes, unused share, blocking ms (summed per page)
	detail string
}

type tallies map[string]*tally

func (t tallies) add(key, page string, bytes int64, extra float64, detail string) {
	x := t[key]
	if x == nil {
		x = &tally{key: key, detail: detail}
		t[key] = x
	}
	if !slices.Contains(x.pages, page) {
		x.pages = append(x.pages, page)
	}
	x.bytes = max(x.bytes, bytes)
	x.extra += extra
	if detail != "" {
		x.detail = detail
	}
}

// sorted returns tallies ordered by score, biggest first.
func (t tallies) sorted(score func(*tally) float64) []*tally {
	out := make([]*tally, 0, len(t))
	for _, x := range t {
		out = append(out, x)
	}
	slices.SortFunc(out, func(a, b *tally) int {
		return cmp.Or(cmp.Compare(score(b), score(a)), strings.Compare(a.key, b.key))
	})
	return out
}

// pagesOf merges the pages of tallies.
func pagesOf(ts []*tally) []string {
	var out []string
	for _, t := range ts {
		for _, p := range t.pages {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return out
}

// list renders the top tallies for Detail.
func list(ts []*tally, line func(*tally) string) string {
	var parts []string
	for _, t := range ts[:min(len(ts), topN)] {
		parts = append(parts, line(t))
	}
	if len(ts) > topN {
		parts = append(parts, fmt.Sprintf("and %d more", len(ts)-topN))
	}
	return strings.Join(parts, "; ")
}

// count writes "1 image" or "3 images".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func verb(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// onPages counts the pages a grouped suggestion applies to: each has at
// least one of the files listed, not necessarily all of them.
func onPages(n, of int) string {
	return fmt.Sprintf("%d of %d pages", n, of)
}

// resourceBytes indexes a page's resources by URL.
func resourceBytes(p model.Page) map[string]int64 {
	m := map[string]int64{}
	for _, r := range p.Resources {
		m[r.URL] = max(m[r.URL], r.Bytes)
	}
	return m
}

func (s site) oversizedImages() []model.Issue {
	t := tallies{}
	for _, p := range s.pages {
		f := s.facts[p.Path]
		if f == nil {
			continue
		}
		bytes := resourceBytes(p)
		dpr := f.DPR
		if dpr <= 0 {
			dpr = 1
		}
		for _, img := range f.Images {
			if img.Natural <= 0 || img.Width <= 0 || img.Height <= 0 || img.NaturalH <= 0 {
				continue
			}
			if float64(img.Natural) <= oversizeRatio*img.Width*dpr {
				continue
			}
			b := bytes[img.Src]
			shown := img.Width * dpr * img.Height * dpr
			waste := int64(float64(b) * max(0, 1-shown/float64(img.Natural*img.NaturalH)))
			if b > 0 && waste < oversizeWaste {
				continue
			}
			t.add(s.short(img.Src), p.Path, b, float64(waste),
				fmt.Sprintf("%d×%d shown at %.0f×%.0f", img.Natural, img.NaturalH, img.Width, img.Height))
		}
	}
	if len(t) == 0 {
		return nil
	}
	ts := t.sorted(func(x *tally) float64 { return x.extra })
	pages := pagesOf(ts)
	var waste float64
	for _, x := range ts {
		waste += x.extra
	}
	level := model.Notice
	if waste/float64(len(pages)) >= veryHeavy {
		level = model.Warning
	}
	return []model.Issue{{Check: "speed/oversized-images", Level: level, Title: "Images are much larger than they're displayed",
		Detail: fmt.Sprintf("%s on %s, about %s wasted per page: %s", count(len(ts), "image"), onPages(len(pages), len(s.pages)),
			size(int64(waste/float64(len(pages)))), list(ts, func(x *tally) string {
				return fmt.Sprintf("%s (%s, %s)", x.key, x.detail, size(x.bytes))
			})),
		Fix: "Serve images at the size they're shown: insert them at a matching size (WordPress adds srcset for media-library images), " +
			"regenerate thumbnails after changing the theme and compress them (WebP or AVIF, e.g. with an image optimization plugin).",
		Pages: pages}}
}

func (s site) eagerImages() []model.Issue {
	t := tallies{}
	for _, p := range s.pages {
		f := s.facts[p.Path]
		if f == nil || f.Viewport <= 0 {
			continue
		}
		bytes := resourceBytes(p)
		for _, img := range f.Images {
			if img.Natural <= 0 || img.Width*img.Height < minLazyArea || img.Top < f.Viewport || img.Loading == "lazy" || img.Lazy {
				continue
			}
			t.add(s.short(img.Src), p.Path, bytes[img.Src], 0, "")
		}
	}
	if len(t) == 0 {
		return nil
	}
	ts := t.sorted(func(x *tally) float64 { return float64(x.bytes) * float64(len(x.pages)) })
	pages := pagesOf(ts)
	return []model.Issue{{Check: "speed/lazy-load", Level: model.Notice, Title: "Images below the fold aren't lazy-loaded",
		Detail: fmt.Sprintf("%s on %s %s before anyone scrolls to them: %s", count(len(ts), "image"), onPages(len(pages), len(s.pages)), verb(len(ts), "loads", "load"),
			list(ts, func(x *tally) string { return fmt.Sprintf("%s (%s)", x.key, size(x.bytes)) })),
		Fix: `Add loading="lazy" to images below the first screen. WordPress does this for content images since 5.5; ` +
			`check that the theme or page builder doesn't remove it. Keep the hero image eager.`,
		Pages: pages}}
}

func (s site) renderBlocking() []model.Issue {
	t := tallies{}
	for _, p := range s.pages {
		f := s.facts[p.Path]
		if f == nil {
			continue
		}
		bytes := resourceBytes(p)
		for _, b := range f.Blocking {
			t.add(s.short(b.URL), p.Path, bytes[b.URL], b.Duration, "")
		}
	}
	if len(t) == 0 {
		return nil
	}
	ts := t.sorted(func(x *tally) float64 { return x.extra })
	pages := pagesOf(ts)
	var total float64
	for _, x := range ts {
		total += x.extra
	}
	perPage := total / float64(len(pages))
	level := model.Notice
	if perPage >= 500 {
		level = model.Warning
	}
	return []model.Issue{{Check: "speed/render-blocking", Level: level, Title: "CSS and JavaScript block the first render",
		Detail: fmt.Sprintf("%s on %s must load before anything shows (about %s of loading per page): %s",
			count(len(ts), "file"), onPages(len(pages), len(s.pages)), ms(perPage), list(ts, func(x *tally) string {
				return fmt.Sprintf("%s (%s, %s)", x.key, size(x.bytes), ms(x.extra/float64(len(x.pages))))
			})),
		Fix: `Load scripts with defer or async (in WordPress, wp_enqueue_script's "strategy" => "defer"), inline the CSS the first screen needs ` +
			`and load the rest later, or let a performance plugin do both.`,
		Pages: pages}}
}

func (s site) unused() []model.Issue {
	var out []model.Issue
	for _, kind := range []struct{ typ, name, check, fix string }{
		{"Stylesheet", "CSS", "speed/unused-css",
			"Load stylesheets only on the pages that use them (many plugins have a setting for this, or use an asset manager plugin) and remove unused CSS (e.g. a theme's \"load only used blocks' styles\" option)."},
		{"Script", "JavaScript", "speed/unused-js",
			"Load scripts only on the pages that use them (plugin settings or an asset manager plugin) and drop libraries the site no longer needs."},
	} {
		t := tallies{}
		for _, p := range s.pages {
			for _, r := range p.Resources {
				if r.Type != kind.typ || r.Bytes < minUnusedBytes || r.Unused <= 0 {
					continue
				}
				t.add(s.short(r.URL), p.Path, r.Bytes, float64(r.Unused)/float64(r.Bytes), "")
			}
		}
		for k, x := range t { // average unused share across the pages that load the file
			if x.extra/float64(len(x.pages)) <= unusedShare {
				delete(t, k)
			}
		}
		if len(t) == 0 {
			continue
		}
		ts := t.sorted(func(x *tally) float64 { return float64(x.bytes) * x.extra / float64(len(x.pages)) })
		pages := pagesOf(ts)
		out = append(out, model.Issue{Check: kind.check, Level: model.Notice, Title: kind.name + " files are mostly unused",
			Detail: fmt.Sprintf("%s on %s %s mostly unused: %s", count(len(ts), "file"), onPages(len(pages), len(s.pages)), verb(len(ts), "is", "are"),
				list(ts, func(x *tally) string {
					return fmt.Sprintf("%s (%.0f%% of %s)", x.key, 100*x.extra/float64(len(x.pages)), size(x.bytes))
				})),
			Fix: kind.fix, Pages: pages})
	}
	return out
}

var textMime = regexp.MustCompile(`^(text/|application/(x-)?javascript|application/(ld\+)?json|application/(rss\+|atom\+)?xml|image/svg\+xml|application/manifest\+json|font/(ttf|otf)|application/vnd\.ms-fontobject)`)

func uncompressed(enc string) bool {
	enc = strings.ToLower(strings.TrimSpace(enc))
	return enc == "" || enc == "identity"
}

func (s site) compression() []model.Issue {
	t := tallies{}
	for _, p := range s.pages {
		if p.Headers != nil && uncompressed(p.Headers["content-encoding"]) {
			n, err := strconv.ParseInt(p.Headers["content-length"], 10, 64)
			if err != nil || n > minCompress { // unknown length: chunked, so not tiny
				t.add(p.Path+" (HTML)", p.Path, max(n, 0), 0, "")
			}
		}
		for _, r := range p.Resources {
			if s.sameSite(r.URL) && r.Status == 200 && r.Bytes > minCompress && textMime.MatchString(strings.ToLower(r.Mime)) && uncompressed(r.Encoding) {
				t.add(s.short(r.URL), p.Path, r.Bytes, 0, "")
			}
		}
	}
	if len(t) == 0 {
		return nil
	}
	ts := t.sorted(func(x *tally) float64 { return float64(x.bytes) })
	pages := pagesOf(ts)
	return []model.Issue{{Check: "speed/compression", Level: model.Warning, Title: "Text files are sent without compression",
		Detail: fmt.Sprintf("%s (HTML, CSS, JS or other text) on %s %s no gzip or Brotli: %s", count(len(ts), "response"),
			onPages(len(pages), len(s.pages)), verb(len(ts), "has", "have"), list(ts, func(x *tally) string {
				if x.bytes == 0 {
					return x.key
				}
				return fmt.Sprintf("%s (%s)", x.key, size(x.bytes))
			})),
		Fix:   "Turn on gzip or Brotli on the server (Apache mod_deflate/mod_brotli, nginx gzip on; with gzip_types for CSS and JS) or at the CDN.",
		Pages: pages}}
}

func (s site) httpVersion() []model.Issue {
	if s.base == nil || s.base.Scheme != "https" { // browsers only speak HTTP/2 over TLS
		return nil
	}
	old, all := 0, 0
	var pages []string
	for _, p := range s.pages {
		hit := false
		for _, r := range p.Resources {
			if !s.sameSite(r.URL) || r.Protocol == "" {
				continue
			}
			all++
			if strings.HasPrefix(strings.ToLower(r.Protocol), "http/1") {
				old++
				hit = true
			}
		}
		if hit {
			pages = append(pages, p.Path)
		}
	}
	if old == 0 {
		return nil
	}
	slices.Sort(pages)
	return []model.Issue{{Check: "speed/http-version", Level: model.Notice, Title: "Site isn't served over HTTP/2 or HTTP/3",
		Detail: fmt.Sprintf("%d of %s to the site used HTTP/1.x, on %s", old, count(all, "request"), onPages(len(pages), len(s.pages))),
		Fix:    "Enable HTTP/2 (and HTTP/3 if available) in the web server or hosting panel, or put the site behind a CDN that speaks it.",
		Pages:  pages}}
}

var maxAge = regexp.MustCompile(`(?i)\b(?:s-maxage|max-age)\s*=\s*"?(\d+)`)

// cacheLifetime returns the longest max-age in a Cache-Control header, or
// 0 when the response may not be reused.
func cacheLifetime(cc string) int {
	l := strings.ToLower(cc)
	if strings.Contains(l, "no-store") || strings.Contains(l, "no-cache") {
		return 0
	}
	best := 0
	for _, m := range maxAge.FindAllStringSubmatch(cc, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			best = max(best, n)
		}
	}
	return best
}

var staticTypes = []string{"Script", "Stylesheet", "Image", "Font", "Media"}

func (s site) caching() []model.Issue {
	t := tallies{}
	for _, p := range s.pages {
		for _, r := range p.Resources {
			if !s.sameSite(r.URL) || r.Status != 200 || !slices.Contains(staticTypes, r.Type) {
				continue
			}
			if age := cacheLifetime(r.Cache); age < shortCache {
				note := "no max-age"
				if age > 0 {
					note = "max-age " + duration(age)
				}
				t.add(s.short(r.URL), p.Path, r.Bytes, 0, note)
			}
		}
	}
	if len(t) == 0 {
		return nil
	}
	ts := t.sorted(func(x *tally) float64 { return float64(x.bytes) * float64(len(x.pages)) })
	pages := pagesOf(ts)
	return []model.Issue{{Check: "speed/cache-headers", Level: model.Notice, Title: "Static files aren't cached by browsers for long",
		Detail: fmt.Sprintf("%s (images, scripts, styles or fonts) on %s %s no or a short Cache-Control max-age: %s", count(len(ts), "file"),
			onPages(len(pages), len(s.pages)), verb(len(ts), "has", "have"), list(ts, func(x *tally) string { return fmt.Sprintf("%s (%s)", x.key, x.detail) })),
		Fix: "Send Cache-Control: public, max-age=31536000 for versioned static files (WordPress adds ?ver= to theme and plugin assets), " +
			"via the server config (Apache mod_expires, nginx expires) or a caching plugin.",
		Pages: pages}}
}

func duration(sec int) string {
	switch {
	case sec >= 86400:
		return fmt.Sprintf("%dd", sec/86400)
	case sec >= 3600:
		return fmt.Sprintf("%dh", sec/3600)
	case sec >= 60:
		return fmt.Sprintf("%dm", sec/60)
	}
	return fmt.Sprintf("%ds", sec)
}

// cost is what one plugin, theme or third-party host adds across pages.
type cost struct {
	name     string
	bytes    int64
	requests int
	blocking float64 // ms of render-blocking loads
	pages    []string
}

func (c *cost) perPage() (bytes int64, requests int, blocking float64) {
	n := max(len(c.pages), 1)
	return c.bytes / int64(n), (c.requests + n/2) / n, c.blocking / float64(n)
}

func (c *cost) heavy() bool {
	b, r, bl := c.perPage()
	return b >= heavyBytes || r >= heavyRequests || bl >= heavyBlocking
}

func (c *cost) level() model.Level {
	if b, _, bl := c.perPage(); b >= veryHeavy || bl >= veryBlocking {
		return model.Warning
	}
	return model.Notice
}

func (c *cost) describe(subject string, total int) string {
	b, r, bl := c.perPage()
	out := fmt.Sprintf("%s adds %s and %s to ", subject, size(b), count(r, "request"))
	if len(c.pages) == total && total > 1 {
		out += fmt.Sprintf("every page (%d)", total)
	} else {
		out += fmt.Sprintf("%d of %d pages", len(c.pages), total)
	}
	if bl >= 1 {
		out += fmt.Sprintf(", with %s of render-blocking loads", ms(bl))
	}
	return out + " (lab, per page)"
}

// costs sums resources by a key (plugin slug, host); key returns "" to
// skip a resource.
func costs(pages []model.Page, facts map[string]*facts, key func(r model.Resource) string) map[string]*cost {
	out := map[string]*cost{}
	for i := range pages {
		addCost(out, &pages[i], facts, key)
	}
	sortCosts(out)
	return out
}

// addCost adds what p's resources cost to out, by key; see costs.
// sortCosts puts each cost's pages in order once every page is added.
func addCost(out map[string]*cost, p *model.Page, facts map[string]*facts, key func(r model.Resource) string) {
	blockingMS := map[string]float64{}
	if f := facts[p.Path]; f != nil {
		for _, b := range f.Blocking {
			blockingMS[b.URL] += b.Duration
		}
	}
	for _, r := range p.Resources {
		k := key(r)
		if k == "" {
			continue
		}
		c := out[k]
		if c == nil {
			c = &cost{name: k}
			out[k] = c
		}
		c.bytes += r.Bytes
		c.requests++
		c.blocking += blockingMS[r.URL]
		blockingMS[r.URL] = 0 // count a file's blocking time once per page
		if n := len(c.pages); n == 0 || c.pages[n-1] != p.Path {
			c.pages = append(c.pages, p.Path)
		}
	}
}

// sortCosts sorts each cost's pages and drops the repeats (a path the
// run has twice).
func sortCosts(m map[string]*cost) {
	for _, c := range m {
		slices.Sort(c.pages)
		c.pages = slices.Compact(c.pages)
	}
}

func sortedCosts(m map[string]*cost) []*cost {
	out := make([]*cost, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b *cost) int {
		return cmp.Or(cmp.Compare(b.bytes, a.bytes), strings.Compare(a.name, b.name))
	})
	return out
}

func (s site) thirdParties() []model.Issue {
	var out []model.Issue
	all := costs(s.pages, s.facts, func(r model.Resource) string {
		u, err := url.Parse(r.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || s.sameSite(r.URL) {
			return ""
		}
		return strings.ToLower(u.Hostname())
	})
	for _, c := range sortedCosts(all) {
		if !c.heavy() {
			continue
		}
		out = append(out, model.Issue{Check: "speed/third-party", Level: c.level(), Title: "Heavy third party: " + c.name,
			Source: c.name, Detail: c.describe(c.name, len(s.pages)),
			Fix: "Check the site still needs it. Load it later (defer, or on first interaction for chat widgets and embeds), " +
				"self-host fonts and use a lightweight placeholder for videos and maps.",
			Pages: c.pages})
	}
	return out
}

// wpAsset finds a plugin or theme in an asset URL:
// /wp-content/plugins/elementor/assets/x.js → plugin elementor.
var wpAsset = regexp.MustCompile(`/wp-content/(plugins|mu-plugins|themes)/([^/?#]+)/`)

// component returns "plugin <slug>" or "theme <slug>" for an asset URL,
// or "".
func component(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	m := wpAsset.FindStringSubmatch(u.Path)
	if m == nil {
		return ""
	}
	kind := "plugin"
	if m[1] == "themes" {
		kind = "theme"
	}
	slug, err := url.PathUnescape(m[2])
	if err != nil {
		slug = m[2]
	}
	return kind + " " + slug
}

func (s site) plugins() []model.Issue {
	var out []model.Issue
	all := costs(s.pages, s.facts, func(r model.Resource) string { return component(r.URL) })
	for _, c := range sortedCosts(all) {
		if !c.heavy() {
			continue
		}
		kind, slug, _ := strings.Cut(c.name, " ")
		name := strings.ToUpper(kind[:1]) + kind[1:] + " " + slug
		fix := "Check whether " + slug + " needs to load on every page: many plugins can limit their assets to the pages that use them " +
			"(in their settings, or with an asset manager plugin), or can be replaced with a lighter one."
		if kind == "theme" {
			fix = "Turn off theme features the site doesn't use (sliders, icon fonts, animation libraries), or switch to a lighter theme."
		}
		out = append(out, model.Issue{Check: "speed/" + kind + "-cost", Level: c.level(), Title: name + " adds weight to pages",
			Source: kind + "s/" + slug, Detail: c.describe(name, len(s.pages)), Fix: fix, Pages: c.pages})
	}
	return out
}

// sameSite reports whether raw is on the checked site (www or not).
func (s site) sameSite(raw string) bool {
	if s.base == nil {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && strings.EqualFold(strings.TrimPrefix(u.Hostname(), "www."), strings.TrimPrefix(s.base.Hostname(), "www."))
}

// short shows site URLs as paths and others in full, without the query.
func (s site) short(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery, u.Fragment = "", ""
	if s.sameSite(raw) {
		return u.EscapedPath()
	}
	return u.String()
}
