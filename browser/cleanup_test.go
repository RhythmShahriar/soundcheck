// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !linux

package browser

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
)

// A Chrome whose exit is never seen (a launcher that never started one
// stands in for it) doesn't hold stop: its profile goes after
// cleanupWait.
func TestCleanupDoesNotWaitForever(t *testing.T) {
	wait := cleanupWait
	cleanupWait = 50 * time.Millisecond
	t.Cleanup(func() { cleanupWait = wait })
	dir := filepath.Join(t.TempDir(), "profile")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	l := launcher.New().Set(flags.UserDataDir, dir)
	done := make(chan struct{})
	go func() { cleanup(l); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup still waits for a Chrome that never exits")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the profile is still there: %v", err)
	}
}
