// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// The catalog has 38, 50 and 55 A and AA criteria in number order,
// each once, with its principle and EN clause.
func TestCriteria(t *testing.T) {
	for version, want := range map[string]int{"": 38, "2.0": 38, "2.1": 50, "2.2": 55, "3.0": 0, "2": 0} {
		got := Criteria(version)
		if len(got) != want {
			t.Errorf("Criteria(%q) has %d criteria, want %d", version, len(got), want)
		}
		seen := map[string]bool{}
		for i, c := range got {
			if seen[c.ID] || c.Title == "" || (c.Level != "A" && c.Level != "AA") || c.EN != "9."+c.ID || c.Principle == "" {
				t.Errorf("%s: %+v", version, c)
			}
			seen[c.ID] = true
			if i > 0 && Compare(got[i-1].ID, c.ID) >= 0 {
				t.Errorf("%s: %s before %s", version, got[i-1].ID, c.ID)
			}
		}
	}
	has := func(version, id string) bool {
		return slices.ContainsFunc(Criteria(version), func(c Criterion) bool { return c.ID == id })
	}
	// 2.1 added reflow, 2.2 added focus not obscured and removed parsing.
	if has("2.0", "1.4.10") || !has("2.1", "1.4.10") || has("2.1", "2.4.11") || !has("2.2", "2.4.11") || !has("2.1", "4.1.1") || has("2.2", "4.1.1") {
		t.Error("criteria in the wrong versions")
	}
	perceivable := 0
	for _, c := range Criteria("2.1") {
		if c.Principle == "Perceivable" {
			perceivable++
		}
	}
	if perceivable != 20 {
		t.Errorf("%d Perceivable criteria in 2.1, want 20", perceivable)
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"1.4.3", "1.4.10", -1}, {"2.4.11", "2.4.7", 1}, {"2.1", "2.1", 0}, {"2.1", "2.1.1", -1}} {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestStandard(t *testing.T) {
	for version, want := range map[string]string{
		"2.1": "WCAG 2.1 level AA / EN 301 549 V3.2.1",
		"2.2": "WCAG 2.2 level AA / EN 301 549 V4.1.1",
		"2.0": "WCAG 2.0 level AA",
	} {
		if got := Standard(version); got != want {
			t.Errorf("Standard(%s) = %q, want %q", version, got, want)
		}
	}
}

// Summarize always carries the version and the evidence sentence.
func TestSummarize(t *testing.T) {
	run := &model.Run{Options: model.Options{EAA: true, WCAG: "2.2"}}
	s := Summarize(run, func(func(*model.Page) bool) {}, nil, nil)
	if s.WCAG != "2.2" || s.EN301549 != "V4.1.1" || s.EvidenceNote != model.EvidenceNote {
		t.Errorf("Summarize = %+v", s)
	}
	if s := Summarize(&model.Run{}, func(func(*model.Page) bool) {}, nil, nil); s.WCAG != "2.0" || s.EN301549 != "" {
		t.Errorf("a run before 1.3: %+v", s)
	}
}

// Compare is a consistent order on any input: antisymmetric and zero
// only for itself or an equal number.
func FuzzCompare(f *testing.F) {
	for _, s := range [][2]string{{"1.4.3", "1.4.10"}, {"2.2", "2.1"}, {"", "x.y"}, {"1..2", "-1.2"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		if Compare(a, b) != -Compare(b, a) {
			t.Errorf("Compare(%q, %q) = %d but Compare(%q, %q) = %d", a, b, Compare(a, b), b, a, Compare(b, a))
		}
		if Compare(a, a) != 0 {
			t.Errorf("Compare(%q, itself) = %d", a, Compare(a, a))
		}
	})
}
