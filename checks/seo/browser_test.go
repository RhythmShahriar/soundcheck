// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seo

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
)

func needBrowser(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// product is a shop page as an SEO plugin prints it. After a bad plugin
// update the canonical, the description, the German link, og:image and
// the rating markup are gone, the page is noindex, robots.txt disallows
// everything and the sitemap fails; /old/ now redirects to the home page.
func product(broken bool) http.Handler {
	head := `<meta name="description" content="Fresh beans, roasted every Monday.">
<link rel="canonical" href="/beans/">
<link rel="alternate" hreflang="en" href="/beans/"><link rel="alternate" hreflang="de" href="/de/beans/">
<meta property="og:title" content="Beans"><meta property="og:image" content="/beans.jpg">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product","name":"Beans",
"aggregateRating":{"@type":"AggregateRating","ratingValue":"4.8","reviewCount":"12"}}</script>`
	robots := "User-agent: *\nDisallow: /wp-admin/\n"
	if broken {
		head = `<meta name="robots" content="noindex, follow">
<link rel="alternate" hreflang="en" href="/beans/">
<meta property="og:title" content="Beans">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product","name":"Beans"}</script>`
		robots = "User-agent: *\nDisallow: /\n"
	}
	mux := http.NewServeMux()
	page := func(title string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>%s</title>%s</head>
<body><h1>%s</h1><p>%s</p></body></html>`, title, head, title, strings.Repeat("Single origin, washed. ", 10))
		}
	}
	mux.HandleFunc("/{$}", page("Shop"))
	mux.HandleFunc("/beans/", page("Beans"))
	mux.HandleFunc("/old/", func(w http.ResponseWriter, r *http.Request) {
		if broken {
			http.Redirect(w, r, "/", http.StatusMovedPermanently)
			return
		}
		page("Old beans")(w, r)
	})
	// A favicon, so "No favicon" can't come and go: headless Chrome asks
	// for /favicon.ico after the page loaded. On a slow runner one run
	// may finish a page before it asks.
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write([]byte{0, 0, 1, 0, 0, 0})
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, robots) })
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		if broken {
			http.Error(w, "Fatal error", http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, `<urlset><url><loc>/</loc></url><url><loc>/beans/</loc></url><url><loc>/old/</loc></url></urlset>`)
	})
	return mux
}

func TestSEOInBrowser(t *testing.T) {
	needBrowser(t)
	var broken atomic.Bool
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { product(broken.Load()).ServeHTTP(w, r) }))
	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 2
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"health", "seo"}
	ctx := context.Background()
	mods := []check.Module{health.Module{}, Module{}}
	type stored struct {
		run *model.Run
		dir string
	}
	run := func() stored {
		t.Helper()
		dir := t.TempDir()
		r, err := runner.Run(ctx, runner.Options{Config: cfg, Target: srv.URL, Paths: []string{"/beans/", "/old/"}, Dir: dir,
			Version: "test", Modules: mods, Browser: browser.Options{NoDownload: true}})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Warnings) > 0 {
			t.Errorf("warnings: %v", r.Warnings)
		}
		return stored{r, dir}
	}
	diff := func(b, a stored) []model.Change {
		t.Helper()
		return compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: b.run, After: a.run, BeforeDir: b.dir, AfterDir: a.dir}, mods, "test").Changes
	}

	snap := run()
	var f Facts
	if p := snap.run.Page("/beans/"); p == nil || !load(snap.dir, pageFile(p), &f) {
		t.Fatal("no facts stored for /beans/")
	}
	want := Facts{Description: "Fresh beans, roasted every Monday.", Canonical: srv.URL + "/beans/", H1: 1,
		Hreflang: map[string]string{"en": srv.URL + "/beans/", "de": srv.URL + "/de/beans/"},
		OG:       map[string]string{"og:title": "Beans", "og:image": srv.URL + "/beans.jpg"}, // made absolute
		JSONLD:   []string{"AggregateRating", "Product"}}
	if fmt.Sprintf("%+v", f) != fmt.Sprintf("%+v", want) {
		t.Errorf("facts =\n%+v\nwant\n%+v", f, want)
	}
	if cs := diff(snap, run()); len(cs) > 0 {
		t.Errorf("unchanged site: %+v", cs)
	}

	broken.Store(true)
	got := map[string]model.Change{}
	for _, c := range diff(snap, run()) {
		t.Logf("%s %s %s %v: %s → %s %s", c.Class, c.Check, c.Title, c.Pages, c.Before, c.After, c.Detail)
		got[c.Check+" "+c.Title] = c
	}
	for _, k := range []string{
		"seo/canonical Canonical link removed",
		"seo/description Meta description removed",
		"seo/robots-meta Page is now set to noindex",
		"seo/hreflang Language links (hreflang) removed",
		"seo/og Open Graph tags removed",
		"seo/jsonld Structured data removed: AggregateRating",
		"seo/robots-txt robots.txt now blocks the whole site",
		"seo/sitemap Sitemap returns 500",
		"seo/redirect Page now redirects to the home page",
	} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %q", k)
		}
	}
	if c := got["seo/redirect Page now redirects to the home page"]; !slices.Equal(c.Pages, []string{"/old/"}) {
		t.Errorf("redirect pages = %v", c.Pages)
	}
	// health says the same two things; seo's lines replace them. (/old/
	// now ends on the noindex home page, which health still reports.)
	for k, c := range got {
		if strings.HasPrefix(k, "health/redirect") || strings.HasPrefix(k, "health/noindex") && slices.Contains(c.Pages, "/beans/") {
			t.Errorf("%q %v is already told by seo", k, c.Pages)
		}
	}
}
