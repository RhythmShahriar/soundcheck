// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mouseonly

import "github.com/RhythmShahriar/soundcheck/checks/keyboard/internal/kbtest"

// The helpers the keyboard module's browser test packages share.
type pages = kbtest.Pages

var (
	launch     = kbtest.Launch
	live       = kbtest.Live
	runContext = kbtest.RunContext
	serve      = kbtest.Serve
	links      = kbtest.Links
	reports    = kbtest.Reports
	noResponse = kbtest.NoResponse
)
