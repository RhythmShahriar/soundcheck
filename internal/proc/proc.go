// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package proc holds the process helpers that differ between Unix and
// Windows: how a child is asked to stop when its context is canceled
// and how a browser and its children are stopped.
package proc

import "os/exec"

// Interrupt sets cmd.Cancel to ask the child to stop: an interrupt on
// Unix, so it can clean up (close its browser, finish writing files),
// and a kill on Windows, which can't send an interrupt to another
// process. A child that ignores the interrupt is still killed after
// cmd.WaitDelay.
func Interrupt(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return stop(cmd) }
}
