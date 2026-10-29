// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/model"
)

// The rules from hand-written candidates.
func TestClassifyMouseOnly(t *testing.T) {
	email := "jane" + "@" + "example.test"
	cases := []struct {
		name string
		c    Candidate
		want string
	}{
		{"unreachable", Candidate{Selector: "#a", Events: []string{"mousedown"}, Tag: "div", Name: "Menu"},
			ids.NotReachable + `|div "Menu" reacts to mousedown but Tab never reaches it.`},
		{"unreachable, name redacted", Candidate{Selector: "#a", Events: []string{"click"}, Tag: "span", Name: email},
			ids.NotReachable + `|span "***@***" reacts to click but Tab never reaches it.`},
		{"tabindex -1 the walk passed", Candidate{Selector: "#a", Events: []string{"click"}, Tag: "div", Seen: true}, ""},
		{"focusable, untested", Candidate{Selector: "#a", Events: []string{"click"}, Tag: "div", Focusable: true}, ""},
		{"click only", Candidate{Selector: "#b", Events: []string{"click"}, Tag: "div", Focusable: true, Tested: true, Click: PressDOM},
			ids.NotOperable + `|div reacts to a mouse click but not to Enter or Space.`},
		{"link: Space not expected", Candidate{Selector: "#b", Events: []string{"click"}, Tag: "span", Focusable: true, Link: true, Tested: true, Click: PressNavigation},
			ids.NotOperable + `|span reacts to a mouse click but not to Enter or Space.`},
		{"Enter works", Candidate{Selector: "#b", Events: []string{"click"}, Focusable: true, Tested: true, Enter: PressRequest}, ""},
		{"Space works", Candidate{Selector: "#b", Events: []string{"click"}, Focusable: true, Tested: true, Space: PressDOM}, ""},
		{"keys: only a navigation the press didn't start", Candidate{Selector: "#b", Events: []string{"click"}, Tag: "div", Focusable: true, Tested: true, Enter: PressUnknown, Space: PressUnknown, Click: PressDOM},
			ids.NotOperable + `|div reacts to a mouse click but not to Enter or Space.`},
		{"click: only a navigation it didn't start", Candidate{Selector: "#b", Events: []string{"click"}, Focusable: true, Tested: true, Enter: PressUnknown, Click: PressUnknown}, ""},
		{"click does nothing either", Candidate{Selector: "#b", Events: []string{"click"}, Focusable: true, Tested: true}, ""},
		{"covered", Candidate{Selector: "#b", Events: []string{"click"}, Focusable: true, Tested: true, Skipped: "covered", Click: PressDOM}, ""},
		{"no events", Candidate{Selector: "#b", Tag: "div"}, ""},
	}
	for _, c := range cases {
		tr := Trace{Schema: Schema, MainIndex: -1, Listeners: []Candidate{c.c}}
		var got []string
		for _, i := range Classify(tr, defaults(), "2.2") {
			got = append(got, i.Check+"|"+i.Detail)
			if i.Source != c.c.Selector || i.WCAG[0] != "2.1.1" || i.Level != model.Warning {
				t.Errorf("%s: issue %+v", c.name, i)
			}
		}
		if strings.Join(got, "\n") != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, strings.Join(got, "\n"), c.want)
		}
	}
}

// The controls the walk couldn't judge are listed, by key: one whose
// click had an effect it couldn't tell from the page's own activity and
// one it couldn't press. A control it judged, one it never meant to
// press and one Tab can't reach are not.
func TestUnjudgedControls(t *testing.T) {
	cand := func(sel string, c Candidate) Candidate {
		c.Selector, c.Events, c.Tag, c.Focusable = sel, []string{"click"}, "div", true
		return c
	}
	tr := Trace{Schema: Schema, MainIndex: -1, Listeners: []Candidate{
		cand("#unknown", Candidate{Tested: true, Enter: PressUnknown, Space: PressUnknown, Click: PressUnknown}),
		cand("#quiet-keys", Candidate{Tested: true, Click: PressUnknown}),
		cand("#covered", Candidate{Tested: true, Skipped: "covered"}),
		cand("#budget", Candidate{Skipped: "keyboard.widgets reached"}),
		cand("#left", Candidate{}),
		cand("#clicked", Candidate{Tested: true, Click: PressDOM}),
		cand("#dead", Candidate{Tested: true}),
		cand("#enter", Candidate{Tested: true, Enter: PressDOM}),
		cand("#space", Candidate{Tested: true, Space: PressDOM, Click: PressUnknown}),
		cand("#link", Candidate{Tested: true, Link: true, Click: PressUnknown}),
		cand("#delete", Candidate{Skipped: "looks destructive"}),
		{Selector: "#far", Events: []string{"click"}, Tag: "div"},
	}}
	got := unjudged(tr)
	want := []string{
		ids.NotOperable + "|#unknown", ids.NotOperable + "|#quiet-keys", ids.NotOperable + "|#covered",
		ids.NotOperable + "|#budget", ids.NotOperable + "|#left", ids.NotOperable + "|#link",
	}
	if !slices.Equal(got, want) {
		t.Errorf("unjudged %q\nwant %q", got, want)
	}
	for _, i := range Classify(tr, defaults(), "2.2") {
		if slices.Contains(got, i.Key) {
			t.Errorf("%s is reported and unjudged", i.Key)
		}
	}
}
