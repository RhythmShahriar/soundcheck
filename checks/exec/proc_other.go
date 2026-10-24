// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !unix

package exec

import osexec "os/exec"

// inGroup leaves the default on systems without process groups: the
// command itself is killed on timeout and WaitDelay closes the pipes its
// children may hold.
func inGroup(*osexec.Cmd) {}
