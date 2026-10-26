// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package ids

import (
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// The 15 checks have unique ids, a level, a fixed title, a known impact
// and EN clauses that follow their WCAG 2.1 criteria.
func TestChecks(t *testing.T) {
	if len(Checks) != 15 {
		t.Fatalf("%d checks, want 15", len(Checks))
	}
	seen := map[string]bool{}
	for _, c := range Checks {
		if seen[c.ID] || !strings.HasPrefix(c.ID, "keyboard/") {
			t.Errorf("%s: duplicate or outside keyboard/", c.ID)
		}
		seen[c.ID] = true
		if c.Title == "" || c.Doc == "" || !slices.Contains([]model.Level{model.Error, model.Warning, model.Notice}, c.Level) {
			t.Errorf("%s: title %q, doc %q, level %q", c.ID, c.Title, c.Doc, c.Level)
		}
		if !slices.Contains([]string{"critical", "serious", "moderate", "minor"}, c.Impact) {
			t.Errorf("%s: impact %q", c.ID, c.Impact)
		}
		if got := c.Clauses("2.1"); !slices.Equal(got, c.EN) {
			t.Errorf("%s: EN %v, want %v", c.ID, c.EN, got)
		}
	}
}

func TestLookupAndVersions(t *testing.T) {
	trap, ok := Lookup(Trap)
	if !ok || trap.Level != model.Error || trap.Title != "Keyboard focus is trapped" || !slices.Equal(trap.WCAG("2.1"), []string{"2.1.2"}) {
		t.Errorf("Lookup(Trap) = %+v, %v", trap, ok)
	}
	if _, ok := Lookup("keyboard/nope"); ok {
		t.Error("Lookup found a check that doesn't exist")
	}
	// focus-obscured fails 2.4.11, which only WCAG 2.2 has.
	obscured, _ := Lookup(FocusObscured)
	if len(obscured.WCAG("2.1")) != 0 || !slices.Equal(obscured.Clauses("2.2"), []string{"9.2.4.11"}) {
		t.Errorf("focus-obscured: 2.1 %v, 2.2 %v", obscured.WCAG("2.1"), obscured.Clauses("2.2"))
	}
	// Advisory checks fail no criterion in either version.
	missing, _ := Lookup(SkipLinkMissing)
	if missing.WCAG("2.1") != nil || missing.WCAG("2.2") != nil {
		t.Errorf("skip-link-missing has criteria: %+v", missing)
	}
}
