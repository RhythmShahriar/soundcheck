// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// Every way --eaa is refused, with the exact message and exit 2.
func TestEAAUsageErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	for _, tc := range []struct {
		config string
		env    string
		args   []string
		want   string
	}{
		{"", "", []string{"--eea"}, "unknown flag --eea (did you mean --eaa?)"},
		{"", "", []string{"--wcag", "2"}, `--wcag (a11y.wcag): "2" isn't one of 2.0, 2.1, 2.2`},
		{"", "", []string{"--eaa", "--wcag", "3.0"}, `--wcag (a11y.wcag): "3.0" isn't one of 2.0, 2.1, 2.2`},
		{"", "", []string{"--eaa", "--skip", "keyboard"},
			"--eaa needs the a11y and keyboard modules; drop --skip keyboard, or leave out --eaa to run without them"},
		{"eaa: true\n", "", []string{"--skip", "a11y,keyboard"},
			"--eaa needs the a11y and keyboard modules; drop --skip a11y,keyboard, or leave out --eaa to run without them"},
		// --skip comes first, then --wcag's value, then 2.0.
		{"", "", []string{"--eaa", "--skip", "a11y", "--wcag", "2"},
			"--eaa needs the a11y and keyboard modules; drop --skip a11y, or leave out --eaa to run without them"},
		{"", "", []string{"--eaa", "--wcag", "2.0"},
			"--eaa maps to WCAG 2.1 AA (EN 301 549 V3.2.1); use --wcag 2.1 or 2.2, or leave out --eaa to run the 2.0 rules"},
		{"", "", []string{"--eaa", "--browser", "firefox"},
			"--eaa needs Chrome: the keyboard walkthrough and axe use the Chrome DevTools protocol. Drop --browser firefox, or use --modules +a11y without --eaa"},
		{"eaa: true\n", "", []string{"--browser", "webkit"},
			"--eaa needs Chrome: the keyboard walkthrough and axe use the Chrome DevTools protocol. Drop --browser webkit, or use --modules +a11y without --eaa"},
		{"", "1", []string{"--eaa"},
			"--eaa won't turn off Chrome's sandbox from SOUNDCHECK_NO_SANDBOX alone; add --no-sandbox to the command if you mean it"},
		// The flag set to false asks for the sandbox the variable would
		// still turn off.
		{"", "1", []string{"--eaa", "--no-sandbox=false"},
			"--eaa won't turn off Chrome's sandbox from SOUNDCHECK_NO_SANDBOX alone; add --no-sandbox to the command if you mean it"},
	} {
		writeConfig(t, tc.config)
		t.Setenv("SOUNDCHECK_NO_SANDBOX", tc.env)
		// compare and --against are refused before they read a snapshot
		// or start a browser, like run and snapshot.
		for _, cmd := range [][]string{
			{"run", "127.0.0.1:20260"},
			{"snapshot", "127.0.0.1:20260"},
			{"compare"},
			{"127.0.0.1:20260", "--against", "127.0.0.1:20261"},
		} {
			code, out, errOut := runCLI(t, append(cmd, tc.args...)...)
			if want := "rhythm-soundcheck: " + tc.want + "\n"; code != exitUsage || errOut != want || out != "" {
				t.Errorf("%v %v (config %q): exit %d\n%s\nwant\n%s", cmd, tc.args, tc.config, code, errOut, want)
			}
		}
	}
	// Not refused: the sandbox flag on the command line, Chrome by name,
	// --skip of another module and 2.0 without --eaa.
	writeConfig(t, "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "1")
	for _, args := range [][]string{
		{"--eaa", "--no-sandbox"},
		{"--eaa", "--no-sandbox", "--browser", "chrome", "--skip", "health"},
		{"--wcag", "2.0", "--modules", "+a11y", "--no-sandbox"},
		{"--eaa=false", "--browser", "firefox"},
	} {
		if _, err := parse(append([]string{"a.test"}, args...)); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// report shows a stored run as it was made, so the flags that choose how
// a site is checked are refused there; without them it shows the run.
func TestReportRefusesEAAFlags(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	writeConfig(t, "")
	storeRun(t, time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), model.Options{EAA: true, WCAG: "2.1"})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--eaa"}, "--eaa doesn't apply to report"},
		{[]string{"--wcag", "2.1"}, "--wcag doesn't apply to report"},
		{[]string{"--staging"}, "--staging doesn't apply to report"},
	} {
		code, out, errOut := runCLI(t, append([]string{"report"}, tc.args...)...)
		if code != exitUsage || errOut != "rhythm-soundcheck: "+tc.want+"\n" || out != "" {
			t.Errorf("report %v: exit %d %q %q", tc.args, code, out, errOut)
		}
	}
	if code, _, errOut := runCLI(t, "report", "--plain"); code == exitUsage {
		t.Errorf("plain report: exit %d %q", code, errOut)
	}
}

