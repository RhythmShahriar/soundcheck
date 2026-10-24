// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package proc

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"testing"
	"time"
)

// TestMain makes the test binary stand in for a child that waits for an
// interrupt and says it got one.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "proc-child" {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		os.Stdout.WriteString("ready\n")
		select {
		case <-ch:
			os.Exit(3)
		case <-time.After(30 * time.Second):
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// A canceled child stops at once: interrupted on Unix, killed on Windows.
func TestInterruptStopsTheChild(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, os.Args[0], "proc-child")
	Interrupt(cmd)
	cmd.WaitDelay = 20 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if _, err := out.Read(buf); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	cancel()
	_ = cmd.Wait()
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %s to stop", d)
	}
	if runtime.GOOS != "windows" && cmd.ProcessState.ExitCode() != 3 {
		t.Errorf("exit %d, want 3 (the child saw the interrupt)", cmd.ProcessState.ExitCode())
	}
}
