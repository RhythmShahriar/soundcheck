// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package exec

import (
	osexec "os/exec"
	"syscall"
)

// inGroup starts the command in its own process group and, on timeout,
// kills the whole group: a script's own children (php, node) would
// otherwise live on and keep the pipes open.
func inGroup(cmd *osexec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
