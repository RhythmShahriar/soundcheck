// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// EAA mode's part of the benchmark: the scoreboard of s49 to s89, the
// false alarms and determinism of --eaa on unchanged sites, what --eaa
// costs and which WCAG criteria the tests reach. PRD 16.3 sets the
// targets the section prints beside each measurement.

// Section markers in docs/benchmark.md: a run of the EAA scenarios
// alone (-only eaa) replaces only what is between them.
const (
	eaaBegin = "<!-- eaa:begin -->"
	eaaEnd   = "<!-- eaa:end -->"
)

// Targets of PRD 16.3, in seconds or as a factor.
const (
	targetA11yMedian = 2.0  // axe at most 2 s median per page
	targetWalkMedian = 3.0  // keyboard walk at most 3 s median per page
	targetFlow       = 15.0 // one key flow at most 15 s
	targetRunFactor  = 3.0  // a whole --eaa run at most 3 times a default run
)

// framing is PRD 15 item 10, printed with the evidence note.
const framing = "The testbed is written by the same people as the tool. It shows each detector works on a known case, not how much of a real site's accessibility it covers. Deque reports axe-based tests found 57% of issues by count in its own audits; that is a vendor figure counting issues, not success criteria."

// Board is everything the scoreboard is rendered from.
type Board struct {
	Manifest *Manifest
	Outcomes []Outcome
	Rounds   []ControlRound
	Cost     *Cost
	Version  string
	BinErr   error
	Tools    []*Tool // the tools that ran
	Partial  bool    // -only was used
	Date     string  // the run's date, YYYY-MM-DD
	// Criteria is the WCAG catalog the coverage table is made from
	// (wcag.Criteria when nil).
	Criteria func(version string) []wcag.Criterion
}

// writeBoard writes the scoreboard to path. An EAA-only run replaces
// just the EAA section of an existing scoreboard, so the rest of the
// benchmark stays as its last full run left it.
func writeBoard(path string, b Board, eaaOnly bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	var md string
	old, err := os.ReadFile(path) //nolint:gosec // the scoreboard the bench writes
	switch {
	case eaaOnly && err == nil:
		md = replaceSection(string(old), eaaSection(b))
	case eaaOnly && !errors.Is(err, fs.ErrNotExist):
		return err
	case eaaOnly:
		md = generatedHeader + "# Soundcheck benchmark\n\n" + eaaSection(b)
	default:
		md = scoreboard(b)
	}
	return os.WriteFile(path, []byte(md), 0o600) //nolint:gosec // -out, the scoreboard the caller asked for
}

// replaceSection puts section in place of the EAA section in md, or at
// its end when it has none. A begin marker without an end marker (a
// hand edit cut the section short) takes everything after it, so no
// second begin marker is left behind.
func replaceSection(md, section string) string {
	i := strings.Index(md, eaaBegin)
	if i < 0 {
		return strings.TrimRight(md, "\n") + "\n\n" + section
	}
	j := strings.Index(md[i:], eaaEnd)
	if j < 0 {
		return md[:i] + section
	}
	rest := strings.TrimLeft(md[i+j+len(eaaEnd):], "\n")
	if rest != "" {
		rest = "\n" + rest
	}
	return md[:i] + section + rest
}

// classicOutcomes are the outcomes of the scenarios before EAA mode.
func classicOutcomes(outcomes []Outcome) []Outcome {
	return slices.DeleteFunc(slices.Clone(outcomes), func(o Outcome) bool { return o.Scenario.EAA() })
}

// eaaOutcomes are the outcomes of the EAA scenarios.
func eaaOutcomes(outcomes []Outcome) []Outcome {
	return slices.DeleteFunc(slices.Clone(outcomes), func(o Outcome) bool { return !o.Scenario.EAA() })
}

// classicRounds are the control rounds run without --eaa.
func classicRounds(rounds []ControlRound) []ControlRound {
	return slices.DeleteFunc(slices.Clone(rounds), func(r ControlRound) bool { return r.EAA })
}

