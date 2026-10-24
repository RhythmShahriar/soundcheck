// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package visual

import (
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(Module{}) }

// Checks lists the checks the module reports.
func (Module) Checks() []check.CheckInfo {
	return []check.CheckInfo{
		{ID: "visual/page", Level: model.Notice, Title: "Page looks different", Doc: "The page's screenshot changed."},
		{ID: "visual/block", Level: model.Notice, Title: "Part of the page looks different", Doc: "One area of the page changed."},
		{ID: "visual/shared", Level: model.Notice, Title: "Same change on several pages", Doc: "An area changed the same way on several pages."},
		{ID: "visual/size", Level: model.Notice, Title: "Page size changed", Doc: "The page got taller or shorter."},
	}
}

// NeedsShots asks the runner to store a screenshot per viewport.
func (Module) NeedsShots() bool { return true }

// NeedsSecondShot asks for a second capture in snapshots, which shows
// the areas that change on their own (sliders, clocks).
func (Module) NeedsSecondShot(snapshot bool) bool { return snapshot }
