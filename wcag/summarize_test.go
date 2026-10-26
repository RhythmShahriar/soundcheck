// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import (
	"fmt"
	"runtime"
	"slices"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// eaaRun is an --eaa run of WCAG 2.1 with the modules it runs.
func eaaRun(modules ...string) *model.Run {
	if modules == nil {
		modules = []string{"health", "interact", "a11y", "keyboard"}
	}
	return &model.Run{Options: model.Options{EAA: true, WCAG: "2.1", Modules: modules,
		A11y: &model.A11yRules{Axe: "4.14.0"}}}
}

// ok is the audits of a page both modules finished.
func ok() map[string]model.Audit {
	return map[string]model.Audit{"a11y": {Status: model.AuditOK}, "keyboard": {Status: model.AuditOK}}
}

func issue(check, source string, wcag ...string) model.Issue {
	i := model.Issue{Check: check, Title: check, Source: source, WCAG: wcag}
	i.Identify()
	return i
}

func each(pages []model.Page) func(func(*model.Page) bool) {
	return func(yield func(*model.Page) bool) {
		for i := range pages {
			if !yield(&pages[i]) {
				return
			}
		}
	}
}

func failed(s model.EAASummary, id string) (model.CriterionCount, bool) {
	i := slices.IndexFunc(s.Criteria.Failed, func(c model.CriterionCount) bool { return c.ID == id })
	if i < 0 {
		return model.CriterionCount{}, false
	}
	return s.Criteria.Failed[i], true
}

// A clean run: no failures, every partly tested criterion is "no
// failures found" and the rest not tested, never anything passed.
func TestSummarizeClean(t *testing.T) {
	pages := []model.Page{
		{Path: "/", Audits: ok(), Keyboard: &model.KeyboardSummary{Stops: 12, Widgets: 2}},
		{Path: "/shop/", Audits: ok(), Keyboard: &model.KeyboardSummary{Stops: 30, Widgets: 1}},
	}
	run := eaaRun()
	run.KeyFlows = &model.KeyFlows{Run: 1, Skipped: []model.SkippedFlow{{File: "checkout.yaml", Reason: "needs --level flows"}}}
	s := Summarize(run, each(pages), nil, nil)
	if len(s.Criteria.Failed) != 0 || len(s.Criteria.NoFailuresFound) != 27 || len(s.Criteria.NotTested) != 23 || s.Open != 0 {
		t.Errorf("clean: %d failed, %d no failures, %d not tested, %d open", len(s.Criteria.Failed), len(s.Criteria.NoFailuresFound), len(s.Criteria.NotTested), s.Open)
	}
	if !slices.Contains(s.Criteria.NotTested, "4.1.1") || !slices.Contains(s.Criteria.NoFailuresFound, "2.1.2") {
		t.Errorf("4.1.1 or 2.1.2 in the wrong list: %+v", s.Criteria)
	}
	want := model.EAAKeyboard{PagesWalked: 2, WidgetsTested: 3, FlowsRun: 1, FlowsSkipped: run.KeyFlows.Skipped}
	if fmt.Sprint(s.Keyboard) != fmt.Sprint(want) {
		t.Errorf("keyboard = %+v, want %+v", s.Keyboard, want)
	}
	if s.WCAG != "2.1" || s.EN301549 != "V3.2.1" || s.Axe != "4.14.0" || s.EvidenceNote != model.EvidenceNote || s.Interrupted || s.NotAudited != nil {
		t.Errorf("summary = %+v", s)
	}
}

