// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/crawl"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// planMod records the paths it was planned with.
type planMod struct {
	plain
	got *[][]string
}

func (m planMod) Plan(paths []string) { *m.got = append(*m.got, paths) }

func TestPlanPages(t *testing.T) {
	var got [][]string
	mods := []check.Module{plain("health"), planMod{"keyboard", &got}}
	done := []*model.Page{{Path: "/done/"}}
	pages := []crawl.Page{{URL: "https://example.test/b/"}, {URL: "https://example.test/a/?q=1"}}
	planPages(mods, done, pages)
	want := []string{"/done/", "/b/", "/a/?q=1"}
	if len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("Plan got %q, want one call with %q", got, want)
	}

	// Without a planner nothing is built or called.
	got = nil
	planPages([]check.Module{plain("health")}, done, pages)
	if got != nil {
		t.Errorf("Plan called without a planner: %q", got)
	}
}
