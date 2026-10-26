// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import "github.com/RhythmShahriar/soundcheck/model"

// frameTrapPresses is how many presses in a row focus may stay in a
// frame the walk can't look into before it counts as stuck: a payment
// form in an iframe holds focus for many presses by design.
const frameTrapPresses = 50

// Classify turns a page's trace into its keyboard issues, under the
// WCAG version (2.1 or 2.2). It needs no browser.
func Classify(t Trace, o model.KeyboardOptions, wcag string) []model.Issue {
	return nil
}
