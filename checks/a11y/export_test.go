// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import "time"

// SetTimeout makes a page's audit time out after d until restore runs.
// Tests that call it don't run in parallel.
func SetTimeout(d time.Duration) (restore func()) {
	old := timeout
	timeout = d
	return func() { timeout = old }
}

// OwnRules is ownRules for the WCAG version's tags.
func OwnRules(version string) []string { return ownRules(tags(version)) }
