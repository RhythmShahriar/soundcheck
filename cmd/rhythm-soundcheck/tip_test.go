// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// storedComparison saves a comparison with changes of the given classes
// in a run folder, for report to show without a browser.
func storedComparison(t *testing.T, classes ...string) string {
	t.Helper()
	dir := filepath.Join(".soundcheck", "runs", "cmp-"+strings.Join(classes, "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	run := &model.Run{Schema: model.Schema, Version: "1.0.0", Target: "http://127.0.0.1:20260", Started: now,
		Pages: []model.Page{{Path: "/"}}}
	c := &model.Comparison{Schema: model.Schema, Tool: "rhythm-soundcheck", Version: "1.0.0", Pages: 1, Created: now,
		Before: model.RunRef{Target: run.Target, Name: "good", Started: now}, After: model.RunRef{Target: run.Target, Started: now}}
	for _, class := range classes {
		c.Changes = append(c.Changes, model.Change{Class: class, Check: "health.status", Title: "Page fails", Pages: []string{"/"}})
	}
	if err := store.SaveRun(dir, run); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveComparison(dir, c); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFailOnTip(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	const broke = "tip: add --fail-on broke to make this exit 1 in scripts\n"
	brokeDir, newDir := storedComparison(t, model.Broke, model.New), storedComparison(t, model.New)
	quietDir, fixedDir := storedComparison(t, model.Changed, model.Slower), storedComparison(t, model.Fixed)

	for _, tc := range []struct {
		name string
		args []string
		want string // on stderr, or "" for no tip
	}{
		{"broke", []string{brokeDir, "--plain"}, broke},
		{"only new", []string{newDir, "--plain"}, "tip: add --fail-on new to make this exit 1 in scripts\n"},
		{"changed and slower", []string{quietDir, "--plain"}, ""},
		{"fixed", []string{fixedDir, "--plain"}, ""},
		{"quiet", []string{brokeDir, "--plain", "-q"}, ""},
		{"json on stdout", []string{brokeDir, "--json"}, ""},
		{"md on stdout", []string{brokeDir, "--md"}, ""},
		{"junit on stdout", []string{brokeDir, "--junit"}, ""},
		{"file output keeps the live view", []string{brokeDir, "--plain", "-o", "r.json"}, broke},
		{"--fail-on given", []string{brokeDir, "--plain", "--fail-on", "changed"}, ""},
	} {
		code, _, errOut := runCLI(t, append([]string{"report"}, tc.args...)...)
		if tc.want == "" && strings.Contains(errOut, "tip:") || tc.want != "" && !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: exit %d, stderr %q; want tip %q", tc.name, code, errOut, tc.want)
		}
		if tc.want != "" && code != exitOK {
			t.Errorf("%s: exit %d, want 0", tc.name, code)
		}
	}

	// fail_on in the config counts as given.
	writeConfig(t, "fail_on: slower\n")
	if code, _, errOut := runCLI(t, "report", brokeDir, "--plain"); code != exitFindings || strings.Contains(errOut, "tip:") {
		t.Errorf("fail_on: exit %d %q", code, errOut)
	}
}
