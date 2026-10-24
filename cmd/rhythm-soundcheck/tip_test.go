// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

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

func TestSeveralOutputs(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	dir := storedComparison(t, model.Broke)
	code, out, errOut := runCLI(t, "report", dir, "--plain", "-o", "out/r.json", "--output", "out/r.md", "-o", "out/r.xml", "-o", "out/r.html")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "[BROKE]") {
		t.Errorf("the live view stays on stdout:\n%s", out)
	}
	for _, f := range []string{"out/r.json", "out/r.md", "out/r.xml", "out/r.html"} {
		if !strings.Contains(errOut, "Report written to "+f+"\n") {
			t.Errorf("no %q in %q", "Report written to "+f, errOut)
		}
	}
	if !strings.Contains(out, "full report: out/r.html") {
		t.Errorf("the hint points at the -o HTML file:\n%s", out)
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var c model.Comparison
	if err := json.Unmarshal(read("out/r.json"), &c); err != nil || c.Count(model.Broke) != 1 {
		t.Errorf("r.json: %v %+v", err, c)
	}
	if md := string(read("out/r.md")); !strings.HasPrefix(md, "#") || !strings.Contains(md, "BROKE") {
		t.Errorf("r.md:\n%s", md)
	}
	var suites struct {
		Failures int `xml:"failures,attr"`
	}
	if err := xml.Unmarshal(read("out/r.xml"), &suites); err != nil {
		t.Errorf("r.xml: %v", err)
	}
	if doc, err := html.Parse(bytes.NewReader(read("out/r.html"))); err != nil || doc.FirstChild == nil ||
		!bytes.Contains(read("out/r.html"), []byte("Page fails")) {
		t.Errorf("r.html: %v", err)
	}

	// One -o with a format flag keeps the flag's format, whatever the name.
	if code, _, errOut := runCLI(t, "report", dir, "--md", "-o", "summary.txt"); code != exitOK || !strings.HasPrefix(string(read("summary.txt")), "#") {
		t.Errorf("--md -o summary.txt: exit %d %q", code, errOut)
	}
	// A format flag alone still goes to stdout.
	if code, out, _ := runCLI(t, "report", dir, "--json"); code != exitOK || json.Unmarshal([]byte(out), &c) != nil {
		t.Errorf("--json: exit %d %q", code, out)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-o", "a.json", "-o", "a.pdf"}, "rhythm-soundcheck: can't tell the format of a.pdf from its extension; use .json, .md, .xml or .html\n"},
		{[]string{"-o", "a.pdf"}, "rhythm-soundcheck: can't tell the format of a.pdf; add --json, --md, --junit or --html\n"},
		{[]string{"-o", "a.json", "-o", "./a.json"}, "rhythm-soundcheck: -o a.json and -o ./a.json are the same file\n"},
		{[]string{"--json", "-o", "a.json", "-o", "a.md"}, "rhythm-soundcheck: --json with several -o: each file's format comes from its extension, so leave out --json\n"},
		{[]string{"--json", "--md", "-o", "a.json"}, "rhythm-soundcheck: pick one output format, not --json and --md\n"},
	} {
		code, _, errOut := runCLI(t, append([]string{"report", dir}, tc.args...)...)
		if code != exitUsage || errOut != tc.want {
			t.Errorf("%v: exit %d %q, want 2 %q", tc.args, code, errOut, tc.want)
		}
	}
	if _, err := os.Stat("a.json"); err == nil {
		t.Error("a usage error must write nothing")
	}

	// A file that can't be written fails the command but not the others.
	if err := os.WriteFile("blocker", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runCLI(t, "report", dir, "-o", "blocker/r.json", "-o", "ok.md")
	if code != exitError || !strings.Contains(errOut, "writing blocker/r.json:") || !strings.Contains(errOut, "Report written to ok.md") {
		t.Errorf("unwritable: exit %d %q", code, errOut)
	}
}
