// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// manifest writes a scenarios.yaml with one scenario made from body and
// loads it.
func manifest(t *testing.T, body string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenarios.yaml")
	yaml := "sites:\n  php: http://localhost:18810\nscenarios:\n" + body
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadManifest(path)
	return err
}

func TestLoadManifestRejects(t *testing.T) {
	const ok = "    name: n\n    site: php\n    path: /\n    toggle: t\n"
	cases := map[string]struct{ body, want string }{
		"valid":               {"  - id: s01\n" + ok + "    expect: [{class: new, check: health/}]\n", ""},
		"no id":               {"  - name: n\n    site: php\n    path: /\n    toggle: t\n    expect: [{clean: true}]\n", "malformed"},
		"bad id":              {"  - id: x1\n" + ok + "    expect: [{clean: true}]\n", "malformed"},
		"duplicate id":        {"  - id: s01\n" + ok + "    expect: [{clean: true}]\n  - id: s01\n" + ok + "    expect: [{clean: true}]\n", "duplicate"},
		"unknown site":        {"  - id: s01\n    name: n\n    site: nope\n    path: /\n    toggle: t\n    expect: [{clean: true}]\n", "unknown site"},
		"no toggle":           {"  - id: s01\n    name: n\n    site: php\n    path: /\n    expect: [{clean: true}]\n", "no toggle"},
		"no expect":           {"  - id: s01\n" + ok, "no expect"},
		"two kinds":           {"  - id: s01\n" + ok + "    expect: [{clean: true, exit: 3}]\n", "exactly one"},
		"empty expect":        {"  - id: s01\n" + ok + "    expect: [{}]\n", "exactly one"},
		"unknown class":       {"  - id: s01\n" + ok + "    expect: [{class: unstable}]\n", "unknown class"},
		"pending class":       {"  - id: s01\n" + ok + "    pending: true\n    expect: [{class: unstable}]\n", ""},
		"forbid not a change": {"  - id: s01\n" + ok + "    expect: [{clean: true}]\n    forbid: [{exit: 1}]\n", "forbid"},
		"unknown harness":     {"  - id: s01\n" + ok + "    harness: wobble\n    expect: [{clean: true}]\n", "unknown harness"},
		"interrupt delay":     {"  - id: s01\n" + ok + "    harness: interrupt\n    expect: [{interrupted: true}]\n", "interrupt_after"},
		"misspelled field":    {"  - id: s01\n" + ok + "    expcet: [{clean: true}]\n", "expcet"},
	}
	for name, c := range cases {
		err := manifest(t, c.body)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: error %v, want one containing %q", name, err, c.want)
		}
	}
}

func intp(n int) *int { return &n }

