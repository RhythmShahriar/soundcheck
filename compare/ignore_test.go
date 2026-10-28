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

// A rule's source is a glob on the change's source, such as the
// selector of an a11y issue's element.
func TestIgnoreBySource(t *testing.T) {
	c := &model.Comparison{Changes: []model.Change{
		{Class: model.New, Check: "a11y/color-contrast", Title: "contrast", Source: "footer a", Pages: []string{"/"}, Score: 1},
		{Class: model.New, Check: "a11y/color-contrast", Title: "contrast", Source: "header a", Pages: []string{"/"}, Score: 1},
	}}
	c.Complete()
	ApplyIgnore(c, []IgnoreRule{{Check: "a11y/*", Source: "footer *", Reason: "brand colors"}}, day)
	if len(c.Ignored) != 1 || c.Ignored[0].Source != "footer a" || c.Ignored[0].Reason != "brand colors" {
		t.Errorf("ignored = %+v", c.Ignored)
	}
	if len(c.Changes) != 1 || c.Changes[0].Source != "header a" {
		t.Errorf("changes = %+v", c.Changes)
	}

	// Without a glob the source must be the same.
	c = &model.Comparison{Changes: []model.Change{{Class: model.New, Check: "a11y/link-name", Source: "header a", Pages: []string{"/"}}}}
	ApplyIgnore(c, []IgnoreRule{{Source: "footer a"}}, day)
	if len(c.Ignored) != 0 || len(c.Changes) != 1 {
		t.Errorf("a source-only rule for footer a matched header a: %+v", c.Ignored)
	}
	ApplyIgnore(c, []IgnoreRule{{Source: "header a"}}, day)
	if len(c.Ignored) != 1 || len(c.Warnings) != 0 {
		t.Errorf("a source-only rule is a rule: ignored %+v, warnings %q", c.Ignored, c.Warnings)
	}

	// A selector copied from a report matches itself: brackets aren't a
	// character class.
	sel := `form > input[name="email"]`
	for _, c := range []struct {
		source string
		want   int
	}{{sel, 1}, {`form > input[name="email"] *`, 0}, {`form > input[name="*"]`, 1}, {`form > input[name=email]`, 0}} {
		cmp := &model.Comparison{Changes: []model.Change{{Class: model.New, Check: "a11y/label", Source: sel, Pages: []string{"/"}}}}
		ApplyIgnore(cmp, []IgnoreRule{{Source: c.source}}, day)
		if len(cmp.Ignored) != c.want {
			t.Errorf("source %q ignored %d changes, want %d", c.source, len(cmp.Ignored), c.want)
		}
	}
}

func TestEmptyRuleNamesSource(t *testing.T) {
	c := ignoreFixture()
	ApplyIgnore(c, []IgnoreRule{{Reason: "everything"}}, day)
	want := "ignore rule 1 has no check, title, source or pages and would hide every change; it is skipped"
	if len(c.Ignored) != 0 || !slices.Equal(c.Warnings, []string{want}) {
		t.Errorf("ignored %d, warnings %q", len(c.Ignored), c.Warnings)
	}
}

func pagesOf(ps ...model.Page) func(func(*model.Page) bool) {
	return func(yield func(*model.Page) bool) {
		for i := range ps {
			if !yield(&ps[i]) {
				return
			}
		}
	}
}

