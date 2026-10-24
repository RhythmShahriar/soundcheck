// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// hits counts the requests for each path.
type hits struct {
	mu sync.Mutex
	n  map[string]int
}

func (h *hits) add(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n == nil {
		h.n = map[string]int{}
	}
	h.n[path]++
	return h.n[path]
}

func (h *hits) get(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n[path]
}

// siteChecked notes whether CheckSite ran.
type siteChecked struct{ ran *atomic.Bool }

func (siteChecked) Name() string                                { return "sitechecked" }
func (s siteChecked) CheckSite(context.Context, *check.Context) { s.ran.Store(true) }

func quietConfig() config.Config {
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Tabs = []string{"health"}, []string{"desktop"}, 0, 2
	return cfg
}

// Revisit visits only the pages it's given, again: a page that failed on
// its first load and works on the second comes back working. The pages
// it links to aren't visited.
func TestRevisit(t *testing.T) {
	needBrowser(t)
	var h hits
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := h.add(r.URL.Path)
		if r.URL.Path == "/flaky/" && n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
		}
		fmt.Fprint(w, `<!doctype html><title>Page</title><a href="/other/">Other</a> <a href="/">Home</a>`)
	}))
	defer srv.Close()
	var ran atomic.Bool
	opts := Options{Config: quietConfig(), Target: srv.URL, Paths: []string{"/flaky/"}, Dir: t.TempDir(), Version: "test",
		Browser: browser.Options{NoDownload: true}, Modules: []check.Module{siteChecked{&ran}}}
	first, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if p := first.Page("/flaky/"); p == nil || p.Status != http.StatusInternalServerError {
		t.Fatalf("first visit = %+v", first.Pages)
	}
	ran.Store(false)

	// Revisit ignores Paths, Done and OnPage from the options it's given.
	opts.Paths = []string{"/elsewhere/"}
	opts.Done = []*model.Page{{URL: srv.URL + "/flaky/", Path: "/flaky/"}}
	opts.OnPage = func(*model.Page) { t.Error("OnPage was called") }
	again, err := Revisit(context.Background(), opts, []string{"/flaky/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Pages) != 1 || again.Pages[0].Path != "/flaky/" || again.Pages[0].Status != http.StatusOK {
		t.Errorf("revisit = %+v", again.Pages)
	}
	for _, path := range []string{"/other/", "/elsewhere/"} {
		if h.get(path) != 0 {
			t.Errorf("%s was visited", path)
		}
	}
	if ran.Load() {
		t.Error("CheckSite ran on a revisit")
	}
	if _, err := Revisit(context.Background(), opts, nil); err == nil {
		t.Error("Revisit with no paths discovered the site")
	}
}

// A resumed run visits only the pages the interrupted one didn't finish.
// The finished pages' links still lead to the rest, the run has every
// page and OnPage hears of each new one once.
func TestResume(t *testing.T) {
	needBrowser(t)
	var h hits
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/a/" && r.URL.Path != "/b/" && r.URL.Path != "/c/" && r.URL.Path != "/d/" {
			http.NotFound(w, r)
			return
		}
		h.add(r.URL.Path)
		fmt.Fprint(w, `<!doctype html><title>Page</title><a href="/">Home</a>`)
	}))
	defer srv.Close()
	done := []*model.Page{
		// Only the home page linked to the others. It is done.
		{URL: srv.URL + "/", Path: "/", Title: "Home", Links: []string{srv.URL + "/a/", srv.URL + "/b/", srv.URL + "/c/", srv.URL + "/d/"}},
		{URL: srv.URL + "/a/", Path: "/a/", Title: "A"},
		{URL: srv.URL + "/b/", Path: "/b/", Title: "B"},
	}
	var (
		onPage   []string
		progress []string
	)
	run, err := Run(context.Background(), Options{Config: quietConfig(), Target: srv.URL, Dir: t.TempDir(), Version: "test",
		Browser: browser.Options{NoDownload: true}, Done: done,
		OnPage: func(p *model.Page) { onPage = append(onPage, p.Path) },
		Hooks:  Hooks{Page: func(_ *model.Page, d, total int) { progress = append(progress, fmt.Sprintf("%d/%d", d, total)) }}})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range run.Pages {
		paths = append(paths, p.Path)
	}
	if want := []string{"/", "/a/", "/b/"}; len(paths) != 5 || !slices.Equal(paths[:3], want) {
		t.Fatalf("pages = %v, want %v then /c/ and /d/", paths, want)
	}
	if run.Page("/a/").Title != "A" {
		t.Error("a finished page wasn't kept as it was")
	}
	for path, want := range map[string]int{"/a/": 0, "/b/": 0, "/c/": 1, "/d/": 1} {
		if got := h.get(path); got != want {
			t.Errorf("%s was loaded %d times, want %d", path, got, want)
		}
	}
	slices.Sort(onPage)
	if !slices.Equal(onPage, []string{"/c/", "/d/"}) {
		t.Errorf("OnPage got %v", onPage)
	}
	if !slices.Equal(progress, []string{"4/5", "5/5"}) {
		t.Errorf("progress = %v", progress)
	}
}