func TestEvaluate(t *testing.T) {
	broke := model.Change{Class: model.Broke, Check: "health/status", Title: "Server error 503", Pages: []string{"/"}}
	changed := model.Change{Class: model.Changed, Check: "health/status", Title: "Server error 503"}
	visual := model.Change{Class: model.Changed, Check: "visual/page", Title: "Page looks different (desktop)"}
	cmp := func(w []string, cs ...model.Change) *model.Comparison {
		return &model.Comparison{Changes: cs, Warnings: w}
	}
	cases := []struct {
		name string
		s    Scenario
		obs  Observed
		want string
	}{
		{"change caught", Scenario{Expect: []Expect{{Class: model.Broke, Check: "health/status", Title: "503"}}}, Observed{Comparison: cmp(nil, broke)}, Caught},
		{"any class", Scenario{Expect: []Expect{{Check: "health/status"}}}, Observed{Comparison: cmp(nil, changed)}, Caught},
		{"other class", Scenario{Expect: []Expect{{Class: model.Broke, Check: "health/status"}}}, Observed{Comparison: cmp(nil, changed)}, OtherClass},
		{"change missed", Scenario{Expect: []Expect{{Class: model.Broke, Check: "health/status"}}}, Observed{Comparison: cmp(nil, visual)}, Missed},
		{"warning caught", Scenario{Expect: []Expect{{Warning: "screenshots from the snapshot are missing"}}},
			Observed{Comparison: cmp([]string{"visual: 4 screenshots from the snapshot are missing or unreadable"})}, Caught},
		{"warning missed", Scenario{Expect: []Expect{{Warning: "screenshots"}}}, Observed{Comparison: cmp([]string{"other"})}, Missed},
		{"interrupted", Scenario{Expect: []Expect{{Interrupted: true}}}, Observed{Comparison: cmp(nil), Interrupted: true}, Caught},
		{"not interrupted", Scenario{Expect: []Expect{{Interrupted: true}}}, Observed{Comparison: cmp(nil)}, Missed},
		{"clean", Scenario{Expect: []Expect{{Clean: true}}}, Observed{Comparison: cmp(nil, visual)}, Caught},
		{"not clean", Scenario{Expect: []Expect{{Clean: true}}}, Observed{Comparison: cmp(nil, broke)}, Missed},
		{"exit", Scenario{Expect: []Expect{{Exit: intp(3)}}}, Observed{Exit: 3}, Caught},
		{"other exit", Scenario{Expect: []Expect{{Exit: intp(3)}}}, Observed{Exit: 0, Comparison: cmp(nil)}, Missed},
		{"forbidden change", Scenario{Expect: []Expect{{Interrupted: true}}, Forbid: []Expect{{Class: model.Broke}}},
			Observed{Comparison: cmp(nil, broke), Interrupted: true}, FalseAlarm},
		{"forbid not hit", Scenario{Expect: []Expect{{Class: model.Changed, Check: "visual/"}}, Forbid: []Expect{{Class: model.Broke}}},
			Observed{Comparison: cmp(nil, visual)}, Caught},
	}
	for _, c := range cases {
		if got := Evaluate(c.s, c.obs).Outcome; got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestScoreboardPendingAndControl(t *testing.T) {
	m := &Manifest{Control: Control{Sites: []string{"php"}, Limit: 40}, Scenarios: []Scenario{{ID: "s01"}, {ID: "s02", Pending: true, Note: "waits for X"}}}
	outcomes := []Outcome{
		{Scenario: Scenario{ID: "s01", Name: "one", Module: "health", Expect: []Expect{{Class: "broke", Check: "health/"}}}, Soundcheck: Result{Outcome: Caught}},
		{Scenario: Scenario{ID: "s02", Name: "two", Module: "seo", Pending: true, Note: "waits for X", Expect: []Expect{{Check: "seo/og"}}}, Soundcheck: Result{Outcome: Missed}},
	}
	rounds := []ControlRound{
		{Site: "php", Round: 1, Pages: 9, Alarms: []string{"NEW health/js-error: JS error: x (/)"}},
		{Site: "php", Round: 2, Pages: 9},
	}
	md := scoreboard(m, outcomes, rounds, "rhythm-soundcheck 1.2.3", nil, nil, false)
	for _, want := range []string{
		"**Soundcheck caught 1 of 1 scenarios.** **False alarms: 1** in 4 compares of an unchanged site.",
		"1 more scenarios wait for detectors",
		"## Pending",
		"- **s02**: waits for X",
		"## Control pass",
		"| php | 2 | 4 | 9 | 1 | 0 |",
		"- NEW health/js-error: JS error: x (/) (1×)",
		"any seo/og",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("scoreboard lacks %q:\n%s", want, md)
		}
	}
	if i, j := strings.Index(md, "| s01 |"), strings.Index(md, "## Pending"); i < 0 || i > j {
		t.Error("the counted scenario isn't above the pending section")
	}
	if strings.Index(md, "| s02 |") < strings.Index(md, "## Pending") {
		t.Error("the pending scenario is in the counted table")
	}
}