// eaaRounds are the control rounds run with --eaa.
func eaaRounds(rounds []ControlRound) []ControlRound {
	return slices.DeleteFunc(slices.Clone(rounds), func(r ControlRound) bool { return !r.EAA })
}

// good reports whether an EAA scenario is a good one: nothing broken,
// the compare must stay quiet (s77 to s89).
func good(s Scenario) bool {
	return s.Same || slices.ContainsFunc(s.Expect, func(e Expect) bool { return e.kind() == kindClean })
}

// eaaTally is EAA mode's score.
type eaaTally struct {
	broken, caught, pending, pendingCaught int // broken scenarios
	good, goodAlarms                       int // good scenarios; those with a false alarm
	pendingGood, pendingGoodAlarms         int // the same for good scenarios still pending
	notRun                                 []string
}

func eaaScore(outcomes []Outcome) eaaTally {
	var t eaaTally
	for _, o := range eaaOutcomes(outcomes) {
		r := o.Soundcheck
		switch {
		case r.Outcome == NotRun:
			t.notRun = append(t.notRun, o.Scenario.ID)
		case good(o.Scenario) && o.Scenario.Pending.Is():
			t.pendingGood++
			if r.Outcome == FalseAlarm {
				t.pendingGoodAlarms++
			}
		case good(o.Scenario):
			t.good++
			if r.Outcome == FalseAlarm {
				t.goodAlarms++
			}
		case o.Scenario.Pending.Is():
			t.pending++
			if r.Outcome == Caught {
				t.pendingCaught++
			}
		default:
			t.broken++
			if r.Outcome == Caught {
				t.caught++
			}
		}
	}
	return t
}

// eaaMark is the table cell of a result in the EAA section. On a good
// scenario nothing broke: Soundcheck meeting the expectation stayed
// quiet; another tool "catching" (a violation in the second state
// that the first lacks) raised a false alarm.
func eaaMark(s Scenario, r Result, other bool) string {
	if good(s) {
		switch {
		case r.Outcome == Caught && other:
			return "false alarm"
		case r.Outcome == Caught, r.Outcome == Missed && other:
			return "**quiet**"
		}
	}
	return mark(r)
}

