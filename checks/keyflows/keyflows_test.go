// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyflows

import (
	"context"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Without key flows, Run leaves the run as it was.
func TestRunWithoutFlows(t *testing.T) {
	cc := &check.Context{Run: &model.Run{}}
	Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 150})
	if len(cc.Run.Issues) != 0 || len(cc.Run.Warnings) != 0 {
		t.Errorf("Run changed a run without key flows: %+v", cc.Run)
	}
}