// Findings of every kind: axe with and without criteria on the issue,
// keyboard, Soundcheck's own a11y checks, site issues and ignored ones.
func TestSummarizeFindings(t *testing.T) {
	contrast := issue("a11y/color-contrast", "footer a", "1.4.3")
	pages := []model.Page{
		{Path: "/", Audits: ok(), Issues: []model.Issue{contrast, issue("keyboard/trap", "#card"), issue("a11y/reflow", "table")}},
		{Path: "/shop/", Audits: ok(), Issues: []model.Issue{contrast, issue("a11y/image-alt", "img.hero"), issue("health/php-error", "x.php:1")}},
		{Path: "/legal/", Audits: ok(), Issues: []model.Issue{contrast, issue("keyboard/focus-obscured", "#x")}},
	}
	site := []model.Issue{issue("a11y/bypass", "html", "2.4.1")}
	site[0].Pages = []string{"/", "/about/"}
	ignored := []model.Change{{Check: "a11y/color-contrast", Source: "footer a", Reason: "new theme"}}
	run := eaaRun()
	run.Interrupted = true
	s := Summarize(run, each(pages), site, ignored)
	for id, want := range map[string]model.CriterionCount{
		"1.4.3":  {ID: "1.4.3", Issues: 1, Pages: 3}, // ignored still counts as found
		"2.1.2":  {ID: "2.1.2", Issues: 1, Pages: 1},
		"1.4.10": {ID: "1.4.10", Issues: 1, Pages: 1},
		"1.1.1":  {ID: "1.1.1", Issues: 1, Pages: 1}, // from the axe table
		"2.4.1":  {ID: "2.4.1", Issues: 1, Pages: 2}, // a site issue, on a page not streamed too
	} {
		if got, _ := failed(s, id); got != want {
			t.Errorf("%s: %+v, want %+v", id, got, want)
		}
	}
	// focus-obscured is 2.2 only; health isn't accessibility.
	if len(s.Criteria.Failed) != 5 {
		t.Errorf("failed: %+v", s.Criteria.Failed)
	}
	if !slices.IsSortedFunc(s.Criteria.Failed, func(a, b model.CriterionCount) int { return Compare(a.ID, b.ID) }) {
		t.Errorf("failed not in number order: %+v", s.Criteria.Failed)
	}
	// contrast is ignored: open are trap, reflow, image-alt, focus-obscured and bypass.
	if s.Open != 5 || !s.Interrupted {
		t.Errorf("open %d, interrupted %v", s.Open, s.Interrupted)
	}
	if slices.Contains(s.Criteria.NoFailuresFound, "1.4.3") || slices.Contains(s.Criteria.NotTested, "1.4.3") {
		t.Error("a failed criterion is in another list")
	}
}

// One missing label reported by axe and by interact counts once.
func TestSummarizeLabelOnce(t *testing.T) {
	unlabeled := model.Issue{Check: "interact/unlabeled", Title: "Required field has no label: Email", Detail: "#billing_email"}
	unlabeled.Identify()
	unnamed := model.Issue{Check: "interact/unnamed-field", Title: "Form field has no name: Phone", Detail: "in form.checkout (#billing_phone)"}
	unnamed.Identify()
	pages := []model.Page{
		{Path: "/checkout/", Audits: ok(), Issues: []model.Issue{issue("a11y/label", "#billing_email", "4.1.2"), unlabeled, unnamed, issue("a11y/label", "#billing_phone", "4.1.2")}},
		{Path: "/account/", Audits: ok(), Issues: []model.Issue{issue("a11y/label", "#billing_email", "4.1.2")}},
	}
	s := Summarize(eaaRun(), each(pages), nil, nil)
	if got, _ := failed(s, "4.1.2"); got.Issues != 2 || got.Pages != 2 {
		t.Errorf("4.1.2: %+v, want 2 issues on 2 pages", got)
	}
	// interact/unlabeled also fails 3.3.2, still as one finding.
	if got, _ := failed(s, "3.3.2"); got.Issues != 1 || got.Pages != 1 {
		t.Errorf("3.3.2: %+v", got)
	}
	if s.Open != 2 {
		t.Errorf("open %d, want 2 (interact isn't counted, the label once)", s.Open)
	}
	if FindingKey(unnamed) != "label|#billing_phone" || FindingKey(issue("a11y/label", "")) != "a11y/label|" {
		t.Errorf("keys %q, %q", FindingKey(unnamed), FindingKey(issue("a11y/label", "")))
	}
}

