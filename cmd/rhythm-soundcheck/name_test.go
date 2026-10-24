// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"sync/atomic"
	"testing"
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
