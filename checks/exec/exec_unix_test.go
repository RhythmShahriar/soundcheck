// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package exec

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestTimeoutKillsTheGroup runs a shell script that starts a sleeping
// child: on timeout both must be gone.
func TestTimeoutKillsTheGroup(t *testing.T) {
	sh, err := osexec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	m := module(t, Command{Name: "group", Run: []string{sh, "-c", `sleep 30 & echo $! > "$1"; wait`, "sh", pidFile}, Timeout: 500 * time.Millisecond})
	cc, p := ctx(), page()
	m.CheckPage(cc, p)
	if len(cc.Run.Warnings) != 1 || !strings.Contains(cc.Run.Warnings[0], "took longer than") {
		t.Fatalf("warnings %q", cc.Run.Warnings)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("the child outlived the timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
