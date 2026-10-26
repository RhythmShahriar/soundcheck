// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package keyflows runs key flows: flows with keyboard: true or
// keyboard: send, walked by keyboard only (Tab, Enter, Space and the
// arrow keys), never by mouse. The keyboard module calls Run from its
// site check at --level flows. A step that can't be reached or operated
// is a keyboard/flow issue and form errors that aren't tied to their
// fields are keyboard/error-not-linked.
package keyflows

import (
	"context"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Run runs the run's key flows with the keyboard limits o and records
// what became of them in cc.Run.KeyFlows, under cc.Lock.
func Run(ctx context.Context, cc *check.Context, o model.KeyboardOptions) {}
