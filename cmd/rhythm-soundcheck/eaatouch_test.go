// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The picker's accessibility entry runs an --eaa snapshot of the site
// asked for; the other entries keep their command lines.
func TestPickerEAA(t *testing.T) {
	var labels, values []string
	for _, o := range pickOptions("") {
		labels, values = append(labels, o.Key), append(values, o.Value)
	}
	if !slices.Contains(labels, "Check accessibility (EAA evidence)") || values[len(values)-1] != pickEAA || slices.Contains(values, "compare") {
		t.Errorf("options %v %v", labels, values)
	}
	if opts := pickOptions(`"before" of x, 1 hour ago`); opts[0].Value != "compare" {
		t.Errorf("compare isn't first: %v", opts)
	}
	for _, tc := range []struct {
		action, site, output string
		want                 []string
	}{
		{pickEAA, " shop.test ", "open", []string{"snapshot", "shop.test", "--eaa", "--open"}},
		{pickEAA, "shop.test", "md", []string{"snapshot", "shop.test", "--eaa", "-o", "soundcheck.md"}},
		{"snapshot", "shop.test", "terminal", []string{"snapshot", "shop.test"}},
		{"compare", "", "open", []string{"compare", "--open"}},
	} {
		if got := pickArgs(tc.action, tc.site, tc.output); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.action, got, tc.want)
		}
	}
}

// The --eaa tip shows once per project after a check with a11y and
// never with -q, --json, in CI or under --eaa.
func TestEAATip(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	t.Setenv("CI", "")
	writeConfig(t, "")
	f := &fakeEAA{pages: clean}
	f.install(t)
	const tip = "tip: --eaa adds a keyboard walkthrough and an accessibility-statement draft\n"
	marker := filepath.Join(".soundcheck", tipEAAFile)

	for _, args := range [][]string{
		{"--modules", "+a11y", "-q"},
		{"--modules", "+a11y", "--json"},
		{"--eaa"},
		{"--modules", "health"},
	} {
		_, _, errOut := runCLI(t, append([]string{"run", "127.0.0.1:37103", "--plain"}, args...)...)
		if strings.Contains(errOut, "tip: --eaa") {
			t.Errorf("%v: tip shown\n%s", args, errOut)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the marker was written without the tip")
	}
	t.Setenv("CI", "true")
	if _, _, errOut := runCLI(t, "run", "127.0.0.1:37103", "--plain", "--modules", "+a11y"); strings.Contains(errOut, "tip: --eaa") {
		t.Errorf("CI: tip shown\n%s", errOut)
	}
	t.Setenv("CI", "")
	if _, _, errOut := runCLI(t, "run", "127.0.0.1:37103", "--plain", "--modules", "+a11y"); !strings.Contains(errOut, tip) {
		t.Errorf("first run: no tip\n%s", errOut)
	}
	if m := filePerm(t, marker, 0o600); m != 0o600 {
		t.Errorf("marker mode %o", m)
	}
	if _, _, errOut := runCLI(t, "run", "127.0.0.1:37103", "--plain", "--modules", "+a11y"); strings.Contains(errOut, "tip: --eaa") {
		t.Errorf("second run: tip again\n%s", errOut)
	}
}

// -o and report.html are only their owner's (0600) and -o refuses a
// symlink at the destination.
func TestReportFileModes(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := storedComparison(t, "new")
	code, _, errOut := runCLI(t, "report", dir, "--plain", "-o", "out/r.md", "-o", "out/r.html")
	if code != exitOK {
		t.Fatalf("exit %d %s", code, errOut)
	}
	for _, p := range []string{"out/r.md", "out/r.html", filepath.Join(dir, "report.html")} {
		if m := filePerm(t, p, 0o600); m != 0o600 {
			t.Errorf("%s: mode %o", p, m)
		}
	}
}
