// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/help.golden")

// TestHelpGolden keeps the help text from changing by accident: a change
// shows up in review as a diff of testdata/help.golden. Run
// go test ./cmd/rhythm-soundcheck -run HelpGolden -update to accept one.
func TestHelpGolden(t *testing.T) {
	code, out, _ := runCLI(t, "--help")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	golden := filepath.Join("testdata", "help.golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Errorf("help changed; if that's intended, run with -update\n--- got\n%s", out)
	}
	for i, line := range strings.Split(out, "\n") {
		if n := len([]rune(line)); n > 80 {
			t.Errorf("help line %d is %d wide: %q", i+1, n, line)
		}
	}
	// What -v really adds, not the per-page lines plain output has anyway.
	if !strings.Contains(out, "-v, --verbose              also show the browser used") {
		t.Error("-v help")
	}
}

// Every value error reads "<flag> (<config key>): <what's wrong>", or
// "<flag>: ..." for flags with no config key; it suggests the closest
// value or flag.
func TestValueErrorWording(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--modules", "visul"}, `--modules (modules): "visul" isn't one of health, visual, interact, cache, speed, a11y, seo, echo, exec (did you mean "visual"?)`},
		{[]string{"--level", "submitt"}, `--level (interactions.levels): "submitt" isn't one of inspect, click, submit, flows (did you mean "submit"?)`},
		{[]string{"--level", "click,+submit"}, `--level (interactions.levels): put + before the first value to add to the list, e.g. +submit`},
		{[]string{"--viewport", "tablet"}, `--viewport (viewports): "tablet" isn't one of desktop, mobile`},
		{[]string{"--skip", "visul"}, `--skip: "visul" isn't one of health, visual, interact, cache, speed, a11y, seo, echo, exec (did you mean "visual"?)`},
		{[]string{"--fail-on", "brok"}, `--fail-on (fail_on): "brok" isn't one of broke, new, changed, slower, error, warning, notice, none (did you mean "broke"?)`},
		{[]string{"--fail-on", "broke"}, `--fail-on (fail_on): "broke" is for comparisons; run and snapshot take error, warning or notice`},
		{[]string{"--tabs", "abc"}, `--tabs (tabs): "abc" isn't a whole number`},
		{[]string{"--tabs", "99"}, `--tabs (tabs): must be between 1 and 16`},
		{[]string{"--limit", "x"}, `--limit (pages.limit): "x" isn't a whole number`},
		{[]string{"--rate", "fast"}, `--rate (rate): "fast" isn't a number`},
		{[]string{"--rate", "0"}, `--rate (rate): 0 isn't between 0.01 and 1000 page loads a second`},
		{[]string{"--timeout", "soon"}, `--timeout (timeout): "soon" isn't a number of seconds or a duration like 30s`},
		{[]string{"--plain=maybe"}, `--plain: "maybe" isn't true or false`},
		{[]string{"-H", "nocolon"}, `--header: "nocolon" isn't a header; use "Name: value"`},
		{[]string{"--proxy", "ftp://x"}, `--proxy: "ftp://x" isn't an http, https or socks5 proxy, e.g. http://127.0.0.1:8080 or socks5://127.0.0.1:1080`},
		{[]string{"--name", "../x"}, `--name: "../x" isn't a snapshot name; use letters, digits, dots, dashes and underscores`},
		{[]string{"--allow-env", "A=B"}, `--allow-env: "A=B" isn't an environment variable name`},
		{[]string{"--allow-host", "://"}, `--allow-host: "://" isn't a host like example.com or localhost:8080`},
		{[]string{"--viewprot", "mobile"}, `unknown flag --viewprot (did you mean --viewport?)`},
		{[]string{"--frobnicate"}, `unknown flag --frobnicate`},
		{[]string{"-x"}, `unknown flag -x`},
		{[]string{"--fail-on"}, `--fail-on needs a value`},
		{[]string{"-o"}, `-o needs a value`},
	} {
		code, _, errOut := runCLI(t, append([]string{"run", "a.test"}, tc.args...)...)
		if code != exitUsage || errOut != "rhythm-soundcheck: "+tc.want+"\n" {
			t.Errorf("%v: exit %d\n got %q\nwant %q", tc.args, code, errOut, tc.want)
		}
	}
	// compare words the other kind the other way round.
	if code, _, errOut := runCLI(t, "compare", "--fail-on", "error"); code != exitUsage ||
		errOut != `rhythm-soundcheck: --fail-on (fail_on): "error" is for run and snapshot; a comparison takes broke, new, changed or slower`+"\n" {
		t.Errorf("compare --fail-on error: exit %d %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "run", "a.test", "--config", "missing.yaml"); code != exitUsage ||
		!strings.HasPrefix(errOut, "rhythm-soundcheck: --config: missing.yaml: ") || strings.Contains(errOut, "stat ") {
		t.Errorf("--config missing.yaml: exit %d %q", code, errOut)
	}
}

func TestTiming(t *testing.T) {
	run := &model.Run{Duration: 2500, Pages: []model.Page{{Path: "/a", Duration: 800}, {Path: "/b", Duration: 500},
		{Path: "/c", Duration: 900}, {Path: "/d", Duration: 120}}}
	if got := timing(run); got != "4 pages in 2.5s; slowest /c 900ms, /a 800ms, /b 500ms" {
		t.Errorf("timing = %q", got)
	}
	run.Pages = nil
	if got := timing(run); got != "0 pages in 2.5s" {
		t.Errorf("no pages: %q", got)
	}
}

// help wp-update lists every exit code wp-update gives, 130 included.
func TestWPUpdateHelpExitCodes(t *testing.T) {
	_, out, _ := runCLI(t, "help", "wp-update")
	if text := strings.Join(strings.Fields(out), " "); !strings.Contains(text, "Exit codes: 0 every update worked, 1 an update broke the site (also when --rollback put it back and the site works now), 2 usage error, 3 an update or check failed, 130 interrupted.") {
		t.Errorf("help wp-update:\n%s", out)
	}
}
