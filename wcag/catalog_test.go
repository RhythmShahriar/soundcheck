// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// WCAG 2.1 has 30 A and 20 AA criteria, 2.2 has 31 A and 24 AA.
func TestLevelCounts(t *testing.T) {
	for version, want := range map[string][2]int{"2.1": {30, 20}, "2.2": {31, 24}} {
		var a, aa int
		for _, c := range Criteria(version) {
			switch c.Level {
			case "A":
				a++
			case "AA":
				aa++
			}
		}
		if a != want[0] || aa != want[1] {
			t.Errorf("%s: %d A and %d AA, want %d and %d", version, a, aa, want[0], want[1])
		}
	}
}

// 2.2 drops 4.1.1 and adds six criteria to 2.1's 50.
func TestVersionDifference(t *testing.T) {
	ids := func(v string) []string {
		var out []string
		for _, c := range Criteria(v) {
			out = append(out, c.ID)
		}
		return out
	}
	v21, v22 := ids("2.1"), ids("2.2")
	var added, dropped []string
	for _, id := range v22 {
		if !slices.Contains(v21, id) {
			added = append(added, id)
		}
	}
	for _, id := range v21 {
		if !slices.Contains(v22, id) {
			dropped = append(dropped, id)
		}
	}
	if want := []string{"2.4.11", "2.5.7", "2.5.8", "3.2.6", "3.3.7", "3.3.8"}; !slices.Equal(added, want) {
		t.Errorf("2.2 adds %v, want %v", added, want)
	}
	if !slices.Equal(dropped, []string{"4.1.1"}) {
		t.Errorf("2.2 drops %v, want [4.1.1]", dropped)
	}
}

// Every row is complete: a slug made from its title, the principle of
// its first digit, its EN clause and a coverage that matches its tests.
func TestCatalogRows(t *testing.T) {
	principles := map[byte]string{'1': "Perceivable", '2': "Operable", '3': "Understandable", '4': "Robust"}
	nonWord := regexp.MustCompile(`[^a-z0-9]+`)
	for _, e := range catalog() {
		if want := strings.Trim(nonWord.ReplaceAllString(strings.ToLower(e.Title), "-"), "-"); e.Slug != want {
			t.Errorf("%s: slug %q, want %q", e.ID, e.Slug, want)
		}
		if e.Principle != principles[e.ID[0]] || e.EN != "9."+e.ID || !slices.Contains(Versions, e.Since) {
			t.Errorf("%s: %+v", e.ID, e)
		}
		if e.Coverage != CoveragePartly && e.Coverage != CoverageNone {
			t.Errorf("%s: coverage %q", e.ID, e.Coverage)
		}
		// coverage: none promises nothing tests it: no check of ours and
		// no axe rule a run applies.
		if e.Coverage == CoverageNone {
			if len(e.TestedBy) > 0 {
				t.Errorf("%s: coverage none but tested by %v", e.ID, e.TestedBy)
			}
			for id, r := range AxeRules {
				if r.Runs("2.2") && CanFail(id) && slices.Contains(r.WCAG, e.ID) {
					t.Errorf("%s: coverage none but axe %s tests it", e.ID, id)
				}
			}
		}
		for _, check := range e.TestedBy {
			if strings.HasPrefix(check, "axe:") {
				t.Errorf("%s: list axe rules in axe_rules.gen.go, not tested_by (%s)", e.ID, check)
			}
		}
	}
	// coverage: partly needs a test in the newest version that has it.
	for _, c := range Criteria("2.2") {
		if e := entryOf(c.ID); e.Coverage == CoveragePartly && c.Coverage != CoveragePartly {
			t.Errorf("%s: coverage partly but nothing tests it", c.ID)
		}
	}
	if c, _ := Lookup("4.1.1"); c.Coverage != CoveragePartly && len(c.TestedBy) == 0 {
		if c.Note != "not tested: W3C now treats it as always met for HTML; the owner decides how to state it" || c.HowTested() != c.Note {
			t.Errorf("4.1.1: %+v", c)
		}
	} else {
		t.Errorf("4.1.1 is tested: %+v", c)
	}
}

func entryOf(id string) entry {
	for _, e := range catalog() {
		if e.ID == id {
			return e
		}
	}
	return entry{}
}

