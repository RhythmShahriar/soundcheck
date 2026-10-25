// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/cli"
)

// names are the ways Soundcheck is started and what it should call itself.
var names = []struct {
	argv0, env, want string
}{
	{"rhythm-soundcheck", "", "rhythm-soundcheck"},
	{"/usr/local/bin/rhythm-soundcheck.exe", "", "rhythm-soundcheck"},
	{`C:\Tools\Rhythm-Soundcheck.EXE`, "", "Rhythm-Soundcheck"},
	{"/home/me/go/bin/rhythm-soundcheck", "rhythm soundcheck", "rhythm soundcheck"},
	// Something that can't be a command name falls back.
	{"/opt/rhythm-soundcheck", "rhythm\x1b[2Jsoundcheck", "rhythm-soundcheck"},
	{"/", "", "rhythm-soundcheck"},
}

func asCommand(t *testing.T, a0, env string) {
	t.Helper()
	old := argv0
	argv0 = a0
	t.Cleanup(func() { argv0 = old })
	t.Setenv(invokedAs, env)
}

func TestCmdName(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range names {
		asCommand(t, tc.argv0, tc.env)
		if got := cmdName(); got != tc.want {
			t.Errorf("argv0 %q, %s=%q: %q, want %q", tc.argv0, invokedAs, tc.env, got, tc.want)
		}
		_, out, _ := runCLI(t, "--help")
		for _, want := range []string{"♪ " + tc.want + "  ", "\n  " + tc.want + " snapshot <url> [flags]   record",
			"\n  " + tc.want + " compare --open\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: help lacks %q", tc.want, want)
			}
		}
		if tc.want != "rhythm soundcheck" && strings.Contains(out, "rhythm soundcheck") {
			t.Errorf("%s: help still says rhythm soundcheck", tc.want)
		}
		_, _, errOut := runCLI(t, "snapshot")
		if want := tc.want + ": snapshot needs one site\n  try: " + tc.want + " snapshot staging.example.com\n"; errOut != want {
			t.Errorf("snapshot with no site:\n%q\nwant\n%q", errOut, want)
		}
		// A message from the store names the command too.
		_, _, errOut = runCLI(t, "compare")
		if want := "take one first: " + tc.want + " snapshot <url>"; !strings.Contains(errOut, want) {
			t.Errorf("compare with no snapshot: %q, want %q", errOut, want)
		}
		// The run header uses the same name as the hints.
		t.Setenv("SOUNDCHECK_ECHO", "")
		_, out, _ = runCLI(t, "run", "127.0.0.1:20260", "--echo="+deadSite(t), "--plain", "--modules", "health")
		if want := tc.want + " 1.0.0-test - 127.0.0.1:20260\n"; !strings.HasPrefix(out, want) {
			t.Errorf("header: %q, want it to start with %q", out, want)
		}
	}
}

// Where the name is one a terminal can show, it is the kit's cli.Name:
// the rhythm CLI's RHYTHM_INVOKED_AS, else the binary's name.
func TestCmdNameIsKitName(t *testing.T) {
	for _, env := range []string{"", "rhythm soundcheck"} {
		asCommand(t, os.Args[0], env)
		if env == "" {
			os.Unsetenv(invokedAs)
		}
		if got, want := cmdName(), cli.Name(); got != want {
			t.Errorf("%s=%q: cmdName %q, cli.Name %q", invokedAs, env, got, want)
		}
	}
	for code, want := range map[int]int{exitOK: 0, exitFindings: 1, exitUsage: 2, exitError: 3, exitCanceled: 130} {
		if code != want {
			t.Errorf("exit code %d, want %d", code, want)
		}
	}
}

func TestNextHintName(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs the browser")
	}
	var broken atomic.Bool
	srv := shop(&broken)
	defer srv.Close()
	for _, tc := range names[1:4] {
		t.Chdir(t.TempDir())
		asCommand(t, tc.argv0, tc.env)
		code, out, errOut := runCLI(t, "snapshot", srv.URL, "--plain", "--no-download", "--viewport", "desktop",
			"--modules", "health", "--rate", "50", "--limit", "1")
		if want := "then run: " + tc.want + " compare"; code != exitOK || !strings.Contains(out, want) {
			t.Errorf("exit %d, want the hint %q:\n%s\n%s", code, want, out, errOut)
		}
	}
}

// The usage rows' wrapped descriptions stay in their column under any
// command name.
func TestUsageLinesUpForAnyName(t *testing.T) {
	for _, name := range []string{"rhythm-soundcheck", "rhythm soundcheck", "sc", "my-very-long-soundcheck-name"} {
		text := withName(help, name)
		lines := strings.Split(text, "\n")
		col := -1
		for i, l := range lines {
			if strings.HasPrefix(l, "  "+name+" report ") {
				col = strings.Index(l, "show the last result again")
				if next := lines[i+1]; strings.Index(next, "(--serve") != col {
					t.Errorf("%s: %q under %q", name, next, l)
				}
			}
			if strings.HasPrefix(l, "  "+name+" help ") {
				at := strings.Index(l, "more on")
				if next := lines[i+1]; strings.Index(next, "import,") != at {
					t.Errorf("%s: %q under %q", name, next, l)
				}
			}
		}
		if col < 0 {
			t.Errorf("%s: no report row", name)
		}
		if strings.Contains(text, "rhythm soundcheck") && name != "rhythm soundcheck" {
			t.Errorf("%s: the placeholder name is left", name)
		}
	}
}

// A snapshot ends with the path of its report.html, as compare does,
// then what to run next; -q and --json leave the hint out.
func TestSnapshotFullReportHint(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs the browser")
	}
	var broken atomic.Bool
	srv := shop(&broken)
	defer srv.Close()
	t.Chdir(t.TempDir())
	snap := func(name string, extra ...string) (int, string, string) {
		args := append([]string{"snapshot", srv.URL, "--name", name, "--no-download", "--viewport", "desktop",
			"--modules", "health", "--rate", "50", "--limit", "1"}, extra...)
		return runCLI(t, args...)
	}
	code, out, errOut := snap("plain", "--plain")
	want := "full report: .soundcheck/snapshots/plain/report.html\n"
	if code != exitOK || !strings.Contains(out, want) {
		t.Fatalf("exit %d, want %q:\n%s\n%s", code, want, out, errOut)
	}
	if strings.Index(out, want) > strings.Index(out, "next: ") {
		t.Errorf("the full report line comes before the next hint:\n%s", out)
	}
	if b, err := os.ReadFile(".soundcheck/snapshots/plain/report.html"); err != nil || !strings.Contains(string(b), "<html") {
		t.Errorf("the snapshot's report.html: %v", err)
	}
	for _, extra := range [][]string{{"--plain", "-q"}, {"--json"}} {
		code, out, errOut := snap("quiet", extra...)
		if code != exitOK || strings.Contains(out+errOut, "full report:") {
			t.Errorf("%v: exit %d, the hint shows:\n%s\n%s", extra, code, out, errOut)
		}
		if _, err := os.Stat(".soundcheck/snapshots/quiet/report.html"); err != nil {
			t.Errorf("%v: no report.html: %v", extra, err)
		}
	}
}
