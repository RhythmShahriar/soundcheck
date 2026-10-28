// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fakeCriteria is a small fixed catalog, so the golden doesn't change
// when wcag/criteria.yaml does.
func fakeCriteria(version string) []wcag.Criterion {
	all := []wcag.Criterion{
		{ID: "1.1.1", Title: "Non-text Content", Level: "A", EN: "9.1.1.1", TestedBy: []string{"axe:image-alt"}},
		{ID: "1.2.1", Title: "Audio-only and Video-only (Prerecorded)", Level: "A", EN: "9.1.2.1"},
		{ID: "2.1.2", Title: "No Keyboard Trap", Level: "A", EN: "9.2.1.2", TestedBy: []string{"keyboard/trap"}},
		{ID: "2.4.7", Title: "Focus Visible", Level: "AA", EN: "9.2.4.7", TestedBy: []string{"a11y/focus-visible", "keyboard/no-focus-ring"}},
		{ID: "2.4.11", Title: "Focus Not Obscured (Minimum)", Level: "AA", EN: "9.2.4.11", TestedBy: []string{"keyboard/focus-obscured"}},
		{ID: "4.1.1", Title: "Parsing", Level: "A", EN: "9.4.1.1"},
	}
	return slices.DeleteFunc(all, func(c wcag.Criterion) bool {
		return version == "2.1" && c.ID == "2.4.11" || version == "2.2" && c.ID == "4.1.1"
	})
}

// fakeBoard is a fixed result of an -only eaa run.
func fakeBoard() Board {
	sc := func(id, name, module, lane string, same bool, expect []Expect, harness ...string) Scenario {
		return Scenario{ID: id, Name: name, Module: module, Args: []string{"--eaa"}, Pending: Pending(lane), Same: same, Expect: expect, Harness: harness}
	}
	clean := []Expect{{Clean: true}}
	axe, pa11y := toolByKey("axe"), toolByKey("pa11y")
	return Board{
		Manifest: &Manifest{Control: Control{Sites: []string{"wp", "php"}, Limit: 40}},
		Version:  "rhythm-soundcheck 1.3.0",
		Date:     "2026-10-26",
		Tools:    []*Tool{axe, pa11y},
		Partial:  true,
		Criteria: fakeCriteria,
		Outcomes: []Outcome{
			{Scenario: Scenario{ID: "s01", Name: "classic", Module: "health"}, Soundcheck: Result{Outcome: Caught}},
			{Scenario: sc("s49", "Keyboard trap in the coupon field", "keyboard", "keyboard", false,
				[]Expect{{Class: "broke", Check: "keyboard/trap", Source: "#tb_coupon"}}, "orders"),
				Soundcheck: Result{Outcome: Caught, Found: "BROKE keyboard/trap: Focus is trapped [#tb_coupon] (/checkout/)"},
				Tools:      map[string]Result{"axe": {Outcome: Missed}, "pa11y": {Outcome: Failed, Detail: "pa11y: exit 1"}}},
			{Scenario: sc("s50", "Checkout fields lose their labels", "a11y", "", false, []Expect{{Class: "new", Check: "a11y/label", Count: 3}}, "statement"),
				Soundcheck: Result{Outcome: Missed, Detail: "the expectation held (NEW a11y/label) but harness: statement: says \"conform\""},
				Tools:      map[string]Result{"axe": {Outcome: Caught, Found: "new violations: label #tb_billing_name", Good: 0}, "pa11y": {Outcome: Caught, Good: 2}}},
			{Scenario: sc("s61", "Low-contrast Place order", "a11y", "", false, []Expect{{Class: "new", Check: "a11y/color-contrast"}}),
				Soundcheck: Result{Outcome: Caught, Found: "NEW a11y/color-contrast: Elements must meet contrast [#place_order] (/checkout/)"},
				Tools:      map[string]Result{"axe": {Outcome: Caught, Good: 1}, "pa11y": {Outcome: Caught}}},
			{Scenario: sc("s77", "Correct native dialog", "keyboard", "keyboard", true, clean),
				Soundcheck: Result{Outcome: FalseAlarm, Found: "NEW keyboard/dialog-focus: x"},
				Tools:      map[string]Result{"axe": {Outcome: Caught, Good: 3}}},
			{Scenario: sc("s89", "Small site with no known accessibility problems", "keyboard", "statement", true, clean, "statement", "requests"),
				Soundcheck: Result{Outcome: Caught, Found: "nothing at or above NEW"}},
		},
		Rounds: []ControlRound{
			{Site: "wp", Round: 1, Pages: 40},
			{Site: "wp", Round: 1, Pages: 38, EAA: true, Keys: []string{"/ a11y/region|main"}},
			{Site: "wp", Round: 2, Pages: 38, EAA: true, Keys: []string{"/ a11y/region|main"}},
			{Site: "php", Round: 1, Pages: 36, EAA: true, Keys: []string{"/a11y/backlog.php a11y/color-contrast|#row-1"}},
			{Site: "php", Round: 2, Pages: 36, EAA: true, Alarms: []string{"NEW a11y/color-contrast: x (/a11y/backlog.php)"},
				Keys: []string{"/a11y/backlog.php a11y/color-contrast|#row-2"}},
			{Site: "s89", Round: 1, Pages: 1, EAA: true},
			{Site: "s89", Round: 2, EAA: true, Error: "snapshot: exit 1"},
		},
		Cost: &Cost{
			Cores: 8, OS: "linux/amd64", Chrome: "141.0.7390.54", Axe: "4.11.0", Tags: []string{"wcag2a", "wcag2aa", "wcag21a", "wcag21aa"}, Date: "2026-10-26",
			Sites: []SiteCost{
				{Site: "wp", Pages: 38, Default: []float64{1, 1.2, 1.4}, EAA: []float64{3, 3.5, 6}, A11y: []float64{0.8, 1.1, 2.6}, Walk: []float64{1.5, 2, 4.2},
					DefaultRun: 40, EAARun: 130},
				{Site: "php", Error: "eaa: exit 1", Default: []float64{0.5}},
			},
			Flow: FlowCost{Seconds: 9.4, Flows: 1},
		},
	}
}

