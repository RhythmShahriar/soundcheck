// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
	"github.com/RhythmShahriar/soundcheck/store"
)

// These tests need the testbed running (make -C testbed up) and Chrome:
//
//	go test -tags testbed -v ./testbed/bench/
//
// They skip when either is missing.

// setup loads the manifest and skips unless the sites answer.
func setup(t *testing.T) (string, *Manifest) {
	t.Helper()
	dir, err := Testbed()
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir + "/scenarios.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range m.Sites {
		resp, err := http.Get(base) //nolint:gosec,noctx // the local testbed
		if err != nil {
			t.Skipf("testbed isn't running (make -C testbed up): %v", err)
		}
		_ = resp.Body.Close()
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	t.Cleanup(func() { _ = Toggle(dir, "all", "good") })
	if err := Toggle(dir, "all", "good"); err != nil {
		t.Fatal(err)
	}
	return dir, m
}

// healthScenarios are the scenarios the health module alone should catch.
var healthScenarios = []string{"s01", "s02", "s03", "s04", "s05", "s06", "s07", "s08", "s09", "s10", "s16", "s17"}

// TestHealthScenarios runs snapshot → break → compare with the health
// module through the runner and compare packages, the way the CLI will,
// and checks each scenario's expectation from scenarios.yaml. s15 is
// included because health's "not responsive" check can catch it when
// screenshots are taken.
func TestHealthScenarios(t *testing.T) {
	dir, m := setup(t)
	scenarios, err := m.Pick(append(slices.Clone(healthScenarios), "s15"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	modules := []check.Module{health.Module{}}
	score, total := 0, 0
	var board []string
	for _, s := range scenarios {
		cfg := config.Default()
		cfg.Modules = []string{"health"}
		cfg.Viewports = []string{"desktop"}
		cfg.Pages.Include = s.Include
		cfg.Pages.Limit = 20
		if s.ID == "s15" {
			// Screenshots (the visual module's capture) give health the
			// rendered mobile width.
			cfg.Modules = []string{"health", "visual"}
			cfg.Viewports = []string{"desktop", "mobile"}
		}
		res, err := runScenario(ctx, dir, m, s, cfg, modules, t.TempDir())
		if err != nil {
			t.Fatalf("%s: %v", s.ID, err)
		}
		line := fmt.Sprintf("%s %-55s %-12s %s%s", s.ID, s.Name, res.Outcome, res.Found, res.Detail)
		if len(res.Others) > 0 {
			line += "  [also: " + strings.Join(res.Others, "; ") + "]"
		}
		board = append(board, line)
		if slices.Contains(healthScenarios, s.ID) {
			total++
			if res.Outcome == Caught {
				score++
			} else {
				t.Errorf("%s (%s): %s; expected %v", s.ID, s.Name, res.Outcome, s.Expect)
			}
		}
	}
	t.Logf("health scenarios caught: %d/%d\n%s", score, total, strings.Join(board, "\n"))
}

// runScenario snapshots the good state, breaks the scenario, re-checks the
// snapshot's pages and compares.
func runScenario(ctx context.Context, dir string, m *Manifest, s Scenario, cfg config.Config, modules []check.Module, root string) (Result, error) {
	defer func() { _ = Toggle(dir, s.ID, "good") }()
	st := store.Store{Root: root}
	opts := func(name, d string, snap bool, paths []string) runner.Options {
		return runner.Options{Config: cfg, Target: m.URL(s), Snapshot: snap, Name: name, Dir: d, Paths: paths,
			Version: "testbed", Modules: modules, Browser: browser.Options{NoDownload: true}}
	}
	if err := Toggle(dir, s.ID, "good"); err != nil {
		return Result{}, err
	}
	snapDir, err := st.NewDir("snapshots", "good")
	if err != nil {
		return Result{}, err
	}
	before, err := runner.Run(ctx, opts("good", snapDir, true, nil))
	if err != nil {
		return Result{}, err
	}
	var paths []string
	for _, p := range before.Pages {
		paths = append(paths, p.Path)
	}

	if err := Toggle(dir, s.ID, "broken"); err != nil {
		return Result{}, err
	}
	runDir, err := st.NewDir("runs", "broken")
	if err != nil {
		return Result{}, err
	}
	after, err := runner.Run(ctx, opts("", runDir, false, paths))
	if err != nil {
		return Result{}, err
	}
	cmp := compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: before, After: after,
		BeforeDir: snapDir, AfterDir: runDir}, modules, "testbed")
	res := Evaluate(s, cmp)
	if res.Outcome != Caught {
		// What the broken run saw, to tell a testbed problem from a check's blind spot.
		for _, p := range after.Pages {
			var loaded []string
			for _, r := range p.Resources {
				loaded = append(loaded, fmt.Sprintf("%s %d", r.URL, r.Status))
			}
			res.Detail += fmt.Sprintf(" %s: status %d, %d chars, resources %v;", p.Path, p.Status, p.TextLength, loaded)
		}
	}
	return res, nil
}

