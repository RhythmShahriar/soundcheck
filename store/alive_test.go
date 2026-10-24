// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix || windows

package store

import (
	"os"
	"os/exec"
	"testing"
)

// This process runs; a child that exited and was waited for doesn't.
func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("this process isn't alive")
	}
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if processAlive(cmd.Process.Pid) {
		t.Errorf("exited child %d is alive", cmd.Process.Pid)
	}
}
