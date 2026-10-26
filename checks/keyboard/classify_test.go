// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/model"
)

// fixture reads a hand-written trace from testdata.
func fixture(t testing.TB, name string) Trace {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	tr, err := DecodeTrace(data)
	if err != nil {
		t.Fatal(name, err)
	}
	return tr
}

// keys lists an issue list as check|source, sorted.
func keys(issues []model.Issue) []string {
	out := []string{}
	for _, i := range issues {
		out = append(out, i.Check+"|"+i.Source)
	}
	slices.Sort(out)
	return out
}

func find(issues []model.Issue, check string) (model.Issue, bool) {
	i := slices.IndexFunc(issues, func(i model.Issue) bool { return i.Check == check })
	if i < 0 {
		return model.Issue{}, false
	}
	return issues[i], true
}

// Each rule fires on its positive traces and stays quiet on the ones
// that look like it but aren't.
func TestClassify(t *testing.T) {
	o := defaults()
	cases := []struct {
		trace string
		want  []string
	}{
		{"trap-single.json", []string{"keyboard/trap|#date"}},
		{"trap-cycle.json", []string{"keyboard/trap|#date"}},
		// A walk that hit max_tabs (or ran out of time) proves no trap.
		{"trap-capped.json", []string{}},
		// A native dialog goes round through the document's stop:
		// containment, not a trap.
		{"dialog-native.json", []string{}},
		// A modal that Escape closes holds focus on purpose.
		{"dialog-aria-escape.json", []string{}},
		// A payment frame holds focus for ten presses by design.
		{"iframe-10.json", []string{}},
		{"iframe-60.json", []string{"keyboard/trap|#pay"}},
		// The document's stop inside the cycle is the browser going
		// round, never a trap.
		{"body-mid-cycle.json", []string{}},
		{"skip-missing-target.json", []string{"keyboard/skip-link-broken|a"}},
		// A target without tabindex passes when the next Tab lands in or
		// after it.
		{"skip-no-tabindex.json", []string{}},
		{"skip-stays.json", []string{"keyboard/skip-link-broken|a"}},
		// Three stops before the main content: landmarks do the job.
		{"landmarks-3.json", []string{}},
		{"landmarks-12.json", []string{"keyboard/skip-link-missing|document"}},
		{"no-main.json", []string{"keyboard/skip-link-missing|document"}},
		{"judged.json", []string{
			"keyboard/context-change|#auto", "keyboard/context-change|#go", "keyboard/context-change|#pop",
			"keyboard/focus-invisible|#clear", "keyboard/focus-invisible|#off", "keyboard/focus-invisible|#sr", "keyboard/focus-invisible|#vis",
			"keyboard/focus-obscured|#under",
		}},
	}
	for _, c := range cases {
		t.Run(c.trace, func(t *testing.T) {
			got := Classify(fixture(t, c.trace), o, "2.1")
			if k := keys(got); !slices.Equal(k, c.want) {
				t.Errorf("issues %q, want %q", k, c.want)
			}
			for _, i := range got {
				info, _ := ids.Lookup(i.Check)
				if i.Title != info.Title || i.Impact != info.Impact || i.Key == "" || i.Detail == "" {
					t.Errorf("%s: %+v", i.Check, i)
				}
			}
		})
	}
}

// The trap's issue names the element, the presses, what it keeps out of
// reach and the stops before it, under the run's WCAG version.
func TestClassifyTrapIssue(t *testing.T) {
	got := Classify(fixture(t, "trap-cycle.json"), defaults(), "2.2")
	i, ok := find(got, ids.Trap)
	if !ok {
		t.Fatal("no trap")
	}
	want := "Tab and Shift+Tab kept focus here for 8 presses; 3 focusable elements after it (#after, #after2 ...) can't be reached."
	if i.Detail != want {
		t.Errorf("detail %q, want %q", i.Detail, want)
	}
	if i.Level != model.Error || i.Impact != "critical" || !slices.Equal(i.WCAG, []string{"2.1.2"}) || !slices.Equal(i.EN301549, []string{"9.2.1.2"}) {
		t.Errorf("issue %+v", i)
	}
	if !slices.Equal(i.FocusPath, []string{"#top", "#q", "#date", "#pick", "#date", "#pick"}) {
		t.Errorf("focus path %q", i.FocusPath)
	}
	single := Classify(fixture(t, "trap-single.json"), defaults(), "2.1")
	if i, _ := find(single, ids.Trap); !strings.Contains(i.Detail, "for 8 presses; 2 focusable elements after it (#after, #after2) can't") {
		t.Errorf("single: %q", i.Detail)
	}
}

