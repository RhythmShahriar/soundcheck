// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seo

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// fixture is one run's view of a page: what the browser recorded and the
// SEO facts.
type fixture struct {
	robots string // meta robots + X-Robots-Tag, as capture records it
	final  string // FinalURL, when it differs from the page's URL
	facts  Facts
}

// stored writes a run of target with one page at /p/ and the site facts
// into a new directory.
func stored(t *testing.T, target string, f fixture, site *Site) (*model.Run, string) {
	t.Helper()
	dir := t.TempDir()
	save := store.Saver(dir)
	u := target + "/p/"
	p := model.Page{ID: model.PageID(u), URL: u, Path: "/p/", Status: 200, Robots: f.robots, FinalURL: u}
	if f.final != "" {
		p.FinalURL = f.final
	}
	write := func(name string, v any) {
		data, _ := json.Marshal(v)
		if err := save(name, data); err != nil {
			t.Fatal(err)
		}
	}
	write(pageFile(&p), f.facts)
	if site != nil {
		write(siteFile, site)
	}
	return &model.Run{Target: target + "/", Pages: []model.Page{p}, Options: model.Options{Modules: []string{"health", "seo"}}}, dir
}

func diff(t *testing.T, b, a fixture) []model.Change {
	t.Helper()
	return diffOn(t, "https://shop.test", "https://shop.test", b, a, nil, nil)
}

func diffOn(t *testing.T, hostB, hostA string, b, a fixture, sb, sa *Site) []model.Change {
	t.Helper()
	before, bdir := stored(t, hostB, b, sb)
	after, adir := stored(t, hostA, a, sa)
	return Module{}.Compare(context.Background(), &check.CompareContext{Before: before, After: after, BeforeDir: bdir, AfterDir: adir})
}

// good is a page with everything an SEO plugin prints.
func good() Facts {
	return Facts{
		Description: "Fresh coffee beans, roasted every Monday.",
		Canonical:   "https://shop.test/p/",
		H1:          1,
		Hreflang:    map[string]string{"en": "https://shop.test/p/", "de": "https://shop.test/de/p/"},
		OG:          map[string]string{"og:title": "Beans", "og:image": "https://shop.test/beans.jpg", "og:updated_time": "2026-10-01"},
		JSONLD:      []string{"AggregateRating", "Offer", "Product"},
	}
}

func one(t *testing.T, cs []model.Change, check, class string) model.Change {
	t.Helper()
	var got []model.Change
	for _, c := range cs {
		if c.Check == check {
			got = append(got, c)
		}
	}
	if len(got) != 1 || got[0].Class != class {
		t.Fatalf("%s: want one %s change, got %+v (all %+v)", check, class, got, cs)
	}
	return got[0]
}

func none(t *testing.T, cs []model.Change) {
	t.Helper()
	if len(cs) > 0 {
		t.Errorf("want no changes, got %+v", cs)
	}
}

func TestUnchangedPage(t *testing.T) {
	none(t, diff(t, fixture{robots: "index, follow", facts: good()}, fixture{robots: "index, follow", facts: good()}))
	// Staging compared with production: the same paths on another host,
	// and a canonical that still names production.
	f := good()
	f.Hreflang = map[string]string{"en": "https://staging.shop.test/p/", "de": "https://staging.shop.test/de/p/"}
	f.OG["og:image"] = "https://staging.shop.test/beans.jpg"
	none(t, diffOn(t, "https://shop.test", "https://staging.shop.test", fixture{facts: good()}, fixture{facts: f}, nil, nil))
}

func TestCanonical(t *testing.T) {
	a := good()
	a.Canonical = ""
	c := one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/canonical", model.New)
	if c.Title != "Canonical link removed" || c.Before != "/p/" || !slices.Equal(c.Pages, []string{"/p/"}) {
		t.Errorf("removed = %+v", c)
	}
	a.Canonical = "https://shop.test/"
	c = one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/canonical", model.Changed)
	if c.Title != "Canonical URL changed" || c.Before != "/p/" || c.After != "/" {
		t.Errorf("changed = %+v", c)
	}
	a.Canonical = "https://other.test/p/"
	if c = one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/canonical", model.Changed); c.After != "https://other.test/p/" {
		t.Errorf("another site's URL must show whole: %+v", c)
	}
	// Adding a canonical the snapshot didn't have isn't a regression.
	b := good()
	b.Canonical = ""
	none(t, diff(t, fixture{facts: b}, fixture{facts: good()}))
}

func TestDescription(t *testing.T) {
	a := good()
	a.Description = ""
	c := one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/description", model.New)
	if c.Title != "Meta description removed" || c.Before != good().Description {
		t.Errorf("removed = %+v", c)
	}
	a.Description = "Coffee."
	c = one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/description", model.Changed)
	if c.Title != "Meta description changed" || c.After != "Coffee." {
		t.Errorf("changed = %+v", c)
	}
}

