// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package store keeps Soundcheck's files in the project's .soundcheck/
// folder: named snapshots, recent runs and comparisons, each a directory
// with run.json (or comparison.json) and its screenshots.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Incoming holds a snapshot while it's being taken, so a failed or
// canceled snapshot never replaces a good one with the same name.
const Incoming = ".incoming"

// Store is a .soundcheck folder.
type Store struct{ Root string }

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidName reports whether name can be used as a snapshot name.
func ValidName(name string) bool { return safeName.MatchString(name) }

// Stamp is the default name for snapshots and runs: UTC to the
// millisecond, so names sort by time and rarely collide.
func Stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15-04-05.000Z") }

// owner is the file in a folder NewDir created that says which process
// is still writing it, so Prune leaves it alone.
const owner = ".owner"

// maxSuffix bounds how many "-2", "-3"… folder names NewDir tries.
const maxSuffix = 1000

// NewDir creates a new folder kind/name (kind is "snapshots", "runs" or
// Incoming) and returns it. It never reuses or empties an existing
// folder: when kind/name exists, another run made it (two commands in
// the same millisecond, or a crashed one), so it creates name-2, name-3
// and so on instead.
func (s Store) NewDir(kind, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("%q isn't a valid name: use letters, digits, dots, dashes and underscores", name)
	}
	parent := filepath.Join(s.Root, kind)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", err
	}
	for n := 1; n <= maxSuffix; n++ {
		dir := filepath.Join(parent, name)
		if n > 1 {
			dir = filepath.Join(parent, fmt.Sprintf("%s-%d", name, n))
		}
		// Mkdir fails when the folder exists, which makes it the claim:
		// two processes can't both create the same one.
		err := os.Mkdir(dir, 0o750)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return dir, writeOwner(filepath.Join(dir, owner))
	}
	return "", fmt.Errorf("couldn't create a folder for %q in %s: %d names are taken", name, parent, maxSuffix)
}

// Release marks a folder from NewDir as finished, so Prune may delete it
// once it's old enough. Folders of processes that have exited count as
// finished anyway.
func Release(dir string) error {
	err := os.Remove(filepath.Join(dir, owner))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Saver returns a function that writes files into dir.
func Saver(dir string) func(name string, data []byte) error {
	return func(name string, data []byte) error {
		clean := filepath.Clean(filepath.FromSlash(name))
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return fmt.Errorf("refusing to write %q outside the run folder", name)
		}
		return cache.WriteFile(filepath.Join(dir, clean), data)
	}
}

// SaveRun writes run.json into dir. A run with pages whose visit was
// canceled is marked as interrupted, so it's never read back as a full
// result.
func SaveRun(dir string, r *model.Run) error {
	if slices.ContainsFunc(r.Pages, func(p model.Page) bool { return p.Canceled() }) {
		r.Interrupted = true
	}
	return cache.WriteJSON(filepath.Join(dir, "run.json"), r)
}

// LoadRun reads run.json from dir.
func LoadRun(dir string) (*model.Run, error) {
	var r model.Run
	if err := cache.ReadJSON(filepath.Join(dir, "run.json"), &r); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no run in %s", dir)
		}
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if r.Schema != model.Schema {
		return nil, fmt.Errorf("%s was made by an incompatible Soundcheck (%s); take a new snapshot", dir, r.Schema)
	}
	return &r, nil
}

// SaveComparison writes comparison.json into dir.
func SaveComparison(dir string, c *model.Comparison) error {
	return cache.WriteJSON(filepath.Join(dir, "comparison.json"), c)
}

