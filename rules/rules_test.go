// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package rules

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Every built-in rule's samples pass.
func TestBuiltinSamples(t *testing.T) {
	rep, err := TestBuiltin()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rules != len(Builtin().Rules()) || rep.Samples < 2*rep.Rules {
		t.Errorf("ran %d rules and %d samples", rep.Rules, rep.Samples)
	}
	for _, f := range rep.Failures {
		t.Error(f)
	}
}

func TestBuiltinOrder(t *testing.T) {
	var ids []string
	for _, r := range Builtin().Rules() {
		ids = append(ids, r.ID)
		if !r.Builtin || r.Group != "error-screen" {
			t.Errorf("%s: builtin %v, group %q", r.ID, r.Builtin, r.Group)
		}
	}
	// The order the health module tried them in before they were rules.
	if want := []string{"health/wp-critical", "health/database", "health/error-page"}; !slices.Equal(ids, want) {
		t.Errorf("order = %v, want %v", ids, want)
	}
}

const goodRule = `id: health/elementor-missing-widget
match: text
pattern: 'Widget (?P<widget>"[^"]+") not found'
level: error
title: Elementor widget missing
fix: Reinstall the add-on that provides this widget.
source: widget
`

const goodSamples = `positive:
  - name: Elementor 3 message
    text: 'Widget "price-table" not found.'
    source: '"price-table"'
negative:
  - name: a page that mentions widgets
    text: Our widget shop has 40 widgets.
`

// dir writes files into a new folder.
func dir(t *testing.T, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestLoad(t *testing.T) {
	set, err := Load(dir(t, map[string]string{"elementor.yaml": goodRule, "elementor_test.yaml": goodSamples, "README.md": "notes"}))
	if err != nil {
		t.Fatal(err)
	}
	rs := set.Rules()
	if len(rs) != 4 || rs[3].ID != "health/elementor-missing-widget" || rs[3].Builtin || rs[3].File != "elementor.yaml" {
		t.Fatalf("rules = %+v", rs)
	}
	hits := set.Check(&model.Page{Status: 200, Text: `Widget "price-table" not found. Widget "faq" not found. Widget "faq" not found.`})
	var srcs []string
	for _, h := range hits {
		srcs = append(srcs, h.Issue.Source)
		if h.Issue.Check != "health/elementor-missing-widget" || h.Issue.Level != model.Error || h.Issue.Title != "Elementor widget missing" {
			t.Errorf("issue = %+v", h.Issue)
		}
	}
	if !slices.Equal(srcs, []string{`"price-table"`, `"faq"`}) {
		t.Errorf("one issue per distinct source: got %v", srcs)
	}

	// No folder: just the built-in rules.
	set, err = Load(filepath.Join(t.TempDir(), "missing"))
	if err != nil || len(set.Rules()) != 3 {
		t.Errorf("missing folder: %v, %d rules", err, len(set.Rules()))
	}
}

func TestLoadRejects(t *testing.T) {
	with := func(rule string) map[string]string {
		return map[string]string{"r.yaml": rule, "r_test.yaml": goodSamples}
	}
	replace := func(old, new string) map[string]string { return with(strings.Replace(goodRule, old, new, 1)) }
	for name, c := range map[string]struct {
		files    map[string]string
		reserved []string
		want     string
	}{
		"invalid regex":    {files: replace(`'Widget (?P<widget>"[^"]+") not found'`, `'Widget (unclosed'`), want: "pattern: error parsing regexp"},
		"backreference":    {files: replace(`'Widget (?P<widget>"[^"]+") not found'`, `'(a)\1'`), want: "pattern: error parsing regexp"},
		"long pattern":     {files: replace(`'Widget (?P<widget>"[^"]+") not found'`, `'`+strings.Repeat("a", 513)+`'`), want: "pattern is 513 characters; the most is 512"},
		"not health":       {files: replace("health/elementor-missing-widget", "visual/widget"), want: `id "visual/widget" must start with health/`},
		"bad match":        {files: replace("match: text", "match: body"), want: `match "body" must be text, header, console or status`},
		"bad level":        {files: replace("level: error", "level: critical"), want: `level "critical" must be error, warning or notice`},
		"no title":         {files: replace("title: Elementor widget missing", "title: ''"), want: "title is empty"},
		"bad source":       {files: replace("source: widget", "source: 2"), want: `source "2" isn't a capture group`},
		"unknown field":    {files: replace("fix:", "fixx:"), want: "field fixx not found"},
		"missing samples":  {files: map[string]string{"r.yaml": goodRule}, want: "r.yaml: no samples; add r_test.yaml"},
		"no negative":      {files: map[string]string{"r.yaml": goodRule, "r_test.yaml": "positive:\n  - text: x\n"}, want: "needs at least one positive and one negative sample"},
		"orphan samples":   {files: map[string]string{"gone_test.yaml": goodSamples}, want: "gone_test.yaml: samples without a rule"},
		"duplicate id":     {files: map[string]string{"a.yaml": goodRule, "a_test.yaml": goodSamples, "b.yaml": goodRule, "b_test.yaml": goodSamples}, want: "b.yaml: id health/elementor-missing-widget is already used by a.yaml"},
		"builtin override": {files: replace("health/elementor-missing-widget", "health/wp-critical"), want: "id health/wp-critical belongs to a built-in check"},
		// A notice can't take over a check health reports in Go either.
		"reserved id": {files: replace("health/elementor-missing-widget", "health/status"), reserved: []string{"health/status"}, want: "id health/status belongs to a built-in check"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(dir(t, c.files), c.reserved...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want it to say %q", err, c.want)
			}
		})
	}
}

