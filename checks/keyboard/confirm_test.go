// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"slices"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

const trapKey = "keyboard/trap|#date"

// judge is the confirm pass's rule for a keyboard issue seen on the
// first visit, as decided for --eaa: kept when the re-walk saw it again,
// UNSTABLE when the re-walk can't tell (Unjudged) and FIXED only when a
// clean re-walk didn't find it.
func judge(again *model.Page, key string) string {
	switch {
	case reports(again, key):
		return "kept"
	case slices.Contains(Unjudged(again, []string{key}), key):
		return model.Unstable
	}
	return model.Fixed
}

// Only a clean walk proves a keyboard issue gone; a capped, timed out,
// failed, skipped or missing one leaves it unjudged unless the page
// reports it anyway. Other modules' keys are never returned.
func TestUnjudged(t *testing.T) {
	audited := func(status string, issues ...model.Issue) *model.Page {
		p := &model.Page{Path: "/", Issues: issues}
		if status != "" {
			p.Audits = map[string]model.Audit{"keyboard": {Status: status}}
		}
		return p
	}
	trap := model.Issue{Check: "keyboard/trap", Source: "#date"}
	keys := []string{trapKey, "keyboard/no-focus-ring|#a", "a11y/label|#email", trapKey}
	cases := []struct {
		name string
		page *model.Page
		want []string
	}{
		{"ok", audited(model.AuditOK), nil},
		{"ok, reported", audited(model.AuditOK, trap), nil},
		{"capped", audited(model.AuditCapped), []string{trapKey, "keyboard/no-focus-ring|#a"}},
		{"timeout", audited(model.AuditTimeout), []string{trapKey, "keyboard/no-focus-ring|#a"}},
		{"timeout, reported", audited(model.AuditTimeout, trap), []string{"keyboard/no-focus-ring|#a"}},
		{"error", audited(model.AuditError), []string{trapKey, "keyboard/no-focus-ring|#a"}},
		{"skipped", audited(model.AuditSkipped), []string{trapKey, "keyboard/no-focus-ring|#a"}},
		{"no audit", audited(""), []string{trapKey, "keyboard/no-focus-ring|#a"}},
		{"no page", nil, []string{trapKey, "keyboard/no-focus-ring|#a"}},
	}
	for _, c := range cases {
		if got := Unjudged(c.page, keys); !slices.Equal(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if got := New().Unjudged(c.page, keys); !slices.Equal(got, c.want) {
			t.Errorf("%s: the method gives %q", c.name, got)
		}
	}
	if got := Unjudged(audited(model.AuditTimeout), []string{"a11y/label|#email"}); got != nil {
		t.Errorf("another module's key: %q", got)
	}
}

// A widget or control issue missing from a page whose widgets weren't
// tested this time can't be judged there, however clean the walk: the
// control is still broken, only tested on another page of its group.
// Other keyboard issues are judged by the walk; a page that reports
// the issue judges it.
func TestUnjudgedWidgetsOff(t *testing.T) {
	page := func(on bool, issues ...model.Issue) *model.Page {
		return &model.Page{Path: "/", Issues: issues, Audits: map[string]model.Audit{"keyboard": {Status: model.AuditOK}},
			Keyboard: &model.KeyboardSummary{WidgetsOn: on}}
	}
	operable := "keyboard/not-operable|#menu"
	keys := []string{operable, "keyboard/menu|#nav", "keyboard/dialog-focus|#d", "keyboard/escape|#d", "keyboard/focus-lost|#d", trapKey}
	if got, want := Unjudged(page(false), keys), keys[:5]; !slices.Equal(got, want) {
		t.Errorf("widgets not tested: %q, want %q", got, want)
	}
	if got := Unjudged(page(true), keys); got != nil {
		t.Errorf("widgets tested: %q", got)
	}
	reported := model.Issue{Check: "keyboard/not-operable", Source: "#menu"}
	if got := Unjudged(page(false, reported), []string{operable}); got != nil {
		t.Errorf("a page that reports the issue: %q", got)
	}
	noSummary := page(false)
	noSummary.Keyboard = nil
	if got := Unjudged(noSummary, keys); got != nil {
		t.Errorf("a page without a keyboard summary: %q", got)
	}
}

// slowTrap is the date picker's trap on a page whose stops each hold it
// up for 100 ms, so a 1 s walk can't get to the end.
var slowTrap = datePicker[:len(datePicker)-len(`</main></body></html>`)] + `<script>
document.addEventListener('focusin', () => { const end = Date.now() + 100; while (Date.now() < end) {} });
</script></main></body></html>`

// On the confirm visit, a re-walk that times out or is capped finds no
// trap and leaves the first visit's trap UNSTABLE; a re-walk that finds
// the page fixed marks it FIXED; one that finds it again keeps it.
func TestConfirmRewalk(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/slow": slowTrap, "/trap": datePicker, "/fixed": links(3, "")}})
	c := launch(t)

	tab, p := c.open(srv.URL + "/slow")
	m := New()
	m.limit = time.Second
	live(t, m, withSaver(runContext(t)), tab, p)
	if p.Audits["keyboard"].Status != model.AuditTimeout || reports(p, trapKey) {
		t.Fatalf("timed out walk: audit %+v, issues %+v", p.Audits, p.Issues)
	}
	if got := judge(p, trapKey); got != model.Unstable {
		t.Errorf("timed out re-walk: %s, want UNSTABLE", got)
	}

	tab, p = c.open(srv.URL + "/trap")
	m = New()
	m.set.MaxTabs = 3
	live(t, m, withSaver(runContext(t)), tab, p)
	if p.Audits["keyboard"].Status != model.AuditCapped || reports(p, trapKey) {
		t.Fatalf("capped walk: audit %+v, issues %+v", p.Audits, p.Issues)
	}
	if got := judge(p, trapKey); got != model.Unstable {
		t.Errorf("capped re-walk: %s, want UNSTABLE", got)
	}

	tab, p = c.open(srv.URL + "/fixed")
	live(t, New(), withSaver(runContext(t)), tab, p)
	if got := judge(p, trapKey); got != model.Fixed {
		t.Errorf("clean re-walk: %s, want FIXED (audit %+v)", got, p.Audits)
	}

	tab, p = c.open(srv.URL + "/trap")
	live(t, New(), withSaver(runContext(t)), tab, p)
	if got := judge(p, trapKey); got != "kept" {
		t.Errorf("re-walk that finds the trap: %s (issues %+v)", got, p.Issues)
	}
}

// A control the walk tested yet couldn't judge (its click had an
// effect the walk couldn't tell from a ticking clock) leaves its key
// unjudged on a clean walk that tested the page's widgets. Other keys
// are judged as before.
func TestUnjudgedUndecided(t *testing.T) {
	operable := "keyboard/not-operable|#more"
	p := &model.Page{Path: "/", Audits: map[string]model.Audit{"keyboard": {Status: model.AuditOK}},
		Keyboard: &model.KeyboardSummary{WidgetsOn: true, Unjudged: []string{operable}}}
	keys := []string{operable, "keyboard/not-operable|#other", trapKey}
	if got, want := Unjudged(p, keys), []string{operable}; !slices.Equal(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
	if got := judge(p, operable); got != model.Unstable {
		t.Errorf("undecided control: %s, want UNSTABLE", got)
	}
	if !Undecided(p, operable) || Undecided(p, trapKey) || Undecided(nil, operable) {
		t.Error("Undecided")
	}
}
