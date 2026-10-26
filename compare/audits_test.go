// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func a11yIssue(rule, source string) model.Issue {
	i := model.Issue{Check: "a11y/" + rule, Level: model.Warning, Title: rule, Source: source, WCAG: []string{"1.3.1"}}
	i.Identify()
	return i
}

func ok() map[string]model.Audit { return map[string]model.Audit{"a11y": {Status: model.AuditOK}} }

// both compares the runs with Compare and with Stream, which must agree.
func both(t *testing.T, before, after *model.Run) *model.Comparison {
	t.Helper()
	c := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	s := streamed(t, before, after, nil)
	if got, want := comparisonJSON(t, s), comparisonJSON(t, c); got != want {
		t.Fatalf("Stream differs from Compare\nstream:\n%s\ncompare:\n%s", got, want)
	}
	return c
}

// A page whose audit timed out found nothing: its snapshot's
// violations aren't FIXED (security.md 3.1). They're kept aside with a
// reason and the comparison says the page wasn't checked.
func TestTimedOutAuditFixesNothing(t *testing.T) {
	label, contrast := a11yIssue("label", "#email"), a11yIssue("color-contrast", "footer a")
	php := issue("health/php-error", model.Warning, "PHP Warning")
	opts := model.Options{Modules: []string{"health", "a11y"}}
	before := &model.Run{Options: opts, Pages: []model.Page{
		{Path: "/shop/", Issues: []model.Issue{label, contrast, php}, Audits: ok()},
		{Path: "/", Issues: []model.Issue{label}, Audits: ok()},
	}}
	after := &model.Run{Options: opts, Pages: []model.Page{
		{Path: "/shop/", Audits: map[string]model.Audit{"a11y": {Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}}},
		{Path: "/", Audits: ok()},
	}}
	c := both(t, before, after)
	var fixed []string
	for _, ch := range c.Changes {
		if ch.Class == model.Fixed {
			fixed = append(fixed, ch.Check+" "+ch.Pages[0])
		}
	}
	// The PHP warning went away for real; the label on / was audited.
	if want := []string{"a11y/label /", "health/php-error /shop/"}; !slices.Equal(fixed, want) {
		t.Errorf("fixed = %v, want %v", fixed, want)
	}
	if len(c.Ignored) != 2 {
		t.Fatalf("ignored = %+v", c.Ignored)
	}
	for _, ch := range c.Ignored {
		if ch.Class != model.Fixed || ch.Reason != "not checked on this page: a11y timed out" || !slices.Equal(ch.Pages, []string{"/shop/"}) ||
			!slices.Equal(ch.WCAG, []string{"1.3.1"}) || ch.ID == "" {
			t.Errorf("ignored change = %+v", ch)
		}
	}
	w := "not checked: /shop/ (a11y: axe-core timed out after 45s); its issues are kept from the snapshot, not counted as fixed"
	if !slices.Contains(c.Warnings, w) {
		t.Errorf("warnings = %q", c.Warnings)
	}
	if c.Summary.Fixed != 2 || c.Summary.Ignored != 2 {
		t.Errorf("summary = %+v", c.Summary)
	}
}

