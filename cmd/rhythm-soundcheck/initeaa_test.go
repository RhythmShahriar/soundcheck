// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/evidence"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/statement"
	"github.com/RhythmShahriar/soundcheck/store"
)

// keepTemplates restores the key flow templates after a test stands
// in for them.
func keepTemplates(t *testing.T) {
	t.Helper()
	old := keyboardTemplates
	t.Cleanup(func() { keyboardTemplates = old })
}

// wantKeyFlow checks that path holds the shipped template name and that
// it parses as a key flow.
func wantKeyFlow(t *testing.T, path, name string) {
	t.Helper()
	tpl, ok := flow.KeyboardTemplates()[name]
	if !ok {
		t.Fatalf("the flow package ships no %s", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(tpl) {
		t.Errorf("%s isn't the shipped template:\n%s", path, data)
	}
	f, err := flow.ReadFile(path)
	if err != nil {
		t.Errorf("%s doesn't load: %v", path, err)
	} else if f.Keyboard != flow.KeyboardDry {
		t.Errorf("%s isn't a dry key flow (keyboard: true): %q", path, f.Keyboard)
	}
}

// filePerm is path's permission bits, or want where Windows can't say.
func filePerm(t *testing.T, path string, want os.FileMode) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return want
	}
	return fi.Mode().Perm()
}