// A rule is stale once the run no longer has the issue it hides. It is
// judged on issues: a problem still there makes no change, which must
// not look fixed.
func TestStaleIgnores(t *testing.T) {
	contrast := model.Issue{Check: "a11y/color-contrast", Title: "contrast", Source: "footer a"}
	rules := []IgnoreRule{
		{Check: "a11y/color-contrast", Pages: []string{"/legal/*"}, Reason: "brand colors"},
		{Check: "a11y/color-contrast", Pages: []string{"/legal/*"}, Until: "2026-10-01"}, // expired
		{Check: "visual/*", Pages: []string{"/team/*"}},                                  // may hide a change no issue carries
		{Check: "a11y/label", Source: "#q"},
		{Check: "a11y/keyboard-trap", Pages: []string{"/legal/*"}},
		{Check: "a11y/label", Until: "soon"},
		{Reason: "empty"},
	}
	with := pagesOf(model.Page{Path: "/legal/terms/", Issues: []model.Issue{contrast}}, model.Page{Path: "/", Issues: []model.Issue{{Check: "a11y/label", Source: "#search"}}})
	site := []model.Issue{{Check: "a11y/keyboard-trap", Title: "Keyboard trap", Pages: []string{"/legal/terms/"}}}
	if got, want := StaleIgnores(rules, day, with, site), []int{3}; !slices.Equal(got, want) {
		t.Errorf("stale = %v, want %v", got, want)
	}
	gone := pagesOf(model.Page{Path: "/legal/terms/"}, model.Page{Path: "/", Issues: []model.Issue{contrast}})
	if got, want := StaleIgnores(rules, day, gone, nil), []int{0, 3, 4}; !slices.Equal(got, want) {
		t.Errorf("stale once fixed = %v, want %v", got, want)
	}
	if got := StaleIgnores(nil, day, gone, nil); got != nil {
		t.Errorf("no rules: %v", got)
	}
	// Without pages nothing was checked, so no rule looks fixed.
	if got := StaleIgnores(rules[:1], day, nil, nil); len(got) != 0 {
		t.Errorf("no pages: %v", got)
	}
}

// A rule is stale only when the run checked a page it covers and that
// page's audit of the rule's module finished: compare --snapshot big
// never visited /a11y/widget.php, so the rule for it says nothing.
func TestStaleIgnoresNeedsACheckedPage(t *testing.T) {
	rules := []IgnoreRule{
		{Check: "a11y/label", Pages: []string{"/a11y/widget.php"}},
		{Check: "a11y/color-contrast", Pages: []string{"/a11y/big.php"}},
		{Check: "keyboard/trap", Pages: []string{"/a11y/big.php"}},
		{Check: "a11y/link-name"},
	}
	audits := map[string]model.Audit{"a11y": {Status: model.AuditOK}, "keyboard": {Status: model.AuditTimeout}}
	only := pagesOf(model.Page{Path: "/a11y/big.php", Audits: audits})
	if got, want := StaleIgnores(rules, day, only, nil), []int{1, 3}; !slices.Equal(got, want) {
		t.Errorf("stale = %v, want %v", got, want)
	}
	failed := pagesOf(model.Page{Path: "/a11y/widget.php", Error: "timeout"})
	if got := StaleIgnores(rules, day, failed, nil); len(got) != 0 {
		t.Errorf("a page that didn't load counts as checked: %v", got)
	}
	both := pagesOf(model.Page{Path: "/a11y/big.php", Audits: audits}, model.Page{Path: "/a11y/widget.php"})
	if got, want := StaleIgnores(rules, day, both, nil), []int{0, 1, 3}; !slices.Equal(got, want) {
		t.Errorf("stale with both pages = %v, want %v", got, want)
	}
}

// A rule for a module that reports changes of its own is never stale:
// it may hide a change no issue carries, such as a team page whose
// title changes weekly or a page that stopped being cached.
func TestStaleIgnoresKeepsComparerRules(t *testing.T) {
	rules := []IgnoreRule{
		{Check: "health/title", Pages: []string{"/team/"}},
		{Check: "cache/stopped"},
		{Check: "health/content-lost"},
		{Check: "seo/*"},
		{Check: "visual/*"},
		{Check: "a11y/label"}, // issues only: stale once gone
	}
	gone := pagesOf(model.Page{Path: "/team/"})
	if got, want := StaleIgnores(rules, day, gone, nil), []int{5}; !slices.Equal(got, want) {
		t.Errorf("stale = %v, want %v", got, want)
	}
}

// A registered module that implements check.Comparer counts as one even
// when it isn't built in; others don't.
func TestStaleIgnoresAsksTheRegistry(t *testing.T) {
	old := comparers
	t.Cleanup(func() { comparers = old })
	comparers = func() []string { return append(slices.Clone(builtinComparers), "forms") }
	rules := []IgnoreRule{{Check: "forms/sent"}, {Check: "a11y/label"}}
	if got, want := StaleIgnores(rules, day, pagesOf(model.Page{Path: "/"}), nil), []int{1}; !slices.Equal(got, want) {
		t.Errorf("stale = %v, want %v", got, want)
	}
	if !slices.Contains(old(), "health") || !slices.Contains(old(), "cache") {
		t.Errorf("comparers() = %v, want health and cache", old())
	}
}
