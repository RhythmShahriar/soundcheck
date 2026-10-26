// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"slices"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/model"
)

func ignoreFixture() *model.Comparison {
	c := &model.Comparison{Changes: []model.Change{
		{Class: model.New, Check: "health/favicon", Title: "No favicon", Pages: []string{"/", "/about/"}, Score: 2},
		{Class: model.Changed, Check: "visual/page", Title: "Page looks different (desktop)", Pages: []string{"/team/alice/"}, Score: 4.2},
		{Class: model.Changed, Check: "visual/page", Title: "Page looks different (desktop)", Pages: []string{"/shop/"}, Score: 3.1},
		{Class: model.Broke, Check: "health/status", Title: "Server error 500", Pages: []string{"/cart/", "/team/", "/team/bob/"}, Score: 3},
	}}
	Sort(c.Changes)
	c.Complete()
	return c
}

var day = time.Date(2026, 10, 24, 15, 0, 0, 0, time.UTC)

func ignoreLines(cs []model.Change) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Check+" "+c.Title+" "+c.Pages[0])
	}
	return out
}

func TestIgnoreByCheck(t *testing.T) {
	c := ignoreFixture()
	ApplyIgnore(c, []IgnoreRule{{Check: "health/favicon", Reason: "no favicon by design"}}, day)
	if len(c.Ignored) != 1 || c.Ignored[0].Check != "health/favicon" || c.Ignored[0].Reason != "no favicon by design" {
		t.Fatalf("ignored = %+v", c.Ignored)
	}
	if slices.ContainsFunc(c.Changes, func(ch model.Change) bool { return ch.Check == "health/favicon" }) {
		t.Errorf("the ignored change still counts: %v", ignoreLines(c.Changes))
	}
	if c.Summary.Ignored != 1 || c.Summary.New != 0 || c.Summary.Broke != 1 {
		t.Errorf("summary = %+v", c.Summary)
	}
	if c.Ignored[0].ID == "" || len(c.Warnings) != 0 {
		t.Errorf("ignored change has no id or warnings: %+v %q", c.Ignored[0], c.Warnings)
	}
}

func TestIgnoreByPageGlobSplitsChanges(t *testing.T) {
	c := ignoreFixture()
	broke := c.Changes[0]
	ApplyIgnore(c, []IgnoreRule{{Check: "*", Pages: []string{"/team/*"}}}, day)
	// The team pages' visual change and the team part of the 500 are
	// ignored; /shop/ and /cart/ still count.
	got := ignoreLines(c.Ignored)
	want := []string{"health/status Server error 500 /team/", "visual/page Page looks different (desktop) /team/alice/"}
	if !slices.Equal(got, want) {
		t.Errorf("ignored = %q, want %q", got, want)
	}
	var rest *model.Change
	for i := range c.Changes {
		if c.Changes[i].Check == "health/status" {
			rest = &c.Changes[i]
		}
	}
	if rest == nil || !slices.Equal(rest.Pages, []string{"/cart/"}) || rest.Score != 1 {
		t.Fatalf("the 500 on /cart/ should still count: %+v", c.Changes)
	}
	if rest.ID == broke.ID || rest.ID == "" {
		t.Errorf("a split change keeps its old id %q", rest.ID)
	}
	if !slices.Contains(ignoreLines(c.Changes), "visual/page Page looks different (desktop) /shop/") {
		t.Errorf("/shop/'s visual change was ignored: %v", ignoreLines(c.Changes))
	}
	if c.Summary.Ignored != 2 || c.Summary.Broke != 1 {
		t.Errorf("summary = %+v", c.Summary)
	}
}

func TestIgnoreByTitle(t *testing.T) {
	c := ignoreFixture()
	ApplyIgnore(c, []IgnoreRule{{Title: "Page looks different (*)", Pages: []string{"/shop/"}}}, day)
	if got := ignoreLines(c.Ignored); !slices.Equal(got, []string{"visual/page Page looks different (desktop) /shop/"}) {
		t.Errorf("ignored = %q", got)
	}
}

func TestIgnoreUntil(t *testing.T) {
	for _, tc := range []struct {
		until   string
		ignored int
		warning string
	}{
		{"2026-10-24", 1, ""}, // the last day counts in full
		{"2026-12-31", 1, ""},
		{"2026-10-23", 0, "ignore rule 1 (check health/favicon) expired on 2026-10-23; its changes count again"},
		{"next week", 0, `ignore rule 1 (check health/favicon): until "next week" isn't a date like 2026-12-31; the rule is skipped`},
	} {
		c := ignoreFixture()
		ApplyIgnore(c, []IgnoreRule{{Check: "health/favicon", Until: tc.until}}, day)
		if len(c.Ignored) != tc.ignored {
			t.Errorf("until %s: %d ignored, want %d", tc.until, len(c.Ignored), tc.ignored)
		}
		if tc.warning == "" && len(c.Warnings) != 0 || tc.warning != "" && !slices.Equal(c.Warnings, []string{tc.warning}) {
			t.Errorf("until %s: warnings = %q, want %q", tc.until, c.Warnings, tc.warning)
		}
		if tc.ignored == 0 && c.Summary.New != 1 {
			t.Errorf("until %s: the change should count as NEW again: %+v", tc.until, c.Summary)
		}
	}
}

func TestIgnoreEmptyRuleIsSkipped(t *testing.T) {
	c := ignoreFixture()
	ApplyIgnore(c, []IgnoreRule{{Reason: "oops"}}, day)
	if len(c.Ignored) != 0 || len(c.Warnings) != 1 {
		t.Errorf("an empty rule ignored %d changes; warnings %q", len(c.Ignored), c.Warnings)
	}
}

func TestIgnoreNoRulesLeavesComparison(t *testing.T) {
	c := ignoreFixture()
	before := slices.Clone(c.Changes)
	ApplyIgnore(c, nil, day)
	if len(c.Changes) != len(before) || len(c.Ignored) != 0 {
		t.Errorf("no rules changed the comparison: %+v", c)
	}
}

func TestIgnoreRuleYAML(t *testing.T) {
	var rules []IgnoreRule
	src := "- check: health/favicon\n- check: visual/page\n  pages: [\"/team/*\"]\n  until: 2026-12-31\n  reason: team photos change weekly\n"
	if err := yaml.Unmarshal([]byte(src), &rules); err != nil {
		t.Fatal(err)
	}
	want := []IgnoreRule{{Check: "health/favicon"},
		{Check: "visual/page", Pages: []string{"/team/*"}, Until: "2026-12-31", Reason: "team photos change weekly"}}
	if len(rules) != 2 || rules[0].Check != want[0].Check || rules[1].Until != want[1].Until ||
		!slices.Equal(rules[1].Pages, want[1].Pages) || rules[1].Reason != want[1].Reason {
		t.Errorf("rules = %+v", rules)
	}
}

// StaleIgnores finds nothing stale without rules.
func TestStaleIgnoresNoRules(t *testing.T) {
	if got := StaleIgnores(nil, time.Now(), func(func(*model.Page) bool) {}, nil); len(got) != 0 {
		t.Errorf("StaleIgnores(nil) = %v", got)
	}
}
