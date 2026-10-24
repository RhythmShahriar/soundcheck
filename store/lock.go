// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The store's lock is a file, .soundcheck/.lock, that one process at a
// time creates around changes several runs could make at once
// (promoting a snapshot, moving LATEST, pruning runs). It holds the
// owner's PID and the time, so a lock left by a crashed process is
// broken instead of blocking every later run.
const lockFile = ".lock"

var (
	// lockWait is how long to wait for another process's lock.
	lockWait = 30 * time.Second
	// staleLock is when a lock counts as left behind even if its PID is
	// alive (the PID may have been reused).
	staleLock = time.Hour
	// staleOwner is the same for a folder's owner file: a run can take a
	// long time, so it gets longer than the lock.
	staleOwner = 24 * time.Hour
	// lockMu serializes goroutines of this process, which share a PID and
	// so can't tell each other's lock files from a stale one of their own.
	lockMu sync.Mutex
)

// lock takes the store's lock and returns the function that releases it.
func (s Store) lock() (func(), error) {
	if err := os.MkdirAll(s.Root, 0o750); err != nil {
		return nil, err
	}
	lockMu.Lock()
	path := filepath.Join(s.Root, lockFile)
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // the store's own lock file
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d\n%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				_ = os.Remove(path)
				lockMu.Unlock()
				return nil, werr
			}
			return func() {
				_ = os.Remove(path)
				lockMu.Unlock()
			}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			lockMu.Unlock()
			return nil, err
		}
		pid, alive := holder(path, staleLock)
		if !alive {
			// Two processes may both find the same stale lock; the
			// window is a few microseconds after a crash, so it's
			// accepted rather than solved with a second lock.
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			lockMu.Unlock()
			return nil, fmt.Errorf("another Soundcheck (pid %d) is updating %s; try again when it finishes", pid, s.Root)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// writeOwner records this process as the owner in path.
func writeOwner(path string) error {
	return os.WriteFile(path, fmt.Appendf(nil, "%d\n%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)), 0o600)
}

// inUse reports whether the owner file at path names another process
// that is still running. This process's own folders are never in use:
// it calls Prune itself, after it finished writing.
func inUse(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	pid, alive := holder(path, staleOwner)
	return alive && pid != os.Getpid()
}

// holder reads a lock or owner file and reports its PID and whether the
// process still holds it: the PID is running and the file is younger
// than maxAge. A file still being written (no PID yet) is held for a few
// seconds.
func holder(path string, maxAge time.Duration) (int, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // the store's own files
	if err != nil {
		return 0, !errors.Is(err, fs.ErrNotExist) // unreadable: assume held
	}
	pidLine, stampLine, _ := strings.Cut(string(data), "\n")
	pid, perr := strconv.Atoi(strings.TrimSpace(pidLine))
	at, terr := time.Parse(time.RFC3339, strings.TrimSpace(stampLine))
	if perr != nil || terr != nil || pid <= 0 {
		info, err := os.Stat(path)
		return 0, err == nil && time.Since(info.ModTime()) < 5*time.Second
	}
	return pid, time.Since(at) < maxAge && processAlive(pid)
}