// What stops a trap from counting, one condition at a time.
func TestClassifyTrapGuards(t *testing.T) {
	base := fixture(t, "trap-single.json")
	cases := map[string]func(*Trace){
		"Escape got out":            func(tr *Trace) { tr.TrapProbe.EscapeLeft = true },
		"nothing after it":          func(tr *Trace) { tr.TrapProbe.FocusablesAfter = 0 },
		"no probe":                  func(tr *Trace) { tr.TrapProbe = nil },
		"Shift+Tab got out":         func(tr *Trace) { tr.Backward[2] = stop(2, "#q") },
		"Shift+Tab too short":       func(tr *Trace) { tr.Backward = tr.Backward[:3] },
		"three presses only":        func(tr *Trace) { tr.Forward = tr.Forward[:5] },
		"ignored":                   func(tr *Trace) { tr.Forward[2].Ignored = true },
		"dialog Escape closes":      func(tr *Trace) { dialogTrap(tr, true) },
		"capped":                    func(tr *Trace) { tr.Capped = true },
		"document in the backward":  func(tr *Trace) { tr.Backward[1] = body },
		"higher trap_after setting": nil,
	}
	for name, change := range cases {
		tr := base
		tr.Forward, tr.Backward = slices.Clone(base.Forward), slices.Clone(base.Backward)
		p := *base.TrapProbe
		tr.TrapProbe = &p
		o := defaults()
		if change != nil {
			change(&tr)
		} else {
			o.TrapAfter = 5
		}
		if got := Classify(tr, o, "2.1"); len(got) != 0 {
			t.Errorf("%s: %q", name, keys(got))
		}
	}
	// A dialog that keeps focus without letting Escape close it is a
	// trap all the same.
	tr := base
	tr.Forward = slices.Clone(base.Forward)
	p := *base.TrapProbe
	tr.TrapProbe = &p
	dialogTrap(&tr, false)
	if _, ok := find(Classify(tr, defaults(), "2.1"), ids.Trap); !ok {
		t.Error("a dialog Escape doesn't close isn't a trap")
	}
}

// dialogTrap puts the trap of tr in a dialog whose Escape test closed
// it or not.
func dialogTrap(tr *Trace, closes bool) {
	for i := range tr.Forward {
		if tr.Forward[i].BackendNodeID == 3 {
			tr.Forward[i].InDialog = true
		}
	}
	tr.Widgets = []WidgetResult{{Kind: "dialog", Trigger: "#open", Opened: true, FocusInside: true, EscapeTried: true, EscapeClosed: closes}}
}

// The details of the stop checks, with names redacted and the early
// navigation of the page's own timer left out.
func TestClassifyStopDetails(t *testing.T) {
	got := Classify(fixture(t, "judged.json"), defaults(), "2.1")
	want := map[string]string{
		"#off":   `Tab stop 2 (a "Off screen") has focus but is hidden (outside the viewport).`,
		"#clear": `Tab stop 3 (a) has focus but is hidden (opacity 0).`,
		"#sr":    `Tab stop 4 (a "Clipped") has focus but is hidden (clipped to 1 px).`,
		"#vis":   `Tab stop 5 (a "Gone") has focus but is hidden (visibility hidden).`,
		"#under": `Tab stop 7 (a "Under the bar") is fully covered by #bar.`,
		"#auto":  `Moving focus to input "Auto" with Tab sent a form without Enter.`,
		"#go":    `Moving focus to a "Signed in as ***@***" with Tab opened a new page without Enter.`,
		"#pop":   `Moving focus to button "Help" with Tab opened a new window without Enter.`,
	}
	for _, i := range got {
		if w, ok := want[i.Source]; ok && i.Detail != w {
			t.Errorf("%s %s: %q, want %q", i.Check, i.Source, i.Detail, w)
		}
		if strings.Contains(i.Detail, "@example") {
			t.Errorf("an address in %q", i.Detail)
		}
	}
	// focus-obscured fails 2.4.11 only under WCAG 2.2.
	o21, _ := find(got, ids.FocusObscured)
	o22, _ := find(Classify(fixture(t, "judged.json"), defaults(), "2.2"), ids.FocusObscured)
	if o21.WCAG != nil || !slices.Equal(o22.WCAG, []string{"2.4.11"}) || !slices.Equal(o22.EN301549, []string{"9.2.4.11"}) || o22.Level != model.Notice {
		t.Errorf("2.1 %+v, 2.2 %+v", o21, o22)
	}
	inv, _ := find(got, ids.FocusInvisible)
	if inv.Level != model.Warning || !slices.Equal(inv.WCAG, []string{"2.4.7"}) {
		t.Errorf("focus-invisible %+v", inv)
	}
}

