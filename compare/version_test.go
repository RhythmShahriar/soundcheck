// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"context"
	"slices"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func TestVersionNote(t *testing.T) {
	const tail = "; changed detectors can show as NEW or FIXED"
	for _, tc := range []struct{ snapshot, now, want string }{
		{"1.0.0", "1.2.0", "snapshot made by Soundcheck 1.0.0, this is 1.2.0" + tail},
		{"1.2.0", "2.2.0", "snapshot made by Soundcheck 1.2.0, this is 2.2.0" + tail},
		{"1.2.0", "1.1.9", "snapshot made by Soundcheck 1.2.0, this is 1.1.9" + tail},
		{"v1.0.0", "v1.1.0", "snapshot made by Soundcheck 1.0.0, this is 1.1.0" + tail},
		{"1.0.0", "1.1.0-rc.1", "snapshot made by Soundcheck 1.0.0, this is 1.1.0-rc.1" + tail},
		{"1.0.0", "dev", "snapshot made by Soundcheck 1.0.0, this is dev" + tail},
		{"dev", "1.1.0", "snapshot made by Soundcheck dev, this is 1.1.0" + tail},
		{"1.2.0", "1.2.7", ""},      // patch releases keep the detectors
		{"v1.2.0", "1.2.0", ""},     // the same release, written two ways
		{"1.2", "1.2.1", ""},        // no patch number
		{"1.2.0-rc.1", "1.2.0", ""}, // a release candidate of the same line
		{"dev", "dev", ""},          // two dev builds
		{"dev", "(devel)", ""},      // go install of a checkout
		{"", "1.2.0", ""},           // an old run without a version
		{"1.0.0", "", ""},
	} {
		if got := versionNote(tc.snapshot, tc.now); got != tc.want {
			t.Errorf("versionNote(%q, %q) = %q, want %q", tc.snapshot, tc.now, got, tc.want)
		}
	}
}

func TestCompareWarnsAcrossVersions(t *testing.T) {
	before := &model.Run{Version: "1.0.0", Pages: []model.Page{{Path: "/"}}}
	after := &model.Run{Version: "1.2.0", Pages: []model.Page{{Path: "/"}}}
	c := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "1.2.0")
	want := "snapshot made by Soundcheck 1.0.0, this is 1.2.0; changed detectors can show as NEW or FIXED"
	if !slices.Contains(c.Warnings, want) {
		t.Errorf("warnings = %q, want %q", c.Warnings, want)
	}
	after.Version = "1.0.3"
	c = Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "1.0.3")
	if len(c.Warnings) != 0 {
		t.Errorf("warnings = %q, want none for a patch release", c.Warnings)
	}
}
