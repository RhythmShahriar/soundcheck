// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package evidence

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A listed file swapped for a FIFO is reported, not opened: opening
// it would block Verify for good.
func TestVerifyFIFO(t *testing.T) {
	dir, run := saveRun(t, t.TempDir(), "runs", "r1", time.Unix(1000, 0), true, target)
	if err := Write(dir, run, nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "pages.jsonl")
	if err := syscall.Unlink(p); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skip(err)
	}
	done := make(chan error, 1)
	go func() { done <- Verify(dir) }()
	select {
	case err := <-done:
		if want := "evidence: pages.jsonl isn't a regular file"; err == nil || err.Error() != want {
			t.Errorf("Verify = %v, want %s", err, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Verify blocked on a FIFO")
	}
}