// skip_link decides how a missing skip link is reported: a notice with
// the landmark guard, a warning failing 2.4.1 without it, or nothing.
func TestClassifySkipLinkSetting(t *testing.T) {
	o := defaults()
	notice, _ := find(Classify(fixture(t, "landmarks-12.json"), o, "2.1"), ids.SkipLinkMissing)
	if notice.Level != model.Notice || notice.WCAG != nil ||
		notice.Detail != "12 Tab stops come before the main content and none of the first 3 is a skip link." {
		t.Errorf("expected: %+v", notice)
	}
	o.SkipLink = SkipLinkRequired
	req, ok := find(Classify(fixture(t, "landmarks-3.json"), o, "2.1"), ids.SkipLinkMissing)
	if !ok || req.Level != model.Warning || !slices.Equal(req.WCAG, []string{"2.4.1"}) || !slices.Equal(req.EN301549, []string{"9.2.4.1"}) ||
		!strings.HasPrefix(req.Detail, "3 Tab stops come before") {
		t.Errorf("required: %+v", req)
	}
	o.SkipLink = SkipLinkOff
	if got := Classify(fixture(t, "no-main.json"), o, "2.1"); len(got) != 0 {
		t.Errorf("off: %q", keys(got))
	}
	broken, _ := find(Classify(fixture(t, "skip-missing-target.json"), defaults(), "2.1"), ids.SkipLinkBroken)
	if broken.Detail != `Skip link "Skip to content" points at #main, which doesn't exist.` || broken.Level != model.Warning {
		t.Errorf("broken: %+v", broken)
	}
	stays, _ := find(Classify(fixture(t, "skip-stays.json"), defaults(), "2.1"), ids.SkipLinkBroken)
	if stays.Detail != `Skip link "Skip to content" points at #main, which isn't where the next Tab goes.` {
		t.Errorf("stays: %+v", stays)
	}
}

// A page that hides every link reports 20 of them, not hundreds.
func TestClassifyCap(t *testing.T) {
	var tr Trace
	for i := range 50 {
		s := stop(i+1, fmt.Sprintf("#l%d", i))
		s.Hidden = HiddenOffscreen
		tr.Forward = append(tr.Forward, s)
	}
	tr.MainIndex = 0
	if got := Classify(tr, defaults(), "2.1"); len(got) != maxPerCheck {
		t.Errorf("%d issues, want %d", len(got), maxPerCheck)
	}
}

// FuzzClassify feeds random walks to Classify: it never panics, gives
// the same issues twice, finds no trap in a walk of distinct elements
// that goes round through the document's stop and no focus loss in a
// Tab walk, which can't show one.
func FuzzClassify(f *testing.F) {
	f.Add([]byte{1, 2, 3, 0, 1}, uint8(4), false)
	f.Add([]byte{1, 2, 2, 2, 2, 2, 3}, uint8(3), true)
	f.Add([]byte{1, 2, 3, 2, 3, 2, 3, 0}, uint8(4), false)
	f.Add([]byte{9, 9, 9, 9, 9, 0, 9}, uint8(20), true)
	f.Fuzz(func(t *testing.T, walk []byte, trapAfter uint8, probe bool) {
		tr := Trace{Schema: Schema, MainIndex: -1}
		for i, id := range walk[:min(len(walk), 300)] {
			s := stop(int(id), fmt.Sprintf("#e%d", id))
			if id == 0 {
				s = body
			}
			s.IsFrame = id >= 200
			s.Hidden = []string{"", HiddenClip, ""}[i%3]
			s.Obscured = i%5 == 0
			s.Effect = []string{"", EffectNavigation, EffectForm, EffectTarget}[i%4]
			s.AtMS = i * 400
			tr.Forward = append(tr.Forward, s)
		}
		tr.Backward = slices.Clone(tr.Forward)
		slices.Reverse(tr.Backward)
		if probe {
			tr.TrapProbe = &TrapProbe{Set: []string{"#e1"}, FocusablesAfter: 2, After: []string{"#x"}}
		}
		o := defaults()
		o.TrapAfter = int(trapAfter%18) + 3
		a := Classify(tr, o, "2.1")
		b := Classify(tr, o, "2.1")
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Fatal("Classify gave two answers")
		}
		for _, i := range a {
			if i.Check == ids.FocusLost {
				t.Fatalf("focus loss from a Tab walk: %+v", i)
			}
		}
		if distinctWrap(tr.Forward) {
			if _, ok := find(a, "keyboard/trap"); ok {
				t.Fatalf("a trap in a walk of distinct elements that went round: %v", walk)
			}
		}
	})
}

