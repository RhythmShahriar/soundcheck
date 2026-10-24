// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seo

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// ogKeys are the Open Graph properties whose values make a shared link's
// preview; a change to any other (og:locale, og:updated_time) is routine.
var ogKeys = []string{"og:title", "og:description", "og:type", "og:url", "og:image", "og:site_name"}

// sitemapDrop is how much smaller, in percent, the sitemap's page count
// must get to be reported: a few pages come and go every week.
const sitemapDrop = 20

// Compare reports what changed in the SEO facts of the pages checked in
// both runs and in robots.txt and the sitemap. A run made without the
// module has no facts, so then there's nothing to compare.
func (Module) Compare(_ context.Context, cmp *check.CompareContext) []model.Change {
	if !ran(cmp.Before) || !ran(cmp.After) {
		return nil
	}
	d := &differ{hosts: hosts(cmp.Before, cmp.After)}
	for _, pair := range cmp.Pairs() {
		b, a := pair[0], pair[1]
		if a.Error != "" || a.Status >= 400 || b.Status >= 400 {
			continue // health reports pages that fail
		}
		// A page that now redirects shows another page's facts; the
		// redirect is the news.
		if d.redirect(b, a) {
			continue
		}
		var fb, fa Facts
		if load(cmp.BeforeDir, pageFile(b), &fb) && load(cmp.AfterDir, pageFile(a), &fa) {
			d.page(a.Path, b.Robots, a.Robots, fb, fa)
		}
	}
	var sb, sa Site
	if load(cmp.BeforeDir, siteFile, &sb) && load(cmp.AfterDir, siteFile, &sa) {
		d.robotsTxt(sb.Robots, sa.Robots)
		d.sitemap(sb.Sitemap, sa.Sitemap)
	}
	return d.list()
}

func ran(r *model.Run) bool { return slices.Contains(r.Options.Modules, "seo") }

// hosts are the two runs' sites. A URL on either counts as the site's
// own, so staging compared with production (whose canonicals often name
// production) compares by path.
func hosts(runs ...*model.Run) []*url.URL {
	var out []*url.URL
	for _, r := range runs {
		if u, err := url.Parse(r.Target); err == nil && u.Host != "" {
			out = append(out, u)
		}
	}
	return out
}

// differ collects changes, merging the same change on several pages
// into one, as the issue diff does.
type differ struct {
	hosts []*url.URL
	order []string
	by    map[string]*model.Change
}

func (d *differ) add(c model.Change, page string) {
	if d.by == nil {
		d.by = map[string]*model.Change{}
	}
	k := strings.Join([]string{c.Class, c.Check, c.Title, c.Detail, c.Source, c.Before, c.After}, "|")
	have, ok := d.by[k]
	if !ok {
		have = &c
		d.by[k] = have
		d.order = append(d.order, k)
	}
	if page != "" && !slices.Contains(have.Pages, page) {
		have.Pages = append(have.Pages, page)
	}
}

func (d *differ) list() []model.Change {
	out := make([]model.Change, 0, len(d.order))
	for _, k := range d.order {
		c := *d.by[k]
		slices.Sort(c.Pages)
		if c.Score == 0 {
			c.Score = float64(len(c.Pages))
		}
		out = append(out, c)
	}
	return out
}

// show writes a URL on the site as its path and other URLs whole.
func (d *differ) show(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return raw
	}
	for _, h := range d.hosts {
		if sameHost(u, h) {
			s := u.RequestURI()
			if u.Fragment != "" {
				s += "#" + u.Fragment
			}
			return s
		}
	}
	return raw
}

// redirect reports a page that now ends at another URL: one that used
// to load at its own address, or that used to redirect somewhere else.
// A redirect from http to https or to the address with a slash counts,
// since search engines index the final URL. It returns whether it
// reported one.
func (d *differ) redirect(b, a *model.Page) bool {
	bt, at := d.target(b), d.target(a)
	if at == "" || at == bt {
		return false
	}
	title := "Page now redirects to " + at
	if at == "/" {
		title = "Page now redirects to the home page"
	}
	d.add(model.Change{Class: model.Changed, Check: "seo/redirect", Title: title, Before: cmpOr(bt, "no redirect"), After: at,
		Fix: "If this page should still load, remove the redirect (a redirect plugin, .htaccess or the server config). " +
			"Search engines treat a redirect to the home page as a missing page."}, a.Path)
	return true
}

