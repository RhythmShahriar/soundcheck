// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browsertest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/visual"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
	"github.com/RhythmShahriar/soundcheck/store"
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

// site is a small blog whose theme update changes the header color on
// every page, pushes /about/ down with a new block and recolors a box on
// /blog/. The home page has a carousel that changes by itself.
func site(t *testing.T, changed *atomic.Bool) *httptest.Server {
	t.Helper()
	render := func(w http.ResponseWriter, title, body string) {
		header := "#222"
		if changed.Load() {
			header = "#b33"
		}
		fmt.Fprintf(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title>
<style>body{margin:0;font:16px/1.5 sans-serif;color:#222} header{height:60px;background:%s} header a{color:#fff;margin:0 8px;line-height:60px}
main{padding:20px} .box{width:300px;height:150px} .slides{width:300px;height:100px}</style></head>
<body><header><a href="/">Home</a><a href="/about/">About</a><a href="/blog/">Blog</a></header><main><h1>%s</h1>%s
<p>Thoughtful words about coffee, written slowly and read quickly.</p><div style="height:1400px"></div><p>More words at the bottom of the page.</p></main></body></html>`,
			title, header, title, body)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		render(w, "Home", `<div class="slides" id="s"></div>
<script>const s = document.getElementById('s'); const tick = () => { s.style.background = 'hsl(' + Math.floor(Date.now() / 10 % 360) + ',70%,50%)'; }; tick(); setInterval(tick, 150);</script>`)
	})
	mux.HandleFunc("/about/", func(w http.ResponseWriter, r *http.Request) {
		extra := ""
		if changed.Load() {
			extra = `<div style="height:340px;background:#fc6"></div>`
		}
		render(w, "About", "<div>We roast on Tuesdays.</div>"+extra+"<div>And deliver on Fridays.</div>")
	})
	mux.HandleFunc("/blog/", func(w http.ResponseWriter, r *http.Request) {
		color := "#36c"
		if changed.Load() {
			color = "#3a6"
		}
		render(w, "Blog", `<div class="box" style="background:`+color+`"></div>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSnapshotAndCompareInChrome(t *testing.T) {
	needBrowser(t)
	var changed atomic.Bool
	srv := site(t, &changed)
	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 3
	cfg.Modules = []string{"visual"}
	modules := []check.Module{visual.Module{}}
	st := store.Store{Root: t.TempDir()}
	ctx := context.Background()

	snapDir, _ := st.NewDir("snapshots", "before")
	before, err := runner.Run(ctx, runner.Options{Config: cfg, Target: srv.URL, Snapshot: true, Name: "before", Dir: snapDir,
		Version: "test", Modules: modules, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(snapDir, before); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range before.Pages {
		paths = append(paths, p.Path)
		desk := p.Shots["desktop"]
		if desk.File == "" || desk.Second == "" || p.Shots["mobile"].Second == "" {
			t.Fatalf("%s: shots %+v", p.Path, p.Shots)
		}
		for vp, s := range p.Shots {
			if (len(s.Dynamic) > 0) != (p.Path == "/") {
				t.Errorf("%s %s: dynamic regions %+v", p.Path, vp, s.Dynamic)
			}
		}
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/", "/about/", "/blog/"}) {
		t.Fatalf("discovered %v", paths)
	}

	// Nothing changed yet: a second run compares clean, carousel and all.
	runDir, _ := st.NewDir("runs", "same")
	same, err := runner.Run(ctx, runner.Options{Config: cfg, Target: srv.URL, Paths: paths, Dir: runDir, Version: "test",
		Modules: modules, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(snapDir)
	if err != nil {
		t.Fatal(err)
	}
	cmpDir, _ := st.NewDir("runs", "same-cmp")
	cmp := compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: loaded, After: same, BeforeDir: snapDir, AfterDir: runDir,
		Dir: cmpDir, Save: store.Saver(cmpDir)}, modules, "test")
	if len(cmp.Changes) != 0 {
		t.Fatalf("unchanged site reported %+v", cmp.Changes)
	}

	changed.Store(true)
	runDir, _ = st.NewDir("runs", "now")
	after, err := runner.Run(ctx, runner.Options{Config: cfg, Target: srv.URL, Paths: paths, Dir: runDir, Version: "test",
		Modules: modules, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	cmpDir, _ = st.NewDir("runs", "now-cmp")
	cmp = compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: loaded, After: after, BeforeDir: snapDir, AfterDir: runDir,
		Dir: cmpDir, Save: store.Saver(cmpDir)}, modules, "test")
	for _, c := range cmp.Changes {
		t.Logf("%s %s %v %.2f %s", c.Class, c.Title, c.Pages, c.Score, c.Detail)
	}
	// The header changed everywhere; /about/ got a 340 px block (only
	// there, so it's that page's change); /blog/'s box changed color.
	for _, want := range []struct{ title, pages, detail string }{
		{"Header changed on 3 pages (desktop)", "/,/about/,/blog/", ""},
		{"Header changed on 3 pages (mobile)", "/,/about/,/blog/", ""},
		{"Page looks different (desktop)", "/about/", "A 340 px tall block was inserted"},
		{"Page looks different (mobile)", "/about/", "A 340 px tall block was inserted"},
		{"Page looks different (desktop)", "/blog/", "changed, in 1 area"},
		{"Page looks different (mobile)", "/blog/", "changed, in 1 area"},
	} {
		i := slices.IndexFunc(cmp.Changes, func(c model.Change) bool {
			return c.Title == want.title && strings.Join(c.Pages, ",") == want.pages
		})
		if i < 0 {
			t.Errorf("missing %q on %s", want.title, want.pages)
			continue
		}
		c := cmp.Changes[i]
		if c.Class != model.Changed || !strings.Contains(c.Detail, want.detail) {
			t.Errorf("%s: %s %s", want.title, c.Class, c.Detail)
		}
		for _, f := range c.Files {
			if _, err := os.Stat(filepath.Join(cmpDir, f)); err != nil {
				t.Errorf("%s: %v", want.title, err)
			}
		}
	}
	if len(cmp.Changes) != 6 {
		t.Errorf("%d changes, want 6", len(cmp.Changes))
	}
}

// A plugin adds the same banner to the top of every page and a notice
// above the end of it: every page gets taller and everything below each
// block moves, which is one change per block per viewport, not one per
// page. The notice wraps onto more lines on mobile.
func TestBannerOnEveryPageInChrome(t *testing.T) {
	needBrowser(t)
	var banner atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.Count(r.URL.Path, "/") != 2 {
			http.NotFound(w, r)
			return
		}
		top, notice := "", ""
		if banner.Load() {
			top = `<div style="height:320px;background:#fc6;padding:20px"><p>Big summer sale, everything must go!</p></div>`
			notice = `<p style="background:#fee"><b>Warning</b>: Undefined array key "promo" in /var/www/html/wp-content/plugins/promo/promo.php on line 88</p>`
		}
		fmt.Fprintf(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><title>%[1]s</title>
<style>body{margin:0;font:16px/1.5 sans-serif} header{height:60px;background:#222}</style></head><body>%[2]s
<header></header><main style="padding:20px"><h1>Page %[1]s</h1><p>Words that differ: %[1]s %[1]s %[1]s.</p>
<a href="/a/">A</a> <a href="/b/">B</a> <a href="/c/">C</a> <a href="/d/">D</a><div style="height:1500px"></div><p>End.</p>%[3]s
<footer style="height:120px;background:#333"></footer></main></body></html>`,
			r.URL.Path, top, notice)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 3
	cfg.Modules = []string{"visual"}
	modules := []check.Module{visual.Module{}}
	st := store.Store{Root: t.TempDir()}
	ctx := context.Background()
	snapDir, _ := st.NewDir("snapshots", "before")
	before, err := runner.Run(ctx, runner.Options{Config: cfg, Target: srv.URL, Snapshot: true, Dir: snapDir, Version: "test",
		Modules: modules, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range before.Pages {
		paths = append(paths, p.Path)
	}
	if len(paths) != 5 {
		t.Fatalf("discovered %v", paths)
	}
	banner.Store(true)
	runDir, _ := st.NewDir("runs", "now")
	after, err := runner.Run(ctx, runner.Options{Config: cfg, Target: srv.URL, Paths: paths, Dir: runDir, Version: "test",
		Modules: modules, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	cmpDir, _ := st.NewDir("runs", "cmp")
	cmp := compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: before, After: after, BeforeDir: snapDir, AfterDir: runDir,
		Dir: cmpDir, Save: store.Saver(cmpDir)}, modules, "test")
	var titles []string
	for _, c := range cmp.Changes {
		t.Logf("%s %s %v %.2f %s", c.Class, c.Title, c.Pages, c.Score, c.Detail)
		titles = append(titles, c.Title)
		if c.Check == "visual/page" {
			t.Errorf("page-level change: %s %v", c.Title, c.Pages)
		}
		if len(c.Pages) != 5 {
			t.Errorf("%s: pages %v", c.Title, c.Pages)
		}
	}
	for _, want := range []string{"New block at the top of 5 pages (desktop)", "New block at the top of 5 pages (mobile)",
		"New block near the bottom of 5 pages (desktop)", "New block near the bottom of 5 pages (mobile)"} {
		if !slices.Contains(titles, want) {
			t.Errorf("missing %q in %v", want, titles)
		}
	}
	if len(titles) != 4 { // one per block per viewport; the size change is told with them
		t.Errorf("%d changes: %v", len(titles), titles)
	}
}

// Short pages (shorter than the window) get a banner at the top and a
// PHP warning in the footer: the screenshots stay window-tall, the
// content just eats into the blank filler below it. Each block is one
// change per viewport.
func TestShortPagesInChrome(t *testing.T) {
	needBrowser(t)
	var broken atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		words := map[string]int{"/": 20, "/a/": 45, "/b/": 70, "/c/": 30}[r.URL.Path]
		if words == 0 {
			http.NotFound(w, r)
			return
		}
		banner, warning := "", ""
		if broken.Load() {
			banner = `<div style="height:48px;background:#ff7c52;color:#fff;line-height:48px;text-align:center">Free shipping on orders over $50!</div>`
			warning = `<br /><b>Warning</b>:  Undefined array key "layout" in <b>/var/www/html/wp-content/themes/acme/footer.php</b> on line <b>42</b><br />`
		}
		fmt.Fprintf(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><title>%[1]s</title>
<style>body{margin:0;font:16px/1.5 serif} header{background:#111;padding:16px} header a{color:#fff;margin-right:12px} main{padding:20px 32px} footer{background:#ececec;padding:40px 32px}</style></head>
<body>%[2]s<header><a href="/">Home</a><a href="/a/">A</a><a href="/b/">B</a><a href="/c/">C</a></header>
<main><h1>Page %[1]s</h1><p>%[3]s</p></main><footer>%[4]s<p>&copy; 2026 Acme Studio</p></footer></body></html>`,
			r.URL.Path, banner, strings.Repeat("words that wrap ", words), warning)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 3
	cfg.Modules = []string{"visual"}
	modules := []check.Module{visual.Module{}}
	st := store.Store{Root: t.TempDir()}
	ctx := context.Background()
	snapDir, _ := st.NewDir("snapshots", "before")
	before, err := runner.Run(ctx, runner.Options{Config: cfg, Target: srv.URL, Snapshot: true, Dir: snapDir, Version: "test",
		Modules: modules, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range before.Pages {
		paths = append(paths, p.Path)
		if h := p.Shots["desktop"].Height; h != 900 {
			t.Errorf("%s: desktop shot is %d px tall; the fixture should be shorter than the window", p.Path, h)
		}
	}
	broken.Store(true)
	runDir, _ := st.NewDir("runs", "now")
	after, err := runner.Run(ctx, runner.Options{Config: cfg, Target: srv.URL, Paths: paths, Dir: runDir, Version: "test",
		Modules: modules, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	cmp := compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: before, After: after, BeforeDir: snapDir, AfterDir: runDir},
		modules, "test")
	var titles []string
	for _, c := range cmp.Changes {
		t.Logf("%s %v %.2f %s", c.Title, c.Pages, c.Score, c.Detail)
		titles = append(titles, c.Title)
	}
	for _, want := range []string{
		"New block at the top of 4 pages (desktop)", "New block at the top of 4 pages (mobile)",
		"New block near the bottom of 4 pages (desktop)", "New block near the bottom of 4 pages (mobile)",
	} {
		if !slices.Contains(titles, want) {
			t.Errorf("missing %q", want)
		}
	}
	if len(titles) != 4 {
		t.Errorf("%d changes, want 4: %v", len(titles), titles)
	}
}
