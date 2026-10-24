// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package echo

import (
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(Module{}) }

// Checks lists the checks the module reports.
// Comparisons of what was sent are echo/<what>.
func (Module) Checks() []check.CheckInfo {
	return []check.CheckInfo{
		{ID: "echo/message/missing", Level: model.Error, Title: "Message no longer sent", Doc: "A form or flow sent an email, SMS or webhook before and doesn't now."},
		{ID: "echo/no-message", Level: model.Warning, Title: "Form sends nothing", Doc: "A form said it sent but no message arrived."},
	}
}