// target is where a page's redirects end, or "" when it loads at its
// own address.
func (d *differ) target(p *model.Page) string {
	if p.FinalURL == "" || strip(p.FinalURL) == strip(p.URL) {
		return ""
	}
	f, err1 := url.Parse(p.FinalURL)
	u, err2 := url.Parse(p.URL)
	if err1 == nil && err2 == nil && f.RequestURI() == u.RequestURI() {
		return f.Scheme + "://" + f.Host + f.RequestURI() // only the scheme or host changed
	}
	return d.show(p.FinalURL)
}

func strip(raw string) string {
	raw, _, _ = strings.Cut(raw, "#")
	return raw
}

func (d *differ) page(path, robotsB, robotsA string, b, a Facts) {
	d.canonical(path, b, a)
	d.description(path, b, a)
	d.robotsMeta(path, robotsB, robotsA)
	d.h1(path, b, a)
	d.hreflang(path, b, a)
	d.og(path, b, a)
	d.jsonld(path, b, a)
}

func (d *differ) canonical(path string, b, a Facts) {
	bc, ac := d.show(b.Canonical), d.show(a.Canonical)
	switch {
	case bc == "" || bc == ac:
	case ac == "":
		d.add(model.Change{Class: model.New, Check: "seo/canonical", Title: "Canonical link removed", Before: bc,
			Fix: `Every page should print <link rel="canonical"> with its own URL; check the SEO plugin's settings after the update.`}, path)
	default:
		d.add(model.Change{Class: model.Changed, Check: "seo/canonical", Title: "Canonical URL changed", Before: bc, After: ac,
			Fix: "Search engines index the canonical URL instead of this one. If it isn't the page's own address, check the SEO plugin's canonical settings."}, path)
	}
}

func (d *differ) description(path string, b, a Facts) {
	switch {
	case b.Description == "" || b.Description == a.Description:
	case a.Description == "":
		d.add(model.Change{Class: model.New, Check: "seo/description", Title: "Meta description removed", Before: short(b.Description),
			Fix: "Restore it in the SEO plugin or the theme's <head>; without it search engines pick their own snippet."}, path)
	default:
		d.add(model.Change{Class: model.Changed, Check: "seo/description", Title: "Meta description changed",
			Before: short(b.Description), After: short(a.Description),
			Fix: "If nobody edited it, check the SEO plugin's meta description templates."}, path)
	}
}

// robotsMeta compares the robots meta tags and X-Robots-Tag header
// (capture joins them). The defaults (index, follow, all) are the same
// as saying nothing, so adding them isn't a change.
func (d *differ) robotsMeta(path, b, a string) {
	tb, ta := robotsTokens(b), robotsTokens(a)
	if slices.Equal(tb, ta) {
		return
	}
	before, after := cmpOr(strings.Join(tb, ", "), "(none)"), cmpOr(strings.Join(ta, ", "), "(none)")
	if noindex(ta) && !noindex(tb) {
		d.add(model.Change{Class: model.Broke, Check: "seo/robots-meta", Title: "Page is now set to noindex", Before: before, After: after,
			Fix: "Search engines will drop this page. Remove the noindex (WordPress: Settings → Reading, or the SEO plugin's settings for the page)."}, path)
		return
	}
	d.add(model.Change{Class: model.Changed, Check: "seo/robots-meta", Title: "Robots meta changed", Before: before, After: after,
		Fix: "If nobody changed it, check the SEO plugin's robots settings for this page."}, path)
}

