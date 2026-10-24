// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/store"
)

// closedSite returns a URL on this machine that nothing answers.
func closedSite(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:20279")
	if err != nil {
		t.Skip("port 20279 is in use: ", err)
	}
	_ = ln.Close()
	return "http://127.0.0.1:20279/"
}

// Negative: a site that can't be checked leaves no .soundcheck folder in
// the project.
func TestFailedRunLeavesNoFolder(t *testing.T) {
	needBrowser(t)
	site := closedSite(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"run", site},
		{"snapshot", site, "--name", "gone"},
		{site, "--against", site},
	} {
		code, _, errOut := runCLI(t, append(args, "--no-download", "--plain", "--timeout", "5s")...)
		if code != exitError {
			t.Errorf("%v: exit %d, want %d\n%s", args, code, exitError, errOut)
		}
		if _, err := os.Stat(".soundcheck"); err == nil {
			t.Errorf("%v left .soundcheck behind", args)
			_ = os.RemoveAll(".soundcheck")
		}
	}
}

// Positive: a failed run removes only its own folder; what was stored
// before stays.
func TestDiscardKeepsStoredRuns(t *testing.T) {
	t.Chdir(t.TempDir())
	keep := filepath.Join(".soundcheck", "snapshots", "good", "run.json")
	failed := filepath.Join(".soundcheck", "runs", "2026-10-24T12-00-00")
	for _, p := range []string{keep, filepath.Join(failed, "shot.png")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := &session{st: store.Store{Root: config.Dir}}
	s.discard(failed)
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("the stored snapshot went: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".soundcheck", "runs")); err == nil {
		t.Error("the empty runs folder stayed")
	}
}
