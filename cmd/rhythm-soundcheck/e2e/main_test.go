// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package e2e

import (
	"testing"

	"github.com/RhythmShahriar/soundcheck/cmd/rhythm-soundcheck/e2e/internal/e2etest"
)

// TestMain builds rhythm-soundcheck for the package's tests.
func TestMain(m *testing.M) { e2etest.Main(m) }

// The exit codes, as the command names them.
const (
	exitOK    = e2etest.ExitOK
	exitError = e2etest.ExitError
)

// The helpers the end-to-end packages share (see e2etest).
var (
	runCLI      = e2etest.RunCLI
	needBrowser = e2etest.NeedBrowser
)
