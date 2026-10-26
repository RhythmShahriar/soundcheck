// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// fakeTemplates stands in for the flow package's key flow templates,
// which lane flows writes.
func fakeTemplates(t *testing.T) {
	t.Helper()
	old := keyboardTemplates
	t.Cleanup(func() { keyboardTemplates = old })
	keyboardTemplates = func() map[string][]byte {
		out := map[string][]byte{}
		for _, n := range keyFlowFiles {
			out[n] = []byte("name: " + n + "\nstart: /\nkeyboard: true\nsteps:\n  - expect: {focus_within: body}\n")
		}
		return out
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
	fakeTemplates(t)
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
		if msg := err.Error(); strings.Contains(msg, `unknown setting "a11y"`) || strings.Contains(msg, `unknown setting "keyboard"`) {
			t.Logf("pending: axe, keyboard (%v)", err)
		} else {
			t.Errorf("uncommented blocks: %v", err)
		}
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
	fakeTemplates(t)
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
	}
	if keys := keyFlows(filepath.Join(".soundcheck", "flows")); len(keys) != 4 {
		t.Errorf("key flows found: %v", keys)
	}
	if err := os.WriteFile(".soundcheck/flows/keyboard-menu.yaml", []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runCLI(t, "flows", "init", "--keyboard")
	if code != exitUsage || errOut != "rhythm-soundcheck: .soundcheck/flows/keyboard-checkout.yaml already exists\n" {
		t.Errorf("again: exit %d %q", code, errOut)
	}
	if data, _ := os.ReadFile(".soundcheck/flows/keyboard-menu.yaml"); string(data) != "mine\n" {
		t.Errorf("replaced a flow: %q", data)
	}
	// Plain flows init still writes the example only.
	if code, out, _ := runCLI(t, "flows", "init"); code != exitOK || !strings.Contains(out, "example.yaml") {
		t.Errorf("flows init: exit %d %s", code, out)
	}
	if code, _, errOut := runCLI(t, "flows", "init", "--keybaord"); code != exitUsage || !strings.Contains(errOut, "did you mean --keyboard?") {
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
	if ok, err := path.Match(r.Source, `footer a[href*="x"]`); !ok || err != nil {
		t.Errorf("source %q doesn't match the selector: %v", r.Source, err)
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

func TestIgnoreRuleAndEscape(t *testing.T) {
	for in, want := range map[string]string{"": "", "footer a": "footer a", `a[x*="?"]\`: `a\[x\*="\?"\]\\`} {
		if got := globEscape(in); got != want {
			t.Errorf("globEscape(%q) = %q, want %q", in, got, want)
		}
		if in != "" {
			if ok, err := path.Match(globEscape(in), in); !ok || err != nil {
				t.Errorf("%q doesn't match itself: %v", in, err)
			}
		}
	}
	r := ignoreRule(model.Change{Check: "visual/page", Title: "Page looks different", Pages: []string{"/team/"}}, "photos", "")
	if r.Title != "Page looks different" || r.Source != "" {
		t.Errorf("no source: %+v", r)
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