// init --eaa writes a config with EAA mode on that loads, leaves the
// modules to --eaa and writes the checkout key flow; a second run keeps
// the flow.
func TestInitEAA(t *testing.T) {
	t.Chdir(t.TempDir())
	keepTemplates(t)
	code, out, errOut := runCLI(t, "init", "--eaa")
	if code != exitOK {
		t.Fatalf("exit %d %s", code, errOut)
	}
	for _, want := range []string{"Wrote .soundcheck/config.yaml.", "Wrote .soundcheck/flows/keyboard-checkout.yaml, a key flow to edit",
		"Fill in statement: before you run rhythm-soundcheck statement."} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	data, err := os.ReadFile(".soundcheck/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "init-eaa.golden", string(data))
	if m := filePerm(t, ".soundcheck/config.yaml", 0o644); m != 0o644 {
		t.Errorf("config mode %o", m)
	}
	if m := filePerm(t, ".soundcheck/flows/keyboard-checkout.yaml", 0o644); m != 0o644 {
		t.Errorf("flow mode %o", m)
	}
	wantKeyFlow(t, ".soundcheck/flows/keyboard-checkout.yaml", "keyboard-checkout.yaml")
	c, err := config.Load(".soundcheck/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !c.EAA || c.InFile("modules") || !c.InFile("pages.limit") {
		t.Errorf("eaa %v, modules in file %v", c.EAA, c.InFile("modules"))
	}
	for _, want := range []string{"# a11y:", "# keyboard:", "# statement:", "role address", "is published"} {
		if !strings.Contains(strings.Join(strings.Fields(string(data)), " "), want) {
			t.Errorf("template lacks %q", want)
		}
	}

	// The commented blocks load once uncommented, with the modules that
	// decode them.
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		for _, b := range []string{"# a11y:", "# keyboard:", "# statement:", "#   "} {
			if strings.HasPrefix(l, b) {
				l = strings.TrimPrefix(l, "# ")
				break
			}
		}
		lines = append(lines, l)
	}
	if err := os.WriteFile("open.yaml", []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load("open.yaml"); err != nil {
		t.Errorf("uncommented blocks: %v", err)
	}

	code, out, _ = runCLI(t, "init", "--eaa", "--force")
	if code != exitOK || !strings.Contains(out, "Kept .soundcheck/flows/keyboard-checkout.yaml, which is already there.") {
		t.Errorf("second init: exit %d %s", code, out)
	}
	// Without --eaa the file stays as before 1.3 and no flow is written.
	t.Chdir(t.TempDir())
	if code, out, _ := runCLI(t, "init"); code != exitOK || strings.Contains(out, "key flow") {
		t.Errorf("init: exit %d %s", code, out)
	}
	if _, err := os.Stat(".soundcheck/flows/keyboard-checkout.yaml"); err == nil {
		t.Error("init without --eaa wrote a key flow")
	}
	if c, err := config.Load(".soundcheck/config.yaml"); err != nil || c.EAA {
		t.Errorf("plain init: %v %v", c.EAA, err)
	}
	// A build without the templates says so and exits 3.
	keyboardTemplates = func() map[string][]byte { return nil }
	t.Chdir(t.TempDir())
	if code, _, errOut := runCLI(t, "init", "--eaa"); code != exitError || !strings.Contains(errOut, "this build has no key flow template keyboard-checkout.yaml") {
		t.Errorf("no templates: exit %d %s", code, errOut)
	}
}

// flows init --keyboard writes the four key flows and replaces none.
func TestFlowsInitKeyboard(t *testing.T) {
	t.Chdir(t.TempDir())
	code, out, errOut := runCLI(t, "flows", "init", "--keyboard")
	if code != exitOK {
		t.Fatalf("exit %d %s", code, errOut)
	}
	for _, n := range keyFlowFiles {
		p := filepath.Join(".soundcheck", "flows", n)
		if _, err := os.Stat(p); err != nil {
			t.Error(err)
		}
		if !strings.Contains(out, "Wrote "+filepath.ToSlash(p)) {
			t.Errorf("stdout lacks %s:\n%s", p, out)
		}
		if m := filePerm(t, p, 0o644); m != 0o644 {
			t.Errorf("%s mode %o", p, m)
		}
		wantKeyFlow(t, p, n)
	}
	if fs, err := flow.Load(filepath.Join(".soundcheck", "flows")); err != nil || len(fs) != 4 {
		t.Errorf("the flows folder loads %d flows: %v", len(fs), err)
	}
	if keys := keyFlows(filepath.Join(".soundcheck", "flows")); len(keys) != 4 {
		t.Errorf("key flows found: %v", keys)
	}
	if err := os.WriteFile(".soundcheck/flows/keyboard-menu.yaml", []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runCLI(t, "flows", "init", "--keyboard")
	if code != exitUsage || errOut != "rhythm-soundcheck: the four key flows are already in .soundcheck/flows; nothing was written\n" {
		t.Errorf("again: exit %d %q", code, errOut)
	}
	if data, _ := os.ReadFile(".soundcheck/flows/keyboard-menu.yaml"); string(data) != "mine\n" {
		t.Errorf("replaced a flow: %q", data)
	}
	// After init --eaa, which writes keyboard-checkout.yaml, the other
	// three are written and that one is kept.
	for _, n := range keyFlowFiles[1:] {
		if err := os.Remove(filepath.Join(".soundcheck", "flows", n)); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errOut = runCLI(t, "flows", "init", "--keyboard")
	if code != exitOK || !strings.Contains(out, "Kept .soundcheck/flows/keyboard-checkout.yaml: it's already there") ||
		strings.Count(out, "Wrote ") != 3 || errOut != "" {
		t.Errorf("after init --eaa: exit %d\n%s\n%s", code, out, errOut)
	}
	// Plain flows init still writes the example only.
	if code, out, _ := runCLI(t, "flows", "init"); code != exitOK || !strings.Contains(out, "example.yaml") {
		t.Errorf("flows init: exit %d %s", code, out)
	}
	if code, _, errOut := runCLI(t, "flows", "init", "--keybaord"); code != exitUsage || !strings.Contains(errOut, "did you mean --keyboard?") { //nolint:misspell // the typo is the test
		t.Errorf("typo: exit %d %s", code, errOut)
	}
}

// acceptFixture stores a snapshot and a comparison with it whose changes
// are a NEW contrast issue on /legal/ (with a selector holding glob
// characters), a BROKE trap on / and a CHANGED title.
func acceptFixture(t *testing.T) {
	t.Helper()
	now := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	target := "http://127.0.0.1:37100"
	contrast := model.Issue{Check: "a11y/color-contrast", Level: model.Warning, Title: "Elements must meet minimum color contrast ratio thresholds", Source: `footer a[href*="x"]`}
	trap := model.Issue{Check: "keyboard/trap", Level: model.Error, Title: "Keyboard focus is trapped", Source: "#chat"}
	contrast.Key = model.IssueKey(contrast.Check, contrast.Title, contrast.Source)
	trap.Key = model.IssueKey(trap.Check, trap.Title, trap.Source)
	before := &model.Run{Schema: model.Schema, Version: "1.0.0", Target: target, Name: "good", Started: now,
		Pages: []model.Page{{Path: "/", Status: 200}, {Path: "/legal/", Status: 200}}}
	after := &model.Run{Schema: model.Schema, Version: "1.0.0", Target: target, Started: now,
		Pages: []model.Page{{Path: "/", Status: 200, Issues: []model.Issue{trap}}, {Path: "/legal/", Status: 200, Issues: []model.Issue{contrast}}}}
	snap := filepath.Join(".soundcheck", "snapshots", "good")
	runDir := filepath.Join(".soundcheck", "runs", "cmp-1")
	for dir, r := range map[string]*model.Run{snap: before, runDir: after} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveRun(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	c := &model.Comparison{Schema: model.Schema, Tool: "rhythm-soundcheck", Version: "1.0.0", Pages: 2, Created: now,
		Before: model.RunRef{Target: target, Name: "good", Started: now}, After: model.RunRef{Target: target, Started: now},
		Changes: []model.Change{
			{ID: "n1", Class: model.New, Check: contrast.Check, Title: contrast.Title, Source: contrast.Source, Key: contrast.Key, Pages: []string{"/legal/"}},
			{ID: "b1", Class: model.Broke, Check: trap.Check, Title: trap.Title, Source: trap.Source, Key: trap.Key, Pages: []string{"/"}},
		}}
	if err := store.SaveComparison(runDir, c); err != nil {
		t.Fatal(err)
	}
}

// accept --ignore writes a rule with the change's check, pages and source
// that still loads and matches the selector itself; its flags are
// checked first.
func TestAcceptIgnore(t *testing.T) {
	t.Chdir(t.TempDir())
	acceptFixture(t)
	writeConfig(t, "# mine\nrate: 2\n")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"n1", "--ignore"}, "--ignore needs --reason: say why this is known"},
		{[]string{"n1", "--ignore", "--reason", "  "}, "--ignore needs --reason: say why this is known"},
		{[]string{"n1", "--reason", "x"}, "--reason and --until go with --ignore"},
		{[]string{"--page", "*", "--check", "a11y/*", "--ignore", "--reason", "x"}, "--ignore takes change ids, not --page and --check"},
		{[]string{"n1", "--ignore", "--reason", "x", "--until", "soon"}, `--until: "soon" isn't a date like 2026-12-31`},
		{[]string{"zz", "--ignore", "--reason", "x"}, `no change "zz" in this comparison`},
	} {
		code, _, errOut := runCLI(t, append([]string{"accept"}, tc.args...)...)
		if code != exitUsage || errOut != "rhythm-soundcheck: "+tc.want+"\n" {
			t.Errorf("%v: exit %d %q", tc.args, code, errOut)
		}
	}
	code, out, errOut := runCLI(t, "accept", "n1", "--ignore", "--reason", "new theme in December", "--until", "2026-12-31")
	if code != exitOK {
		t.Fatalf("exit %d %s", code, errOut)
	}
	if !strings.Contains(out, "Ignored n1 a11y/color-contrast (/legal/): rule added to .soundcheck/config.yaml") {
		t.Errorf("stdout:\n%s", out)
	}
	c, err := config.Load(".soundcheck/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Ignore) != 1 || c.Rate != 2 {
		t.Fatalf("rules %+v rate %v", c.Ignore, c.Rate)
	}
	r := c.Ignore[0]
	if r.Check != "a11y/color-contrast" || !slices.Equal(r.Pages, []string{"/legal/"}) || r.Until != "2026-12-31" ||
		r.Reason != "new theme in December" || r.Title != "" {
		t.Errorf("rule %+v", r)
	}
	if !model.Glob(r.Source, `footer a[href*="x"]`) {
		t.Errorf("source %q doesn't match the selector", r.Source)
	}
	// The config is meant to be committed, so the rule writer leaves it
	// readable (0644) even when it was private before.
	if m := filePerm(t, ".soundcheck/config.yaml", 0o644); m != 0o644 {
		t.Errorf("config mode %o", m)
	}
	if data, _ := os.ReadFile(".soundcheck/config.yaml"); !strings.Contains(string(data), "# mine") {
		t.Errorf("comments lost:\n%s", data)
	}
	// The snapshot is untouched: ignoring isn't accepting.
	if info, err := store.LoadSnapshotInfo(filepath.Join(".soundcheck", "snapshots", "good")); err != nil || len(info.Accepted) != 0 {
		t.Errorf("snapshot changed: %+v %v", info, err)
	}
}

// A BROKE id is accepted like a CHANGED one; a --page and --check
// backlog is counted.
func TestAcceptNewBrokeAndBacklog(t *testing.T) {
	t.Chdir(t.TempDir())
	acceptFixture(t)
	code, out, errOut := runCLI(t, "accept", "b1")
	if code != exitOK || !strings.Contains(out, "Accepted b1 Keyboard focus is trapped (/)") {
		t.Fatalf("accept b1: exit %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut = runCLI(t, "accept", "--page", "*", "--check", "a11y/*")
	if code != exitOK || !strings.HasPrefix(out, "Accepted 1 change\n") {
		t.Errorf("backlog: exit %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out, "Accepted n1") {
		t.Errorf("a backlog lists each change:\n%s", out)
	}
}

// After an accept into an --eaa snapshot, evidence verify passes for
// the snapshot and for the run chained to it. Accept counts every
// change the snapshot holds, not only this call's.
func TestAcceptKeepsTheEvidenceChain(t *testing.T) {
	t.Chdir(t.TempDir())
	acceptFixture(t)
	snap := filepath.Join(".soundcheck", "snapshots", "good")
	runDir := filepath.Join(".soundcheck", "runs", "cmp-1")
	for _, dir := range []string{snap, runDir} {
		run, err := store.LoadRun(dir)
		if err != nil {
			t.Fatal(err)
		}
		run.Options.EAA = true
		if err := store.SaveRun(dir, run); err != nil {
			t.Fatal(err)
		}
	}
	if err := evidence.Write(snap, must(store.LoadRun(snap)), nil); err != nil {
		t.Fatal(err)
	}
	prev, err := evidence.Previous(".soundcheck", "http://127.0.0.1:37100")
	if err != nil || prev == nil {
		t.Fatalf("Previous = %v, %v", prev, err)
	}
	if err := evidence.Write(runDir, must(store.LoadRun(runDir)), prev); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, "accept", "b1")
	if code != exitOK || !strings.Contains(out, `Snapshot "good" now holds 1 accepted change.`) ||
		!strings.Contains(errOut, "its evidence pack was recorded again") {
		t.Fatalf("accept b1: exit %d\n%s\n%s", code, out, errOut)
	}
	code, out, _ = runCLI(t, "accept", "n1")
	if code != exitOK || !strings.Contains(out, `Snapshot "good" now holds 2 accepted changes.`) {
		t.Errorf("accept n1: exit %d\n%s", code, out)
	}
	code, out, errOut = runCLI(t, "evidence", "verify", runDir)
	if code != exitOK || !strings.Contains(out, "Verified") || !strings.Contains(errOut, "snapshots/good changed by accept after runs/cmp-1 was chained to it") {
		t.Errorf("verify run: exit %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut = runCLI(t, "evidence", "verify", "good")
	if code != exitOK || !strings.Contains(errOut, "snapshots/good was recorded again after accept") {
		t.Errorf("verify snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	// Deleting the snapshot keeps its pack for the run's chain.
	code, _, errOut = runCLI(t, "snapshots", "delete", "good")
	if code != exitOK || !strings.Contains(errOut, `note: snapshot "good" had an evidence pack; it is kept in .soundcheck/evidence/`) {
		t.Errorf("delete: exit %d %s", code, errOut)
	}
	if code, out, errOut := runCLI(t, "evidence", "verify", runDir); code != exitOK || !strings.Contains(errOut, "was deleted or taken again") {
		t.Errorf("verify after delete: exit %d\n%s\n%s", code, out, errOut)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestIgnoreRule(t *testing.T) {
	// A selector is written as it is and matches itself as compare reads
	// it; brackets and question marks are text there.
	for _, sel := range []string{`input[name="email"]`, `a[x*="?"]\`, "footer a"} {
		r, err := ignoreRule(model.Change{Check: "a11y/label", Source: sel, Pages: []string{"/"}}, "", "")
		if err != nil || r.Source != sel || !model.Glob(r.Source, sel) {
			t.Errorf("%q: rule %+v %v", sel, r, err)
		}
		if model.Glob(r.Source, "input#other") {
			t.Errorf("%q matches another selector", sel)
		}
		// The rule as the config holds it ignores the change in a
		// comparison, so StaleIgnores never calls it stale.
		c := &model.Comparison{Changes: []model.Change{{Class: model.New, Check: "a11y/label", Source: sel, Pages: []string{"/"}}}}
		compare.ApplyIgnore(c, ignoreRules(config.Config{Ignore: []config.IgnoreRule{r}}), time.Now())
		if len(c.Changes) != 0 || len(c.Ignored) != 1 {
			t.Errorf("%q: the rule didn't ignore its change: %+v", sel, c)
		}
	}
	r, err := ignoreRule(model.Change{Check: "visual/page", Title: "Page looks different", Pages: []string{"/team/"}}, "photos", "")
	if err != nil || r.Title != "Page looks different" || r.Source != "" {
		t.Errorf("no source: %+v %v", r, err)
	}
	// Brackets and ? are literal in a page glob, so those pages are kept
	// as they are and match only themselves.
	odd := []string{"/q?a=[1]/"}
	if r, err := ignoreRule(model.Change{ID: "c1", Check: "a11y/x", Pages: odd}, "known", ""); err != nil || !slices.Equal(r.Pages, odd) || !model.Glob(r.Pages[0], odd[0]) || model.Glob(r.Pages[0], "/q/") {
		t.Errorf("literal page: %+v %v", r, err)
	}
	// A * would widen the rule to other pages: refused.
	_, err = ignoreRule(model.Change{ID: "c2", Check: "a11y/x", Pages: []string{"/ok/", "/a*b/"}}, "known", "")
	if err == nil || !strings.Contains(err.Error(), `can't ignore c2: page "/a*b/" has a *`) {
		t.Errorf("star page: %v", err)
	}
	if got := ruleText(config.IgnoreRule{Check: "a11y/color-contrast", Pages: []string{"/legal/*"}}); got != "a11y/color-contrast on /legal/*" {
		t.Errorf("ruleText = %q", got)
	}
	if got := ruleText(config.IgnoreRule{Title: "x"}); got != `"x" on every page` {
		t.Errorf("ruleText = %q", got)
	}
}

// history --metric takes history.Metrics and suggests the closest name.
func TestHistoryMetric(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, m := range []string{"a11y", "keyboard", "lcp"} {
		if code, _, errOut := runCLI(t, "history", "--metric", m); code != exitOK {
			t.Errorf("--metric %s: exit %d %s", m, code, errOut)
		}
	}
	code, _, errOut := runCLI(t, "history", "--metric", "a11", "--plain")
	if want := "rhythm-soundcheck: --metric: \"a11\" isn't one of lcp, tbt, ttfb, a11y, keyboard (did you mean \"a11y\"?)\n"; code != exitUsage || errOut != want {
		t.Errorf("--metric a11: exit %d %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "history", "--metric", "bogus"); code != exitUsage || !strings.Contains(errOut, `"bogus" isn't one of`) {
		t.Errorf("--metric bogus: exit %d %q", code, errOut)
	}
}

// Under --eaa an ignore rule that matched nothing is named, so the
// committed baseline can lose it; -q and runs without --eaa stay quiet.
func TestStaleIgnoreNote(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	writeConfig(t, "ignore:\n  - check: visual/page\n    pages: [/team/*]\n  - check: a11y/color-contrast\n    pages: [/legal/*]\n    reason: new theme\n")
	f := &fakeEAA{pages: clean}
	f.install(t)
	old := staleIgnores
	t.Cleanup(func() { staleIgnores = old })
	var got int
	staleIgnores = func(rules []compare.IgnoreRule, _ time.Time, pages func(func(*model.Page) bool), _ []model.Issue) []int {
		got = len(rules)
		n := 0
		for range pages {
			n++
		}
		if n != 2 {
			t.Errorf("pages: %d", n)
		}
		return []int{1, 7}
	}
	const note = "note: ignore rule 2 (a11y/color-contrast on /legal/*) matched nothing; the problem looks fixed: remove it\n"
	code, out, errOut := runCLI(t, "snapshot", "127.0.0.1:37101", "--eaa", "--name", "s1", "--plain")
	if code != exitOK || !strings.Contains(errOut, note) || got != 2 {
		t.Errorf("--eaa: exit %d rules %d\n%s\n%s", code, got, out, errOut)
	}
	if strings.Contains(errOut, "rule 8") {
		t.Errorf("an index past the rules was named:\n%s", errOut)
	}
	if _, _, errOut := runCLI(t, "snapshot", "127.0.0.1:37101", "--eaa", "--name", "s2", "-q"); strings.Contains(errOut, "note: ignore rule") {
		t.Errorf("-q: %s", errOut)
	}
	if _, _, errOut := runCLI(t, "snapshot", "127.0.0.1:37101", "--modules", "health", "--name", "s3", "--plain"); strings.Contains(errOut, "note: ignore rule") {
		t.Errorf("without --eaa: %s", errOut)
	}
}

// Taking a snapshot again under the name of one with an evidence pack
// keeps that pack first and says where.
func TestRetakeKeepsTheEvidencePack(t *testing.T) {
	t.Chdir(t.TempDir())
	acceptFixture(t)
	snap := filepath.Join(".soundcheck", "snapshots", "good")
	var b strings.Builder
	s := &session{st: store.Store{Root: ".soundcheck"}, stderr: &b}
	if err := s.keepReplaced("good"); err != nil || b.Len() != 0 {
		t.Errorf("no pack: %v %q", err, b.String())
	}
	if err := evidence.Write(snap, must(store.LoadRun(snap)), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.keepReplaced("good"); err != nil || !strings.Contains(b.String(), `note: snapshot "good" had an evidence pack; it is kept in .soundcheck/evidence/`) {
		t.Errorf("pack: %v %q", err, b.String())
	}
	if entries, err := os.ReadDir(filepath.Join(".soundcheck", "evidence")); err != nil || len(entries) != 1 {
		t.Errorf("kept: %v %v", entries, err)
	}
	if err := s.keepReplaced("other"); err != nil {
		t.Errorf("no snapshot: %v", err)
	}
}

// The statement lists a change accepted into the snapshot under
// "Accepted or ignored in Soundcheck" while the run still shows it. It
// still counts it as open, as compare does.
func TestStatementListsAcceptedChanges(t *testing.T) {
	t.Chdir(t.TempDir())
	acceptFixture(t)
	runDir := filepath.Join(".soundcheck", "runs", "cmp-1")
	run := must(store.LoadRun(runDir))
	run.Options.EAA, run.Options.WCAG = true, "2.1"
	if err := store.SaveRun(runDir, run); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runCLI(t, "accept", "b1"); code != exitOK {
		t.Fatalf("accept: exit %d\n%s\n%s", code, out, errOut)
	}
	in, err := statementInput(runDir, "")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(in.Ignored, func(c model.Change) bool { return c.Check == "keyboard/trap" })
	if i < 0 || !slices.Equal(in.Ignored[i].Pages, []string{"/"}) || !strings.HasPrefix(in.Ignored[i].Reason, "accepted into the snapshot on ") {
		t.Fatalf("ignored = %+v", in.Ignored)
	}
	if in.Summary.Open != 2 {
		t.Errorf("open = %d, want the trap and the contrast issue", in.Summary.Open)
	}
	in.Owner = statement.Owner{Owner: "Example Ltd", Website: "https://shop.example", Contact: statement.Contact{Email: "a@shop.example"}}
	d, err := statement.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := statement.Markdown(&b, d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "**2.1.2**") || !strings.Contains(b.String(), "(keyboard/trap) on /: accepted into the snapshot on") {
		t.Errorf("statement:\n%s", b.String())
	}

	// Once the run no longer shows it, it isn't listed.
	run.Pages[0].Issues = nil
	if err := store.SaveRun(runDir, run); err != nil {
		t.Fatal(err)
	}
	if in, err := statementInput(runDir, ""); err != nil || slices.ContainsFunc(in.Ignored, func(c model.Change) bool { return c.Check == "keyboard/trap" }) {
		t.Errorf("fixed since: %+v %v", in.Ignored, err)
	}
}

// Accept into an --eaa snapshot writes its statement draft again, so
// the draft lists the accepted problem as statement of the snapshot
// does. A draft that can't be written again is removed, never left
// stale.
func TestAcceptRewritesTheStatementDraft(t *testing.T) {
	t.Chdir(t.TempDir())
	acceptFixture(t)
	snap := filepath.Join(".soundcheck", "snapshots", "good")
	run := must(store.LoadRun(snap))
	run.Options.EAA, run.Options.WCAG = true, "2.1"
	if err := store.SaveRun(snap, run); err != nil {
		t.Fatal(err)
	}
	if err := evidence.Write(snap, must(store.LoadRun(snap)), nil); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(snap, eaaDir, draftFile)
	if err := os.WriteFile(draft, []byte("stale: failures of 0 of 50 criteria\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, "accept", "b1")
	if code != exitOK {
		t.Fatalf("accept b1: exit %d\n%s\n%s", code, out, errOut)
	}
	got := string(must(os.ReadFile(draft)))
	if strings.Contains(got, "stale") || !strings.Contains(got, "(keyboard/trap) on /: accepted into the snapshot on") ||
		!strings.Contains(got, "**2.1.2**") {
		t.Errorf("draft after accept:\n%s", got)
	}
	in := must(statementInput(snap, ""))
	if len(in.Summary.Criteria.Failed) == 0 {
		t.Errorf("snapshot summary = %+v, want the accepted failure", in.Summary)
	}

	// The config file is broken, so the draft can't be drawn: it is
	// removed and the note says how to draft one.
	if err := os.WriteFile(filepath.Join(".soundcheck", "config.yaml"), []byte("statement: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runCLI(t, "accept", "n1")
	if code != exitOK || !strings.Contains(errOut, `the statement draft of snapshot "good" couldn't be written again`) ||
		!strings.Contains(errOut, "statement good") {
		t.Errorf("accept n1: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(draft); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stale draft kept: %v", err)
	}
}
