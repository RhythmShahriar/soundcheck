// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Reasons the a11y guards give the changes they put aside.
const (
	reasonNotInBaseline = "rule not in the baseline"
	reasonNotRun        = "rule not in this run"
	reasonBeyondCap     = "beyond the elements kept per rule"
)

// ruleSets are the a11y rules each run applied, which decide whether an
// a11y change can be a real one. A rule the snapshot didn't run found
// nothing there, so all its issues would look NEW: a run made before
// 1.3 had only the WCAG 2.0 rules and an axe-core upgrade adds rules
// too. The same goes for FIXED from a rule the later run didn't apply
// (a 2.2 snapshot compared with a 2.1 run).
type ruleSets struct {
	on       bool            // a11y ran in both runs
	baseline map[string]bool // the snapshot's rules
	current  map[string]bool // the later run's; nil when it recorded none
	added    []string        // rules of the later run the snapshot didn't apply, sorted
}

// rulesOf works out the rule sets of before and after. ran says whether
// a module ran in both.
func rulesOf(before, after *model.Run, ran func(string) bool) ruleSets {
	rs := ruleSets{on: ran("a11y")}
	if !rs.on {
		return rs
	}
	base := baselineRules
	if a := before.Options.A11y; a != nil {
		base = a.Rules
	}
	rs.baseline = setOf(base)
	if a := after.Options.A11y; a != nil {
		rs.current = setOf(a.Rules)
		for _, r := range a.Rules {
			if !rs.baseline[r] {
				rs.added = append(rs.added, r)
			}
		}
		slices.Sort(rs.added)
		rs.added = slices.Compact(rs.added)
	}
	return rs
}

func setOf(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// a11yRule returns the axe rule (or the module's own check) of an a11y
// issue's check: "color-contrast" for "a11y/color-contrast".
func a11yRule(check string) (string, bool) {
	return strings.CutPrefix(check, "a11y/")
}

// pageGuard puts aside the a11y changes of one page pair that the runs
// can't tell apart from the rules they applied or the elements they
// kept (see guard).
type pageGuard struct {
	rules *ruleSets
	// beforeCapped and afterCapped are each side's total element count of
	// the rules that hit the per-rule cap on the page.
	beforeCapped, afterCapped map[string]int
	// beforeCount counts the snapshot's issues of each rule on the page,
	// which is its total when the rule wasn't capped.
	beforeCount map[string]int
	// fixed counts the FIXED changes of each rule capped in the later run.
	fixed map[string]int
}

// newPageGuard is the guard of a page pair: the snapshot's issues on the
// page and both sides' audits of it.
func newPageGuard(rs *ruleSets, before []model.Issue, b, a map[string]model.Audit) *pageGuard {
	if !rs.on {
		return nil
	}
	g := &pageGuard{rules: rs, beforeCapped: b["a11y"].CappedRules, afterCapped: a["a11y"].CappedRules}
	if len(g.afterCapped) > 0 {
		g.beforeCount = map[string]int{}
		seen := map[string]bool{}
		for _, i := range before {
			if r, ok := a11yRule(i.Check); ok && !seen[i.Key] {
				seen[i.Key] = true
				g.beforeCount[r]++
			}
		}
	}
	return g
}

// counts reports whether the guard has to count the page's FIXED
// changes before judging them: a rule was capped in the later run.
func (g *pageGuard) counts() bool { return g != nil && len(g.afterCapped) > 0 }

// count notes a change of the page before any is judged: a FIXED of a
// capped rule counts only when the elements it frees fit in the drop of
// the rule's total (see reason).
func (g *pageGuard) count(class string, i model.Issue) {
	if g == nil || class != model.Fixed {
		return
	}
	r, ok := a11yRule(i.Check)
	if !ok || g.afterCapped[r] == 0 {
		return
	}
	if g.fixed == nil {
		g.fixed = map[string]int{}
	}
	g.fixed[r]++
}

// reason returns why a change of class from issue i doesn't count, or
// "" when it does.
//
// A rule capped on a page keeps only its first max_elements elements,
// so the elements kept shift when one before them is fixed or added. A
// NEW of a rule the snapshot capped may be one that was there all along
// beyond the cap. A FIXED of a rule the later run capped may be one
// pushed past the cap. Fixing one of 300 contrast problems drops the
// total by one and frees one element: that one FIXED counts. More FIXED
// than the total dropped by can't all be real; the run can't tell
// which are, so none count.
func (g *pageGuard) reason(class string, i model.Issue) string {
	if g == nil {
		return ""
	}
	r, ok := a11yRule(i.Check)
	if !ok {
		return ""
	}
	switch class {
	case model.New, model.Broke:
		if !g.rules.baseline[r] {
			return reasonNotInBaseline
		}
		if g.beforeCapped[r] > 0 {
			return reasonBeyondCap
		}
	case model.Fixed:
		if g.rules.current != nil && !g.rules.current[r] {
			return reasonNotRun
		}
		if after := g.afterCapped[r]; after > 0 {
			before := g.beforeCapped[r]
			if before == 0 {
				before = g.beforeCount[r]
			}
			if g.fixed[r] > before-after {
				return reasonBeyondCap
			}
		}
	}
	return ""
}

// rulesNote is the comparison's warning naming the rules the snapshot
// didn't apply: the later run's (rs.added) and those of NEW changes put
// aside for it (moved). It's "" when there are none.
func rulesNote(rs ruleSets, moved map[string]bool) string {
	all := slices.Clone(rs.added)
	all = append(all, slices.Collect(maps.Keys(moved))...)
	slices.Sort(all)
	all = slices.Compact(all)
	if len(all) == 0 {
		return ""
	}
	n := "1 rule didn't"
	if len(all) > 1 {
		n = fmt.Sprintf("%d rules didn't", len(all))
	}
	return fmt.Sprintf("note: %s run in the baseline (%s); their issues aren't counted as new. Take a new snapshot to include them",
		n, strings.Join(all, ", "))
}