// eaaSection renders the EAA mode section of the scoreboard.
func eaaSection(b Board) string {
	var sb strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&sb, format, a...) }
	outcomes := eaaOutcomes(b.Outcomes)
	rounds := eaaRounds(b.Rounds)
	var tools []*Tool
	for _, t := range b.Tools {
		if t.Scope != scopeClassic {
			tools = append(tools, t)
		}
	}
	ran := map[string]bool{}
	for _, t := range tools {
		ran[t.Key] = true
	}
	allEAA := slices.DeleteFunc(slices.Clone(allTools), func(t *Tool) bool { return t.Scope == scopeClassic })

	w("%s\n## EAA mode\n\n", eaaBegin)
	w("The scenarios s49 to s89 run with `--eaa` (`go run -tags testbed ./testbed/bench -only eaa`). ")
	w("Broken ones must be caught; good ones (nothing broken, or a page built to be hard) must stay quiet. ")
	w("Each scenario's harness also checks what must never happen: an order placed or a side effect on a GET or on focus, ")
	w("a request to a host outside the testbed, a canary value from the page in any file Soundcheck wrote, ")
	w("or a statement draft that claims the site meets the law.\n\n")
	w("> %s\n>\n> %s\n\n", model.EvidenceNote, framing)

	t := eaaScore(b.Outcomes)
	switch {
	case b.BinErr != nil:
		w("**Soundcheck wasn't run:** %s.\n\n", b.BinErr)
	case len(outcomes) == 0:
		w("The EAA scenarios weren't run.\n\n")
	default:
		wait := "wait"
		if t.pending == 1 {
			wait = "waits"
		}
		w("**Caught %d of %d broken scenarios.** %d more %s for a detector that isn't merged yet (%d of them caught already). ",
			t.caught, t.broken, t.pending, wait, t.pendingCaught)
		w("**False alarms: %d of %d good scenarios", t.goodAlarms, t.good)
		if len(rounds) > 0 {
			w(", %d** in %d `--eaa` compares of unchanged sites.", falseAlarmCount(rounds), 2*len(rounds))
		} else {
			w("**; the `--eaa` control pass wasn't run.")
		}
		if t.pendingGood > 0 {
			w(" Good scenarios still pending: %d of %d with a false alarm.", t.pendingGoodAlarms, t.pendingGood)
		}
		w(" Target (PRD 16.3): every broken scenario caught, none pending, no false alarm.\n\n")
		if len(t.notRun) > 0 {
			w("Not run and not counted: %s (%s).\n\n", strings.Join(t.notRun, ", "), notRunReasons(outcomes))
		}
	}
	ver := b.Version
	if ver == "" {
		ver = "unknown version"
	}
	w("Run on %s with %s (%s/%s).\n\n", b.Date, ver, runtime.GOOS, runtime.GOARCH)

	if len(outcomes) > 0 {
		w("### Scenarios\n\n")
		w("| # | Scenario | Expected | Soundcheck | Detector |")
		for _, tl := range allEAA {
			w(" %s |", tl.Name)
		}
		w("\n|---|---|---|---|---|")
		for range allEAA {
			w("---|")
		}
		w("\n")
		for _, o := range outcomes {
			s := o.Scenario
			kind := "broken"
			if good(s) {
				kind = "good"
			}
			lane := "merged"
			if s.Pending.Is() {
				lane = "pending"
				if l := s.Pending.Lane(); l != "" {
					lane += " (" + l + " lane)"
				}
			}
			w("| %s | %s (%s) | %s | %s | %s |", s.ID, cell(s.Name), kind, expected(s), cell(eaaMark(s, o.Soundcheck, false)), lane)
			for _, tl := range allEAA {
				r, ok := o.Tools[tl.Key]
				switch {
				case !ran[tl.Key]:
					w(" not run |")
				case !ok || r.Outcome == NotRun:
					w(" – |")
				default:
					w(" %s |", cell(eaaMark(s, r, true)))
				}
			}
			w("\n")
		}
		w("\n**caught**: a broken scenario's problem was reported · **quiet**: a good scenario got no report it rules out · false alarm: a good scenario got one (for another tool: a violation in the second state that the first lacks) · missed · error: the tool failed · –: the tool has no result for that scenario · not run: the tool wasn't available\n\n")
		var notes []string
		for _, o := range outcomes {
			r := o.Soundcheck
			text := r.Found
			switch {
			case r.Outcome == FalseAlarm && r.Detail != "":
				text = r.Found + "; " + r.Detail
			case r.Outcome != Caught && r.Detail != "":
				text = r.Detail
			}
			if text != "" {
				notes = append(notes, fmt.Sprintf("- **%s**: %s", o.Scenario.ID, cell(text)))
			}
		}
		if len(notes) > 0 {
			w("%s\n\n", strings.Join(notes, "\n"))
		}
	}

	eaaControl(&sb, rounds)
	determinism(&sb, rounds)
	costSection(&sb, b.Cost)
	criteria := b.Criteria
	if criteria == nil {
		criteria = wcag.Criteria
	}
	coverage(&sb, criteria)
	toolsSection(&sb, outcomes, allEAA, ran)

	w("### Run it\n\n")
	w("```sh\nmake build\nmake -C testbed up\nmake -C testbed bench-eaa      # go run -tags testbed ./testbed/bench -only eaa (10 control rounds)\n")
	w("go run -tags testbed ./testbed/bench -only eaa -tools axe,pa11y,unlighthouse   # also run the other tools\n```\n\n")
	w("An `-only eaa` run rewrites only this section of `docs/benchmark.md`. Runs are kept in `testbed/bench/work/<id>/` (with `statement.md` for the statement harness), ")
	w("the `--eaa` control pass in `work/control/eaa-<site>/` and the cost pass in `work/cost/`.\n%s\n", eaaEnd)
	return sb.String()
}

