// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/runner"
)

// discover returns the paths a health-only run finds on base, up to limit.
func discover(t *testing.T, base string, limit int) []string {
	t.Helper()
	cfg := config.Default()
	cfg.Modules = []string{"health"}
	cfg.Viewports = []string{"desktop"}
	cfg.Pages.Limit = limit
	run, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: base, Dir: t.TempDir(), Version: "testbed",
		Modules: []check.Module{health.Module{}}, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range run.Pages {
		paths = append(paths, p.Path)
	}
	slices.Sort(paths)
	return paths
}

// TestGoodStateAudit is step zero (testbed/README.md): with every
// scenario good, the testbed's own audit (audit.go: axe-core with the
// WCAG 2.1 A and AA rules, placeholder labels, reflow and a Tab walk)
// finds nothing on any page of either site: the pages a run discovers
// and every scenario's own page. Set TESTBED_AUDIT_LOG=1 to list every
// finding instead of the first ones per page.
func TestGoodStateAudit(t *testing.T) {
	dir, m := setup(t)
	a, err := newAuditor(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.walk = 400 // the catalog has 320 links and must still wrap round
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := browser.NewTab(sess)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tab.Close() }()
	for _, site := range []string{"wp", "php"} {
		base := m.Sites[site]
		// What a run discovers, plus every scenario's own page.
		paths := discover(t, base, 60)
		for _, s := range m.Scenarios {
			if s.Site == site && !slices.Contains(paths, s.Path) {
				paths = append(paths, s.Path)
			}
		}
		total := 0
		for _, p := range paths {
			start := time.Now()
			found, err := a.Audit(context.Background(), tab, strings.TrimRight(base, "/")+p, p)
			if err != nil {
				t.Errorf("%s %s: %v", site, p, err)
				continue
			}
			total += len(found)
			t.Logf("%s %s: %d findings in %s", site, p, len(found), time.Since(start).Round(time.Millisecond))
			for i, f := range found {
				if i < 5 || os.Getenv("TESTBED_AUDIT_LOG") != "" {
					t.Errorf("%s %s", site, f)
				}
			}
		}
		t.Logf("%s: %d pages, %d findings", site, len(paths), total)
	}
}

// TestAuditPage audits the one page TESTBED_AUDIT_URL names and lists
// what the testbed's audit finds there, for working on a fixture.
func TestAuditPage(t *testing.T) {
	url := os.Getenv("TESTBED_AUDIT_URL")
	if url == "" {
		t.Skip("set TESTBED_AUDIT_URL to audit one page")
	}
	dir, err := Testbed()
	if err != nil {
		t.Fatal(err)
	}
	a, err := newAuditor(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := browser.NewTab(sess)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tab.Close() }()
	start := time.Now()
	found, err := a.Audit(context.Background(), tab, url, url)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Log(f)
	}
	t.Logf("%d findings in %s", len(found), time.Since(start).Round(time.Millisecond))
}

// TestPreflight switches every accessibility scenario good and broken
// and checks its page does what scenarios.yaml says (preflight.go): the
// plain-PHP pages' self-tests pass in both states.
func TestPreflight(t *testing.T) {
	dir, m := setup(t)
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := browser.NewTab(sess)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tab.Close() }()
	results, err := preflight(context.Background(), dir, m.Sites, tab, wpLogin(m.Sites["wp"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if !r.OK() {
			t.Errorf("%s, want %s", r, map[bool]string{true: r.Probe.Good, false: r.Probe.Broken}[r.State == "good"])
		} else {
			t.Log(r)
		}
	}
}