func TestRobotsMeta(t *testing.T) {
	f := good()
	c := one(t, diff(t, fixture{facts: f}, fixture{robots: "noindex, follow", facts: f}), "seo/robots-meta", model.Broke)
	if c.Title != "Page is now set to noindex" || c.Before != "(none)" || c.After != "noindex" {
		t.Errorf("noindex = %+v", c)
	}
	one(t, diff(t, fixture{robots: "index,follow", facts: f}, fixture{robots: "index,follow, googlebot: noindex", facts: f}), "seo/robots-meta", model.Broke)
	one(t, diff(t, fixture{robots: "index, follow", facts: f}, fixture{robots: "none", facts: f}), "seo/robots-meta", model.Broke)
	c = one(t, diff(t, fixture{facts: f}, fixture{robots: "nofollow", facts: f}), "seo/robots-meta", model.Changed)
	if c.Title != "Robots meta changed" || c.After != "nofollow" {
		t.Errorf("nofollow = %+v", c)
	}
	// Spelling out the defaults says nothing new.
	none(t, diff(t, fixture{facts: f}, fixture{robots: "index, follow", facts: f}))
	none(t, diff(t, fixture{robots: "noindex,follow", facts: f}, fixture{robots: "follow, noindex", facts: f}))
}

func TestH1(t *testing.T) {
	a := good()
	a.H1 = 0
	if c := one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/h1", model.New); c.Title != "Page has no <h1> heading anymore" {
		t.Errorf("lost = %+v", c)
	}
	a.H1 = 3
	if c := one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/h1", model.Changed); c.After != "3" {
		t.Errorf("several = %+v", c)
	}
	b := good()
	b.H1 = 2
	none(t, diff(t, fixture{facts: b}, fixture{facts: a}))
}

func TestHreflang(t *testing.T) {
	a := good()
	delete(a.Hreflang, "de")
	c := one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/hreflang", model.New)
	if c.Title != "Language links (hreflang) removed" || c.Detail != "No longer links to: de." {
		t.Errorf("lost = %+v", c)
	}
	a = good()
	a.Hreflang["de"] = "https://shop.test/"
	c = one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/hreflang", model.Changed)
	if c.Detail != "de: /de/p/ → /" {
		t.Errorf("moved = %+v", c)
	}
}

func TestOpenGraph(t *testing.T) {
	a := good()
	a.OG = nil
	c := one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/og", model.New)
	if c.Detail != "No longer printed: og:image, og:title, og:updated_time." {
		t.Errorf("lost = %+v", c)
	}
	a = good()
	a.OG["og:title"] = "Beans | Shop"
	a.OG["og:updated_time"] = "2026-10-20"
	c = one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/og", model.Changed)
	if c.Detail != "og:title: Beans → Beans | Shop" {
		t.Errorf("changed = %+v", c)
	}
	a = good()
	a.OG["og:updated_time"] = "2026-10-20"
	none(t, diff(t, fixture{facts: good()}, fixture{facts: a}))
}

func TestJSONLD(t *testing.T) {
	a := good()
	a.JSONLD = []string{"Offer", "Product", "WebPage"}
	c := one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/jsonld", model.New)
	if c.Title != "Structured data removed: AggregateRating" {
		t.Errorf("lost = %+v", c)
	}
	a = good()
	a.JSONLDErrors = 1
	if c := one(t, diff(t, fixture{facts: good()}, fixture{facts: a}), "seo/jsonld", model.New); c.Title != "Structured data is no longer valid JSON" {
		t.Errorf("invalid = %+v", c)
	}
}

func TestRedirect(t *testing.T) {
	f := good()
	c := one(t, diff(t, fixture{facts: f}, fixture{final: "https://shop.test/", facts: f}), "seo/redirect", model.Changed)
	if c.Title != "Page now redirects to the home page" || c.Before != "no redirect" || c.After != "/" {
		t.Errorf("home = %+v", c)
	}
	c = one(t, diff(t, fixture{final: "https://shop.test/new/", facts: f}, fixture{final: "https://shop.test/newer/", facts: f}), "seo/redirect", model.Changed)
	if c.Title != "Page now redirects to /newer/" || c.Before != "/new/" {
		t.Errorf("moved = %+v", c)
	}
	// The scheme is part of the address search engines index.
	c = one(t, diffOn(t, "http://shop.test", "http://shop.test", fixture{facts: f}, fixture{final: "https://shop.test/p/", facts: f}, nil, nil), "seo/redirect", model.Changed)
	if c.After != "https://shop.test/p/" {
		t.Errorf("https = %+v", c)
	}
	// The home page's facts aren't the page's: only the redirect shows.
	if cs := diff(t, fixture{facts: f}, fixture{final: "https://shop.test/", facts: Facts{H1: 1}}); len(cs) != 1 || cs[0].Check != "seo/redirect" {
		t.Errorf("redirected page = %+v", cs)
	}
	none(t, diff(t, fixture{final: "https://shop.test/new/", facts: f}, fixture{final: "https://shop.test/new/", facts: f}))
	none(t, diff(t, fixture{final: "https://shop.test/new/", facts: f}, fixture{facts: f}))
}