func toolByKey(key string) *Tool {
	i := slices.IndexFunc(allTools, func(t *Tool) bool { return t.Key == key })
	return allTools[i]
}

// TestEAASectionGolden renders the EAA section of a fixed result. Run
// with -update after a deliberate change and read the diff.
func TestEAASectionGolden(t *testing.T) {
	got := eaaSection(fakeBoard())
	got = strings.ReplaceAll(got, runtime.GOOS+"/"+runtime.GOARCH+")", "OS/ARCH)")
	path := filepath.Join("testdata", "eaa-section.golden.md")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("EAA section differs from %s (rerun with -update and review the diff):\n%s", path, got)
	}
	for _, s := range []string{model.EvidenceNote, framing, "**Caught 1 of 2 broken scenarios.** 1 more wait", "**False alarms: 0 of 0 good scenarios, 1** in 12 `--eaa` compares", "Good scenarios still pending: 1 of 2 with a false alarm.",
		"median at most 2 s", "at most 3×", "at most 15 s", "**over**", "| php | 2 | 1 | 2 |", "| 2.1 | 5 | 2 | 2 | 2 |", "| axe-core CLI | 2 of 3 | 1 of 1 | 4 | 0 |"} {
		if !strings.Contains(got, s) {
			t.Errorf("the section lacks %q", s)
		}
	}
	// A good scenario's row: another tool's "catch" is a false alarm,
	// Soundcheck meeting the expectation is quiet.
	for _, s := range []string{"| s77 | Correct native dialog (good) | nothing at or above NEW (keyboard) | false alarm | pending (keyboard lane) | not run | false alarm | – |",
		"| s89 | Small site with no known accessibility problems (good) | nothing at or above NEW (keyboard)<br>harness: statement, requests | **quiet** |",
		"| s61 | Low-contrast Place order (broken) | NEW a11y/color-contrast (a11y) | **caught** | merged | not run | **caught** | **caught** |"} {
		if !strings.Contains(got, s) {
			t.Errorf("the table lacks %q", s)
		}
	}
	if strings.Contains(got, "| s01 |") {
		t.Error("a scenario before EAA mode is in the EAA table")
	}
}