// --eaa sets the modules nothing else chose, adds a11y and keyboard to
// a list that was chosen and says so in the header.
func TestEAAPreset(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	for _, tc := range []struct {
		config  string
		args    []string
		modules string
		header  string
	}{
		{"", nil, "health,visual,interact,cache,speed", ""},
		{"", []string{"--eaa"}, "health,a11y,keyboard", "--eaa: modules health, a11y, keyboard; WCAG 2.1"},
		{"", []string{"--eaa", "--wcag", "2.2"}, "health,a11y,keyboard", "--eaa: modules health, a11y, keyboard; WCAG 2.2"},
		{"", []string{"--eaa", "--modules", "visual"}, "visual,a11y,keyboard",
			"--eaa: modules visual, a11y, keyboard (--modules kept; a11y and keyboard added); WCAG 2.1"},
		{"", []string{"--eaa", "--modules", "+visual"}, "health,a11y,keyboard,visual", "--eaa: modules health, a11y, keyboard, visual; WCAG 2.1"},
		{"modules: [health, visual]\n", []string{"--eaa"}, "health,visual,a11y,keyboard",
			"--eaa: modules health, visual, a11y, keyboard (.soundcheck/config.yaml kept; a11y and keyboard added); WCAG 2.1"},
		{"modules: [health, visual]\n", []string{"--eaa", "--modules", "+seo"}, "health,visual,seo,a11y,keyboard",
			"--eaa: modules health, visual, seo, a11y, keyboard (.soundcheck/config.yaml kept; a11y and keyboard added); WCAG 2.1"},
		{"eaa: true\n", nil, "health,a11y,keyboard", "--eaa: modules health, a11y, keyboard; WCAG 2.1"},
		{"eaa: true\n", []string{"--eaa=false"}, "health,visual,interact,cache,speed", ""},
		{"", []string{"--eaa", "--quick"}, "health,interact,a11y,keyboard",
			"--eaa: modules health, interact, a11y, keyboard (--quick kept; a11y and keyboard added); WCAG 2.1"},
		{"", []string{"--eaa", "--skip", "health"}, "a11y,keyboard", "--eaa: modules a11y, keyboard; WCAG 2.1"},
	} {
		writeConfig(t, tc.config)
		p, err := parse(append([]string{"a.test"}, tc.args...))
		if err != nil {
			t.Errorf("%q %v: %v", tc.config, tc.args, err)
			continue
		}
		s := &session{plan: p}
		if m, h := strings.Join(p.cfg.Modules, ","), slashes(s.eaaHeader()); m != tc.modules || h != tc.header {
			t.Errorf("%q %v: modules %s header %q\nwant %s %q", tc.config, tc.args, m, h, tc.modules, tc.header)
		}
		if p.cfg.EAA != (tc.header != "") {
			t.Errorf("%q %v: cfg.EAA %v", tc.config, tc.args, p.cfg.EAA)
		}
		if p.cfg.EAA && (p.keyboard == nil || p.keyboard.MaxTabs == 0) {
			t.Errorf("%q %v: keyboard options %+v", tc.config, tc.args, p.keyboard)
		}
	}
}

