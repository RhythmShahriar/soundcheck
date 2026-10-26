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
// next to the most common one. Which rules run follows the WCAG version
// (a11y.wcag or --wcag).
func (Module) Checks() []check.CheckInfo {
	return []check.CheckInfo{
		{ID: "a11y/label", Level: model.Warning, Title: "Form field without a label", Doc: "A field has no label for screen readers."},
		{ID: "a11y/<axe rule id>", Level: model.Warning, Title: "Every axe-core rule that fails, e.g. a11y/color-contrast (a notice when axe rates it minor or moderate)",
			Doc: "The A and AA rules of the WCAG version the run uses (2.1 unless a11y.wcag or --wcag says 2.0 or 2.2), each issue naming the WCAG criteria and EN 301 549 clauses it fails. The rule ids are axe-core's (https://dequeuniversity.com/rules/axe/); ignore: rules in the config name them as a11y/<rule id>."},
	}
}