func TestScoreboardKeepsEAAApart(t *testing.T) {
	b := fakeBoard()
	b.Manifest.Scenarios = []Scenario{{ID: "s01"}}
	md := scoreboard(b)
	i, j := strings.Index(md, "| s01 |"), strings.Index(md, eaaBegin)
	if !strings.HasPrefix(md, generatedHeader) || i < 0 || j < i || !strings.HasSuffix(md, eaaEnd+"\n") {
		t.Fatalf("the full scoreboard isn't the classic part then the EAA section:\n%s", md)
	}
	classic := md[:j]
	for _, s := range []string{"| s49 |", "**axe-core CLI**", "**pa11y**", "| axe-core CLI |"} {
		if strings.Contains(classic, s) {
			t.Errorf("the classic part has %q", s)
		}
	}
	if !strings.Contains(classic, "**Soundcheck caught 1 of 1 scenarios.** **False alarms: 0** in 2 compares") {
		t.Errorf("the classic score counts EAA scenarios or rounds:\n%s", classic)
	}

	// Without EAA results there is no EAA section.
	b.Outcomes, b.Rounds = classicOutcomes(b.Outcomes), classicRounds(b.Rounds)
	if strings.Contains(scoreboard(b), eaaBegin) {
		t.Error("an EAA section without EAA results")
	}
}

func TestWriteBoard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docs", "benchmark.md")
	b := fakeBoard()
	// A first EAA-only run writes a scoreboard with just the section.
	if err := writeBoard(path, b, true); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(first), generatedHeader+"# Soundcheck benchmark\n\n"+eaaBegin) {
		t.Errorf("first EAA-only run:\n%s", first)
	}
	// A full scoreboard without the section gets it appended; a later
	// EAA-only run replaces only the section.
	full := generatedHeader + "# Soundcheck benchmark\n\nclassic results\n\n## Run it\n\nmake bench\n"
	if err := os.WriteFile(path, []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBoard(path, b, true); err != nil {
		t.Fatal(err)
	}
	b.Date = "2026-11-02"
	if err := writeBoard(path, b, true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), full+"\n"+eaaBegin) || strings.Count(string(got), eaaBegin) != 1 ||
		!strings.Contains(string(got), "Run on 2026-11-02") || strings.Contains(string(got), "Run on 2026-10-26") {
		t.Errorf("EAA-only runs on a full scoreboard:\n%s", got)
	}
	if got := replaceSection("a\n"+eaaBegin+"\nold\n"+eaaEnd+"\n\nafter\n", eaaBegin+"\nnew\n"+eaaEnd+"\n"); got != "a\n"+eaaBegin+"\nnew\n"+eaaEnd+"\n\nafter\n" {
		t.Errorf("replaceSection keeps what follows: %q", got)
	}
	if got := replaceSection("a\n"+eaaBegin+"\nold, cut short\n", eaaBegin+"\nnew\n"+eaaEnd+"\n"); got != "a\n"+eaaBegin+"\nnew\n"+eaaEnd+"\n" {
		t.Errorf("replaceSection with no end marker: %q", got)
	}
	if got := replaceSection("a\n"+eaaEnd+"\n"+eaaBegin+"\nold\n"+eaaEnd+"\nafter\n", "new\n"); got != "a\n"+eaaEnd+"\nnew\n\nafter\n" {
		t.Errorf("replaceSection with a stray end marker before the section: %q", got)
	}
	if got := replaceSection("a\n", "new\n"); got != "a\n\nnew\n" {
		t.Errorf("replaceSection with no section: %q", got)
	}
	// A full run rewrites the whole file.
	if err := writeBoard(path, b, false); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if strings.Contains(string(got), "classic results") {
		t.Error("a full run kept the old classic part")
	}
	// A scoreboard that can't be read isn't overwritten.
	dir := filepath.Join(t.TempDir(), "board")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := writeBoard(dir, b, true); err == nil {
		t.Error("a folder in place of the scoreboard: no error")
	}
}