// Every axe rule a WCAG run applies names at least one criterion of the
// catalog, so no finding is left without a row.
func TestAxeRulesMapToCatalog(t *testing.T) {
	levels := []string{"wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"}
	known := map[string]bool{}
	for _, v := range Versions {
		for _, c := range Criteria(v) {
			known[c.ID] = true
		}
	}
	n := 0
	for id, r := range AxeRules {
		if r.Experimental || !slices.ContainsFunc(levels, func(l string) bool { return slices.Contains(r.Tags, l) }) {
			continue
		}
		n++
		if !slices.ContainsFunc(r.WCAG, func(c string) bool { return known[c] }) {
			t.Errorf("axe %s (%v) maps to no catalog criterion", id, r.WCAG)
		}
	}
	if n < 60 {
		t.Errorf("only %d WCAG rules: is the table empty?", n)
	}
}

// The keyboard module's own list of criteria agrees with the catalog.
func TestKeyboardChecksAgree(t *testing.T) {
	for _, c := range ids.Checks {
		for _, v := range []string{"2.1", "2.2"} {
			got, want := ForCheck(c.ID, v), c.WCAG(v)
			if !slices.Equal(got, want) && len(got)+len(want) > 0 {
				t.Errorf("ForCheck(%s, %s) = %v, ids has %v", c.ID, v, got, want)
			}
		}
	}
}

func TestForCheck(t *testing.T) {
	for _, tc := range []struct {
		check, version string
		want           []string
	}{
		{"a11y/color-contrast", "2.1", []string{"1.4.3"}},
		{"a11y/area-alt", "2.1", []string{"2.4.4", "4.1.2"}},
		{"a11y/target-size", "2.2", []string{"2.5.8"}},
		{"a11y/target-size", "2.1", nil},             // 2.5.8 is 2.2 only
		{"a11y/region", "2.1", nil},                  // a best practice
		{"a11y/color-contrast-enhanced", "2.1", nil}, // AAA
		{"a11y/reflow", "2.1", []string{"1.4.10"}},
		{"a11y/placeholder-label", "2.2", []string{"3.3.2"}},
		{"keyboard/trap", "2.1", []string{"2.1.2"}},
		{"keyboard/flow", "2.2", []string{"2.1.1"}},
		{"keyboard/focus-obscured", "2.1", nil},
		{"keyboard/focus-obscured", "2.2", []string{"2.4.11"}},
		{"keyboard/skip-link-missing", "2.1", nil},
		{"interact/unlabeled", "2.1", []string{"3.3.2", "4.1.2"}},
		{"interact/unnamed-field", "2.1", nil},
		{"health/php-error", "2.1", nil},
		{"a11y/no-such-rule", "2.1", nil},
		{"keyboard/trap", "3.0", nil},
	} {
		if got := ForCheck(tc.check, tc.version); !slices.Equal(got, tc.want) {
			t.Errorf("ForCheck(%s, %s) = %v, want %v", tc.check, tc.version, got, tc.want)
		}
	}
}

// The catalog's reader rejects what isn't a catalog.
func TestParseCatalog(t *testing.T) {
	for _, bad := range []string{"- id: 1.1.1\n  titel: x\n", "id: 1\n", "- [\n"} {
		if _, err := parseCatalog([]byte(bad)); err == nil {
			t.Errorf("parseCatalog(%q) succeeded", bad)
		}
	}
	if e, err := parseCatalog([]byte("- id: \"1.1.1\"\n  tested_by: [keyboard/trap]\n")); err != nil || len(e) != 1 || e[0].TestedBy[0] != "keyboard/trap" {
		t.Errorf("parseCatalog = %v, %v", e, err)
	}
}

func FuzzParseCatalog(f *testing.F) {
	f.Add(criteriaYAML)
	f.Add([]byte("- id: x\n  coverage: [a, b]\n"))
	f.Add([]byte("&a [*a, *a]"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseCatalog(data)
	})
}

// The coverage table, by version: the fixed "how Soundcheck tests it"
// column of the statement and of the docs.
func TestCoverageGolden(t *testing.T) {
	for _, v := range []string{"2.1", "2.2"} {
		var b strings.Builder
		partly := 0
		fmt.Fprintf(&b, "| WCAG %s | Level | EN 301 549 | How Soundcheck tests it |\n|---|---|---|---|\n", v)
		for _, c := range Criteria(v) {
			fmt.Fprintf(&b, "| %s %s | %s | %s | %s |\n", c.ID, c.Title, c.Level, c.EN, c.HowTested())
			if c.Coverage == CoveragePartly {
				partly++
			}
		}
		fmt.Fprintf(&b, "\n%d of %d criteria partly tested, %d need a person.\n", partly, len(Criteria(v)), len(Criteria(v))-partly)
		golden(t, "coverage-"+v+".md", b.String())
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs (run with -update to rewrite):\n%s", path, got)
	}
}