// A keyboard walk that failed on either side makes neither NEW nor
// FIXED keyboard changes; a11y on the same page is diffed as usual.
func TestFailedKeyboardWalk(t *testing.T) {
	trap := model.Issue{Check: "keyboard/trap", Level: model.Error, Title: "Keyboard trap", Source: "#map"}
	trap.Identify()
	lost := model.Issue{Check: "keyboard/focus-lost", Level: model.Warning, Title: "Focus lost", Source: "#menu"}
	lost.Identify()
	label := a11yIssue("label", "#email")
	opts := model.Options{Modules: []string{"a11y", "keyboard"}}
	failed := model.Audit{Status: model.AuditError, Reason: "the page navigated away"}
	audits := func(kb model.Audit) map[string]model.Audit {
		return map[string]model.Audit{"a11y": {Status: model.AuditOK}, "keyboard": kb}
	}
	before := &model.Run{Options: opts, Pages: []model.Page{{Path: "/", Issues: []model.Issue{lost}, Audits: audits(failed)}}}
	after := &model.Run{Options: opts, Pages: []model.Page{{Path: "/", Issues: []model.Issue{trap, label}, Audits: audits(model.Audit{Status: model.AuditOK})}}}
	c := both(t, before, after)
	if len(c.Changes) != 1 || c.Changes[0].Check != "a11y/label" || c.Changes[0].Class != model.New {
		t.Errorf("changes = %+v", c.Changes)
	}
	var got []string
	for _, ch := range c.Ignored {
		got = append(got, ch.Class+" "+ch.Check+" "+ch.Reason)
	}
	want := []string{"broke keyboard/trap not checked on this page: keyboard failed", "fixed keyboard/focus-lost not checked on this page: keyboard failed"}
	if !slices.Equal(got, want) {
		t.Errorf("ignored = %q, want %q", got, want)
	}
	w := "not checked: / (keyboard: the page navigated away); its issues are kept from the snapshot, not counted as fixed"
	if !slices.Contains(c.Warnings, w) {
		t.Errorf("warnings = %q", c.Warnings)
	}
}

// A snapshot made before 1.3 has no audits: its pages count as audited,
// so the comparison is the usual diff when the run's audits finished.
func TestSnapshotWithoutAudits(t *testing.T) {
	label, contrast := a11yIssue("label", "#email"), a11yIssue("color-contrast", "footer a")
	opts := model.Options{Modules: []string{"a11y"}}
	before := &model.Run{Options: opts, Pages: []model.Page{{Path: "/", Issues: []model.Issue{label}}}}
	after := &model.Run{Options: opts, Pages: []model.Page{{Path: "/", Issues: []model.Issue{contrast}, Audits: ok()}}}
	c := both(t, before, after)
	var got []string
	for _, ch := range c.Changes {
		got = append(got, ch.Class+" "+ch.Check)
	}
	if !slices.Equal(got, []string{"new a11y/color-contrast", "fixed a11y/label"}) || len(c.Ignored) > 0 {
		t.Errorf("changes %q, ignored %+v", got, c.Ignored)
	}
	for _, w := range c.Warnings {
		t.Errorf("warning %q", w)
	}
}

// A FIXED needs the run's audit: a page of an a11y run with none (it
// failed to load, so axe never ran) fixes nothing. A run that names no
// modules (made by hand) is diffed as before audits existed.
func TestFixedNeedsTheRunsAudit(t *testing.T) {
	label := a11yIssue("label", "#email")
	before := &model.Run{Options: model.Options{Modules: []string{"a11y"}}, Pages: []model.Page{{Path: "/", Issues: []model.Issue{label}, Audits: ok()}}}
	after := &model.Run{Options: model.Options{Modules: []string{"a11y"}}, Pages: []model.Page{{Path: "/", Error: "net::ERR_CONNECTION_RESET"}}}
	c := both(t, before, after)
	if len(c.Changes) > 0 || len(c.Ignored) != 1 || c.Ignored[0].Reason != "not checked on this page: a11y skipped" {
		t.Errorf("changes %+v, ignored %+v", c.Changes, c.Ignored)
	}
	w := "not checked: / (a11y: not audited); its issues are kept from the snapshot, not counted as fixed"
	if !slices.Contains(c.Warnings, w) {
		t.Errorf("warnings = %q", c.Warnings)
	}

	before.Options.Modules, after.Options.Modules = nil, nil
	after.Pages[0].Error = ""
	if cs := Issues(before, after); len(cs) != 1 || cs[0].Class != model.Fixed {
		t.Errorf("hand-made runs: %+v", cs)
	}
}

