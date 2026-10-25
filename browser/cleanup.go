// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !linux

package browser

import (
	"os"
	"time"

	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
)

// cleanupWait bounds how long stop waits for Chrome to exit.
var cleanupWait = 10 * time.Second

// cleanup waits for a killed Chrome to exit, then removes its profile.
// rod's Cleanup waits until Chrome's output pipes close: a helper that
// left the process group (a crash reporter, say) can hold them open
// for good, so the wait is bounded and the profile goes regardless.
func cleanup(l *launcher.Launcher) {
	done := make(chan struct{})
	go func() { l.Cleanup(); close(done) }()
	select {
	case <-done:
	case <-time.After(cleanupWait):
		_ = os.RemoveAll(l.Get(flags.UserDataDir))
	}
}