// distinctWrap reports a walk of distinct elements, the document's stop
// and the first element again, where the recorder ends a walk.
func distinctWrap(steps []Step) bool {
	seen := map[int]bool{}
	for i, s := range steps {
		if s.IsBody {
			return i > 0 && i+2 == len(steps) && steps[i+1].BackendNodeID == steps[0].BackendNodeID && len(seen) == i
		}
		if seen[s.BackendNodeID] {
			return false
		}
		seen[s.BackendNodeID] = true
	}
	return false
}

// FuzzTrace decodes arbitrary trace files: no panic, every cap held,
// and Classify copes with whatever comes back.
func FuzzTrace(f *testing.F) {
	for _, name := range []string{"trap-cycle.json", "judged.json", "skip-stays.json", "iframe-60.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{"schema":"rhythm-soundcheck/keyboard-trace/v1","main_index":99,"trap_probe":{"focusables_after":-5}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		tr, err := DecodeTrace(data)
		if err != nil {
			return
		}
		if len(tr.Forward) > maxSteps || len(tr.Backward) > maxSteps || len(tr.Held) > maxList || len(tr.Events) > maxList {
			t.Fatalf("lists past their caps: %d %d %d %d", len(tr.Forward), len(tr.Backward), len(tr.Held), len(tr.Events))
		}
		if tr.MainIndex < -1 || tr.MainIndex >= max(len(tr.Forward), 1) && tr.MainIndex != -1 {
			t.Fatalf("main index %d of %d stops", tr.MainIndex, len(tr.Forward))
		}
		for _, s := range append(slices.Clone(tr.Forward), tr.Backward...) {
			if len([]rune(s.Name)) > maxName || len([]rune(s.Selector)) > maxSelectorRunes {
				t.Fatalf("stop past its caps: %d %d", len([]rune(s.Name)), len([]rune(s.Selector)))
			}
		}
		if tr.TrapProbe != nil && tr.TrapProbe.FocusablesAfter < 0 {
			t.Fatal("negative focusables")
		}
		for _, i := range Classify(tr, defaults(), "2.2") {
			if i.Key == "" || i.Title == "" {
				t.Fatalf("issue without a key or title: %+v", i)
			}
		}
		if _, err := json.Marshal(tr); err != nil {
			t.Fatal(err)
		}
	})
}

// An issue's key never holds an accessible name: a link labelled with
// the signed-in customer's email or phone keeps its key when they
// change. The Detail shows neither.
func TestClassifyKeyIgnoresPII(t *testing.T) {
	run := func(email, phone string) []model.Issue {
		tr := fixture(t, "judged.json")
		for i := range tr.Forward {
			tr.Forward[i].Name = "Signed in as " + email + ", call " + phone
		}
		return Classify(tr, defaults(), "2.2")
	}
	a := run("jane"+"@"+"example.com", "+49 30 1234567")
	b := run("max.mustermann"+"@"+"example.org", "+49 40 7654321")
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("%d and %d issues", len(a), len(b))
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Detail != b[i].Detail {
			t.Errorf("%q / %q, then %q / %q", a[i].Key, a[i].Detail, b[i].Key, b[i].Detail)
		}
		for _, leak := range []string{"example", "1234567", "7654321"} {
			if strings.Contains(a[i].Key+a[i].Detail, leak) {
				t.Errorf("%s in %q / %q", leak, a[i].Key, a[i].Detail)
			}
		}
	}
}
