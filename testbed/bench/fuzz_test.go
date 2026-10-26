// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The a11y tools print what they like. A broken or newer version can
// print anything; the parsers must fail cleanly on it, never panic.

func FuzzAxeViolations(f *testing.F) {
	f.Add(`[{"url":"http://localhost:18810/a11y/","violations":[{"id":"label","nodes":[{"target":["#card"]},{"target":["#zip"]}]},{"id":"color-contrast","nodes":[{"target":["iframe",".pay"]}]}]}]`)
	f.Add(`{"not":"a list"}`)
	f.Add(`[{"violations":[{"id":"x","nodes":[{"target":[]}]}]}]`)
	f.Add(`[]`)
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		got, err := axeViolations([]byte(jsonArray(s)))
		if err != nil && len(got) != 0 {
			t.Errorf("an error with results: %v, %v", got, err)
		}
	})
}

func FuzzPa11yViolations(f *testing.F) {
	f.Add("Running pa11y\n" + `[{"code":"WCAG2AA.Principle1.Guideline1_3.1_3_1.F68","type":"error","selector":"#card"},{"code":"x","type":"warning","selector":"#zip"}]`)
	f.Add("Error: net::ERR_CONNECTION_REFUSED")
	f.Add(`] [`)
	f.Add(`[{"type":"error"}]`)
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		arr := jsonArray(s)
		if strings.Contains(s, "[") && strings.Contains(s, "]") && len(arr) > len(s) {
			t.Fatalf("jsonArray grew the text: %q", arr)
		}
		got, err := pa11yViolations([]byte(arr))
		if err != nil && len(got) != 0 {
			t.Errorf("an error with results: %v, %v", got, err)
		}
	})
}

func FuzzCheckStatement(f *testing.F) {
	cs, err := newCanaries()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(draft(), "frame")
	f.Add(draft()+"This website is partially compliant with WCAG 2.1.\n", "")
	f.Add(draft()+"[TO DO: say it conforms]\n", "/checkout/")
	f.Add(strings.Repeat("İ", 40)+" conforms "+strings.Repeat("ẞ", 40), "")
	f.Add("Contact "+cs[0].value, "")
	f.Fuzz(func(t *testing.T, text, say string) {
		for _, p := range checkStatement(text, []string{say}, cs) {
			if !utf8.ValidString(text) {
				continue
			}
			if !utf8.ValidString(p) {
				t.Errorf("a problem cuts a character: %q", p)
			}
		}
	})
}
