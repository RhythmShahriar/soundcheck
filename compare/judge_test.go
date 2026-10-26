// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// judged is a module like keyboard: an issue missing from a page whose
// audit isn't ok (capped included) can't be judged there.
type judged struct{}

func (judged) Name() string                          { return "judged" }
func (judged) CheckPage(*check.Context, *model.Page) {}

func (judged) Unjudged(p *model.Page, keys []string) []string {
	var out []string
	for _, k := range keys {
		if !strings.HasPrefix(k, "judged/") || slices.ContainsFunc(p.Issues, func(i model.Issue) bool { return i.Key == k }) {
			continue
		}
		if a, ok := p.Audits["judged"]; !ok || a.Status != model.AuditOK {
			out = append(out, k)
		}
	}
	return out
}

// The compare package's tests find judged in the registry, as a run
// finds keyboard.
func init() { check.Register(judged{}) }

func TestUnjudgersFindsRegisteredModules(t *testing.T) {
	js := unjudgers()
	if _, ok := js["judged"]; !ok {
		t.Errorf("unjudgers() = %v, want judged among them", js)
	}
	if _, ok := js["health"]; ok {
		t.Error("a module without Unjudged is an Unjudger")
	}
}

// A walk capped on the later visit can't call its module's issues
// FIXED: they're kept aside like those of an unfinished audit. A walk
// that ran clean fixes them. So does a capped audit of a module that
// can't say what it couldn't judge.
func TestCappedWalkFixesNothing(t *testing.T) {
	trap := issue("judged/trap", model.Warning, "Keyboard trap")
	other := issue("other/thing", model.Warning, "Thing")
	opts := model.Options{Modules: []string{"judged", "other"}}
	run := func(status string, issues ...model.Issue) *model.Run {
		return &model.Run{Options: opts, Pages: []model.Page{{Path: "/form/", Status: 200, Issues: issues,
			Audits: map[string]model.Audit{"judged": {Status: status, Reason: "stopped at max_tabs 200"}, "other": {Status: status}}}}}
	}
	before := run(model.AuditOK, trap, other)

	c := both(t, before, run(model.AuditCapped))
	if len(c.Ignored) != 1 || c.Ignored[0].Check != "judged/trap" || c.Ignored[0].Class != model.Fixed ||
		c.Ignored[0].Reason != "not checked on this page: judged capped" {
		t.Errorf("ignored = %+v", c.Ignored)
	}
	if len(c.Changes) != 1 || c.Changes[0].Check != "other/thing" || c.Changes[0].Class != model.Fixed {
		t.Errorf("changes = %+v", c.Changes)
	}
	w := "not checked: /form/ (judged: stopped at max_tabs 200); its issues are kept from the snapshot, not counted as fixed"
	if !slices.Contains(c.Warnings, w) {
		t.Errorf("warnings = %q", c.Warnings)
	}

	c = both(t, before, run(model.AuditOK))
	if len(c.Ignored) != 0 || len(c.Changes) != 2 {
		t.Errorf("a clean walk: changes %+v, ignored %+v", c.Changes, c.Ignored)
	}

	// The issue showed again on the capped walk: nothing changed.
	c = both(t, before, run(model.AuditCapped, trap))
	if len(c.Ignored) != 0 || len(c.Changes) != 1 || c.Changes[0].Check != "other/thing" {
		t.Errorf("found again: changes %+v, ignored %+v", c.Changes, c.Ignored)
	}
}

// On the confirm visit a capped re-walk that doesn't find a NEW issue
// can't confirm it: UNSTABLE. One that finds it again keeps it NEW.
func TestConfirmCappedRewalk(t *testing.T) {
	trap := issue("judged/trap", model.Warning, "Keyboard trap")
	opts := model.Options{Modules: []string{"judged"}}
	page := func(status string, issues ...model.Issue) *model.Run {
		return &model.Run{Options: opts, Pages: []model.Page{{Path: "/form/", Status: 200, Issues: issues,
			Audits: map[string]model.Audit{"judged": {Status: status}}}}}
	}
	before := page(model.AuditOK)
	for _, c := range []struct {
		again *model.Run
		want  string
	}{
		{page(model.AuditCapped), model.Unstable},
		{page(model.AuditCapped, trap), model.New},
		{page(model.AuditTimeout), model.Unstable},
		{page(model.AuditOK, trap), model.New},
	} {
		cmp := both(t, before, page(model.AuditOK, trap))
		Confirm(cmp, before, c.again)
		if len(cmp.Changes) != 1 || cmp.Changes[0].Class != c.want {
			t.Errorf("again %+v: changes %+v, want one %s", c.again.Pages[0].Audits, cmp.Changes, c.want)
		}
	}
}
