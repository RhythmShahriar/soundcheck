// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build windows

package proc

import "os/exec"

// Windows has no interrupt to send another process (os.Interrupt fails),
// so waiting for WaitDelay would only delay the same kill.
func stop(cmd *exec.Cmd) error { return cmd.Process.Kill() }