// TestGoodStateIsClean checks that, with every scenario good, the health
// module finds nothing above a notice on any scenario page, so a caught
// scenario is caught because of its broken state alone.
func TestGoodStateIsClean(t *testing.T) {
	_, m := setup(t)
	cfg := config.Default()
	cfg.Modules = []string{"health"}
	cfg.Viewports = []string{"desktop"}
	cfg.Pages.Limit = 60
	for site, base := range m.Sites {
		run, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: base, Dir: t.TempDir(), Version: "testbed",
			Modules: []check.Module{health.Module{}}, Browser: browser.Options{NoDownload: true}})
		if err != nil {
			t.Fatal(err)
		}
		if len(run.Pages) < 3 {
			t.Errorf("%s: only %d pages discovered", site, len(run.Pages))
		}
		for _, i := range run.AllIssues() {
			if i.Level != model.Notice {
				t.Errorf("%s %v: %s %s", site, i.Pages, i.Level, i.Title)
			}
		}
		t.Logf("%s: %d pages, clean", site, len(run.Pages))
	}
}

// TestClickScenarios drives the pages health can't judge (s12, s13) in
// the browser, to prove their good and broken states behave as the
// manifest says, until the interact module can be benchmarked on them.
func TestClickScenarios(t *testing.T) {
	dir, m := setup(t)
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// click loads url, clicks selector and returns JS run afterwards plus
	// any uncaught exception.
	click := func(url, selector, after string) (string, string) {
		tab, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tab.Close() }()
		var (
			mu        sync.Mutex
			exception string
		)
		go tab.EachEvent(func(e *proto.RuntimeExceptionThrown) {
			mu.Lock()
			exception = e.ExceptionDetails.Exception.Description
			mu.Unlock()
		})()
		tab = tab.Timeout(20 * time.Second)
		if err := tab.Navigate(url); err != nil {
			t.Fatal(err)
		}
		tab.MustWaitLoad()
		tab.MustElement(selector).MustClick()
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return tab.MustEval(after).String(), firstLine(exception)
	}
	s12, _ := m.Pick([]string{"s12", "s13"})
	for _, state := range []string{"good", "broken"} {
		if err := Toggle(dir, "s12", state); err != nil {
			t.Fatal(err)
		}
		got, _ := click(m.URL(s12[0]), ".menu-toggle", `() => document.getElementById('browse-panel').hidden + ' ' + document.querySelector('.menu-toggle').getAttribute('aria-expanded')`)
		want := map[string]string{"good": "false true", "broken": "true false"}[state]
		if got != want {
			t.Errorf("s12 %s: panel hidden/expanded = %q, want %q", state, got, want)
		}
		if err := Toggle(dir, "s13", state); err != nil {
			t.Fatal(err)
		}
		text, exc := click(m.URL(s12[1]), ".tb-wishlist", `() => document.querySelector('.tb-wishlist-status').textContent`)
		switch state {
		case "good":
			if text != "Added to your wishlist." || exc != "" {
				t.Errorf("s13 good: status %q, exception %q", text, exc)
			}
		case "broken":
			if !strings.Contains(exc, "WishlistStore is not defined") || text != "" {
				t.Errorf("s13 broken: status %q, exception %q", text, exc)
			}
		}
		t.Logf("%s: s12 panel %q; s13 status %q, exception %q", state, got, text, exc)
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}
