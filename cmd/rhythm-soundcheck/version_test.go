// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"runtime/debug"
	"testing"
)

// A dev build says the release it is heading for and its commit, so a
// source build shows whether it has EAA mode.
func TestDevVersion(t *testing.T) {
	for _, tc := range []struct {
		settings []debug.BuildSetting
		want     string
	}{
		{nil, "dev (" + nextRelease + ")"},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: "f8b0f92aa1b2c3d4"}}, "dev (" + nextRelease + "+f8b0f92)"},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: "f8b0f92aa1b2c3d4"}, {Key: "vcs.modified", Value: "true"}}, "dev (" + nextRelease + "+f8b0f92.modified)"},
	} {
		if got := devVersion(&debug.BuildInfo{Settings: tc.settings}); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.settings, got, tc.want)
		}
	}
	if got := devVersion(nil); got != "dev ("+nextRelease+")" {
		t.Errorf("no build info: %q", got)
	}
	old := version
	t.Cleanup(func() { version = old })
	version = "1.3.0"
	if got := shownVersion(); got != "1.3.0" {
		t.Errorf("release: %q", got)
	}
}
