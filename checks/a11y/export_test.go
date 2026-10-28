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

// SetReflowTimeout bounds the reflow test at d until restore runs.
func SetReflowTimeout(d time.Duration) (restore func()) {
	old := reflowTimeout
	reflowTimeout = d
	return func() { reflowTimeout = old }
}

// SetFieldsJS replaces the placeholder check's script until restore
// runs, so a test can make it fail.
func SetFieldsJS(js string) (restore func()) {
	old := fieldsJS
	fieldsJS = js
	return func() { fieldsJS = old }
}

// ShortText is shortText.
func ShortText(s string, n int) string { return shortText(s, n) }
