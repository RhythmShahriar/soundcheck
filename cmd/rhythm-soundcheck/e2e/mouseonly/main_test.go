// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mouseonly

import (
	"testing"

	"github.com/RhythmShahriar/soundcheck/cmd/rhythm-soundcheck/e2e/internal/e2etest"
)

// TestMain builds rhythm-soundcheck for the package's tests.
func TestMain(m *testing.M) { e2etest.Main(m) }

// The exit codes, as the command names them.
const (
	exitOK       = e2etest.ExitOK
	exitFindings = e2etest.ExitFindings
)

// The helpers the end-to-end packages share (see e2etest).
var (
	runCLI      = e2etest.RunCLI
	needBrowser = e2etest.NeedBrowser
	compareJSON = e2etest.CompareJSON
	eaaPage     = e2etest.EAAPage
	eaaCommon   = e2etest.EAACommon
	changeList  = e2etest.ChangeList
)