// LoadComparison reads comparison.json from dir.
func LoadComparison(dir string) (*model.Comparison, error) {
	var c model.Comparison
	if err := cache.ReadJSON(filepath.Join(dir, "comparison.json"), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Promote moves a finished snapshot from Incoming into snapshots/name,
// replacing an older snapshot with that name, and returns its new folder.
// It holds the store's lock, so two snapshots finishing at once each
// leave a whole snapshot, never a mix of both.
func (s Store) Promote(dir, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("%q isn't a valid snapshot name", name)
	}
	unlock, err := s.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	snapshots := filepath.Join(s.Root, "snapshots")
	if err := os.MkdirAll(snapshots, 0o750); err != nil {
		return "", err
	}
	final := filepath.Join(snapshots, name)
	// The old snapshot is moved aside, not deleted, until the new one is
	// in place: a failed rename then puts it back.
	var aside string
	if _, err := os.Lstat(final); err == nil {
		tmp, err := os.MkdirTemp(snapshots, ".replaced-*")
		if err != nil {
			return "", err
		}
		defer func() { _ = os.RemoveAll(tmp) }() //nolint:gosec // our own temporary folder
		aside = filepath.Join(tmp, name)
		if err := os.Rename(final, aside); err != nil {
			return "", err
		}
	}
	if err := os.Rename(dir, final); err != nil {
		if aside != "" {
			_ = os.Rename(aside, final)
		}
		return "", err
	}
	return final, Release(final)
}

// SetLatest records name as the snapshot compare uses by default.
func (s Store) SetLatest(name string) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return cache.WriteFile(filepath.Join(s.Root, "snapshots", "LATEST"), []byte(name+"\n"))
}

// Snapshot returns the directory of a named snapshot, or of the latest
// one when name is empty.
func (s Store) Snapshot(name string) (string, error) {
	if name == "" {
		b, err := os.ReadFile(filepath.Join(s.Root, "snapshots", "LATEST"))
		if err != nil {
			return "", errors.New("no snapshot yet; take one first: rhythm soundcheck snapshot <url>")
		}
		name = strings.TrimSpace(string(b))
	}
	if !ValidName(name) {
		return "", fmt.Errorf("%q isn't a valid snapshot name", name)
	}
	dir := filepath.Join(s.Root, "snapshots", name)
	if _, err := os.Stat(filepath.Join(dir, "run.json")); err != nil { //nolint:gosec // name passed ValidName above
		return "", fmt.Errorf("no snapshot named %q; see rhythm soundcheck snapshots", name)
	}
	return dir, nil
}

// Snapshots lists snapshot names, oldest first.
func (s Store) Snapshots() ([]string, error) {
	return s.list("snapshots")
}

// LatestRun returns the newest run directory (a run or comparison).
func (s Store) LatestRun() (string, error) {
	runs, err := s.list("runs")
	if err != nil || len(runs) == 0 {
		return "", errors.New("nothing to show yet; run rhythm soundcheck compare or run first")
	}
	return filepath.Join(s.Root, "runs", runs[len(runs)-1]), nil
}

func (s Store) list(kind string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, kind))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var names []string
	for _, e := range entries {
		// Dot folders are Promote's temporary ones.
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names) // stamps sort chronologically
	return names, err
}

// Prune keeps the newest keep runs and deletes older ones. Snapshots are
// never pruned, and neither is a run another process is still writing.
func (s Store) Prune(keep int) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	runs, err := s.list("runs")
	if err != nil {
		return err
	}
	for len(runs) > keep {
		dir := filepath.Join(s.Root, "runs", runs[0])
		runs = runs[1:]
		if inUse(filepath.Join(dir, owner)) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}

// ExcludeFromGit adds the store's snapshots and runs to the repository's
// local exclude list (.git/info/exclude), so screenshots never get
// committed by accident and the project's .gitignore stays untouched.
// config.yaml and flows/ stay visible to git, to share with the team. It
// does nothing outside a git repository.
func (s Store) ExcludeFromGit() {
	out, err := exec.Command("git", "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return
	}
	path := strings.TrimSpace(string(out))
	rootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return
	}
	abs, err := filepath.Abs(s.Root)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(strings.TrimSpace(string(rootOut)), abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	// Already ignored (e.g. inside a build or work folder): nothing to add.
	if exec.Command("git", "check-ignore", "-q", abs).Run() == nil { //nolint:gosec // our own store path
		return
	}
	base := "/" + filepath.ToSlash(rel) + "/"
	lines := []string{base + "snapshots/", base + "runs/", base + Incoming + "/"}
	existing, _ := os.ReadFile(path) //nolint:gosec // the repository's own exclude file
	if slices.Contains(strings.Split(string(existing), "\n"), lines[0]) {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // the repository's own exclude file
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString("# Soundcheck snapshots and reports\n" + strings.Join(lines, "\n") + "\n")
}