func robotsTokens(s string) []string {
	var out []string
	for t := range strings.SplitSeq(strings.ToLower(s), ",") {
		t = strings.Join(strings.Fields(t), " ")
		if t == "" || t == "index" || t == "follow" || t == "all" || slices.Contains(out, t) {
			continue
		}
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

func noindex(tokens []string) bool {
	return slices.ContainsFunc(tokens, func(t string) bool {
		_, v, ok := strings.Cut(t, ":") // "googlebot: noindex" in X-Robots-Tag
		if !ok {
			v = t
		}
		v = strings.TrimSpace(v)
		return v == "noindex" || v == "none"
	})
}

// h1 reports a page that lost its <h1> or that went from one to several.
// A page that had several already is left alone.
func (d *differ) h1(path string, b, a Facts) {
	switch {
	case b.H1 > 0 && a.H1 == 0:
		d.add(model.Change{Class: model.New, Check: "seo/h1", Title: "Page has no <h1> heading anymore", Before: strconv.Itoa(b.H1), After: "0",
			Fix: "Restore the page's main heading as an <h1>; a theme or page builder update may have made it another tag."}, path)
	case b.H1 == 1 && a.H1 > 1:
		d.add(model.Change{Class: model.Changed, Check: "seo/h1", Title: "Page now has several <h1> headings", Before: "1", After: strconv.Itoa(a.H1),
			Fix: "Pages usually have one <h1>; check the theme's header and the page builder's heading settings."}, path)
	}
}

func (d *differ) hreflang(path string, b, a Facts) {
	var lost, moved []string
	for _, lang := range sortedKeys(b.Hreflang) {
		ah, ok := a.Hreflang[lang]
		switch {
		case !ok:
			lost = append(lost, lang)
		case d.show(ah) != d.show(b.Hreflang[lang]):
			moved = append(moved, fmt.Sprintf("%s: %s → %s", lang, d.show(b.Hreflang[lang]), d.show(ah)))
		}
	}
	if len(lost) > 0 {
		d.add(model.Change{Class: model.New, Check: "seo/hreflang", Title: "Language links (hreflang) removed",
			Detail: "No longer links to: " + strings.Join(lost, ", ") + ".",
			Fix:    "Restore the language links (the multilingual plugin prints them); without them search engines may show visitors the wrong language."}, path)
	}
	if len(moved) > 0 {
		d.add(model.Change{Class: model.Changed, Check: "seo/hreflang", Title: "Language links (hreflang) changed",
			Detail: strings.Join(moved, "\n"), Fix: "Check that each language link still points at the page's translation."}, path)
	}
}

func (d *differ) og(path string, b, a Facts) {
	var lost, changed []string
	for _, k := range sortedKeys(b.OG) {
		av, ok := a.OG[k]
		bv := b.OG[k]
		if k == "og:url" || k == "og:image" {
			av, bv = d.show(av), d.show(bv)
		}
		switch {
		case !ok:
			lost = append(lost, k)
		case av != bv && slices.Contains(ogKeys, k):
			changed = append(changed, fmt.Sprintf("%s: %s → %s", k, short(bv), short(av)))
		}
	}
	if len(lost) > 0 {
		d.add(model.Change{Class: model.New, Check: "seo/og", Title: "Open Graph tags removed",
			Detail: "No longer printed: " + strings.Join(lost, ", ") + ".",
			Fix:    "Links shared on social media lose their title, text or image; check the SEO plugin's social settings."}, path)
	}
	if len(changed) > 0 {
		d.add(model.Change{Class: model.Changed, Check: "seo/og", Title: "Open Graph tags changed", Detail: strings.Join(changed, "\n"),
			Fix: "If nobody edited them, check the SEO plugin's social settings."}, path)
	}
}

// jsonld reports each structured data type the page no longer declares,
// so the same lost type on many pages is one line.
func (d *differ) jsonld(path string, b, a Facts) {
	for _, t := range b.JSONLD {
		if !slices.Contains(a.JSONLD, t) {
			d.add(model.Change{Class: model.New, Check: "seo/jsonld", Title: "Structured data removed: " + t,
				Fix: "Search results lose what this type gives them (rich results, product details, breadcrumbs). " +
					"Check the SEO or schema plugin's settings, then test the page with Google's Rich Results Test."}, path)
		}
	}
	if a.JSONLDErrors > b.JSONLDErrors {
		d.add(model.Change{Class: model.New, Check: "seo/jsonld", Title: "Structured data is no longer valid JSON",
			Before: strconv.Itoa(b.JSONLDErrors) + " invalid", After: strconv.Itoa(a.JSONLDErrors) + " invalid",
			Fix: "Search engines skip a JSON-LD block that doesn't parse; check the plugin or theme that prints it."}, path)
	}
}

func (d *differ) robotsTxt(b, a Robots) {
	const src = "/robots.txt"
	switch {
	case a.Status >= 500 && b.Status < 500:
		d.add(model.Change{Class: model.Broke, Check: "seo/robots-txt", Title: fmt.Sprintf("robots.txt returns %d", a.Status), Source: src,
			Before: status(b.Status), After: status(a.Status),
			Fix: "Google treats a robots.txt that fails with a server error as if it disallowed everything; fix the error."}, "")
	case a.BlocksAll && !b.BlocksAll:
		d.add(model.Change{Class: model.Broke, Check: "seo/robots-txt", Title: "robots.txt now blocks the whole site", Source: src,
			Detail: "User-agent: * has Disallow: /",
			Fix: `Search engines will stop crawling the site. Remove "Disallow: /" (WordPress: Settings → Reading, "Discourage search engines"). ` +
				"On a staging site that should stay hidden, this is expected."}, "")
	case b.Status == 200 && (a.Status == 404 || a.Status == 410):
		d.add(model.Change{Class: model.Changed, Check: "seo/robots-txt", Title: fmt.Sprintf("robots.txt is gone (%d)", a.Status), Source: src,
			Before: status(b.Status), After: status(a.Status),
			Fix: "Without it the sitemap isn't announced to crawlers; restore the file or the plugin that writes it."}, "")
	case b.Status == 200 && a.Status == 200 && !slices.Equal(b.Disallow, a.Disallow):
		var parts []string
		if add := minus(a.Disallow, b.Disallow); len(add) > 0 {
			parts = append(parts, "Now disallows: "+strings.Join(add, ", ")+".")
		}
		if gone := minus(b.Disallow, a.Disallow); len(gone) > 0 {
			parts = append(parts, "No longer disallows: "+strings.Join(gone, ", ")+".")
		}
		d.add(model.Change{Class: model.Changed, Check: "seo/robots-txt", Title: "robots.txt rules changed", Source: src,
			Detail: strings.Join(parts, " "),
			Fix:    "If nobody changed them, check the plugin or the file that writes robots.txt."}, "")
	}
}

func (d *differ) sitemap(b, a Sitemap) {
	src := d.show(cmpOr(a.URL, b.URL))
	switch {
	case a.Status >= 500 && b.Status < 500:
		d.add(model.Change{Class: model.Broke, Check: "seo/sitemap", Title: fmt.Sprintf("Sitemap returns %d", a.Status), Source: src,
			Before: status(b.Status), After: status(a.Status),
			Fix: "Search engines can't read the sitemap; check the server's error log for this URL."}, "")
	case b.Status == 200 && (a.Status == 404 || a.Status == 410):
		d.add(model.Change{Class: model.Broke, Check: "seo/sitemap", Title: fmt.Sprintf("Sitemap is gone (%d)", a.Status), Source: src,
			Before: status(b.Status), After: status(a.Status),
			Fix: "Restore the sitemap (the SEO plugin's, or WordPress's own at /wp-sitemap.xml) and its line in robots.txt."}, "")
	case b.Status == 200 && a.Status == 200 && b.Pages > 0 && (b.Pages-a.Pages)*100 > b.Pages*sitemapDrop:
		d.add(model.Change{Class: model.Changed, Check: "seo/sitemap", Title: "Sitemap lists fewer pages", Source: src,
			Before: pages(b.Pages), After: pages(a.Pages), Score: float64(b.Pages-a.Pages) * 100 / float64(b.Pages),
			Fix: "Check whether pages, a post type or a taxonomy were left out of the SEO plugin's sitemap settings."}, "")
	}
}

func pages(n int) string {
	if n == 1 {
		return "1 page"
	}
	return strconv.Itoa(n) + " pages"
}

func status(code int) string {
	if code == 0 {
		return "no answer"
	}
	return strconv.Itoa(code)
}

// minus returns the items of a that aren't in b.
func minus(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// short keeps long texts (descriptions, titles) readable in one line.
func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:199]) + "…"
	}
	return s
}

func cmpOr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// FilterChanges drops what health reports for the same cause on the
// same page: its "Page now ends somewhere else" where seo/redirect says
// where and its "Page is set to noindex" (new or fixed) where
// seo/robots-meta shows the change.
func (Module) FilterChanges(cs []model.Change) []model.Change {
	covered := map[string]map[string]bool{"health/redirect": {}, "health/noindex": {}}
	for _, c := range cs {
		var by string
		switch c.Check {
		case "seo/redirect":
			by = "health/redirect"
		case "seo/robots-meta":
			by = "health/noindex"
		default:
			continue
		}
		for _, p := range c.Pages {
			covered[by][p] = true
		}
	}
	var out []model.Change
	for _, c := range cs {
		if m := covered[c.Check]; len(m) > 0 && (c.Check == "health/redirect" || c.Class == model.New || c.Class == model.Fixed) {
			c.Pages = slices.DeleteFunc(slices.Clone(c.Pages), func(p string) bool { return m[p] })
			if len(c.Pages) == 0 {
				continue
			}
			c.Score = float64(len(c.Pages))
		}
		out = append(out, c)
	}
	return out
}
