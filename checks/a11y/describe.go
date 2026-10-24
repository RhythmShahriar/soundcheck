// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(Module{}) }

// Checks lists the checks the module reports.
// Rules from axe-core are reported as a11y/<rule id> as well.
func (Module) Checks() []check.CheckInfo {
	return []check.CheckInfo{
		{ID: "a11y/label", Level: model.Warning, Title: "Form field without a label", Doc: "A field has no label for screen readers."},
	}
}