// expected is the Expected cell of a scenario.
func expected(s Scenario) string {
	var exp []string
	for _, e := range s.Expect {
		exp = append(exp, cell(e.String()))
	}
	join := "<br>or "
	if s.All {
		join = "<br>and "
	}
	out := strings.Join(exp, join) + " (" + s.Module + ")"
	if len(s.Forbid) > 0 {
		var no []string
		for _, f := range s.Forbid {
			no = append(no, cell(f.String()))
		}
		out += "<br>and no " + strings.Join(no, ", ")
	}
	if len(s.Harness) > 0 {
		out += "<br>harness: " + strings.Join(s.Harness, ", ")
	}
	return out
}

// eaaControl writes the --eaa control pass.
func eaaControl(b *strings.Builder, rounds []ControlRound) {
	w := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	w("### False alarms on unchanged sites\n\n")
	if len(rounds) == 0 {
		w("The `--eaa` control pass wasn't run (`-control 0`).\n\n")
		return
	}
	w("Every scenario good. Each round takes an `--eaa` snapshot and compares twice with nothing changed; ")
	w("the rounds marked by a scenario run on its pages with its flags. Target: 0.\n\n")
	w("| Site | Rounds | Compares | Pages | False alarms | Errors |\n|---|---:|---:|---:|---:|---:|\n")
	seen := map[string]int{}
	var order, names []string
	for _, r := range rounds {
		if !slices.Contains(names, r.Site) {
			names = append(names, r.Site)
		}
	}
	for _, site := range names {
		var n, pages, alarms, errs int
		for _, r := range rounds {
			if r.Site != site {
				continue
			}
			n++
			pages = max(pages, r.Pages)
			alarms += len(r.Alarms)
			if r.Error != "" {
				errs++
			}
			for _, a := range r.Alarms {
				if seen[a] == 0 {
					order = append(order, a)
				}
				seen[a]++
			}
		}
		w("| %s | %d | %d | %d | %d | %d |\n", site, n, 2*(n-errs), pages, alarms, errs)
	}
	w("\n")
	for _, a := range order {
		w("- %s (%d×)\n", cell(a), seen[a])
	}
	for _, r := range rounds {
		if r.Error != "" {
			w("- %s round %d didn't finish: %s\n", r.Site, r.Round, cell(r.Error))
		}
	}
	if len(order) > 0 || slices.ContainsFunc(rounds, func(r ControlRound) bool { return r.Error != "" }) {
		w("\n")
	}
}

// determinism writes how far the snapshots of each unchanged site
// differ: every round's issue keys against the first round's.
func determinism(b *strings.Builder, rounds []ControlRound) {
	w := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	w("### Determinism\n\n")
	if len(rounds) == 0 {
		w("Not measured: the `--eaa` control pass wasn't run.\n\n")
		return
	}
	w("Issue keys of each round's snapshot against the first round's, for the same unchanged pages. Target: 0 differing keys.\n\n")
	w("| Site | Snapshots | Issue keys in the first | Differing keys |\n|---|---:|---:|---:|\n")
	var names []string
	for _, r := range rounds {
		if !slices.Contains(names, r.Site) {
			names = append(names, r.Site)
		}
	}
	var diffs []string
	for _, site := range names {
		var first *ControlRound
		n, differ := 0, 0
		for i, r := range rounds {
			if r.Site != site || r.Error != "" {
				continue
			}
			n++
			if first == nil {
				first = &rounds[i]
				continue
			}
			for _, k := range symmetricDiff(first.Keys, r.Keys) {
				differ++
				if len(diffs) < 10 {
					diffs = append(diffs, fmt.Sprintf("- %s round %d: %s", site, r.Round, cell(k)))
				}
			}
		}
		keys := 0
		if first != nil {
			keys = len(first.Keys)
		}
		w("| %s | %d | %d | %d |\n", site, n, keys, differ)
	}
	w("\n")
	if len(diffs) > 0 {
		w("%s\n\n", strings.Join(diffs, "\n"))
	}
}