func TestMatchKinds(t *testing.T) {
	rule := func(match, pattern, source string) *Rule {
		r := &Rule{ID: "health/x", Match: match, Pattern: pattern, Level: model.Warning, Title: "X", Source: source}
		if err := r.compile(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	p := &model.Page{Status: 503, Text: "Briefly unavailable for scheduled maintenance.",
		Headers: map[string]string{"x-powered-by": "PHP/7.4.3", "server": "nginx"},
		Console: []string{"Uncaught ReferenceError: jQuery is not defined", "Failed to load resource: 404", "Uncaught ReferenceError: $ is not defined"}}
	for _, c := range []struct {
		rule *Rule
		want []string // sources, one per issue
	}{
		{rule("text", `(?i)briefly unavailable for scheduled maintenance`, ""), []string{""}},
		{rule("text", `nothing like this`, ""), nil},
		{rule("header", `^x-powered-by: PHP/(7\.[0-4])`, "1"), []string{"7.4"}},
		{rule("header", `^server: apache`, ""), nil},
		{rule("console", `ReferenceError: (\S+) is not defined`, "1"), []string{"jQuery", "$"}},
		{rule("console", `ReferenceError`, ""), []string{""}}, // without a source, once per page
		{rule("status", `^503$`, ""), []string{""}},
		{rule("status", `^5\d\d$`, ""), []string{""}},
		{rule("status", `^404$`, ""), nil},
	} {
		var got []string
		for _, h := range c.rule.find(p) {
			got = append(got, h.Issue.Source)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s %q: sources %q, want %q", c.rule.Match, c.rule.Pattern, got, c.want)
		}
	}
}

// A group reports its first match only. A project's rule in a
// built-in group comes after the built-in ones, so it can't hide them.
func TestGroups(t *testing.T) {
	ownScreen := `id: health/maintenance
match: text
pattern: '(?i)critical error|maintenance'
level: notice
title: Maintenance page
group: error-screen
priority: 100
`
	samples := "positive:\n  - text: Down for maintenance\nnegative:\n  - text: Open\n"
	set, err := Load(dir(t, map[string]string{"m.yaml": ownScreen, "m_test.yaml": samples}))
	if err != nil {
		t.Fatal(err)
	}
	ids := func(text string) []string {
		var out []string
		for _, h := range set.Check(&model.Page{Status: 500, Text: text}) {
			out = append(out, h.Issue.Check)
		}
		return out
	}
	// The WordPress screen with a PHP stack trace: one issue, the most
	// specific.
	if got := ids("There has been a critical error on this website. Stack trace: #0 {main}"); !slices.Equal(got, []string{"health/wp-critical"}) {
		t.Errorf("critical error = %v", got)
	}
	if got := ids("Down for maintenance"); !slices.Equal(got, []string{"health/maintenance"}) {
		t.Errorf("maintenance = %v", got)
	}
}

func TestTest(t *testing.T) {
	d := dir(t, map[string]string{"elementor.yaml": goodRule, "elementor_test.yaml": goodSamples})
	rep, err := Test(d)
	if err != nil || rep.Rules != 1 || rep.Samples != 2 || len(rep.Failures) > 0 {
		t.Errorf("good rule: %+v, %v", rep, err)
	}
	bad := `positive:
  - name: an older message
    text: Elementor widget price-table is missing.
  - name: wrong source
    text: 'Widget "faq" not found.'
    source: '"price-table"'
negative:
  - text: 'Widget "faq" not found.'
`
	d = dir(t, map[string]string{"elementor.yaml": goodRule, "elementor_test.yaml": bad})
	rep, err = Test(d)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"elementor_test.yaml: an older message: health/elementor-missing-widget didn't match",
		`elementor_test.yaml: wrong source: source is "\"faq\"", want "\"price-table\""`,
		"elementor_test.yaml: negative sample 1: health/elementor-missing-widget matched; it mustn't",
	}
	if !slices.Equal(rep.Failures, want) {
		t.Errorf("failures =\n%s\nwant\n%s", strings.Join(rep.Failures, "\n"), strings.Join(want, "\n"))
	}
	if _, err := Test(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("testing a missing folder must fail")
	}
}