// --wcag reaches the a11y module's block, --staging the trust settings,
// and the run records EAA mode, the version and the keyboard limits.
func TestEAARecorded(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	writeConfig(t, "")
	p, err := parse([]string{"a.test", "--eaa", "--wcag", "2.2", "--staging"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.cfg.Trust.Staging {
		t.Error("--staging didn't reach the trust settings")
	}
	var block struct {
		WCAG string `yaml:"wcag"`
	}
	if err := config.DecodeBlock("a11y", p.cfg.Block("a11y"), &block); err != nil || block.WCAG != "2.2" {
		t.Errorf("a11y.wcag = %q, %v", block.WCAG, err)
	}
	s := &session{plan: p}
	run := &model.Run{}
	s.recordEAA(run)
	if !run.Options.EAA || run.Options.WCAG != "2.2" || run.Options.Keyboard == nil || run.Options.Keyboard.MaxTabs != 150 {
		t.Errorf("recorded %+v", run.Options)
	}
	// Without --eaa or the keyboard module only the version is recorded.
	p, err = parse([]string{"a.test", "--modules", "+a11y"})
	if err != nil {
		t.Fatal(err)
	}
	run = &model.Run{}
	(&session{plan: p}).recordEAA(run)
	if run.Options.EAA || run.Options.WCAG != "2.1" || run.Options.Keyboard != nil {
		t.Errorf("recorded without --eaa: %+v", run.Options)
	}
}

// saveSnapshot stores a snapshot named name with the given options, as
// a finished snapshot would leave it.
func saveSnapshot(t *testing.T, name string, o model.Options) {
	t.Helper()
	st := store.Store{Root: config.Dir}
	dir := filepath.Join(config.Dir, "snapshots", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	run := &model.Run{Schema: model.Schema, Tool: "rhythm-soundcheck", Version: "1.0.0", Target: "http://127.0.0.1:20260/", Name: name,
		Options: o, Pages: []model.Page{{Path: "/", URL: "http://127.0.0.1:20260/", Status: 200}}}
	if err := store.SaveRun(dir, run); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLatest(name); err != nil {
		t.Fatal(err)
	}
}

// compare takes the snapshot's EAA mode and WCAG version, notes a
// snapshot that had neither and stops on it with --strict.
func TestCompareInheritsEAA(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	writeConfig(t, "")
	newSession := func(t *testing.T, args ...string) (*session, *strings.Builder) {
		t.Helper()
		p, err := parse(args)
		if err != nil {
			t.Fatal(err)
		}
		var errb strings.Builder
		return &session{plan: p, stderr: &errb}, &errb
	}
	compare := func(t *testing.T, s *session, snap *model.Run) ([]settingChange, error) {
		t.Helper()
		changes := inherit(&s.cfg, snap.Options, s.chosen, s.o.skip)
		return s.inheritEAA(snap, changes)
	}
	eaaSnap := &model.Run{Name: "eaa-1", Options: model.Options{EAA: true, WCAG: "2.2", Modules: []string{"health", "a11y", "keyboard"}}}
	oldSnap := &model.Run{Name: "before", Options: model.Options{Modules: []string{"health", "visual"}}}
	a11ySnap := &model.Run{Name: "a11y", Options: model.Options{WCAG: "2.1", Modules: []string{"health", "a11y"}}}

	// A plain compare of an --eaa snapshot is an --eaa compare.
	s, errb := newSession(t)
	changes, err := compare(t, s, eaaSnap)
	if err != nil || len(changes) > 0 || !s.cfg.EAA || s.wcag != "2.2" || strings.Join(s.cfg.Modules, ",") != "health,a11y,keyboard" || errb.Len() > 0 {
		t.Errorf("--eaa snapshot: %v %v eaa %v wcag %s modules %v\n%s", changes, err, s.cfg.EAA, s.wcag, s.cfg.Modules, errb)
	}
	if s.keyboard == nil {
		t.Error("no keyboard options from the --eaa snapshot")
	}
	// ...and refused like one.
	s, _ = newSession(t, "--skip", "keyboard")
	if _, err := compare(t, s, eaaSnap); err == nil || !strings.Contains(err.Error(), "drop --skip keyboard") {
		t.Errorf("--skip keyboard on an --eaa snapshot: %v", err)
	}

	// A 1.2 snapshot without a11y: the 2.0 rules with no note.
	s, errb = newSession(t)
	changes, err = compare(t, s, oldSnap)
	if err != nil || len(changes) > 0 || s.cfg.EAA || s.wcag != "" || errb.Len() > 0 {
		t.Errorf("1.2 snapshot: %v %v eaa %v wcag %q\n%s", changes, err, s.cfg.EAA, s.wcag, errb)
	}
	var block struct {
		WCAG string `yaml:"wcag"`
	}
	if err := config.DecodeBlock("a11y", s.cfg.Block("a11y"), &block); err != nil || block.WCAG != "2.0" {
		t.Errorf("a11y.wcag for a 1.2 snapshot = %q, %v", block.WCAG, err)
	}
	// With a11y asked for, the note about the 2.0 rules.
	s, errb = newSession(t, "--modules", "+a11y")
	if _, err := compare(t, s, oldSnap); err != nil || s.wcag != "" || errb.Len() > 0 ||
		!slices.Equal(s.compareNotes, []string{"note: snapshot \"before\" used the WCAG 2.0 rules; take a new snapshot to add the 2.1 rules"}) {
		t.Errorf("1.2 snapshot with +a11y: %v wcag %q\n%s", err, s.wcag, errb)
	}

	// compare --eaa of a snapshot taken without it: both notes, the 2.1
	// rules and what differs for --strict.
	s, errb = newSession(t, "--eaa")
	changes, err = compare(t, s, oldSnap)
	want := "note: snapshot \"before\" was taken without --eaa; keyboard issues will all be new. Take a new snapshot with --eaa\n"
	if err != nil || s.wcag != "2.1" || errb.String() != want || len(s.compareNotes) != 1 {
		t.Errorf("--eaa on a 1.2 snapshot: %v wcag %q\n%s\nwant\n%s", err, s.wcag, errb, want)
	}
	if got := joinChanges(changes); got != "modules health,visual -> health,visual,a11y,keyboard (--eaa); eaa off -> on (--eaa)" {
		t.Errorf("changes: %s", got)
	}

	// --wcag on a compare wins over the snapshot's and is a change.
	s, _ = newSession(t, "--wcag", "2.2")
	changes, err = compare(t, s, a11ySnap)
	if err != nil || s.wcag != "2.2" || joinChanges(changes) != "wcag 2.1 -> 2.2 (--wcag)" {
		t.Errorf("--wcag 2.2 on a 2.1 snapshot: %v %q %s", err, s.wcag, joinChanges(changes))
	}
	// Without it the snapshot's version is kept, even over a11y.wcag.
	s, _ = newSession(t)
	if _, err := compare(t, s, a11ySnap); err != nil || s.wcag != "2.1" {
		t.Errorf("2.1 snapshot: %v %q", err, s.wcag)
	}

	// Through the command: --strict stops before anything runs.
	saveSnapshot(t, "before", oldSnap.Options)
	code, _, errOut := runCLI(t, "compare", "--eaa", "--strict")
	if code != exitUsage || !strings.Contains(errOut, `--strict: this comparison wouldn't use snapshot "before"'s settings: modules health,visual -> health,visual,a11y,keyboard (--eaa); eaa off -> on (--eaa)`) ||
		!strings.Contains(errOut, `note: snapshot "before" was taken without --eaa`) {
		t.Errorf("compare --eaa --strict: exit %d\n%s", code, errOut)
	}
}

// fleet passes --eaa and --wcag to every site's command.
func TestFleetEAA(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("ACME_SOUNDCHECK_TEST_ARGS", "1")
	t.Setenv("SHOP_SOUNDCHECK_TEST_ARGS", "1")
	sites := "- {name: acme, url: \"http://127.0.0.1:20260/\", args: [--limit, \"5\"]}\n- {name: shop, url: \"http://127.0.0.1:20261/\"}\n"
	if err := os.WriteFile("sites.yaml", []byte(sites), 0o600); err != nil {
		t.Fatal(err)
	}
	runCLI(t, "fleet", "sites.yaml", "snapshot", "--eaa", "--wcag", "2.2")
	for site, want := range map[string]string{
		"acme": "snapshot\n--json\n--limit\n5\n--eaa\n--wcag\n2.2\nhttp://127.0.0.1:20260/",
		"shop": "snapshot\n--json\n--eaa\n--wcag\n2.2\nhttp://127.0.0.1:20261/",
	} {
		data, err := os.ReadFile(filepath.Join(".soundcheck-fleet", site, "args.txt"))
		if err != nil || string(data) != want {
			t.Errorf("%s: %v\n%s\nwant\n%s", site, err, data, want)
		}
	}
	if code, _, errOut := runCLI(t, "fleet", "sites.yaml", "snapshot", "--wcag", "2"); code != exitUsage ||
		errOut != "rhythm-soundcheck: --wcag (a11y.wcag): \"2\" isn't one of 2.0, 2.1, 2.2\n" {
		t.Errorf("fleet --wcag 2: exit %d %q", code, errOut)
	}
}