// symmetricDiff lists the keys in exactly one of a and b, each marked
// with the side it is on.
func symmetricDiff(a, b []string) []string {
	var out []string
	for _, k := range a {
		if !slices.Contains(b, k) {
			out = append(out, "only in the first: "+k)
		}
	}
	for _, k := range b {
		if !slices.Contains(a, k) {
			out = append(out, "only in this one: "+k)
		}
	}
	return out
}

// Cost is what --eaa costs on the testbed machine (the cost pass).
type Cost struct {
	Sites  []SiteCost `json:"sites"`
	Flow   FlowCost   `json:"flow"`
	Cores  int        `json:"cores"`
	OS     string     `json:"os"`
	Chrome string     `json:"chrome"`
	Axe    string     `json:"axe"`
	Tags   []string   `json:"tags"`
	Date   string     `json:"date"`
}

// SiteCost is the cost pass on one site, in seconds.
type SiteCost struct {
	Site       string    `json:"site"`
	Pages      int       `json:"pages"`
	Default    []float64 `json:"default"`     // each page in a default run
	EAA        []float64 `json:"eaa"`         // each page in an --eaa run
	A11y       []float64 `json:"a11y"`        // each page's a11y audit: --modules health,a11y minus health
	Walk       []float64 `json:"walk"`        // each page's keyboard walk
	DefaultRun float64   `json:"default_run"` // the whole default run
	EAARun     float64   `json:"eaa_run"`     // the whole --eaa run
	Error      string    `json:"error,omitempty"`
}

// FlowCost is what one key flow costs: the checkout's --eaa run with
// the key flow minus without, per flow that ran.
type FlowCost struct {
	Seconds float64 `json:"seconds"`
	Flows   int     `json:"flows"`
	Error   string  `json:"error,omitempty"`
}

// costPass measures --eaa against a default run on each control site,
// then one key flow on the WordPress checkout.
func costPass(ctx context.Context, o options, testbed string, m *Manifest, bin string) (*Cost, error) {
	if err := Toggle(testbed, "all", "good"); err != nil {
		return nil, err
	}
	c := &Cost{Cores: runtime.NumCPU(), OS: runtime.GOOS + "/" + runtime.GOARCH, Date: time.Now().UTC().Format("2006-01-02")}
	if b, err := browser.Launch(ctx, browser.Options{NoDownload: true}); err == nil {
		c.Chrome = b.Version
		b.Close()
	}
	for _, site := range m.Control.Sites {
		fmt.Fprintf(os.Stderr, "cost %s\n", site)
		sc, eaaRun := siteCost(ctx, o, bin, site, m.Sites[site], m.Control.Limit)
		if eaaRun != nil && eaaRun.Options.A11y != nil {
			c.Axe, c.Tags = eaaRun.Options.A11y.Axe, eaaRun.Options.A11y.Tags
		}
		c.Sites = append(c.Sites, sc)
	}
	fmt.Fprintln(os.Stderr, "cost key flow")
	c.Flow = flowCost(ctx, o, testbed, m, bin)
	return c, nil
}

// costRun takes one snapshot for the cost pass and loads it.
func costRun(ctx context.Context, o options, bin, wd, url, name string, p pages, args ...string) (*model.Run, error) {
	cfg := filepath.Join(wd, ".soundcheck", "config.yaml")
	if err := writeConfig(cfg, p, nil, nil); err != nil {
		return nil, err
	}
	snap := append([]string{"snapshot", url, "--name", name, "--json", "-o", name + ".json", "--config", cfg}, args...)
	if _, _, err := command(ctx, o.timeout, wd, os.Environ(), bin, snap...); err != nil {
		return nil, err
	}
	return store.LoadRun(filepath.Join(wd, ".soundcheck", "snapshots", name))
}

