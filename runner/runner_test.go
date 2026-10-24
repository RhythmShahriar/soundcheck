// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

func needBrowser(t *testing.T) {
	t.Helper()
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// site is a small shop that "breaks" when broken is set, the way a bad
// plugin update would.
func site(t *testing.T, broken *atomic.Bool) *httptest.Server {
	t.Helper()
	page := func(title, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" && strings.Count(r.URL.Path, "/") == 0 {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width"><title>%s</title></head><body>
<nav><a href="/">Home</a> <a href="/shop/">Shop</a> <a href="/contact/">Contact</a></nav>%s</body></html>`, title, body)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		body := "<h1>Welcome</h1><p>Fresh coffee, roasted daily.</p>"
		if broken.Load() {
			body += `<br /><b>Warning</b>:  Undefined array key "id" in <b>/var/www/html/wp-content/themes/brew/functions.php</b> on line <b>212</b><br />`
		}
		page("Brew", body)(w, r)
	})
	mux.HandleFunc("/shop/", func(w http.ResponseWriter, r *http.Request) {
		js := ""
		if broken.Load() {
			js = "<script>jQuery('.cart').show();</script>"
		}
		page("Shop", "<h1>Shop</h1><p>Beans and grinders.</p>"+js)(w, r)
	})
	mux.HandleFunc("/contact/", func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "<html><body>There has been a critical error on this website.</body></html>")
			return
		}
		page("Contact", "<h1>Contact</h1><p>Write to us any time.</p>")(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSnapshotAndCompare(t *testing.T) {
	needBrowser(t)
	var broken atomic.Bool
	srv := site(t, &broken)
	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 2
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"health"}
	modules := []check.Module{health.Module{}}
	st := store.Store{Root: t.TempDir()}
	ctx := context.Background()

	snapDir, _ := st.NewDir("snapshots", "before")
	before, err := Run(ctx, Options{Config: cfg, Target: srv.URL, Snapshot: true, Name: "before", Dir: snapDir, Version: "test",
		Modules: modules, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range before.Pages {
		paths = append(paths, p.Path)
		for _, i := range p.Issues {
			if i.Level != model.Notice { // e.g. "No favicon" is fine
				t.Errorf("healthy %s has %s: %s", p.Path, i.Level, i.Title)
			}
		}
	}
	slices.Sort(paths)
	if !slices.Equal(paths, []string{"/", "/contact/", "/shop/"}) {
		t.Fatalf("discovered %v", paths)
	}
	if err := store.SaveRun(snapDir, before); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(snapDir)
	if err != nil || len(loaded.Pages) != 3 {
		t.Fatalf("reload: %v", err)
	}

	broken.Store(true)
	runDir, _ := st.NewDir("runs", "now")
	after, err := Run(ctx, Options{Config: cfg, Target: srv.URL, Paths: paths, Dir: runDir, Version: "test",
		Modules: modules, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	cmp := compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: loaded, After: after}, modules, "test")
	got := map[string]string{}
	for _, c := range cmp.Changes {
		got[c.Title] = c.Class + " " + strings.Join(c.Pages, ",")
	}
	for title, want := range map[string]string{
		"Server error 500":                                         model.Broke + " /contact/",
		`WordPress "critical error" screen`:                        model.Broke + " /contact/",
		`PHP Warning: Undefined array key "id"`:                    model.New + " /",
		"JS error: Uncaught ReferenceError: jQuery is not defined": model.New + " /shop/",
	} {
		if got[title] != want {
			t.Errorf("%s: got %q, want %q (all: %v)", title, got[title], want, got)
		}
	}
	if len(cmp.Warnings) != 0 {
		t.Errorf("warnings: %v", cmp.Warnings)
	}
}

func TestFailedLoginIsOneWarning(t *testing.T) {
	needBrowser(t)
	var broken atomic.Bool
	srv := site(t, &broken)
	defer srv.Close()
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate = []string{"health"}, []string{"desktop"}, 50
	run, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Dir: t.TempDir(), Version: "test",
		Browser: browser.Options{NoDownload: true}, Modules: []check.Module{health.Module{}},
		Login: func(*url.URL) func(context.Context, *rod.Browser) error {
			return func(context.Context, *rod.Browser) error { return fmt.Errorf("wrong password for us***") }
		}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Options.LoggedIn {
		t.Error("a failed login mustn't mark the run as logged in")
	}
	if !slices.ContainsFunc(run.Warnings, func(w string) bool { return strings.Contains(w, "login failed") }) {
		t.Errorf("warnings = %v", run.Warnings)
	}
}

// A page that answers just inside the timeout is read in full. One
// that answers after it is an error with its real duration.
func TestSlowPages(t *testing.T) {
	needBrowser(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
		select {
		case <-time.After(time.Duration(wait) * time.Second):
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `<!doctype html><html><head><title>Slow</title></head><body><p>Worth the wait.</p></body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Tabs, cfg.Timeout = []string{"health"}, []string{"desktop"}, 0, 2, 5
	run, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Paths: []string{"/slow?wait=4", "/slow?wait=7"},
		Dir: t.TempDir(), Version: "test", Browser: browser.Options{NoDownload: true}, Modules: []check.Module{health.Module{}}})
	if err != nil {
		t.Fatal(err)
	}
	if fast := run.Page("/slow?wait=4"); fast == nil || fast.Title != "Slow" || fast.Error != "" ||
		slices.ContainsFunc(fast.Issues, func(i model.Issue) bool { return i.Check == "health/white-screen" }) {
		t.Errorf("4 s page: %+v", fast)
	}
	if slow := run.Page("/slow?wait=7"); slow == nil || slow.Error != "timed out" || slow.Duration < 4500 {
		t.Errorf("7 s page: %+v", slow)
	}
}

