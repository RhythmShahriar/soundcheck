// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build windows

package store

import (
	"errors"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess reports for a process
// that hasn't exited (STILL_ACTIVE).
const stillActive = 259

// processAlive reports whether a process with this PID is running. A
// process another user owns can't be opened (access denied) but is
// running; one that opens is running until it has an exit code, since
// Windows keeps an exited process's object while a handle to it is open.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // a PID from our own lock file
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return true
	}
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true // it opened, so it exists; don't take its lock
	}
	return code == stillActive
}
