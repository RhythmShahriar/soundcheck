// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
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
// its first load and works on the second comes back working, and the
// pages it links to aren't visited.
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

	// Revisit ignores the Paths in the options it's given.
	opts.Paths = []string{"/elsewhere/"}
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