// siteCost runs a default, a health, a health and a11y and an --eaa
// snapshot of a site and returns the per-page seconds of each, with the
// --eaa run.
func siteCost(ctx context.Context, o options, bin, site, url string, limit int) (SiteCost, *model.Run) {
	sc := SiteCost{Site: site}
	wd := filepath.Join(o.work, "cost", site)
	if err := os.RemoveAll(wd); err != nil {
		sc.Error = err.Error()
		return sc, nil
	}
	p := pages{Limit: limit}
	runs := map[string]*model.Run{}
	for _, step := range []struct {
		name string
		args []string
	}{
		{"default", nil},
		{"health", []string{"--modules", "health"}},
		{"a11y", []string{"--modules", "health,a11y"}},
		{"eaa", []string{"--eaa"}},
	} {
		run, err := costRun(ctx, o, bin, wd, url, step.name, p, step.args...)
		if err != nil {
			sc.Error = step.name + ": " + err.Error()
			return sc, runs["eaa"]
		}
		runs[step.name] = run
	}
	sc.DefaultRun, sc.EAARun = runs["default"].Duration/1000, runs["eaa"].Duration/1000
	sc.Pages = len(runs["eaa"].Pages)
	sc.Default = pageSeconds(runs["default"])
	sc.EAA = pageSeconds(runs["eaa"])
	health := map[string]float64{}
	for _, pg := range runs["health"].Pages {
		health[pg.ID] = pg.Duration
	}
	for _, pg := range runs["a11y"].Pages {
		if h, ok := health[pg.ID]; ok && pg.Error == "" {
			sc.A11y = append(sc.A11y, max(0, pg.Duration-h)/1000)
		}
	}
	for _, pg := range runs["eaa"].Pages {
		if pg.Keyboard != nil && pg.Keyboard.MS > 0 {
			sc.Walk = append(sc.Walk, float64(pg.Keyboard.MS)/1000)
		}
	}
	return sc, runs["eaa"]
}

// pageSeconds lists how long each page that loaded took.
func pageSeconds(run *model.Run) []float64 {
	var out []float64
	for _, p := range run.Pages {
		if p.Error == "" {
			out = append(out, p.Duration/1000)
		}
	}
	return out
}

// flowScenario is the scenario whose key flow the cost pass times.
const flowScenario = "s70"

// flowCost times the key flow of flowScenario on its page in the good
// state: an --eaa snapshot with --level flows and the flow, minus one
// without.
func flowCost(ctx context.Context, o options, testbed string, m *Manifest, bin string) FlowCost {
	i := slices.IndexFunc(m.Scenarios, func(s Scenario) bool { return s.ID == flowScenario })
	if i < 0 || len(m.Scenarios[i].Flows) == 0 {
		return FlowCost{Error: flowScenario + " has no key flow"}
	}
	s := m.Scenarios[i]
	wd := filepath.Join(o.work, "cost", "flow")
	if err := os.RemoveAll(wd); err != nil {
		return FlowCost{Error: err.Error()}
	}
	p := pages{Include: s.Include, Limit: s.Limit}
	without, err := costRun(ctx, o, bin, wd, m.URL(s), "without", p, "--eaa")
	if err != nil {
		return FlowCost{Error: "without the flow: " + err.Error()}
	}
	if err := copyInto(testbed, filepath.Join(wd, ".soundcheck", "flows"), s.Flows, nil); err != nil {
		return FlowCost{Error: err.Error()}
	}
	with, err := costRun(ctx, o, bin, wd, m.URL(s), "with", p, "--eaa", "--level", "flows")
	if err != nil {
		return FlowCost{Error: "with the flow: " + err.Error()}
	}
	if with.KeyFlows == nil || with.KeyFlows.Run == 0 {
		return FlowCost{Error: "no key flow ran"}
	}
	n := with.KeyFlows.Run
	return FlowCost{Seconds: max(0, with.Duration-without.Duration) / 1000 / float64(n), Flows: n}
}

// median and p95 of some seconds (nearest rank); 0 for none.
func median(xs []float64) float64 { return percentile(xs, 50) }
func p95(xs []float64) float64    { return percentile(xs, 95) }

func percentile(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	rank := (p*len(s) + 99) / 100 // ceil(p/100 * n)
	return s[max(rank, 1)-1]
}

