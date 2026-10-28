// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/internal/focus"
	"github.com/RhythmShahriar/soundcheck/model"
)

// keyboardCanaries are a token and email addresses a site put in ids.
func keyboardCanaries() (token, mail, other string) {
	return "gh" + "p_" + strings.Repeat("Q7canary", 5)[:36], "s-jane.doe" + "@" + "mailcanary.test", "b-mark.roe" + "@" + "idcanary.test"
}

// The issues name selectors as their source, in their detail and their
// focus path. Each is masked and the issue keyed again from the masked
// source; two issues that mask to one key keep one.
func TestMaskIssues(t *testing.T) {
	token, mail, other := keyboardCanaries()
	trap := newIssue(ids.Trap, "2.1", "#"+token, `Tab and Shift+Tab kept focus here for 8 presses; 1 focusable element after it (select[id="`+mail+`"]) can't be reached.`)
	trap.FocusPath = []string{"#" + token, `select[id="` + mail + `"]`}
	a := newIssue(ids.FocusInvisible, "2.1", `a[id="`+mail+`"]`, "hidden")
	b := newIssue(ids.FocusInvisible, "2.1", `a[id="`+other+`"]`, "hidden")
	for _, i := range []*model.Issue{&trap, &a, &b} {
		i.Identify()
	}
	got := maskIssues([]model.Issue{trap, a, b})
	if len(got) != 2 {
		t.Fatalf("%d issues: %+v", len(got), got)
	}
	if got[0].Source != "#gh"+"p_•••••" || got[0].Key != ids.Trap+"|#gh"+"p_•••••" ||
		!strings.Contains(got[0].Detail, `(select[id="***@***"]) can't be reached`) ||
		!slices.Equal(got[0].FocusPath, []string{"#gh" + "p_•••••", `select[id="***@***"]`}) {
		t.Errorf("trap = %+v", got[0])
	}
	if got[1].Source != `a[id="***@***"]` || got[1].Key != ids.FocusInvisible+`|a[id="***@***"]` {
		t.Errorf("focus-invisible = %+v", got[1])
	}
	if trap.FocusPath[0] != "#"+token {
		t.Error("maskIssues changed the focus path it was given")
	}
}

// The trace file masks every selector it holds.
func TestTraceMasksSelectors(t *testing.T) {
	token, mail, other := keyboardCanaries()
	sel := func(s string) string { return `select[id="` + s + `"]` }
	tr := Trace{Schema: Schema, Page: "/", MainIndex: -1,
		Forward:   []Step{{Stop: focus.Stop{Selector: "#" + token}, ObscuredBy: sel(mail)}},
		Backward:  []Step{{Stop: focus.Stop{Selector: sel(other)}}},
		TrapProbe: &TrapProbe{Set: []string{"#" + token}, After: []string{sel(mail)}, FocusablesAfter: 1},
		SkipLink:  &SkipLink{Selector: sel(mail), TargetID: other, NextStop: "#" + token},
		Widgets:   []WidgetResult{{Kind: WidgetDialog, Trigger: "#" + token, Target: sel(mail), Leaked: sel(other)}},
		Listeners: []Candidate{{Selector: sel(mail)}},
		Rings:     []Ring{{Selector: "#" + token}},
	}
	data, err := tr.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"Q7canary", "mailcanary", "idcanary"} {
		if strings.Contains(string(data), leak) {
			t.Errorf("the trace keeps %s:\n%s", leak, data)
		}
	}
	if !strings.Contains(string(data), `"#gh`+`p_•••••"`) {
		t.Errorf("no masked selector in the trace:\n%s", data)
	}
}
