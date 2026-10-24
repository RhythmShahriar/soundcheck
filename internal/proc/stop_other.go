// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package proc

import (
	"os"
	"os/exec"
)

func stop(cmd *exec.Cmd) error { return cmd.Process.Signal(os.Interrupt) }
