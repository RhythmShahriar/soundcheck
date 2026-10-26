// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/model"
)

// newIssue is an issue of the keyboard check id, with the check's
// fixed level, title and impact and its criteria under the WCAG
// version.
func newIssue(id, version, source, detail string) model.Issue {
	info, ok := ids.Lookup(id)
	if !ok {
		panic("keyboard: no check " + id) // a typo in this package, caught by its tests
	}
	return model.Issue{Check: id, Level: info.Level, Title: info.Title, Impact: info.Impact,
		Source: source, Detail: detail, Fix: info.Doc,
		WCAG: info.WCAG(version), EN301549: info.Clauses(version)}
}

// maxPerCheck caps the issues one check reports on a page, like the
// a11y module's per-rule cap: a theme that hides every focus ring would
// otherwise add an issue per link.
const maxPerCheck = 20

// issueSet collects a page's issues, keeping the first maxPerCheck of
// each check and one issue per key.
type issueSet struct {
	out  []model.Issue
	n    map[string]int
	seen map[string]bool
}

func (s *issueSet) add(i model.Issue) {
	if s.n == nil {
		s.n, s.seen = map[string]int{}, map[string]bool{}
	}
	i.Identify()
	if s.seen[i.Key] || s.n[i.Check] >= maxPerCheck {
		return
	}
	s.seen[i.Key] = true
	s.n[i.Check]++
	s.out = append(s.out, i)
}
