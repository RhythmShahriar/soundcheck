// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
)

// ownTab is a module that opens a tab of its own after the pages, as the
// cache leak test and the flows do. It loads url in it, guarded with
// check.GuardTab when guard is set.
type ownTab struct {
	url   string
	guard bool
	err   error
}

func (*ownTab) Name() string { return "own-tab" }

func (m *ownTab) CheckSite(ctx context.Context, cc *check.Context) {
	sess, err := cc.NewSession()
	if err != nil {
		m.err = err
		return
	}
	defer func() { _ = sess.Close() }()
	tab, err := browser.NewTab(sess)
	if err != nil {
		m.err = err
		return
	}
	defer func() { _ = tab.Close() }()
	if m.guard {
		release, err := check.GuardTab(ctx, tab)
		if err != nil {
			m.err = err
			return
		}
		defer release()
	}
	tab = tab.Context(ctx).Timeout(15 * time.Second)
	if err := tab.Navigate(m.url); err == nil {
		_ = tab.WaitLoad()
	}
}

// A tab a module opens itself is kept in the run's network scope when
// the module guards it: a public site's page can't make it load a
// neighbor on the local network. Unguarded, the same tab would.
func TestGuardTabKeepsModuleTabsInScope(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome")
	}
	frame := listen(t, 36901, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Router admin</title>`)
	})
	site := listen(t, 36900, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<!doctype html><html lang="en"><title>Shop</title><h1>Shop</h1>`)
		case "/own/":
			fmt.Fprintf(w, `<!doctype html><html lang="en"><title>Own</title><iframe src="%s/admin"></iframe>`, frame.url)
		default:
			http.NotFound(w, r)
		}
	})
	pretendPublic(t)
	run := func(guard bool) {
		t.Helper()
		m := &ownTab{url: site.url + "/own/", guard: guard}
		cfg := config.Default()
		cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Pages.Limit = []string{"own-tab"}, []string{"desktop"}, 0, 1
		if _, err := Run(context.Background(), Options{Config: cfg, Target: site.url, Paths: []string{"/"}, Dir: t.TempDir(),
			Version: "test", Modules: []check.Module{m}, Browser: browser.Options{NoDownload: true}}); err != nil {
			t.Fatal(err)
		}
		if m.err != nil {
			t.Fatal(m.err)
		}
	}
	run(true)
	if n := frame.n.Load(); n != 0 {
		t.Errorf("a guarded tab reached a local neighbor %d times", n)
	}
	run(false)
	if frame.n.Load() == 0 {
		t.Error("the unguarded tab didn't load the neighbor, so the test proves nothing")
	}
}
