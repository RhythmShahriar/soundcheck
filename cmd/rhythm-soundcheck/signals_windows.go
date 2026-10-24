// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"syscall"
)

// stopSignals end a command through the same clean cancel as Ctrl-C.
// Windows has no SIGHUP or SIGQUIT to deliver: closing the console
// arrives as SIGTERM.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}