// Pages whose audit didn't finish are listed; capped ones count as
// audited.
func TestSummarizeNotAudited(t *testing.T) {
	pages := []model.Page{
		{Path: "/big/", Audits: map[string]model.Audit{"a11y": {Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}, "keyboard": {Status: model.AuditCapped, Reason: "max_tabs"}}},
		{Path: "/error/", Audits: map[string]model.Audit{"a11y": {Status: model.AuditSkipped, Reason: "status 500"}, "keyboard": {Status: model.AuditError, Reason: "walk failed"}}},
		{Path: "/", Audits: ok()},
	}
	s := Summarize(eaaRun(), each(pages), nil, nil)
	want := []model.NotAudited{
		{Path: "/big/", Module: "a11y", Status: "timeout", Reason: "axe-core timed out after 45s"},
		{Path: "/error/", Module: "a11y", Status: "skipped", Reason: "status 500"},
		{Path: "/error/", Module: "keyboard", Status: "error", Reason: "walk failed"},
	}
	if !slices.Equal(s.NotAudited, want) {
		t.Errorf("not audited = %+v", s.NotAudited)
	}
}

// Without the keyboard module, criteria only it tests aren't tested.
func TestSummarizeKeyboardSkipped(t *testing.T) {
	walked := map[string]model.Audit{"a11y": {Status: model.AuditOK}}
	skipped := map[string]model.Audit{"a11y": {Status: model.AuditOK}, "keyboard": {Status: model.AuditSkipped, Reason: "needs Chrome"}}
	for name, tc := range map[string]struct {
		run    *model.Run
		audits map[string]model.Audit
	}{
		"not in the run":        {eaaRun("a11y"), walked},
		"skipped on every page": {eaaRun(), skipped},
	} {
		run, ps := tc.run, []model.Page{{Path: "/", Audits: tc.audits}}
		s := Summarize(run, each(ps), nil, nil)
		for _, id := range []string{"2.1.2", "2.4.3", "2.4.7", "3.2.1", "3.3.1"} {
			if !slices.Contains(s.Criteria.NotTested, id) {
				t.Errorf("%s: %s isn't not tested", name, id)
			}
		}
		// 2.1.1 and 2.4.1 have axe rules too.
		for _, id := range []string{"2.1.1", "2.4.1"} {
			if !slices.Contains(s.Criteria.NoFailuresFound, id) {
				t.Errorf("%s: %s isn't no failures found", name, id)
			}
		}
	}
}

// 2.2 has its own criteria: focus-obscured counts there.
func TestSummarizeWCAG22(t *testing.T) {
	run := eaaRun()
	run.Options.WCAG = "2.2"
	pages := []model.Page{{Path: "/", Audits: ok(), Issues: []model.Issue{issue("keyboard/focus-obscured", "#x")}}}
	s := Summarize(run, each(pages), nil, nil)
	if got, ok := failed(s, "2.4.11"); !ok || got.Issues != 1 {
		t.Errorf("2.4.11: %+v", s.Criteria.Failed)
	}
	if n := len(s.Criteria.Failed) + len(s.Criteria.NoFailuresFound) + len(s.Criteria.NotTested); n != 55 || s.EN301549 != "V4.1.1" {
		t.Errorf("%d criteria, EN %s", n, s.EN301549)
	}
}

// 10,000 pages stream through in little memory: Summarize keeps counts,
// not pages.
func TestSummarizeMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	const n = 10000
	var before, during runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	pages := func(yield func(*model.Page) bool) {
		for i := range n {
			p := &model.Page{Path: fmt.Sprintf("/product/%d/", i), Audits: ok(), Keyboard: &model.KeyboardSummary{Stops: 40},
				Issues: []model.Issue{issue("a11y/color-contrast", "footer a", "1.4.3"), issue("a11y/image-alt", fmt.Sprintf("img#p%d", i%500))}}
			if i == n-1 {
				runtime.GC()
				runtime.ReadMemStats(&during)
			}
			if !yield(p) {
				return
			}
		}
	}
	s := Summarize(eaaRun(), pages, nil, nil)
	if got, _ := failed(s, "1.1.1"); got.Issues != 500 || got.Pages != n {
		t.Errorf("1.1.1: %+v", got)
	}
	if grew := int64(during.HeapAlloc) - int64(before.HeapAlloc); grew > 20<<20 {
		t.Errorf("heap grew %d MB over %d pages", grew>>20, n)
	}
}
