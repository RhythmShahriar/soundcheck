// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"crypto/rand"
	"encoding/hex"
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
// (promoting a snapshot, moving the LATEST pointer, pruning runs). It
// holds the owner's PID, the time, a token this process picked at random
// and the machine's hostname, so a lock left by a crashed process is
// broken instead of blocking every later run. Folders a run writes have
// an owner file with the same lines.
//
// A PID only says something on the machine (or container) that wrote
// it: two containers that share a .soundcheck volume often both run as
// PID 1. So a file from another hostname is judged by its age instead:
// the process that wrote it touches it every heartbeat while it runs,
// and one that hasn't for beatStale has stopped. Containers that share a
// store must have hostnames of their own (Docker's default).
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
	// heartbeat is how often this process touches its lock and owner
	// files; beatStale is when another machine's file counts as left
	// behind.
	heartbeat = 20 * time.Second
	beatStale = 90 * time.Second
	// token tells this process's files from those of another process with
	// the same PID; hostname tells this machine's from another's.
	token    = newToken()
	hostname = thisHost()
)

func newToken() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func thisHost() string {
	h, err := os.Hostname()
	if err != nil || strings.ContainsAny(h, " \n") {
		return ""
	}
	return h
}

// stamp is what a lock or owner file of this process holds.
func stamp() []byte {
	return fmt.Appendf(nil, "%d\n%s\n%s\n%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339), token, hostname)
}

// beats are the files this process keeps fresh, see heartbeat.
var beats = struct {
	sync.Mutex
	paths   map[string]bool
	running bool
}{paths: map[string]bool{}}

// beat starts touching path every heartbeat until unbeat.
func beat(path string) {
	beats.Lock()
	defer beats.Unlock()
	beats.paths[folderKey(path)] = true
	if !beats.running {
		beats.running = true
		go beatLoop()
	}
}

func unbeat(path string) {
	beats.Lock()
	delete(beats.paths, folderKey(path))
	beats.Unlock()
}

// beatLoop touches the files in beats until there are none left.
func beatLoop() {
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for range tick.C {
		if !touchBeats() {
			return
		}
	}
}

// touchBeats touches every file in beats and forgets the ones that are
// gone. It reports false, and marks the loop as stopped, when there are
// none.
func touchBeats() bool {
	beats.Lock()
	defer beats.Unlock()
	if len(beats.paths) == 0 {
		beats.running = false
		return false
	}
	now := time.Now()
	for p := range beats.paths {
		// A file that is gone (its folder was promoted or removed) has
		// nothing left to say.
		if err := os.Chtimes(p, now, now); errors.Is(err, fs.ErrNotExist) {
			delete(beats.paths, p)
		}
	}
	return true
}

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
			_, werr := f.Write(stamp())
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				_ = os.Remove(path)
				lockMu.Unlock()
				return nil, werr
			}
			beat(path)
			return func() {
				unbeat(path)
				_ = os.Remove(path)
				lockMu.Unlock()
			}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			lockMu.Unlock()
			return nil, err
		}
		// lockMu is held, so no goroutine of this process holds the lock:
		// one of this machine naming this PID was left by a crashed
		// process that had the same PID (PID 1 in a container, for one).
		h := holder(path, staleLock)
		if !h.alive || h.ours {
			// Two processes may both find the same stale lock; the
			// window is a few microseconds after a crash, so it's
			// accepted rather than solved with a second lock.
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			lockMu.Unlock()
			return nil, fmt.Errorf("another Soundcheck (pid %d) is updating %s; try again when it finishes", h.pid, s.Root)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// writeOwner records this process as the owner in path and keeps the
// file fresh until Release.
func writeOwner(path string) error {
	if err := os.WriteFile(path, stamp(), 0o600); err != nil {
		return err
	}
	beat(path)
	return nil
}

// inUse reports whether the owner file at path names another process
// that is still running. This process's own folders are never in use:
// it calls Prune itself, after it finished writing.
func inUse(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	h := holder(path, staleOwner)
	return h.alive && !h.mine
}

// owned is what a lock or owner file says about the process that wrote
// it.
type owned struct {
	pid   int
	alive bool // the process still holds it
	mine  bool // this process wrote it
	ours  bool // it names this PID on this machine: mine, or a crashed process's with the same PID
}

// holder reads a lock or owner file and reports who holds it. A file of
// this machine is held while its PID runs and it's younger than maxAge;
// one naming this PID is this process's only when it has this process's
// token (another was left by a crashed process with the same PID). A
// file of another machine is held while its process keeps touching it
// (see heartbeat). A file from an older version has no token or
// hostname and counts as this machine's. A file still being written (no
// PID yet) is held for a few seconds.
func holder(path string, maxAge time.Duration) owned {
	data, err := os.ReadFile(path) //nolint:gosec // the store's own files
	if err != nil {
		return owned{alive: !errors.Is(err, fs.ErrNotExist)} // unreadable: assume held
	}
	lines := strings.Split(string(data), "\n")
	field := func(i int) string {
		if i < len(lines) {
			return strings.TrimSpace(lines[i])
		}
		return ""
	}
	pid, perr := strconv.Atoi(field(0))
	at, terr := time.Parse(time.RFC3339, field(1))
	if perr != nil || terr != nil || pid <= 0 {
		info, err := os.Stat(path)
		return owned{alive: err == nil && time.Since(info.ModTime()) < 5*time.Second}
	}
	tok, host := field(2), field(3)
	h := owned{pid: pid, mine: tok != "" && tok == token}
	switch {
	case h.mine:
		h.alive, h.ours = true, true
	case host != "" && host != hostname:
		info, err := os.Stat(path)
		h.alive = err == nil && time.Since(info.ModTime()) < beatStale && time.Since(at) < maxAge
	case pid == os.Getpid():
		h.ours = true // a crashed process of this machine with this PID
	default:
		h.alive = time.Since(at) < maxAge && processAlive(pid)
	}
	return h
}

// cleanIncoming removes the folders in Incoming that no process is
// writing any more: their owner has exited (SIGHUP, kill -9, a CI job
// timeout) or is older than staleOwner. Nothing else would ever remove
// them. A folder without an owner file (from an older version, or one
// just created) is removed once it's older than staleOwner. A folder
// that finished some pages is kept for keepResumable after its last one,
// so --resume can pick it up (see Resumable). It's best effort: a folder
// that can't be read or removed is left for next time.
func (s Store) cleanIncoming() {
	dir := filepath.Join(s.Root, Incoming)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if held(path) {
			continue
		}
		if at, ok := resumable(path); ok && time.Since(at) < keepResumable {
			continue
		}
		_ = os.RemoveAll(path) //nolint:gosec // a folder in our own .incoming
	}
}

// held reports whether a folder NewDir made may still be in use: by
// this process, or by another that still runs (see holder). An owner
// file naming this PID without this process's token was left by a
// crashed process that had the same PID, which is common in containers
// where every run is PID 1.
func held(dir string) bool {
	path := filepath.Join(dir, owner)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		info, err := os.Stat(dir)
		return err != nil || time.Since(info.ModTime()) < staleOwner
	}
	return holder(path, staleOwner).alive
}

// folderKey names dir the same way however it was spelled.
func folderKey(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return filepath.Clean(dir)
}
