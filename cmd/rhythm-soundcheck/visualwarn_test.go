// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// A snapshot whose screenshots are gone can't read as "Nothing changed":
// compare says the visual check couldn't run.
func TestCompareWarnsAboutMissingShots(t *testing.T) {
	needBrowser(t)
	t.Chdir(t.TempDir())
	var broken atomic.Bool
	srv := shop(&broken)
	defer srv.Close()
	common := []string{"--no-download", "--viewport", "desktop", "--modules", "visual", "--rate", "50", "--plain", "--limit", "1"}
	if code, out, errOut := runCLI(t, append([]string{"snapshot", srv.URL, "--name", "good"}, common...)...); code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(".soundcheck/snapshots/good/shots"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(".soundcheck/snapshots/good/shots"); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, append([]string{"compare"}, common...)...)
	if code != exitOK {
		t.Fatalf("compare: exit %d\n%s\n%s", code, out, errOut)
	}
	const want = "visual: 1 screenshot from the snapshot is missing or unreadable"
	if !strings.Contains(out+errOut, want) {
		t.Errorf("want %q in:\n%s\n%s", want, out, errOut)
	}
}
