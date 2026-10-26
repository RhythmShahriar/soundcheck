// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import "github.com/RhythmShahriar/soundcheck/model"

// Summarize sums up an --eaa run against the criteria of its WCAG
// version: pages yields the run's pages (they may come from disk one by
// one), site its site-level issues and ignored the changes ignore rules
// and accepts left out. For a comparison, run is the current run.
func Summarize(run *model.Run, pages func(func(*model.Page) bool), site []model.Issue, ignored []model.Change) model.EAASummary {
	version := orDefault(run.Options.WCAG)
	return model.EAASummary{WCAG: version, EN301549: EN301549(version), EvidenceNote: model.EvidenceNote}
}
