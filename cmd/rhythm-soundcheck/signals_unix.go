// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package main

import (
	"os"
	"syscall"
)

// stopSignals end a command through the same clean cancel as Ctrl-C:
// the browser is closed, a snapshot in progress is not saved and the exit
// code is 130. SIGHUP comes from closing the terminal or a dropped SSH
// session, SIGQUIT from Ctrl-\. Without them Go's default would kill the
// process at once and leave Chrome running and .soundcheck/.incoming
// behind.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}
