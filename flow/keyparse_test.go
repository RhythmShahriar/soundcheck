// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/internal/focus"
)

func TestParseKeyboard(t *testing.T) {
	for in, want := range map[string]string{
		"keyboard: true\n":  KeyboardDry,
		"keyboard: false\n": "",
		"keyboard: send\n":  KeyboardSend,
		"":                  "",
		"keyboard:\n":       "",
	} {
		f, err := Parse([]byte("name: x\n" + in + "steps: [{wait: 1}]\n"))
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if f.Keyboard != want {
			t.Errorf("%q: Keyboard = %q, want %q", in, f.Keyboard, want)
		}
	}
	for in, want := range map[string]string{
		`keyboard: "yes"`: `keyboard: "yes" isn't one of true, false, send`,
		`keyboard: dry`:   `keyboard: "dry" isn't one of true, false, send`,
		`keyboard: 1`:     `keyboard: "1" isn't one of true, false, send`,
		`keyboard: [a]`:   `keyboard: "a list or mapping" isn't one of true, false, send`,
	} {
		_, err := Parse([]byte("name: x\n" + in + "\nsteps: [{wait: 1}]\n"))
		if err == nil || err.Error() != want {
			t.Errorf("%q: got %v, want %q", in, err, want)
		}
	}
	// The rest of the flow decodes as strictly as before.
	if _, err := Parse([]byte("name: x\nkeyboard: true\nkeyboardd: true\nsteps: [{wait: 1}]\n")); err == nil || !strings.Contains(err.Error(), "keyboardd") {
		t.Errorf("unknown key next to keyboard: %v", err)
	}
}

func TestParsePressAndFocus(t *testing.T) {
	f, err := Parse([]byte(`name: Keys
steps:
  - press: Tab
  - press: {key: Shift+Tab, times: 3}
  - press: {key: Space, on: "#terms"}
  - press: {key: Enter, on: {text: Open menu}, times: 50}
  - press: {key: Escape, on: {selector: nav}}
  - expect: {focus: "#place_order"}
  - expect: {focus_within: "[role=dialog]"}
  - expect: {focus: "#gone", absent: true}
`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range f.Steps {
		got = append(got, s.String())
	}
	want := []string{"press Tab", "press Shift+Tab 3 times", "press Space on #terms", `press Enter on "Open menu" 50 times`,
		"press Escape on nav", "expect focus #place_order", "expect focus within [role=dialog]", "expect no focus #gone"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("steps:\n got %q\nwant %q", got, want)
	}
	if p := f.Steps[0].Press; p.Key != focus.Tab || p.Times != 1 || p.On != nil {
		t.Errorf("press Tab = %+v", p)
	}
	if p := f.Steps[2].Press; p.On == nil || p.On.Selector != "#terms" || p.Times != 1 {
		t.Errorf("press on = %+v", p)
	}
	if e := f.Steps[5].Expect; e.Focus != "#place_order" || e.FocusWithin != "" {
		t.Errorf("expect focus = %+v", e)
	}
	if f.Keyboard != "" {
		t.Errorf("a mouse flow with press steps is %q", f.Keyboard)
	}
	for in, msg := range map[string]string{
		"{press: {key: Tab, times: 0}}":         "press: times is 1 to 50, not 0",
		"{press: {key: Tab, times: 51}}":        "press: times is 1 to 50, not 51",
		"{press: {key: Tab, times: -1}}":        "press: times is 1 to 50, not -1",
		"{press: Tabb}":                         `press: key "Tabb" isn't one of Tab, Shift+Tab, Enter, Space, Escape, ArrowUp, ArrowDown, ArrowLeft, ArrowRight, Home, End (did you mean "Tab"?)`,
		"{press: {key: enterr}}":                `(did you mean "Enter"?)`,
		"{press: {times: 2}}":                   "press needs a key",
		"{press: {key: Tab, on: {}}}":           "press: on needs a selector or text",
		"{press: {key: Tab, onn: a}}":           "onn",
		"{press: {key: Tab, on: {selctor: a}}}": "selctor",
		"{expect: {focus_whithin: a}}":          "focus_whithin",
	} {
		_, err := Parse([]byte("name: x\nsteps: [" + in + "]\n"))
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: got %v, want %q", in, err, msg)
		}
	}
}

// FuzzParse feeds Parse flow files, the testdata flows and hostile ones:
// it must never panic. It fails only with an error.
func FuzzParse(f *testing.F) {
	f.Add([]byte(Example))
	for _, b := range KeyboardTemplates() {
		f.Add(b)
	}
	if files, err := filepath.Glob(filepath.Join("testdata", "*.yaml")); err == nil {
		for _, p := range files {
			if b, err := os.ReadFile(p); err == nil { //nolint:gosec // the package's own testdata
				f.Add(b)
			}
		}
	}
	for _, s := range []string{
		"name: x\nkeyboard: true\nsteps: [{press: {key: Tab, times: 50}}, {expect: {focus: a}}]\n",
		"name: &a x\nstart: *a\nsteps: [{click: *a}]\n",
		"a: &a [*a, *a]\nname: x\nsteps: [{wait: 1}]\n",
		"name: x\nsteps: [{press: {key: Tab, on: &o {selector: a}}}, {press: {key: Enter, on: *o}}]\n",
		"name: " + strings.Repeat("x", 1<<16) + "\nsteps: [{press: " + strings.Repeat("T", 1<<12) + "}]\n",
		"name: x\nkeyboard: !!binary dGVzdA==\nsteps: [{wait: 1}]\n",
		"name: x\nsteps: [{press: {key: Tab, times: 99999999999999999999}}]\n",
		"name: x\nsteps: [{press: [Tab]}]\n",
		"---\n...\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fl, err := Parse(data)
		if err == nil {
			if fl.Name == "" || len(fl.Steps) == 0 {
				t.Errorf("Parse accepted a flow without a name or steps: %+v", fl)
			}
			switch fl.Keyboard {
			case "", KeyboardDry, KeyboardSend:
			default:
				t.Errorf("Keyboard = %q", fl.Keyboard)
			}
			for _, s := range fl.Steps {
				if p := s.Press; p != nil && (p.Times < 1 || p.Times > MaxTimes) {
					t.Errorf("press times %d", p.Times)
				}
				_ = s.String()
			}
		}
		var fe *FileError
		if errors.As(err, &fe) {
			t.Errorf("Parse returned a FileError: %v", err)
		}
	})
}
