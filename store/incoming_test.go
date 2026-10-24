// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewDirRemovesStaleIncomingFolders(t *testing.T) {
	s := Store{Root: t.TempDir()}
	in := filepath.Join(s.Root, Incoming)
	old := time.Now().Add(-48 * time.Hour)
	folders := map[string]struct {
		pid   int // 0: no owner file
		at    time.Time
		stays bool
	}{
		"two-days-old":    {os.Getppid(), old, false}, // live PID, but long ago
		"crashed":         {deadPID(t), time.Now(), false},
		"running":         {os.Getppid(), time.Now(), true}, // another live process
		"this-process":    {os.Getpid(), time.Now(), true},  // a parallel snapshot of ours
		"legacy-old":      {0, old, false},
		"legacy-or-fresh": {0, time.Now(), true},
	}
	for name, f := range folders {
		dir := filepath.Join(in, name)
		if err := os.MkdirAll(filepath.Join(dir, "shots"), 0o750); err != nil {
			t.Fatal(err)
		}
		if f.pid != 0 {
			ownerFile(t, filepath.Join(dir, owner), f.pid, f.at)
		}
		if err := os.Chtimes(dir, f.at, f.at); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := s.NewDir(Incoming, "new")
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range folders {
		_, err := os.Stat(filepath.Join(in, name))
		if stays := err == nil; stays != f.stays {
			t.Errorf("%s: stays = %v, want %v", name, stays, f.stays)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, owner)); err != nil {
		t.Error("the new folder has no owner")
	}
	// Runs and snapshots are never cleaned this way.
	stale := filepath.Join(s.Root, "runs", "old")
	_ = os.MkdirAll(stale, 0o750)
	_ = os.Chtimes(stale, old, old)
	if _, err := s.NewDir("runs", "now"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Error("NewDir(runs) removed an old run")
	}
}

func TestFirstIncomingFolderExcludesTheStore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	t.Chdir(repo)
	if _, err := (Store{Root: ".soundcheck"}).NewDir(Incoming, "first"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if !strings.Contains(string(data), "/.soundcheck/.incoming/\n") || !strings.Contains(string(data), "/.soundcheck/snapshots/\n") {
		t.Errorf("exclude file after the first NewDir:\n%s", data)
	}
}
