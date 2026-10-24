// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/a11y"
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

// serve starts h on the first free port of 127.0.0.1:20320-20339, the
// range set aside for these fixtures.
func serve(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	for port := 20320; port < 20340; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv := httptest.NewUnstartedServer(h)
		_ = srv.Listener.Close()
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	t.Fatal("no free port in 20320-20339")
	return nil
}

// signup is a newsletter page. Broken, a theme update has dropped the
// email field's label and made the button's text pale grey on white. Its
// AMD loader and its own global named axe would break axe-core if it ran
// in the page's world.
const signup = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Newsletter</title>
<script>window.define = function () {}; window.define.amd = {}; window.axe = "the site's own";</script></head>
<body><main><h1>Join the newsletter</h1>
<form id="signup" action="/thanks">%s
<button type="submit" style="color:%s;background:#fff">Subscribe</button></form></main></body></html>`

func page(broken bool) string {
	if broken {
		return fmt.Sprintf(signup, `<p><input type="email" name="email"></p>`, "#c8c8c8")
	}
	return fmt.Sprintf(signup, `<p><label for="email">Email</label> <input type="email" id="email" name="email"></p>`, "#1a1a1a")
}

func TestAuditInBrowser(t *testing.T) {
	needBrowser(t)
	var broken atomic.Bool
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, page(broken.Load()))
	}))
	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 1
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"a11y"}
	ctx := context.Background()
	mods := []check.Module{a11y.Module{}}
	run := func() *model.Run {
		t.Helper()
		r, err := runner.Run(ctx, runner.Options{Config: cfg, Target: srv.URL, Paths: []string{"/"}, Dir: t.TempDir(), Version: "test",
			Modules: mods, Browser: browser.Options{NoDownload: true}})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Pages) != 1 || r.Pages[0].Error != "" || len(r.Warnings) > 0 {
			t.Fatalf("pages %+v, warnings %v", r.Pages, r.Warnings)
		}
		return r
	}
	keys := func(r *model.Run) []string {
		var out []string
		for _, i := range r.Pages[0].Issues {
			out = append(out, i.Key)
		}
		slices.Sort(out)
		return out
	}

	fixed := run()
	if k := keys(fixed); len(k) > 0 {
		t.Errorf("the fixed page has violations: %v", fixed.Pages[0].Issues)
	}

	broken.Store(true)
	before, after := run(), run()
	want := []string{"a11y/color-contrast|#signup > button", `a11y/label|input[name="email"]`}
	if k := keys(before); !slices.Equal(k, want) {
		t.Errorf("broken page keys = %v, want %v", k, want)
	}
	for _, i := range before.Pages[0].Issues {
		t.Logf("%s %s [%s]: %s", i.Level, i.Title, i.Source, i.Detail)
		if i.Level != model.Warning || i.Title == "" || !strings.Contains(i.Fix, "dequeuniversity.com") {
			t.Errorf("issue = %+v", i)
		}
	}
	if !slices.Equal(keys(before), keys(after)) {
		t.Errorf("two runs of the same page differ: %v and %v", keys(before), keys(after))
	}
	same := compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: before, After: after}, mods, "test")
	if len(same.Changes) > 0 {
		t.Errorf("unchanged page: %+v", same.Changes)
	}

	// The theme update: the fixed page was the snapshot.
	got := compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: fixed, After: after}, mods, "test")
	var checks []string
	for _, c := range got.Changes {
		if c.Class != model.New || !slices.Equal(c.Pages, []string{"/"}) {
			t.Errorf("change = %+v", c)
		}
		checks = append(checks, c.Check)
	}
	slices.Sort(checks)
	if !slices.Equal(checks, []string{"a11y/color-contrast", "a11y/label"}) {
		t.Errorf("changes = %+v", got.Changes)
	}
}
