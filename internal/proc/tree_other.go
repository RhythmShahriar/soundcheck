// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package proc

import "os"

// KillOnExit does nothing outside Windows, where each browser package
// stops its own children (a process group, leakless or Pdeathsig).
func KillOnExit() {}

// KillTree kills p. A browser's children on Unix notice their parent is
// gone and exit by themselves.
func KillTree(p *os.Process) error { return p.Kill() }
