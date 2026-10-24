// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

func TestStampIsUTCWithMilliseconds(t *testing.T) {
	at := time.Date(2026, 10, 24, 15, 4, 5, 678_000_000, time.FixedZone("BDT", 6*3600))
	if got := Stamp(at); got != "2026-10-24T09-04-05.678Z" {
		t.Errorf("Stamp = %s", got)
	}
	if !ValidName(Stamp(at)) {
		t.Error("a stamp must be a valid name")
	}
}

func TestNewDirIsUniqueUnderContention(t *testing.T) {
	s := Store{Root: t.TempDir()}
	name := Stamp(time.Now())
	dirs := make([]string, 20)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range dirs {
		wg.Go(func() {
			<-start
			dir, err := s.NewDir("runs", name)
			if err != nil {
				t.Error(err)
			}
			dirs[i] = dir
		})
	}
	close(start)
	wg.Wait()
	slices.Sort(dirs)
	if len(slices.Compact(dirs)) != 20 {
		t.Errorf("20 calls in the same millisecond shared folders: %v", dirs)
	}
}

func TestNewDirNeverEmptiesAFolder(t *testing.T) {
	s := Store{Root: t.TempDir()}
	first, err := s.NewDir(Incoming, "good")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(first, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := s.NewDir(Incoming, "good")
	if err != nil {
		t.Fatal(err)
	}
	if second == first || filepath.Base(second) != "good-2" {
		t.Errorf("second folder = %s", second)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Error("NewDir removed another run's files")
	}
}

// snapshot fills an incoming folder whose screenshot matches its run.
func snapshot(t *testing.T, s Store, name, tag string) string {
	t.Helper()
	dir, err := s.NewDir(Incoming, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := Saver(dir)("shots/a.png", []byte(tag)); err != nil {
		t.Fatal(err)
	}
	if err := SaveRun(dir, &model.Run{Schema: model.Schema, Name: name, Target: tag}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParallelPromoteKeepsWholeSnapshots(t *testing.T) {
	s := Store{Root: t.TempDir()}
	var wg sync.WaitGroup
	for i := range 10 {
		dir := snapshot(t, s, "good", fmt.Sprint("run-", i))
		other := snapshot(t, s, fmt.Sprint("other-", i), fmt.Sprint("other-", i))
		wg.Go(func() {
			if _, err := s.Promote(dir, "good"); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if _, err := s.Promote(other, filepath.Base(other)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	names, _ := s.Snapshots()
	if len(names) != 11 {
		t.Errorf("snapshots = %v, want good and ten others", names)
	}
	for _, name := range names {
		dir := filepath.Join(s.Root, "snapshots", name)
		r, err := LoadRun(dir)
		if err != nil {
			t.Fatal(err)
		}
		shot, err := os.ReadFile(filepath.Join(dir, "shots", "a.png"))
		if err != nil || string(shot) != r.Target {
			t.Errorf("%s mixes runs: run %q, screenshot %q (%v)", name, r.Target, shot, err)
		}
		if _, err := os.Stat(filepath.Join(dir, owner)); err == nil {
			t.Errorf("%s still has an owner file", name)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(s.Root, "snapshots"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("left %s behind", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(s.Root, lockFile)); err == nil {
		t.Error("the lock file is still there")
	}
}

func TestPromoteKeepsTheOldSnapshotWhenItFails(t *testing.T) {
	s := Store{Root: t.TempDir()}
	if _, err := s.Promote(snapshot(t, s, "good", "old"), "good"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Promote(filepath.Join(s.Root, Incoming, "missing"), "good"); err == nil {
		t.Fatal("promoting a missing folder must fail")
	}
	r, err := LoadRun(filepath.Join(s.Root, "snapshots", "good"))
	if err != nil || r.Target != "old" {
		t.Errorf("the old snapshot is gone: %+v %v", r, err)
	}
}

// deadPID is the PID of a process that has exited.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // this test binary
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func ownerFile(t *testing.T, path string, pid int, at time.Time) {
	t.Helper()
	data := fmt.Sprintf("%d\n%s\n", pid, at.UTC().Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLockBreaksStaleLocks(t *testing.T) {
	s := Store{Root: t.TempDir()}
	path := filepath.Join(s.Root, lockFile)
	for name, at := range map[string]struct {
		pid int
		at  time.Time
	}{
		"dead pid":         {deadPID(t), time.Now()},
		"live but too old": {os.Getppid(), time.Now().Add(-2 * time.Hour)},
		"this pid":         {os.Getpid(), time.Now()}, // a crashed run with our PID
	} {
		ownerFile(t, path, at.pid, at.at)
		if err := s.SetLatest("good"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the lock wasn't released")
	}
}

func TestLockWaitsForALiveHolder(t *testing.T) {
	s := Store{Root: t.TempDir()}
	wait := lockWait
	lockWait = 100 * time.Millisecond
	t.Cleanup(func() { lockWait = wait })
	ownerFile(t, filepath.Join(s.Root, lockFile), os.Getppid(), time.Now())
	err := s.SetLatest("good")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("another Soundcheck (pid %d)", os.Getppid())) {
		t.Errorf("got %v", err)
	}
}

func TestPruneSkipsRunsInUse(t *testing.T) {
	s := Store{Root: t.TempDir()}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var dirs []string
	for i := range 5 {
		dir, err := s.NewDir("runs", Stamp(start.Add(time.Duration(i)*time.Minute)))
		if err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	ownerFile(t, filepath.Join(dirs[0], owner), os.Getppid(), time.Now())                    // another live process
	ownerFile(t, filepath.Join(dirs[1], owner), deadPID(t), time.Now())                      // crashed
	ownerFile(t, filepath.Join(dirs[2], owner), os.Getppid(), time.Now().Add(-48*time.Hour)) // PID reused long after
	if err := s.Prune(1); err != nil {
		t.Fatal(err)
	}
	runs, _ := s.list("runs")
	want := []string{filepath.Base(dirs[0]), filepath.Base(dirs[4])}
	if !slices.Equal(runs, want) {
		t.Errorf("kept %v, want %v", runs, want)
	}
	if err := Release(dirs[4]); err != nil {
		t.Fatal(err)
	}
	if err := Release(dirs[4]); err != nil {
		t.Errorf("releasing twice: %v", err)
	}
}

// hostFile writes a lock or owner file as another process writes it:
// pid, time, its token and its hostname, last touched at touched.
func hostFile(t *testing.T, path string, pid int, tok, host string, touched time.Time) {
	t.Helper()
	data := fmt.Sprintf("%d\n%s\n%s\n%s\n", pid, time.Now().UTC().Format(time.RFC3339), tok, host)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatal(err)
	}
}

// Two containers sharing a store both run as PID 1. A file of another
// hostname is held while its process keeps touching it, whatever its PID;
// one of this hostname with this PID and another token was left by a
// crashed process.
func TestHolderAcrossContainers(t *testing.T) {
	path := filepath.Join(t.TempDir(), owner)
	for name, c := range map[string]struct {
		pid       int
		tok, host string
		touched   time.Time
		alive     bool
	}{
		"other host, this pid, fresh":  {os.Getpid(), "feedface", "other-container", time.Now(), true},
		"other host, this pid, silent": {os.Getpid(), "feedface", "other-container", time.Now().Add(-2 * beatStale), false},
		"other host, dead pid, fresh":  {deadPID(t), "feedface", "other-container", time.Now(), true},
		"this host, this pid":          {os.Getpid(), "feedface", hostname, time.Now(), false},
		"this host, this token":        {os.Getpid(), token, hostname, time.Now(), true},
		"this host, live pid":          {os.Getppid(), "feedface", hostname, time.Now(), true},
	} {
		hostFile(t, path, c.pid, c.tok, c.host, c.touched)
		if h := holder(path, staleOwner); h.alive != c.alive {
			t.Errorf("%s: %+v, want alive %v", name, h, c.alive)
		}
	}
	// The store acts on it: another container's live run keeps its
	// folder and its lock.
	s := Store{Root: t.TempDir()}
	dir := filepath.Join(s.Root, Incoming, "other")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	hostFile(t, filepath.Join(dir, owner), os.Getpid(), "feedface", "other-container", time.Now())
	s.cleanIncoming()
	if _, err := os.Stat(dir); err != nil {
		t.Error("another container's running snapshot was removed")
	}
	wait := lockWait
	lockWait = 100 * time.Millisecond
	t.Cleanup(func() { lockWait = wait })
	hostFile(t, filepath.Join(s.Root, lockFile), os.Getpid(), "feedface", "other-container", time.Now())
	if err := s.SetLatest("good"); err == nil || !strings.Contains(err.Error(), "another Soundcheck (pid") {
		t.Errorf("took another container's lock: %v", err)
	}
	hostFile(t, filepath.Join(s.Root, lockFile), os.Getpid(), "feedface", "other-container", time.Now().Add(-2*beatStale))
	if err := s.SetLatest("good"); err != nil {
		t.Errorf("a silent container's lock wasn't broken: %v", err)
	}
}

// This process keeps its owner files fresh while it runs, so another
// container sees them held, and stops once they're released.
func TestHeartbeatTouchesOwnerFiles(t *testing.T) {
	s := Store{Root: t.TempDir()}
	dir, err := s.NewDir("runs", "x")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, owner)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	touchBeats()
	if info, err := os.Stat(path); err != nil || time.Since(info.ModTime()) > time.Minute {
		t.Fatal("the owner file wasn't touched")
	}
	if err := Release(dir); err != nil {
		t.Fatal(err)
	}
	beats.Lock()
	_, kept := beats.paths[folderKey(path)]
	beats.Unlock()
	if kept {
		t.Error("a released owner file is still touched")
	}
	// A folder that moved away (a promoted snapshot) is forgotten.
	gone, err := s.NewDir(Incoming, "moved")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(gone, gone+"-elsewhere"); err != nil {
		t.Fatal(err)
	}
	touchBeats()
	beats.Lock()
	_, kept = beats.paths[folderKey(filepath.Join(gone, owner))]
	beats.Unlock()
	if kept {
		t.Error("a moved owner file is still touched")
	}
}
