// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// junitOf writes r as JUnit and parses it back.
func junitOf(t *testing.T, r Result, failOn string) (string, junitSuites) {
	t.Helper()
	var b bytes.Buffer
	if err := JUnit(&b, r, failOn); err != nil {
		t.Fatal(err)
	}
	var s junitSuites
	if err := xml.Unmarshal(b.Bytes(), &s); err != nil {
		t.Fatalf("JUnit doesn't parse: %v\n%s", err, b.String())
	}
	return b.String(), s
}

// pageCase finds the case for page and module in the per-page suites.
func pageCase(t *testing.T, s junitSuites, name string) junitCase {
	t.Helper()
	for _, suite := range s.Suites {
		for _, c := range suite.Cases {
			if c.Name == name {
				return c
			}
		}
	}
	t.Fatalf("no case %q", name)
	return junitCase{}
}

// An --eaa run's JUnit has a case per page and module; the trap fails
// /checkout/ · keyboard and the warnings on other pages fail nothing.
func TestJUnitEAASnapshot(t *testing.T) {
	out, s := junitOf(t, Result{Run: eaaSnapshot()}, "")
	golden(t, "eaa-snapshot.xml.golden", out)
	c := pageCase(t, s, "/checkout/ · keyboard")
	if len(c.Failure) != 1 || c.Failure[0].Type != "error" || !strings.Contains(c.Failure[0].Text, "wcag: 2.1.2") {
		t.Errorf("trap case: want one error failure with its criteria, got %+v", c.Failure)
	}
	if c := pageCase(t, s, "/checkout/ · a11y"); len(c.Failure) != 0 {
		t.Errorf("a warning failed a case: %+v", c.Failure)
	}
	if c := pageCase(t, s, "/ · keyboard"); len(c.Failure) != 0 {
		t.Errorf("a page without errors failed: %+v", c.Failure)
	}
	// 39 pages with two cases each, the level suites and the pages case.
	if pages := len(s.Suites) - len(Levels) - 1; pages != 39 {
		t.Errorf("want 39 page suites, got %d", pages)
	}
	// The trap fails the error suite and its page case.
	if s.Failures != 2 {
		t.Errorf("want 2 failures, got %d", s.Failures)
	}
}

// A comparison fails the page case for the BROKE trap; the same trap
// already in the snapshot is output, not a failure.
func TestJUnitEAAComparison(t *testing.T) {
	r := eaaComparison()
	out, s := junitOf(t, r, "")
	golden(t, "eaa-compare.xml.golden", out)
	if c := pageCase(t, s, "/checkout/ · keyboard"); len(c.Failure) != 1 {
		t.Errorf("BROKE trap: want 1 failure, got %+v", c.Failure)
	}
	r.Comparison.Changes = r.Comparison.Changes[1:] // the trap is known
	_, s = junitOf(t, r, "")
	c := pageCase(t, s, "/checkout/ · keyboard")
	if len(c.Failure) != 0 || !strings.Contains(c.Out, "error: Keyboard focus is trapped") {
		t.Errorf("known trap: want output and no failure, got %+v", c)
	}
}

// A module that didn't finish on a page is skipped; a partial run fails
// no page case.
func TestJUnitEAASkippedAndPartial(t *testing.T) {
	_, s := junitOf(t, Result{Run: eaaNotAudited()}, "")
	c := pageCase(t, s, "/catalog/ · a11y")
	if c.Skipped == nil || !strings.Contains(c.Out, "not checked: a11y: axe-core timed out after 45s") {
		t.Errorf("timed-out audit: want skipped, got %+v", c)
	}
	if c := pageCase(t, s, "/catalog/ · keyboard"); c.Skipped != nil {
		t.Errorf("keyboard finished but was skipped: %+v", c)
	}
	run := eaaSnapshot()
	run.Interrupted = true
	_, s = junitOf(t, Result{Run: run}, "")
	if c := pageCase(t, s, "/checkout/ · keyboard"); len(c.Failure) != 0 || !strings.Contains(c.Out, "Keyboard focus is trapped") {
		t.Errorf("partial: want the trap as output, got %+v", c)
	}
}

// Without --eaa, JUnit has no page suites, even when the keyboard
// module ran: its output is unchanged from 1.2.
func TestJUnitWithoutEAA(t *testing.T) {
	run := eaaSnapshot()
	run.EAA, run.Options.EAA, run.Options.Modules = nil, false, []string{"health", "keyboard"}
	out, s := junitOf(t, Result{Run: run}, "")
	if strings.Contains(out, "soundcheck.page ") || strings.Contains(out, " · keyboard") {
		t.Errorf("a run without --eaa got page suites:\n%s", out)
	}
	if s.Failures != 1 {
		t.Errorf("want the trap's one failure, got %d", s.Failures)
	}
}

// A hostile path and title stay valid XML with no control characters.
func TestJUnitEAAHostile(t *testing.T) {
	run := eaaSnapshot()
	trap, _, _, _ := eaaIssues()
	trap.Title = "Trap \x1b]52;c;aGk=\x07 \u202egnp.exe \uFFFE"
	run.Pages = []model.Page{{Path: "/\x1b[2J<x>&/", Status: 200, Issues: []model.Issue{trap}}}
	out, s := junitOf(t, Result{Run: run}, "")
	if strings.ContainsAny(out, "\x1b\x07\u202e\uFFFE") {
		t.Errorf("JUnit holds control characters:\n%s", out)
	}
	if c := pageCase(t, s, `/\x1b[2J<x>&/ · keyboard`); len(c.Failure) != 1 {
		t.Errorf("hostile page: want the trap failure, got %+v", c)
	}
}