// A good scenario that got a NEW change its forbid list doesn't name is
// still a false alarm in the headline.
func TestEAAScoreUnforbiddenAlarm(t *testing.T) {
	s77 := Scenario{ID: "s77", Args: []string{"--eaa"}, Same: true, Expect: []Expect{{Clean: true}}, Forbid: []Expect{{Check: "keyboard/trap"}}}
	cmp := &model.Comparison{Changes: []model.Change{{Class: model.New, Check: "keyboard/no-focus-ring", Title: "No focus ring"}}}
	r := withHarnesses(Evaluate(s77, Observed{Comparison: cmp}), nil)
	if got := eaaScore([]Outcome{{Scenario: s77, Soundcheck: r}}); got.good != 1 || got.goodAlarms != 1 {
		t.Errorf("eaaScore = %+v (result %+v)", got, r)
	}
	// The same with a harness problem on top.
	s88 := s77
	s88.ID = "s88"
	r = withHarnesses(Evaluate(s88, Observed{Comparison: cmp}), []string{"orders: x"})
	if got := eaaScore([]Outcome{{Scenario: s88, Soundcheck: r}}); got.goodAlarms != 1 {
		t.Errorf("with a harness problem: eaaScore = %+v (result %+v)", got, r)
	}
	// A clean run of the good scenario isn't one.
	r = Evaluate(s77, Observed{Comparison: &model.Comparison{}})
	if got := eaaScore([]Outcome{{Scenario: s77, Soundcheck: r}}); got.good != 1 || got.goodAlarms != 0 {
		t.Errorf("clean: eaaScore = %+v", got)
	}
}

func TestEAAScore(t *testing.T) {
	got := eaaScore(append(fakeBoard().Outcomes, Outcome{Scenario: Scenario{ID: "s90", Args: []string{"--eaa"}}, Soundcheck: Result{Outcome: NotRun}}))
	want := eaaTally{broken: 2, caught: 1, pending: 1, pendingCaught: 1, pendingGood: 2, pendingGoodAlarms: 1, notRun: []string{"s90"}}
	if got.broken != want.broken || got.caught != want.caught || got.pending != want.pending || got.pendingCaught != want.pendingCaught ||
		got.good != want.good || got.goodAlarms != want.goodAlarms || got.pendingGood != want.pendingGood ||
		got.pendingGoodAlarms != want.pendingGoodAlarms || !slices.Equal(got.notRun, want.notRun) {
		t.Errorf("eaaScore = %+v, want %+v", got, want)
	}
}

func TestPercentile(t *testing.T) {
	xs := []float64{5, 1, 4, 2, 3}
	if median(xs) != 3 || p95(xs) != 5 || median(nil) != 0 || p95([]float64{7}) != 7 {
		t.Errorf("median %v, p95 %v", median(xs), p95(xs))
	}
	if !slices.Equal(xs, []float64{5, 1, 4, 2, 3}) {
		t.Error("percentile sorted its input")
	}
	var many []float64
	for i := 1; i <= 100; i++ {
		many = append(many, float64(i))
	}
	if median(many) != 50 || p95(many) != 95 {
		t.Errorf("1..100: median %v, p95 %v", median(many), p95(many))
	}
}

func TestSymmetricDiff(t *testing.T) {
	if got := symmetricDiff([]string{"a", "b"}, []string{"a", "b"}); got != nil {
		t.Errorf("same keys: %v", got)
	}
	got := symmetricDiff([]string{"a", "b"}, []string{"b", "c"})
	if !slices.Equal(got, []string{"only in the first: a", "only in this one: c"}) {
		t.Errorf("symmetricDiff = %v", got)
	}
}

