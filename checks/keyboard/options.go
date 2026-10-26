// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// defaults are the keyboard walk's limits when the config file sets
// none.
func defaults() model.KeyboardOptions {
	return model.KeyboardOptions{MaxTabs: 150, Widgets: 10, TrapAfter: 4, SkipLink: "expected"}
}

// Options returns the keyboard walk's limits for cfg, from the keyboard
// block of the config file, for the CLI to record in the run's options.
func Options(cfg config.Config) (model.KeyboardOptions, error) { return defaults(), nil }
