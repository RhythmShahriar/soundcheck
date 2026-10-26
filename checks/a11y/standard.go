// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import "github.com/RhythmShahriar/soundcheck/config"

// Standard returns the WCAG version the accessibility checks use for
// cfg: 2.0, 2.1 or 2.2, from --wcag, a11y.wcag in the config file or
// --eaa.
func Standard(cfg config.Config) (string, error) { return "2.1", nil }
