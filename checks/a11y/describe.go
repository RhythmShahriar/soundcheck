// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(Module{}) }

// Checks lists the checks the module reports. Every axe-core rule that
// fails is reported as a11y/<rule id>, so the list names the pattern
// next to the most common one. The module's own checks follow;
// --list-checks names the WCAG criterion each tests in a column of its
// own, so titles don't. Which rules run follows the WCAG version
// (a11y.wcag or --wcag).
func (Module) Checks() []check.CheckInfo {
	return []check.CheckInfo{
		{ID: "a11y/label", Level: model.Warning, Title: "Form field without a label", Doc: "A field has no label for screen readers."},
		{ID: "a11y/<axe rule id>", Level: model.Warning, Title: "Any failing axe-core rule",
			Doc: "Such as a11y/color-contrast; a notice when axe rates it minor or moderate. The A and AA rules of the WCAG version the run uses (2.1 unless a11y.wcag or --wcag says 2.0 or 2.2), each issue naming the WCAG criteria and EN 301 549 clauses it fails. The rule ids are axe-core's (https://dequeuniversity.com/rules/axe/); ignore: rules in the config name them as a11y/<rule id>."},
		{ID: "a11y/placeholder-label", Level: model.Warning, Title: "Form field is labeled only by its placeholder",
			Doc: "A field's only name is its placeholder, which disappears when someone types. Search boxes with a search button are left out. Add a visible <label for> or aria-labelledby that points at visible text."},
		{ID: "a11y/reflow", Level: model.Warning, Title: "Page scrolls sideways at 320 px wide",
			Doc: "At 320 CSS px wide (1280 px zoomed to 400%) the page needs scrolling in two directions. Tables, code, media and [data-soundcheck-2d] are exempt. Runs for WCAG 2.1 and 2.2."},
		// A notice that names no WCAG criterion: --list-checks says
		// advisory in the criteria column.
		{ID: "a11y/overlay", Level: model.Notice, Title: "Accessibility overlay widget found",
			Doc: "The page loads an overlay widget (accessiBe, UserWay, EqualWeb, AudioEye, Enable, Access Ally or One Click Accessibility). A widget doesn't make a site accessible; it's reported as a notice."},
	}
}
