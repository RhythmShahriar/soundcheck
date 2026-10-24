// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/model"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// interruptedResult is a compare stopped while two pages were loading:
// the site didn't change, so nothing may be reported but the marker.
func interruptedResult() Result {
	issue := func(chk string, l model.Level, title string) model.Issue {
		i := model.Issue{Check: chk, Level: l, Title: title}
		i.Identify()
		return i
	}
	canceled := func(path string) model.Page {
		return model.Page{Path: path, URL: "http://brew.test" + path, Error: model.CanceledError, Issues: []model.Issue{
			issue("health/unreachable", model.Error, "Page didn't load: context canceled"),
			issue("health/white-screen", model.Error, "Blank page (white screen)"),
		}}
	}
	home := model.Page{Path: "/", URL: "http://brew.test/", Status: 200, Title: "Home"}
	before := &model.Run{Schema: model.Schema, Target: "http://brew.test/", Name: "good",
		Pages: []model.Page{home, {Path: "/slow", Status: 200}, {Path: "/shop/", Status: 200}}}
	after := &model.Run{Schema: model.Schema, Tool: "rhythm-soundcheck", Version: "1.0.0", Target: "http://brew.test/",
		Started: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC), Duration: 6000,
		Pages: []model.Page{home, canceled("/slow"), canceled("/shop/")}, Interrupted: true}
	c := compare.Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "1.0.0")
	c.Created = time.Date(2026, 10, 23, 9, 0, 6, 0, time.UTC)
	return Result{Run: after, Comparison: c}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./report -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file (go test ./report -update rewrites it):\n%s", name, got)
	}
}

func TestInterruptedGolden(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	local := time.Local
	time.Local = time.UTC // the HTML report shows local time
	t.Cleanup(func() { time.Local = local })

	r := interruptedResult()
	if !Partial(r) || !r.Comparison.Interrupted {
		t.Fatal("want a partial result")
	}
	if len(r.Comparison.Changes) != 0 {
		t.Fatalf("canceled pages were compared: %+v", r.Comparison.Changes)
	}
	render := map[string]func(*bytes.Buffer) error{
		"interrupted.pretty.golden": func(b *bytes.Buffer) error { NewPretty(b, b, PrettyOptions{}).Summary(r, ""); return nil },
		"interrupted.plain.golden":  func(b *bytes.Buffer) error { NewPretty(b, b, PrettyOptions{Plain: true}).Summary(r, ""); return nil },
		"interrupted.run.plain.golden": func(b *bytes.Buffer) error {
			NewPretty(b, b, PrettyOptions{Plain: true}).Summary(Result{Run: r.Run}, "")
			return nil
		},
		"interrupted.md.golden":   func(b *bytes.Buffer) error { return Markdown(b, r) },
		"interrupted.xml.golden":  func(b *bytes.Buffer) error { return JUnit(b, r, "broke") },
		"interrupted.json.golden": func(b *bytes.Buffer) error { return JSON(b, r) },
		"interrupted.html.golden": func(b *bytes.Buffer) error { return HTML(b, r) },
	}
	for name, fn := range render {
		var b bytes.Buffer
		if err := fn(&b); err != nil {
			t.Fatal(name, err)
		}
		out := b.String()
		if !strings.Contains(strings.ToLower(out), "interrupted") {
			t.Errorf("%s doesn't say the run was interrupted", name)
		}
		for _, bad := range []string{"BROKE] ", "Blank page", "didn't load: context canceled"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s reports a canceled page: %q", name, bad)
			}
		}
		golden(t, name, out)
	}
}

func TestPartialNeverFails(t *testing.T) {
	r := Result{Run: sampleRun(), Comparison: sampleComparison("")}
	if !FailOn(r, "broke") {
		t.Fatal("a full result with a BROKE change fails")
	}
	r.Comparison.Interrupted = true
	if !Partial(r) || FailOn(r, "broke") || FailOn(r, "slower") {
		t.Error("an interrupted comparison must not fail a build")
	}
	run := sampleRun()
	run.Interrupted = true
	if single := (Result{Run: run}); !Partial(single) || FailOn(single, "notice") {
		t.Error("an interrupted run must not fail a build")
	}
	if Partial(Result{Run: sampleRun()}) {
		t.Error("a full run isn't partial")
	}
}

// JUnit for a partial result with findings at the --fail-on threshold
// writes them as output, not failures: CI's test report must not fail a
// run that FailOn doesn't fail. Only the interrupted case is an error.
func TestInterruptedJUnitHasNoFailures(t *testing.T) {
	r := Result{Run: sampleRun(), Comparison: sampleComparison("")}
	r.Comparison.Interrupted = true
	var b bytes.Buffer
	if err := JUnit(&b, r, "broke"); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "<failure") || !strings.Contains(out, `failures="0" errors="1"`) {
		t.Errorf("want no failures and one error:\n%s", out)
	}
	golden(t, "interrupted-findings.xml.golden", out)
}
