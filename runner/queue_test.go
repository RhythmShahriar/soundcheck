// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Two visits that find the same new links at the same time queue each of
// them once: no page is visited twice.
func TestRenderedLinksQueuedOnce(t *testing.T) {
	needBrowser(t)
	var mu sync.Mutex
	hits := map[string]int{}
	shared := ""
	for i := range 5 {
		shared += fmt.Sprintf(`<a href="/x%d/">X %d</a> `, i, i)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
<url><loc>http://%[1]s/a/</loc></url><url><loc>http://%[1]s/b/</loc></url></urlset>`, r.Host)
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Only the browser's loads: the run fetches the start page once
		// outside it too, to resolve the site.
		if strings.Contains(r.UserAgent(), "Chrome") {
			mu.Lock()
			hits[r.URL.Path]++
			mu.Unlock()
		}
		links := ""
		if r.URL.Path == "/a/" || r.URL.Path == "/b/" {
			links = shared
		}
		fmt.Fprintf(w, `<!doctype html><title>%s</title><p>Page %[1]s.</p>%s`, r.URL.Path, links)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// The first three visits (/, /a/ and /b/ run side by side) meet
	// before adding their links and again before looking them up, so
	// /a/ and /b/ add the same links at the same time.
	meet := func() func() {
		var arrived atomic.Int32
		all := make(chan struct{})
		return func() {
			if n := arrived.Add(1); n == 3 {
				close(all)
			} else if n < 3 {
				select {
				case <-all:
				case <-time.After(10 * time.Second):
					t.Error("the visits never met")
				}
			}
		}
	}
	beforeAdd, afterAdd := meet(), meet()
	addHook = func(added bool) {
		if added {
			afterAdd()
		} else {
			beforeAdd()
		}
	}
	t.Cleanup(func() { addHook = nil })

	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Tabs = []string{"health"}, []string{"desktop"}, 0, 3
	cfg.Pages.PerGroup = -1
	var finalTotal int
	run, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Dir: t.TempDir(), Version: "test",
		Browser: browser.Options{NoDownload: true},
		Hooks:   Hooks{Page: func(_ *model.Page, _, total int) { finalTotal = total }}})
	if err != nil {
		t.Fatal(err)
	}
	var visited []string
	for _, p := range run.Pages {
		visited = append(visited, p.Path)
	}
	slices.Sort(visited)
	want := []string{"/", "/a/", "/b/", "/x0/", "/x1/", "/x2/", "/x3/", "/x4/"}
	if !slices.Equal(visited, want) {
		t.Errorf("visited %v, want %v", visited, want)
	}
	for path, n := range hits {
		if n != 1 {
			t.Errorf("%s was loaded %d times", path, n)
		}
	}
	if finalTotal != len(want) {
		t.Errorf("progress total = %d, want %d", finalTotal, len(want))
	}
}

// textReader is a module that reads page text at the end of the run.
type textReader struct{ withText *atomic.Int32 }

func (textReader) Name() string { return "text-reader" }
func (textReader) NeedsText()   {}
func (m textReader) CheckSite(_ context.Context, cc *check.Context) {
	for _, p := range cc.Run.Pages {
		if p.Text != "" {
			m.withText.Add(1)
		}
	}
}

// Page text is gone once the page's own checks ran, which still see it,
// unless a module says it needs the text later.
func TestTextIsDroppedAfterPageChecks(t *testing.T) {
	needBrowser(t)
	var broken atomic.Bool
	broken.Store(true)
	srv := site(t, &broken)
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate = []string{"health"}, []string{"desktop"}, 0
	opts := Options{Config: cfg, Target: srv.URL, Paths: []string{"/", "/shop/"}, Version: "test",
		Browser: browser.Options{NoDownload: true}, Modules: []check.Module{health.Module{}}}

	opts.Dir = t.TempDir()
	run, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range run.Pages {
		if p.Text != "" || p.TextLength == 0 {
			t.Errorf("%s: text %d characters kept, length %d", p.Path, len(p.Text), p.TextLength)
		}
	}
	home := run.Page("/")
	if home == nil || !slices.ContainsFunc(home.Issues, func(i model.Issue) bool {
		return i.Check == "health/php-error" && strings.Contains(i.Title, "Undefined array key")
	}) {
		t.Errorf("health didn't see the PHP warning in the text: %+v", home)
	}

	var withText atomic.Int32
	opts.Dir = t.TempDir()
	opts.Modules = append(opts.Modules, textReader{&withText})
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if n := withText.Load(); n != 2 {
		t.Errorf("a module that needs the text saw it on %d of 2 pages", n)
	}
}
