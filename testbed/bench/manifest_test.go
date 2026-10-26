// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/rules"
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
		"valid":                {"  - id: s01\n" + ok + "    expect: [{class: new, check: health/}]\n", ""},
		"no id":                {"  - name: n\n    site: php\n    path: /\n    toggle: t\n    expect: [{clean: true}]\n", "malformed"},
		"bad id":               {"  - id: x1\n" + ok + "    expect: [{clean: true}]\n", "malformed"},
		"duplicate id":         {"  - id: s01\n" + ok + "    expect: [{clean: true}]\n  - id: s01\n" + ok + "    expect: [{clean: true}]\n", "duplicate"},
		"unknown site":         {"  - id: s01\n    name: n\n    site: nope\n    path: /\n    toggle: t\n    expect: [{clean: true}]\n", "unknown site"},
		"no toggle":            {"  - id: s01\n    name: n\n    site: php\n    path: /\n    expect: [{clean: true}]\n", "no toggle"},
		"no expect":            {"  - id: s01\n" + ok, "no expect"},
		"two kinds":            {"  - id: s01\n" + ok + "    expect: [{clean: true, exit: 3}]\n", "exactly one"},
		"empty expect":         {"  - id: s01\n" + ok + "    expect: [{}]\n", "exactly one"},
		"unknown class":        {"  - id: s01\n" + ok + "    expect: [{class: wobbly}]\n", "unknown class"},
		"pending class":        {"  - id: s01\n" + ok + "    pending: true\n    expect: [{class: wobbly}]\n", ""},
		"merged class":         {"  - id: s01\n" + ok + "    expect: [{class: unstable}]\n", ""},
		"forbid not a change":  {"  - id: s01\n" + ok + "    expect: [{clean: true}]\n    forbid: [{exit: 1}]\n", "forbid"},
		"unknown harness":      {"  - id: s01\n" + ok + "    harness: wobble\n    expect: [{clean: true}]\n", "unknown harness"},
		"interrupt delay":      {"  - id: s01\n" + ok + "    harness: interrupt\n    expect: [{interrupted: true}]\n", "interrupt_after"},
		"misspelled field":     {"  - id: s01\n" + ok + "    expcet: [{clean: true}]\n", "expcet"},
		"wp-update":            {"  - id: s01\n" + ok + "    harness: wp-update\n    expect: [{culprit: testbed-update-07, compares: 4}]\n", ""},
		"culprit elsewhere":    {"  - id: s01\n" + ok + "    expect: [{culprit: testbed-update-07}]\n", "only harness wp-update"},
		"wp-update change":     {"  - id: s01\n" + ok + "    harness: wp-update\n    expect: [{class: broke}]\n", "only harness wp-update"},
		"compares alone":       {"  - id: s01\n" + ok + "    harness: wp-update\n    expect: [{compares: 4}]\n", "plugin slug"},
		"negative compares":    {"  - id: s01\n" + ok + "    harness: wp-update\n    expect: [{culprit: p, compares: -1}]\n", "negative"},
		"culprit and clean":    {"  - id: s01\n" + ok + "    harness: wp-update\n    expect: [{culprit: p, clean: true}]\n", "exactly one"},
		"wp-update forbid":     {"  - id: s01\n" + ok + "    harness: wp-update\n    expect: [{culprit: p}]\n    forbid: [{class: broke}]\n", "forbid"},
		"harness list":         {"  - id: s01\n" + ok + "    harness: [statement, requests, orders, canaries]\n    says: [frame]\n    expect: [{clean: true}]\n", ""},
		"unknown in list":      {"  - id: s01\n" + ok + "    harness: [statement, wobble]\n    expect: [{clean: true}]\n", "unknown harness"},
		"wp-update and more":   {"  - id: s01\n" + ok + "    harness: [wp-update, orders]\n    expect: [{culprit: p}]\n", "runs alone"},
		"says alone":           {"  - id: s01\n" + ok + "    says: [frame]\n    expect: [{clean: true}]\n", "says needs harness statement"},
		"pending lane":         {"  - id: s01\n" + ok + "    pending: keyboard\n    expect: [{class: new, check: keyboard/trap}]\n", ""},
		"pending false":        {"  - id: s01\n" + ok + "    pending: false\n    expect: [{clean: true}]\n", ""},
		"pending wobble":       {"  - id: s01\n" + ok + "    pending: wobble\n    expect: [{clean: true}]\n", "isn't true or a lane"},
		"source matcher":       {"  - id: s01\n" + ok + "    expect: [{source: \" >>> \"}]\n    forbid: [{check: keyboard/trap, source: \"#news\"}]\n", ""},
		"count":                {"  - id: s01\n" + ok + "    expect: [{class: new, check: a11y/label, count: 3}]\n", ""},
		"count without change": {"  - id: s01\n" + ok + "    expect: [{exit: 3, count: 2}]\n", "count"},
		"negative count":       {"  - id: s01\n" + ok + "    expect: [{check: a11y/, count: -1}]\n", "count"},
		"negative within":      {"  - id: s01\n" + ok + "    within: -1s\n    expect: [{clean: true}]\n", "negative"},
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
	label := func(src string) model.Change {
		return model.Change{Class: model.New, Check: "a11y/label", Title: "Form elements must have labels", Source: src}
	}
	shadow := label("tb-card-field >>> #card")
	trap := func(src string) model.Change {
		return model.Change{Class: model.Broke, Check: "keyboard/trap", Title: "Keyboard focus is trapped", Source: src}
	}
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
		{"source", Scenario{Expect: []Expect{{Class: model.New, Check: "a11y/label", Source: " >>> "}}}, Observed{Comparison: cmp(nil, shadow)}, Caught},
		{"source differs", Scenario{Expect: []Expect{{Class: model.New, Check: "a11y/label", Source: " >>> "}}}, Observed{Comparison: cmp(nil, label("#email"))}, Missed},
		{"count met", Scenario{Expect: []Expect{{Class: model.New, Check: "a11y/label", Count: 3}}},
			Observed{Comparison: cmp(nil, label("#name"), label("#email"), label("#address"))}, Caught},
		{"count short", Scenario{Expect: []Expect{{Class: model.New, Check: "a11y/label", Count: 3}}},
			Observed{Comparison: cmp(nil, label("#name"), label("#email"))}, OtherClass},
		{"all held", Scenario{All: true, Expect: []Expect{{Class: model.Broke, Check: "keyboard/trap"}, {Exit: intp(1)}}},
			Observed{Comparison: cmp(nil, trap("#tb_coupon")), Exit: 1}, Caught},
		{"all, exit differs", Scenario{All: true, Expect: []Expect{{Class: model.Broke, Check: "keyboard/trap"}, {Exit: intp(1)}}},
			Observed{Comparison: cmp(nil, trap("#tb_coupon")), Exit: 0}, OtherClass},
		{"all, warning missing", Scenario{All: true, Expect: []Expect{{Exit: intp(3)}, {Warning: "not checked"}}},
			Observed{Comparison: cmp(nil), Exit: 3}, Missed},
		{"all, both", Scenario{All: true, Expect: []Expect{{Exit: intp(3)}, {Warning: "not checked"}}},
			Observed{Comparison: cmp([]string{"not checked: /a11y/big.php (a11y: axe-core timed out after 45s); its issues are kept from the snapshot, not counted as fixed"}), Exit: 3}, Caught},
		{"forbidden source", Scenario{Expect: []Expect{{Check: "keyboard/trap"}}, Forbid: []Expect{{Check: "keyboard/trap", Source: "#news"}}},
			Observed{Comparison: cmp(nil, trap("#news-email"))}, FalseAlarm},
		{"other source allowed", Scenario{Expect: []Expect{{Check: "keyboard/trap"}}, Forbid: []Expect{{Check: "keyboard/trap", Source: "#news"}}},
			Observed{Comparison: cmp(nil, trap("#tb_coupon"))}, Caught},
	}
	for _, c := range cases {
		if got := Evaluate(c.s, c.obs).Outcome; got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// A scenario Soundcheck wasn't run on (it needs Echo) isn't counted as
// missed: the score leaves it out and says so.
func TestScoreboardNotRun(t *testing.T) {
	m := &Manifest{Scenarios: []Scenario{{ID: "s01"}, {ID: "s33", Echo: true}}}
	outcomes := []Outcome{
		{Scenario: Scenario{ID: "s01", Name: "one", Module: "health", Expect: []Expect{{Class: "broke", Check: "health/"}}}, Soundcheck: Result{Outcome: Caught}},
		{Scenario: Scenario{ID: "s33", Name: "mail", Module: "echo", Echo: true, Expect: []Expect{{Class: "broke", Check: "echo/"}}},
			Soundcheck: Result{Outcome: NotRun, Detail: "needs Echo"}},
	}
	if sc := score(outcomes); sc.caught != 1 || sc.total != 1 || !slices.Equal(sc.notRun, []string{"s33"}) {
		t.Errorf("score = %+v", sc)
	}
	md := scoreboard(Board{Manifest: m, Outcomes: outcomes, Version: "rhythm-soundcheck 1.2.3"})
	for _, want := range []string{"**Soundcheck caught 1 of 1 scenarios.**", "Not run and not counted: s33 (needs Echo)", "| s33 | mail |"} {
		if !strings.Contains(md, want) {
			t.Errorf("scoreboard lacks %q:\n%s", want, md)
		}
	}
}

func TestScoreboardPendingAndControl(t *testing.T) {
	m := &Manifest{Control: Control{Sites: []string{"php"}, Limit: 40}, Scenarios: []Scenario{{ID: "s01"}, {ID: "s02", Pending: "true", Note: "waits for X"}}}
	outcomes := []Outcome{
		{Scenario: Scenario{ID: "s01", Name: "one", Module: "health", Expect: []Expect{{Class: "broke", Check: "health/"}}}, Soundcheck: Result{Outcome: Caught}},
		{Scenario: Scenario{ID: "s02", Name: "two", Module: "seo", Pending: "true", Note: "waits for X", Expect: []Expect{{Check: "seo/og"}}}, Soundcheck: Result{Outcome: Missed}},
	}
	rounds := []ControlRound{
		{Site: "php", Round: 1, Pages: 9, Alarms: []string{"NEW health/js-error: JS error: x (/)"}},
		{Site: "php", Round: 2, Pages: 9},
	}
	md := scoreboard(Board{Manifest: m, Outcomes: outcomes, Rounds: rounds, Version: "rhythm-soundcheck 1.2.3"})
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

func TestExpectString(t *testing.T) {
	cases := map[string]Expect{
		"BROKE health/status “503”":         {Class: "broke", Check: "health/status", Title: "503"},
		"BROKE":                             {Class: "broke"},
		"any seo/og":                        {Check: "seo/og"},
		"warning “captcha”":                 {Warning: "captcha"},
		"marked interrupted":                {Interrupted: true},
		"nothing at or above NEW":           {Clean: true},
		"exit 3":                            {Exit: intp(3)},
		"culprit p07 in at most 4 compares": {Culprit: "p07", Compares: 4},
		"culprit p07 in at most 1 compare":  {Culprit: "p07", Compares: 1},
		"culprit p07":                       {Culprit: "p07"},
		"NEW a11y/label in #card (3×)":      {Class: "new", Check: "a11y/label", Source: "#card", Count: 3},
	}
	for want, e := range cases {
		if got := e.String(); got != want {
			t.Errorf("%+v: %q, want %q", e, got, want)
		}
	}
}

// wp-update's progress, as package wpupdate printed it on the testbed
// (one-by-one with a failed update added).
const (
	bisectOutput = `Snapshot wp-update-2026-10-24T17-36-02 of http://localhost:18800/contact/
Updating 10 plugins
All 10 updated: broke
Rolling back testbed-update-03 to 1.0
Rolling back testbed-update-05 to 1.0
Rolling back testbed-update-01 to 1.0
Rolling back testbed-update-06 to 1.0
Rolling back testbed-update-04 to 1.0
  testbed-update-08, testbed-update-02, testbed-update-10, testbed-update-07, testbed-update-09: broke
Rolling back testbed-update-10 to 1.0
Rolling back testbed-update-07 to 1.0
Rolling back testbed-update-09 to 1.0
  testbed-update-08, testbed-update-02: clean
  testbed-update-10: clean
  testbed-update-07: broke
Culprit: testbed-update-07 1.0 → 1.1 (Server error 500 on /contact/), found in 4 compares
Rolling back testbed-update-07 to 1.0
Without testbed-update-07: clean
`
	oneByOneOutput = `Snapshot wp-update-2026-10-24T17-37-10 of http://localhost:18800/contact/
[1/10] testbed-update-08 1.0 → 1.1: clean
[2/10] testbed-update-02 1.0 → 1.1: clean
[3/10] testbed-update-10: update failed: wp plugin update: disk full
[4/10] testbed-update-07 1.0 → 1.1: broke: Server error 500 on /contact/
Rolling back testbed-update-07 to 1.0
[5/10] testbed-update-09 1.0 → 1.1: clean
`
)

func TestParseWPUpdate(t *testing.T) {
	cases := map[string]struct {
		out  string
		want []Culprit
	}{
		"bisect":      {bisectOutput, []Culprit{{Slug: "testbed-update-07", Old: "1.0", New: "1.1", Evidence: "Server error 500 on /contact/", Compares: 4}}},
		"one by one":  {oneByOneOutput, []Culprit{{Slug: "testbed-update-07", Old: "1.0", New: "1.1", Evidence: "Server error 500 on /contact/", Compares: 4}}},
		"one compare": {"Culprit: p01 1.0 → 1.1 (x (y)), found in 1 compare\n", []Culprit{{Slug: "p01", Old: "1.0", New: "1.1", Evidence: "x (y)", Compares: 1}}},
		"nothing":     {"No plugin updates available.\n", nil},
		"clean only":  {"[1/1] p01 1.0 → 1.1: clean\n", nil},
	}
	for name, c := range cases {
		got := parseWPUpdate(c.out)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
}

func TestEvaluateWPUpdate(t *testing.T) {
	s := Scenario{Harness: Harnesses{"wp-update"}, Expect: []Expect{{Culprit: "p07", Compares: 4}}}
	p07 := func(n int) Culprit { return Culprit{Slug: "p07", Old: "1.0", New: "1.1", Compares: n} }
	cases := []struct {
		name     string
		culprits []Culprit
		want     string
		found    string
	}{
		{"caught", []Culprit{p07(4)}, Caught, "p07 1.0 → 1.1, found in 4 compares"},
		{"too many compares", []Culprit{p07(5)}, Missed, "p07 1.0 → 1.1, found in 5 compares; want at most 4"},
		{"none", nil, Missed, ""},
		{"wrong plugin", []Culprit{{Slug: "p03", Old: "1.0", New: "1.1", Compares: 2}}, FalseAlarm, "blamed p03 1.0 → 1.1, found in 2 compares"},
		{"right and wrong", []Culprit{p07(3), {Slug: "p03", Compares: 1}}, FalseAlarm, ""},
	}
	for _, c := range cases {
		got := EvaluateWPUpdate(s, c.culprits, 1)
		if got.Outcome != c.want || c.found != "" && got.Found != c.found {
			t.Errorf("%s: %s %q, want %s %q", c.name, got.Outcome, got.Found, c.want, c.found)
		}
	}
	if got := EvaluateWPUpdate(Scenario{Expect: []Expect{{Culprit: "p07"}}}, []Culprit{p07(9)}, 1); got.Outcome != Caught {
		t.Errorf("no compare budget: %s, want caught", got.Outcome)
	}
}

func TestShellJoin(t *testing.T) {
	got := shellJoin([]string{"docker", "compose", "-f", "/tmp/my testbed/docker-compose.yml", "it's", ""})
	want := `docker compose -f '/tmp/my testbed/docker-compose.yml' 'it'\''s' ''`
	if got != want {
		t.Errorf("shellJoin: %s, want %s", got, want)
	}
}

// TestTestbedRules loads every rule the scenarios copy into a run and runs
// its samples, so a scenario can't fail on a rule file that doesn't load.
func TestTestbedRules(t *testing.T) {
	dir := filepath.Join("..", "rules")
	if _, err := health.LoadRules(dir); err != nil {
		t.Fatalf("load %s: %v", dir, err)
	}
	rep, err := rules.Test(dir, health.Checks...)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rules == 0 || len(rep.Failures) > 0 {
		t.Errorf("%d rules, failures: %v", rep.Rules, rep.Failures)
	}
}

func TestHarnessesYAML(t *testing.T) {
	var s struct {
		One  Harnesses `yaml:"one"`
		Many Harnesses `yaml:"many"`
		None Harnesses `yaml:"none"`
	}
	if err := yaml.Unmarshal([]byte("one: stress\nmany: [statement, orders]\n"), &s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.One, Harnesses{"stress"}) || !slices.Equal(s.Many, Harnesses{"statement", "orders"}) || s.None != nil {
		t.Errorf("%+v", s)
	}
	if !s.Many.Has("orders") || s.Many.Has("stress") || s.None.Has("") {
		t.Error("Has")
	}
}

func TestPending(t *testing.T) {
	for in, want := range map[string]struct {
		is   bool
		lane string
	}{"true": {true, ""}, "false": {false, ""}, "keyboard": {true, "keyboard"}, `""`: {false, ""}} {
		var v struct {
			P Pending `yaml:"p"`
		}
		if err := yaml.Unmarshal([]byte("p: "+in), &v); err != nil {
			t.Fatal(err)
		}
		if v.P.Is() != want.is || v.P.Lane() != want.lane {
			t.Errorf("pending: %s: is %v lane %q", in, v.P.Is(), v.P.Lane())
		}
	}
}

func TestPickEAA(t *testing.T) {
	m := &Manifest{Scenarios: []Scenario{{ID: "s01"}, {ID: "s49", Args: []string{"--eaa", "--fail-on", "broke"}}, {ID: "s89", Args: []string{"--eaa"}}}}
	got, err := m.Pick([]string{"eaa", "s89", "s01"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"s49", "s89", "s89", "s01"}) {
		t.Errorf("Pick(eaa, s89, s01) = %v", ids)
	}
}

func TestCopyFixture(t *testing.T) {
	testbed := t.TempDir()
	fx := filepath.Join(testbed, "fixtures", "old")
	if err := os.MkdirAll(fx, 0o750); err != nil {
		t.Fatal(err)
	}
	run := `{"target":"http://localhost:18810/a11y/legacy.php"}`
	if err := os.WriteFile(filepath.Join(fx, "run.json"), []byte(run), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "snapshots", "good")
	if err := copyFixture(testbed, "fixtures/old", dst, "http://localhost:18810", "http://localhost:37121/"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"target":"http://localhost:37121/a11y/legacy.php"}` {
		t.Errorf("copied run.json: %s", got)
	}
}