// A capped audit finished: its changes count. Any other module that
// records audits is judged by them the same way.
func TestCappedAndOtherModules(t *testing.T) {
	label := a11yIssue("label", "#email")
	seo := issue("seo/title", model.Notice, "No title")
	audits := map[string]model.Audit{"a11y": {Status: model.AuditCapped, Reason: "max_elements"}, "seo": {Status: model.AuditSkipped, Reason: "status 500"}}
	before := &model.Run{Pages: []model.Page{{Path: "/", Issues: []model.Issue{label, seo}, Audits: ok()}}}
	after := &model.Run{Pages: []model.Page{{Path: "/", Audits: audits}}}
	ch, ignored, notes := IssuesWithGaps(before, after)
	if len(ch) != 1 || ch[0].Check != "a11y/label" || ch[0].Class != model.Fixed {
		t.Errorf("changes = %+v", ch)
	}
	if len(ignored) != 1 || ignored[0].Check != "seo/title" || ignored[0].Reason != "not checked on this page: seo skipped" {
		t.Errorf("ignored = %+v", ignored)
	}
	if want := []string{"not checked: / (seo: status 500); its issues are kept from the snapshot, not counted as fixed"}; !slices.Equal(notes, want) {
		t.Errorf("notes = %q", notes)
	}
}

// The same issue aside on two pages for the same reason is one change
// listing both. Each page gets a warning.
func TestAsideGroupsAcrossPages(t *testing.T) {
	label := a11yIssue("label", "#email")
	timeout := map[string]model.Audit{"a11y": {Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}}
	before := &model.Run{Pages: []model.Page{{Path: "/a/", Issues: []model.Issue{label}}, {Path: "/b/", Issues: []model.Issue{label}}}}
	after := &model.Run{Pages: []model.Page{{Path: "/b/", Audits: timeout}, {Path: "/a/", Audits: timeout}}}
	c := both(t, before, after)
	if len(c.Ignored) != 1 || !slices.Equal(c.Ignored[0].Pages, []string{"/a/", "/b/"}) {
		t.Errorf("ignored = %+v", c.Ignored)
	}
	var notes []string
	for _, w := range c.Warnings {
		if len(w) > 12 && w[:12] == "not checked:" {
			notes = append(notes, w)
		}
	}
	if len(notes) != 2 || !slices.IsSorted(notes) {
		t.Errorf("notes = %q", notes)
	}
}

func TestMergeAudits(t *testing.T) {
	okA := model.Audit{Status: model.AuditOK}
	bad := model.Audit{Status: model.AuditTimeout}
	got := mergeAudits(nil, map[string]model.Audit{"a11y": okA})
	got = mergeAudits(got, map[string]model.Audit{"a11y": bad, "keyboard": okA})
	got = mergeAudits(got, map[string]model.Audit{"a11y": okA})
	if want := map[string]model.Audit{"a11y": bad, "keyboard": okA}; !reflect.DeepEqual(got, want) {
		t.Errorf("merged = %+v", got)
	}
}

// The re-visit that confirms a NEW change can't confirm it with an audit
// that didn't finish: the change is UNSTABLE.
func TestConfirmNeedsAFinishedAudit(t *testing.T) {
	label := a11yIssue("label", "#email")
	trap := model.Issue{Check: "keyboard/trap", Level: model.Error, Title: "Keyboard trap", Source: "#map"}
	trap.Identify()
	before := &model.Run{Pages: []model.Page{{Path: "/", Audits: ok()}}}
	after := &model.Run{Pages: []model.Page{{Path: "/", Issues: []model.Issue{label, trap}, Audits: ok()}}}
	c := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	again := &model.Run{Pages: []model.Page{{Path: "/", Issues: []model.Issue{label, trap}, Audits: map[string]model.Audit{
		"a11y": {Status: model.AuditOK}, "keyboard": {Status: model.AuditTimeout, Reason: "walk timed out"}}}}}
	Confirm(c, before, again)
	got := map[string]string{}
	for _, ch := range c.Changes {
		got[ch.Check] = ch.Class
	}
	if want := map[string]string{"a11y/label": model.New, "keyboard/trap": model.Unstable}; !reflect.DeepEqual(got, want) {
		t.Errorf("classes = %v, want %v", got, want)
	}
}
