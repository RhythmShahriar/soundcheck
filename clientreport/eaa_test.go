// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package clientreport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// eaaFixture is an --eaa snapshot of a shop: a keyboard trap at
// checkout, a missing label, one page axe-core couldn't finish and the
// rest of the WCAG 2.1 criteria split between none found and needs a
// person.
func eaaFixture() report.Result {
	run := &model.Run{Schema: model.Schema, Version: "1.3.0", Target: "https://www.brew.test/",
		Started: time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC),
		Options: model.Options{Viewports: []string{"desktop"}, Modules: []string{"health", "keyboard"}, EAA: true, WCAG: "2.1"}}
	for _, p := range []string{"/", "/shop/", "/checkout/"} {
		run.Pages = append(run.Pages, model.Page{Path: p, Keyboard: &model.KeyboardSummary{Stops: 12}})
	}
	run.Pages[2].Issues = []model.Issue{
		{Check: "keyboard/trap", Level: model.Error, Title: "Keyboard focus is trapped: Card number", Source: "#card-element",
			WCAG: []string{"2.1.2"}, FocusPath: []string{"#billing_email", "#card-element"}},
		{Check: "a11y/label", Level: model.Warning, Title: "Form elements must have labels", Source: "#billing_phone",
			Detail: `<input id="billing_phone">`, WCAG: []string{"1.3.1", "4.1.2"}},
	}
	run.Pages = append(run.Pages, model.Page{Path: "/catalog/", Audits: map[string]model.Audit{"a11y": {Status: model.AuditTimeout}}})
	e := &model.EAASummary{WCAG: "2.1", EN301549: "V3.2.1", Open: 2, EvidenceNote: model.EvidenceNote,
		Keyboard:   model.EAAKeyboard{PagesWalked: 3},
		NotAudited: []model.NotAudited{{Path: "/catalog/", Module: "a11y", Status: model.AuditTimeout, Reason: "axe-core timed out"}},
		Criteria: model.EAACriteria{Failed: []model.CriterionCount{{ID: "1.3.1", Issues: 1, Pages: 1}, {ID: "2.1.2", Issues: 1, Pages: 1},
			{ID: "4.1.2", Issues: 1, Pages: 1}}}}
	for i, c := range wcag.Criteria("2.1") {
		switch {
		case c.ID == "1.3.1" || c.ID == "2.1.2" || c.ID == "4.1.2":
		case i%2 == 0:
			e.Criteria.NoFailuresFound = append(e.Criteria.NoFailuresFound, c.ID)
		default:
			e.Criteria.NotTested = append(e.Criteria.NotTested, c.ID)
		}
	}
	run.EAA = e
	return report.Result{Run: run}
}

// goldenFile compares got with testdata/name, or rewrites it under
// -update.
func goldenFile(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err, " (run go test -update to create it)")
	}
	if got != string(want) {
		t.Errorf("client report differs from %s; run go test ./clientreport -update and review the diff", path)
	}
}

// An --eaa client report says what its accessibility evidence covers,
// counts the criteria, lists what a person must check and carries the
// evidence note; it keeps developer detail out as every client report.
func TestEAAClientReport(t *testing.T) {
	time.Local = time.UTC
	got := render(t, eaaFixture(), Brand{Name: "Acme"})
	goldenFile(t, "eaa.golden.html", got)
	for _, want := range []string{
		"Accessibility evidence: WCAG 2.1 AA automated rules and a keyboard walkthrough of 3 pages",
		"<h2>Accessibility</h2>", "Criteria with failures", "2.1.2 No Keyboard Trap (1 problem on 1 page)",
		"What still needs a person (", "1.2.1 Audio-only and Video-only (Prerecorded)",
		"We couldn't finish the accessibility tests on /catalog/, so that page needs checking again.", model.EvidenceNote,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("--eaa client report lacks %q", want)
		}
	}
	for _, bad := range []string{"Accessibility basics", "#card-element", "billing_phone", "keyboard/trap", "a11y/label", "axe-core timed out"} {
		if strings.Contains(got, bad) {
			t.Errorf("--eaa client report contains %q", bad)
		}
	}
	if n := strings.Count(got, "Accessibility evidence:"); n != 1 {
		t.Errorf("want the evidence line once, got %d", n)
	}
}

// Without --eaa the client report keeps "Accessibility basics" and has
// no accessibility section.
func TestClientReportWithoutEAA(t *testing.T) {
	res := eaaFixture()
	res.Run.EAA, res.Run.Options.EAA = nil, false
	res.Run.Options.Modules = []string{"health", "a11y"}
	got := render(t, res, Brand{Name: "Acme"})
	goldenFile(t, "plain-a11y.golden.html", got)
	if !strings.Contains(got, "<li>Accessibility basics</li>") {
		t.Error("a run without --eaa lost Accessibility basics")
	}
	for _, bad := range []string{"Accessibility evidence", "<h2>Accessibility</h2>", model.EvidenceNote} {
		if strings.Contains(got, bad) {
			t.Errorf("a run without --eaa contains %q", bad)
		}
	}
}

// A comparison takes the comparison's summary, like every format.
func TestEAAClientComparison(t *testing.T) {
	res := eaaFixture()
	c := *res.Run.EAA
	c.Open = 7
	res.Comparison = &model.Comparison{Schema: model.Schema, Pages: 4, EAA: &c}
	got := render(t, res, Brand{Name: "Acme"})
	if !strings.Contains(got, "<b>7</b><span>Open accessibility problems</span>") {
		t.Error("the comparison's summary isn't the one shown")
	}
}
