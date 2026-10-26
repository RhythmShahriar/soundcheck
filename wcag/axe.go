// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import "slices"

// Rule is what the vendored axe-core says about one of its rules.
type Rule struct {
	WCAG []string // the success criteria its tags name, dotted: "1.4.3"
	EN   []string // the EN 301 549 clauses its tags name: "9.1.4.3"
	Tags []string // every tag, as axe gives them
	// Experimental rules are off unless asked for by id, so a run by tag
	// never applies them.
	Experimental bool
}

// AxeTags returns the axe tags of the A and AA rules a WCAG version
// runs: wcag2a and wcag2aa for 2.0, plus wcag21a and wcag21aa for 2.1
// and wcag22aa for 2.2. An unknown version has none.
func AxeTags(version string) []string {
	switch version {
	case "", "2.0":
		return []string{"wcag2a", "wcag2aa"}
	case "2.1":
		return []string{"wcag2a", "wcag2aa", "wcag21a", "wcag21aa"}
	case "2.2":
		return []string{"wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"}
	}
	return nil
}

// Runs reports whether a run of the WCAG version applies the rule: it
// has one of the version's tags and isn't experimental. Deprecated rules
// still run when their tags are asked for.
func (r Rule) Runs(version string) bool {
	if r.Experimental {
		return false
	}
	return slices.ContainsFunc(AxeTags(version), func(t string) bool { return slices.Contains(r.Tags, t) })
}