// costSection writes the cost table with the targets beside.
func costSection(b *strings.Builder, c *Cost) {
	w := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	w("### Cost\n\n")
	if c == nil {
		w("Not measured in this run.\n\n")
		return
	}
	chrome, axe, tags := orUnknown(c.Chrome), orUnknown(c.Axe), "not recorded"
	if len(c.Tags) > 0 {
		tags = strings.Join(c.Tags, ", ")
	}
	date := c.Date
	if date == "" {
		date = "unknown date"
	}
	w("%d cores, %s, Chrome %s, axe-core %s (tags %s), %s. ", c.Cores, c.OS, chrome, axe, tags, date)
	w("Seconds per page, median / p95, over the control pass's pages of each site. ")
	w("A page visit is the time Soundcheck records for the page; the keyboard walk is counted apart. ")
	w("The a11y audit is a run with `--modules health,a11y` minus one with `--modules health`, page by page.\n\n")
	w("| Measure |")
	for _, s := range c.Sites {
		w(" %s |", s.Site)
	}
	w(" Target (PRD 16.3) |\n|---|")
	for range c.Sites {
		w("---:|")
	}
	w("---|\n")
	row := func(name, target string, limit float64, get func(SiteCost) []float64) {
		w("| %s |", name)
		for _, s := range c.Sites {
			xs := get(s)
			switch {
			case s.Error != "" && len(xs) == 0:
				w(" error |")
			case len(xs) == 0:
				w(" – |")
			default:
				w(" %.2f / %.2f%s |", median(xs), p95(xs), verdict(median(xs), limit))
			}
		}
		w(" %s |\n", target)
	}
	row("Page visit, default run", "", 0, func(s SiteCost) []float64 { return s.Default })
	row("Page visit, `--eaa` run", "", 0, func(s SiteCost) []float64 { return s.EAA })
	row("a11y audit per page", fmt.Sprintf("median at most %g s", targetA11yMedian), targetA11yMedian, func(s SiteCost) []float64 { return s.A11y })
	row("Keyboard walk per page", fmt.Sprintf("median at most %g s", targetWalkMedian), targetWalkMedian, func(s SiteCost) []float64 { return s.Walk })
	w("| Whole `--eaa` run ÷ default run |")
	for _, s := range c.Sites {
		if s.DefaultRun <= 0 {
			w(" – |")
			continue
		}
		f := s.EAARun / s.DefaultRun
		w(" %.1f× (%.0f s ÷ %.0f s)%s |", f, s.EAARun, s.DefaultRun, verdict(f, targetRunFactor))
	}
	w(" at most %g× |\n", targetRunFactor)
	w("| One key flow (%s, `/checkout/`) |", flowScenario)
	for i := range c.Sites {
		switch {
		case i > 0:
			w(" |")
		case c.Flow.Error != "":
			w(" error |")
		default:
			w(" %.1f s%s |", c.Flow.Seconds, verdict(c.Flow.Seconds, targetFlow))
		}
	}
	w(" at most %g s |\n\n", targetFlow)
	var errs []string
	for _, s := range c.Sites {
		if s.Error != "" {
			errs = append(errs, fmt.Sprintf("- %s: %s", s.Site, cell(s.Error)))
		}
	}
	if c.Flow.Error != "" {
		errs = append(errs, "- key flow: "+cell(c.Flow.Error))
	}
	if len(errs) > 0 {
		w("%s\n\n", strings.Join(errs, "\n"))
	}
}

