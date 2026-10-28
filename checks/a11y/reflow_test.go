// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
)

// widthProbe is a Live module that runs after a11y and notes the
// viewport width it finds the page at.
type widthProbe struct {
	mu     *sync.Mutex
	widths map[string]int
}

func (widthProbe) Name() string { return "probe" }

func (w widthProbe) Live(_ context.Context, _ *check.Context, tab *rod.Page, p *model.Page) {
	res, err := tab.Eval(`() => window.innerWidth`)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.widths[p.Path] = res.Value.Int()
}

const narrow = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>Shop</title><style>body{margin:0;font:16px sans-serif}</style></head><body><main><h1>Shop</h1>%s<p>Welcome.</p></main></body></html>`

func reflowOn(p *model.Page) []model.Issue {
	var out []model.Issue
	for _, i := range p.Issues {
		if i.Check == "a11y/reflow" {
			out = append(out, i)
		}
	}
	return out
}

func TestReflow(t *testing.T) {
	needBrowser(t)
	srv := pages(t, map[string]string{
		"/promo/": fmt.Sprintf(narrow, `<div id="promo" style="width:600px;background:#ffe">Free delivery this week on every order</div>`),
		"/table/": fmt.Sprintf(narrow, `<div style="overflow-x:auto"><table style="width:900px"><tr><td>Size</td><td>Price</td></tr></table></div>
<pre style="width:700px">wide code</pre><div data-soundcheck-2d style="width:800px;height:40px">map</div>`),
		"/offcanvas/":  fmt.Sprintf(narrow, `<nav id="menu" style="position:absolute;top:0;left:0;width:280px;transform:translateX(400px);visibility:hidden"><a href="/">Home</a></nav>`),
		"/responsive/": fmt.Sprintf(narrow, `<div style="max-width:100%;width:600px">Free delivery this week</div><img alt="" style="max-width:100%" width="900" height="10" src="data:image/gif;base64,R0lGODlhAQABAAAAACw=">`),
	})
	run := func(version string) (*model.Run, map[string]int) {
		t.Helper()
		cfg := config.Default()
		cfg.Rate, cfg.Tabs = 0, 1
		cfg.Viewports = []string{"desktop"}
		cfg.Modules = []string{"a11y"}
		probe := widthProbe{mu: &sync.Mutex{}, widths: map[string]int{}}
		r, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: srv.URL, Dir: t.TempDir(), Version: "test",
			Paths: []string{"/promo/", "/table/", "/offcanvas/", "/responsive/"}, WCAG: version,
			Modules: []check.Module{a11y.Module{}, probe}, Browser: browser.Options{NoDownload: true}})
		if err != nil {
			t.Fatal(err)
		}
		return r, probe.widths
	}
	r, widths := run("2.1")
	got := reflowOn(r.Page("/promo/"))
	if len(got) != 1 {
		t.Fatalf("/promo/: %+v", r.Page("/promo/").Issues)
	}
	i := got[0]
	if i.Source != "#promo" || i.Level != model.Warning || i.Title != "Page scrolls sideways at 320 px wide" ||
		i.Detail != "At 320 CSS px the page is 600 px wide; #promo sticks out." || !slices.Equal(i.WCAG, []string{"1.4.10"}) ||
		!slices.Equal(i.EN301549, []string{"9.1.4.10"}) || i.Impact != "serious" {
		t.Errorf("issue = %+v", i)
	}
	for _, path := range []string{"/table/", "/offcanvas/", "/responsive/"} {
		if got := reflowOn(r.Page(path)); len(got) > 0 {
			t.Errorf("%s: %+v", path, got)
		}
	}
	// The modules after a11y see the page at the desktop width again.
	for path, w := range widths {
		if w != 1366 {
			t.Errorf("%s: the next module found the page %d px wide", path, w)
		}
	}
	if len(widths) != 4 {
		t.Errorf("widths = %v", widths)
	}
	if !slices.Contains(r.Options.A11y.Rules, "reflow") {
		t.Errorf("rules don't list reflow: %v", r.Options.A11y.Rules)
	}
	if len(r.Warnings) > 0 {
		t.Errorf("warnings = %q", r.Warnings)
	}

	// WCAG 2.0 has no 1.4.10.
	r20, _ := run("2.0")
	if got := reflowOn(r20.Page("/promo/")); len(got) > 0 || slices.Contains(r20.Options.A11y.Rules, "reflow") {
		t.Errorf("2.0: %+v, rules %v", got, r20.Options.A11y.Rules)
	}
}

// When one of the module's own checks can't finish, the page's audit
// isn't ok: its problems may be missing, so a comparison mustn't call
// them FIXED. What axe found is kept.
func TestOwnCheckFailureLeavesAuditUnfinished(t *testing.T) {
	needBrowser(t)
	srv := pages(t, map[string]string{
		"/promo/": fmt.Sprintf(narrow, `<div id="promo" style="width:600px">Free delivery</div><input id="q" type="text">`),
	})
	run := func() *model.Run {
		t.Helper()
		cfg := config.Default()
		cfg.Rate, cfg.Tabs = 0, 1
		cfg.Viewports = []string{"desktop"}
		cfg.Modules = []string{"a11y"}
		r, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: srv.URL, Dir: t.TempDir(), Version: "test",
			Paths: []string{"/promo/"}, WCAG: "2.1", Modules: []check.Module{a11y.Module{}}, Browser: browser.Options{NoDownload: true}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	hasLabel := func(p *model.Page) bool {
		return slices.ContainsFunc(p.Issues, func(i model.Issue) bool { return i.Check == "a11y/label" })
	}

	// Positive: the run finishes and the audit is ok.
	r := run()
	if a := r.Page("/promo/").Audits["a11y"]; a.Status != model.AuditOK {
		t.Fatalf("clean run: audit %+v", a)
	}

	restore := a11y.SetReflowTimeout(time.Nanosecond)
	r = run()
	restore()
	p := r.Page("/promo/")
	if a := p.Audits["a11y"]; a.Status != model.AuditError || a.Reason != "reflow check at 320 px: timed out after 1ns" {
		t.Errorf("reflow failed: audit %+v, want a plain timed out reason", a)
	}
	if !hasLabel(p) {
		t.Errorf("axe's issues were dropped: %+v", p.Issues)
	}

	restore = a11y.SetFieldsJS(`() => { throw new Error("fields broke"); }`)
	r = run()
	restore()
	if a := r.Page("/promo/").Audits["a11y"]; a.Status != model.AuditError || !strings.Contains(a.Reason, "placeholder check: ") {
		t.Errorf("placeholder check failed: audit %+v", a)
	}
}