// A sibling site on another port of the same host is another site, even
// when this site's sitemap and links point at it.
func TestSiblingPortIsAnotherSite(t *testing.T) {
	needBrowser(t)
	var sibling atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sibling.Add(1)
		fmt.Fprint(w, `<!doctype html><title>Sibling</title><p>Another site.</p>`)
	}))
	defer other.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>http://%[1]s/about/</loc></url><url><loc>%[2]s/</loc></url><url><loc>%[2]s/pricing/</loc></url></urlset>`, r.Host, other.URL)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html><title>Home</title><p>Welcome.</p><a href="/about/">About</a> <a href="%s/team/">Team</a>`, other.URL)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate = []string{"health"}, []string{"desktop"}, 0
	var discovered map[string]int
	run, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Dir: t.TempDir(), Version: "test",
		Browser: browser.Options{NoDownload: true},
		Hooks:   Hooks{Discovered: func(_ int, skipped map[string]int) { discovered = skipped }}})
	if err != nil {
		t.Fatal(err)
	}
	var visited []string
	for _, p := range run.Pages {
		visited = append(visited, p.URL)
	}
	slices.Sort(visited)
	if want := []string{srv.URL + "/", srv.URL + "/about/"}; !slices.Equal(visited, want) {
		t.Errorf("visited %v, want %v", visited, want)
	}
	if n := sibling.Load(); n != 0 {
		t.Errorf("the sibling site got %d requests", n)
	}
	if discovered["other site"] < 2 || run.Skipped["other site"] < 2 {
		t.Errorf("skipped: discovered %v, run %v", discovered, run.Skipped)
	}
}

// The start page keeps the target's query and follows a redirect's path.
func TestStartPage(t *testing.T) {
	needBrowser(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/q", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html><title>Lang %s</title><p>Language %[1]s.</p>`, r.URL.Query().Get("lang"))
	})
	mux.HandleFunc("/go", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/b", http.StatusFound) })
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>B</title><p>Page b.</p>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate = []string{"health"}, []string{"desktop"}, 0
	cfg.Pages.Limit = 1
	for target, want := range map[string]string{"/q?lang=fr": "/q?lang=fr", "/go": "/b"} {
		run, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL + target, Dir: t.TempDir(), Version: "test",
			Browser: browser.Options{NoDownload: true}})
		if err != nil {
			t.Fatal(err)
		}
		if run.Target != srv.URL+want || len(run.Pages) == 0 || run.Pages[0].Path != want || run.Pages[0].Status != 200 {
			t.Errorf("%s: target %s, pages %+v", target, run.Target, run.Pages)
		}
		if target == "/q?lang=fr" && len(run.Pages) > 0 && run.Pages[0].Title != "Lang fr" {
			t.Errorf("%s: title %q", target, run.Pages[0].Title)
		}
	}
}