// verdict marks a measurement over its target; limit 0 has none.
func verdict(x, limit float64) string {
	if limit > 0 && x > limit {
		return " **over**"
	}
	return ""
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// coverage writes which criteria the tests reach, from the WCAG catalog
// (wcag/criteria.yaml): automated (axe-core and Soundcheck's a11y
// checks), the keyboard walk, or only a person.
func coverage(b *strings.Builder, criteria func(string) []wcag.Criterion) {
	w := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	w("### Coverage\n\n")
	w("Which WCAG A and AA success criteria the tests can find failures of. A criterion with a test still needs a person to judge it: ")
	w("the tests find some failures, never a pass. The rest are `not tested: needs a person`.\n\n")
	w("| WCAG | Criteria | Automated | Keyboard | Needs a person only |\n|---|---:|---:|---:|---:|\n")
	for _, v := range []string{"2.1", "2.2"} {
		cs := criteria(v)
		auto, keys, none := 0, 0, 0
		for _, c := range cs {
			a, k := reach(c)
			if a != "" {
				auto++
			}
			if k != "" {
				keys++
			}
			if a == "" && k == "" {
				none++
			}
		}
		w("| %s | %d | %d | %d | %d |\n", v, len(cs), auto, keys, none)
	}
	w("\n")
	in21 := map[string]bool{}
	for _, c := range criteria("2.1") {
		in21[c.ID] = true
	}
	rows := criteria("2.1")
	for _, c := range criteria("2.2") {
		if !in21[c.ID] {
			rows = append(rows, c)
		}
	}
	slices.SortStableFunc(rows, func(a, b wcag.Criterion) int { return wcag.Compare(a.ID, b.ID) })
	in22 := map[string]bool{}
	for _, c := range criteria("2.2") {
		in22[c.ID] = true
	}
	w("<details><summary>Every criterion</summary>\n\n")
	w("| Criterion | Level | EN 301 549 | WCAG | Automated | Keyboard |\n|---|---|---|---|---|---|\n")
	for _, c := range rows {
		ver := "2.1, 2.2"
		switch {
		case !in22[c.ID]:
			ver = "2.1 only"
		case !in21[c.ID]:
			ver = "2.2 only"
		}
		a, k := reach(c)
		if a == "" && k == "" {
			a, k = "needs a person", "needs a person"
		}
		w("| %s %s | %s | %s | %s | %s | %s |\n", c.ID, cell(c.Title), c.Level, c.EN, ver, orDash(a), orDash(k))
	}
	w("\n</details>\n\n")
}

// reach splits a criterion's tests into automated ones (axe rules and
// a11y checks) and keyboard ones.
func reach(c wcag.Criterion) (automated, keyboard string) {
	var a, k []string
	for _, t := range c.TestedBy {
		if strings.HasPrefix(t, "keyboard/") {
			k = append(k, "`"+t+"`")
		} else {
			a = append(a, "`"+t+"`")
		}
	}
	return strings.Join(a, ", "), strings.Join(k, ", ")
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

// toolsSection writes how the other accessibility tools did: what they
// caught, their false alarms on good scenarios and their good-state
// violations (noise on pages the testbed made clean).
func toolsSection(b *strings.Builder, outcomes []Outcome, tools []*Tool, ran map[string]bool) {
	w := func(format string, a ...any) { fmt.Fprintf(b, format, a...) }
	w("### Other tools\n\n")
	w("Run on the same states when installed. Caught means a violation in the broken state that the good state lacks.\n\n")
	w("| Tool | Caught (broken scenarios) | False alarms (good scenarios) | Violations in good states | Errors |\n|---|---:|---:|---:|---:|\n")
	for _, t := range tools {
		if !ran[t.Key] {
			w("| %s | not run | | | |\n", t.Name)
			continue
		}
		var caught, broken, alarms, goods, errs, violations int
		for _, o := range outcomes {
			r, ok := o.Tools[t.Key]
			if !ok {
				continue
			}
			violations += r.Good
			switch {
			case r.Outcome == Failed:
				errs++
			case good(o.Scenario):
				goods++
				if r.Outcome == Caught {
					alarms++
				}
			default:
				broken++
				if r.Outcome == Caught {
					caught++
				}
			}
		}
		noise := fmt.Sprint(violations)
		if t.Key == "unlighthouse" {
			noise = "scores only"
		}
		w("| %s | %d of %d | %d of %d | %s | %d |\n", t.Name, caught, broken, alarms, goods, noise, errs)
	}
	w("\n")
	for _, t := range tools {
		state := "ran"
		if !ran[t.Key] {
			state = "not run; to include it, " + t.HowTo
		}
		w("- **%s**: %s. %s.\n", t.Name, t.About, upper(state))
	}
	w("\n")
}
