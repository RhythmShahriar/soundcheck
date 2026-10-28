// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Criterion is one WCAG success criterion.
type Criterion struct {
	ID        string // "1.4.3"
	Title     string // "Contrast (Minimum)"
	Slug      string // its anchor in the WCAG recommendation: "contrast-minimum"
	Level     string // A or AA
	Principle string // Perceivable, Operable, Understandable or Robust
	Since     string // the WCAG version that added it: 2.0, 2.1 or 2.2
	EN        string // the EN 301 549 clause: "9." plus ID
	// Coverage says how far Soundcheck's tests can judge the criterion
	// under the version Criteria was asked for: CoveragePartly or
	// CoverageNone. TestedBy lists the checks that test it: Soundcheck's
	// own check ids and "axe:<rule>" for each axe-core rule the version
	// runs.
	Coverage string
	TestedBy []string
	// Note is a fixed result to print instead of the tests' one, such as
	// why 4.1.1 Parsing isn't tested.
	Note string
}

// Coverage values.
const (
	CoveragePartly = "partly" // some tests can find failures of it, never all
	CoverageNone   = "none"   // a person must check it
)

// Versions are the WCAG versions Criteria knows, oldest first.
var Versions = []string{"2.0", "2.1", "2.2"}

// Criteria returns the A and AA criteria of a WCAG version in number
// order: 38 for 2.0, 50 for 2.1 and 55 for 2.2. Empty is 2.0, the rules
// of a run made before 1.3. An unknown version has none. The slice is
// the caller's to change.
func Criteria(version string) []Criterion {
	out := slices.Clone(tables()[orDefault(version)].criteria)
	for i := range out {
		out[i].TestedBy = slices.Clone(out[i].TestedBy)
	}
	return out
}

// Lookup returns the criterion id of any version Criteria knows, with
// the coverage of the newest version that has it.
func Lookup(id string) (Criterion, bool) {
	for _, v := range slices.Backward(Versions) {
		for _, c := range Criteria(v) {
			if c.ID == id {
				return c, true
			}
		}
	}
	return Criterion{}, false
}

// HowTested says in words how Soundcheck tests the criterion, for the
// criteria table: "partly: axe image-alt, role-img-alt; keyboard/trap",
// "not tested: needs a person" or the criterion's Note. A partly
// tested criterion whose tests a run left out says they didn't run.
func (c Criterion) HowTested() string {
	if c.Note != "" {
		return c.Note
	}
	if c.Coverage != CoveragePartly {
		return "not tested: needs a person"
	}
	var axe, own []string
	for _, t := range c.TestedBy {
		if rule, ok := strings.CutPrefix(t, "axe:"); ok {
			axe = append(axe, rule)
		} else {
			own = append(own, t)
		}
	}
	var parts []string
	if len(axe) > 0 {
		parts = append(parts, "axe "+strings.Join(axe, ", "))
	}
	if len(own) > 0 {
		parts = append(parts, strings.Join(own, ", "))
	}
	if len(parts) == 0 {
		// Its tests are left out of a run that didn't run them.
		return "not tested: its tests didn't run"
	}
	return "partly: " + strings.Join(parts, "; ")
}

// ForCheck returns the criteria of a WCAG version an issue of check
// fails, sorted: for an axe rule (a11y/<rule>) the criteria its tags
// name, for Soundcheck's own checks the criteria criteria.yaml lists it
// under. Checks that map to none get nil. Criteria outside the version
// are left out.
func ForCheck(check, version string) []string {
	t := tables()[orDefault(version)]
	if rule, ok := strings.CutPrefix(check, "a11y/"); ok {
		if r, ok := AxeRules[rule]; ok {
			var out []string
			for _, c := range t.criteria {
				if slices.Contains(r.WCAG, c.ID) {
					out = append(out, c.ID)
				}
			}
			return out
		}
	}
	return slices.Clone(t.byCheck[check])
}

// IssueCriteria returns the criteria of a WCAG version an issue fails:
// the ones it carries (Issue.WCAG) that the version has, else ForCheck
// of its check.
func IssueCriteria(i model.Issue, version string) []string {
	if len(i.WCAG) == 0 {
		return ForCheck(i.Check, version)
	}
	var out []string
	for _, c := range tables()[orDefault(version)].criteria {
		if slices.Contains(i.WCAG, c.ID) {
			out = append(out, c.ID)
		}
	}
	return out
}

// older reports whether version a comes before version b.
func older(a, b string) bool { return Compare(a, b) < 0 }

// Compare orders criterion numbers and versions part by part, as
// numbers: 1.4.3 before 1.4.10.
func Compare(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(pa), len(pb)) {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if c := cmp.Compare(x, y); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(pa), len(pb))
}

// EN301549 returns the EN 301 549 version whose clause 9 matches a WCAG
// version: V3.2.1 for 2.1 and V4.1.1 for 2.2. Other versions have none.
func EN301549(version string) string {
	switch version {
	case "2.1":
		return "V3.2.1"
	case "2.2":
		return "V4.1.1"
	}
	return ""
}

// Standard names what a run's checks are measured against, as reports
// and the statement draft write it.
func Standard(version string) string {
	switch version {
	case "2.1", "2.2":
		return "WCAG " + version + " level AA / EN 301 549 " + EN301549(version)
	case "", "2.0":
		return "WCAG 2.0 level AA"
	}
	return "WCAG " + version + " level AA"
}
