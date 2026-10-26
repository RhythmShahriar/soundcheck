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