func TestIssueKeys(t *testing.T) {
	run := &model.Run{
		Issues: []model.Issue{{Check: "health/robots", Title: "x", Key: "health/robots|x|"}},
		Pages: []model.Page{
			{Path: "/", Issues: []model.Issue{{Check: "a11y/label", Source: "#q"}, {Check: "a11y/label", Source: "#q"}}},
			{Path: "/b/", Issues: []model.Issue{{Check: "keyboard/trap", Source: "#c", Key: "keyboard/trap|#c"}}},
		},
	}
	want := []string{"/ a11y/label|#q", "/b/ keyboard/trap|#c", "site health/robots|x|"}
	if got := issueKeys(run); !slices.Equal(got, want) {
		t.Errorf("issueKeys = %v, want %v", got, want)
	}
}

func TestA11yToolOutput(t *testing.T) {
	axe := `[{"url":"http://localhost:18810/a11y/","violations":[{"id":"label","nodes":[{"target":["#card"]},{"target":["#zip"]}]},{"id":"color-contrast","nodes":[{"target":["iframe",".pay"]}]}]}]`
	got, err := axeViolations([]byte(axe))
	if err != nil || !slices.Equal(got, []string{"color-contrast iframe .pay", "label #card", "label #zip"}) {
		t.Errorf("axeViolations = %v, %v", got, err)
	}
	if _, err := axeViolations([]byte(`{"not":"a list"}`)); err == nil {
		t.Error("axe: an object passed")
	}
	pa := "Running pa11y\n" + `[{"code":"WCAG2AA.Principle1.Guideline1_3.1_3_1.F68","type":"error","selector":"#card"},{"code":"x","type":"warning","selector":"#zip"}]`
	got, err = pa11yViolations([]byte(jsonArray(pa)))
	if err != nil || !slices.Equal(got, []string{"WCAG2AA.Principle1.Guideline1_3.1_3_1.F68 #card"}) {
		t.Errorf("pa11yViolations = %v, %v", got, err)
	}
	if _, err := pa11yViolations([]byte("Error: net::ERR_CONNECTION_REFUSED")); err == nil {
		t.Error("pa11y: an error message passed")
	}

	good := &Observation{Violations: []string{"label #zip"}}
	if r := judgeViolations(good, &Observation{Violations: []string{"label #zip"}}); r.Outcome != Missed {
		t.Errorf("same violations: %+v", r)
	}
	r := judgeViolations(good, &Observation{Violations: []string{"a #1", "b #2", "c #3", "d #4", "label #zip"}})
	if r.Outcome != Caught || r.Found != "new violations: a #1, b #2, c #3 and 1 more" {
		t.Errorf("new violations: %+v", r)
	}
}

func TestToolRuns(t *testing.T) {
	classic, eaa := Scenario{ID: "s05"}, Scenario{ID: "s50", Args: []string{"--eaa"}}
	cases := map[string][2]bool{"backstop": {true, false}, "lychee": {true, false}, "unlighthouse": {true, true}, "axe": {false, true}, "pa11y": {false, true}}
	for key, want := range cases {
		tl := toolByKey(key)
		if got := [2]bool{tl.Runs(classic), tl.Runs(eaa)}; got != want {
			t.Errorf("%s runs on classic, EAA = %v, want %v", key, got, want)
		}
	}
	if _, err := pickTools("axe,pa11y,nope"); err == nil || !strings.Contains(err.Error(), "axe, pa11y") {
		t.Errorf("unknown tool: %v", err)
	}
}

func TestCostSectionWithoutCost(t *testing.T) {
	var b strings.Builder
	costSection(&b, nil)
	determinism(&b, nil)
	eaaControl(&b, nil)
	got := b.String()
	for _, s := range []string{"### Cost\n\nNot measured", "Not measured: the `--eaa` control pass wasn't run", "The `--eaa` control pass wasn't run (`-control 0`)"} {
		if !strings.Contains(got, s) {
			t.Errorf("lacks %q:\n%s", s, got)
		}
	}
	b.Reset()
	bd := fakeBoard()
	bd.BinErr = errors.New("bin/rhythm-soundcheck not found (make build)")
	if got := eaaSection(bd); !strings.Contains(got, "**Soundcheck wasn't run:** bin/rhythm-soundcheck not found") {
		t.Errorf("no binary:\n%s", got)
	}
}