func TestRobotsTxt(t *testing.T) {
	open := Robots{Status: 200, Disallow: []string{"/wp-admin/"}}
	site := func(r Robots) *Site { return &Site{Robots: r, Sitemap: Sitemap{Status: 200, Pages: 10}} }
	run := func(b, a Robots) []model.Change {
		return diffOn(t, "https://shop.test", "https://shop.test", fixture{facts: good()}, fixture{facts: good()}, site(b), site(a))
	}
	c := one(t, run(open, Robots{Status: 200, Disallow: []string{"/"}, BlocksAll: true}), "seo/robots-txt", model.Broke)
	if c.Title != "robots.txt now blocks the whole site" || c.Source != "/robots.txt" || c.Pages != nil {
		t.Errorf("blocks all = %+v", c)
	}
	if c := one(t, run(open, Robots{Status: 503}), "seo/robots-txt", model.Broke); c.Title != "robots.txt returns 503" || c.Before != "200" {
		t.Errorf("5xx = %+v", c)
	}
	if c := one(t, run(open, Robots{Status: 404}), "seo/robots-txt", model.Changed); c.Title != "robots.txt is gone (404)" {
		t.Errorf("gone = %+v", c)
	}
	c = one(t, run(open, Robots{Status: 200, Disallow: []string{"/shop/"}}), "seo/robots-txt", model.Changed)
	if c.Detail != "Now disallows: /shop/. No longer disallows: /wp-admin/." {
		t.Errorf("rules = %+v", c)
	}
	none(t, run(open, open))
	none(t, run(Robots{Status: 200, Disallow: []string{"/"}, BlocksAll: true}, Robots{Status: 200, Disallow: []string{"/"}, BlocksAll: true}))
	none(t, run(Robots{Status: 404}, Robots{Status: 404}))
}

func TestSitemap(t *testing.T) {
	sm := func(status, pages int) *Site {
		return &Site{Robots: Robots{Status: 200}, Sitemap: Sitemap{URL: "https://shop.test/wp-sitemap.xml", Status: status, Pages: pages}}
	}
	run := func(b, a *Site) []model.Change {
		return diffOn(t, "https://shop.test", "https://shop.test", fixture{facts: good()}, fixture{facts: good()}, b, a)
	}
	c := one(t, run(sm(200, 100), sm(500, 0)), "seo/sitemap", model.Broke)
	if c.Title != "Sitemap returns 500" || c.Source != "/wp-sitemap.xml" {
		t.Errorf("5xx = %+v", c)
	}
	if c := one(t, run(sm(200, 100), sm(404, 0)), "seo/sitemap", model.Broke); c.Title != "Sitemap is gone (404)" {
		t.Errorf("gone = %+v", c)
	}
	c = one(t, run(sm(200, 100), sm(200, 70)), "seo/sitemap", model.Changed)
	if c.Before != "100 pages" || c.After != "70 pages" || c.Score != 30 {
		t.Errorf("fewer = %+v", c)
	}
	none(t, run(sm(200, 100), sm(200, 81)))
	none(t, run(sm(200, 100), sm(200, 140)))
	none(t, run(sm(500, 0), sm(500, 0)))
	// A count that stopped at the cap isn't compared.
	capped := sm(200, 70)
	capped.Sitemap.Capped = true
	none(t, run(sm(200, 100), capped))
	none(t, run(capped, sm(200, 20)))
}

func TestCompareNeedsTheModuleInBothRuns(t *testing.T) {
	before, bdir := stored(t, "https://shop.test", fixture{facts: good()}, nil)
	after, adir := stored(t, "https://shop.test", fixture{final: "https://shop.test/", facts: Facts{}}, nil)
	before.Options.Modules = []string{"health"}
	if cs := (Module{}).Compare(context.Background(), &check.CompareContext{Before: before, After: after, BeforeDir: bdir, AfterDir: adir}); len(cs) > 0 {
		t.Errorf("snapshot without seo: %+v", cs)
	}
}

func TestFilterChanges(t *testing.T) {
	cs := []model.Change{
		{Class: model.Changed, Check: "seo/redirect", Title: "Page now redirects to the home page", Pages: []string{"/a/"}},
		{Class: model.Changed, Check: "health/redirect", Title: "Page now ends somewhere else", Pages: []string{"/a/"}},
		{Class: model.Changed, Check: "health/redirect", Title: "Page now ends somewhere else", Pages: []string{"/b/"}},
		{Class: model.Broke, Check: "seo/robots-meta", Title: "Page is now set to noindex", Pages: []string{"/a/", "/c/"}},
		{Class: model.New, Check: "health/noindex", Title: "Page is set to noindex", Pages: []string{"/a/", "/c/", "/d/"}},
		{Class: model.New, Check: "health/js-error", Title: "JS error: x", Pages: []string{"/a/"}},
	}
	got := Module{}.FilterChanges(cs)
	var lines []string
	for _, c := range got {
		lines = append(lines, c.Check+" "+strings.Join(c.Pages, ","))
	}
	want := []string{"seo/redirect /a/", "health/redirect /b/", "seo/robots-meta /a/,/c/", "health/noindex /d/", "health/js-error /a/"}
	if !slices.Equal(lines, want) {
		t.Errorf("filtered =\n%v\nwant\n%v", lines, want)
	}
}
