// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package store keeps Soundcheck's files in the project's .soundcheck/
// folder: named snapshots, recent runs and comparisons, each a directory
// with run.json, the run's pages in pages.jsonl, comparison.json for a
// comparison and the screenshots.
package store

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Incoming holds a snapshot while it's being taken, so a failed or
// canceled snapshot never replaces a good one with the same name.
const Incoming = ".incoming"

// Store is a .soundcheck folder.
type Store struct{ Root string }

// Latest is the file in the store's root that names the snapshot compare
// uses by default. Older versions kept it in snapshots/, where it shared
// the namespace with snapshot folders (see latest).
const Latest = "LATEST"

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
//
// For Incoming it first removes snapshots that were never finished (see
// cleanIncoming) and when it creates the Incoming folder itself it adds
// the store to git's exclude list, so screenshots of a snapshot that
// gets killed are never committed.
func (s Store) NewDir(kind, name string) (string, error) {
	if err := CheckName(name); err != nil {
		return "", err
	}
	parent := filepath.Join(s.Root, kind)
	// An incoming snapshot ends up in snapshots/, so that's where its
	// name must not clash; checking now saves a whole scan. Runs are
	// named by Stamp and can't clash.
	if kind == Incoming || kind == "snapshots" {
		if err := caseClash(filepath.Join(s.Root, "snapshots"), name); err != nil {
			return "", err
		}
	}
	_, statErr := os.Stat(parent)
	if err := os.MkdirAll(parent, dirMode); err != nil {
		return "", err
	}
	// A linked runs/ or snapshots/ would put every new folder (with
	// every write into it) wherever the link points.
	if err := refuseLinks(parent); err != nil {
		return "", err
	}
	if kind == Incoming {
		if errors.Is(statErr, fs.ErrNotExist) {
			s.ExcludeFromGit()
		}
		s.cleanIncoming()
	}
	for n := 1; n <= maxSuffix; n++ {
		dir := filepath.Join(parent, name)
		if n > 1 {
			dir = filepath.Join(parent, fmt.Sprintf("%s-%d", name, n))
		}
		// Mkdir fails when the folder exists, which makes it the claim:
		// two processes can't both create the same one.
		err := os.Mkdir(dir, dirMode)
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
	unbeat(filepath.Join(dir, owner))
	err := os.Remove(filepath.Join(dir, owner))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Modes of what the store writes: only the user who runs Soundcheck
// reads a run. Screenshots and traces show pages behind a login and
// run.json holds what the site sent.
const (
	dirMode  = 0o700
	fileMode = 0o600
)

// Saver returns a function that writes files into dir (see writeIn).
func Saver(dir string) func(name string, data []byte) error {
	return func(name string, data []byte) error {
		if _, err := Contained(dir, name); err != nil {
			return fmt.Errorf("refusing to write %q outside the run folder", name)
		}
		return writeIn(dir, name, data)
	}
}

// writeIn writes data to rel inside dir atomically, creating the folders
// on the way, with the store's modes. It refuses to write through a
// symlink: a run folder someone else made, or a link planted in
// .soundcheck/ (runs/x/keyboard pointing at ~/.ssh), must not turn a
// screenshot into a write anywhere on the machine. The folders are
// checked one by one, from just below .soundcheck/ down to the file (see
// refuseLinks). The write goes through an os.Root of dir, so a link
// swapped in between can't lead out of it either.
func writeIn(dir, rel string, data []byte) (err error) {
	rel = filepath.Clean(filepath.FromSlash(rel))
	if err := refuseLinks(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	parts := strings.Split(rel, string(filepath.Separator))
	for i := range parts {
		sub := filepath.Join(parts[:i+1]...)
		fi, err := root.Lstat(sub)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if i < len(parts)-1 {
				if err := root.Mkdir(sub, dirMode); err != nil && !errors.Is(err, fs.ErrExist) {
					return err
				}
			}
		case err != nil:
			return err
		case fi.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("refusing to write through a symlink: %s", filepath.Join(dir, sub))
		}
	}
	tmp := filepath.Join(filepath.Dir(rel), fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano()))
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = root.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, rel)
}

// refuseLinks returns an error when dir or a folder between it and the
// store's root is a symlink: .soundcheck/runs linked elsewhere would send
// every run there. The .soundcheck folder itself may be a link (kept on
// another disk, say): the user made that choice for the whole store. A
// dir outside any .soundcheck folder (a test's temp folder) has only
// itself checked. Folders that don't exist yet end the walk, as nothing
// below them can be a link.
func refuseLinks(dir string) error {
	dir = filepath.Clean(dir)
	chain := []string{dir}
	for p := dir; ; {
		up := filepath.Dir(p)
		if up == p {
			// No .soundcheck above: check dir alone.
			chain = chain[:1]
			break
		}
		if filepath.Base(up) == config.Dir {
			break
		}
		chain = append(chain, up)
		p = up
	}
	for i := len(chain) - 1; i >= 0; i-- {
		fi, err := os.Lstat(chain[i])
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		case fi.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("refusing to write through a symlink: %s", chain[i])
		}
	}
	return nil
}

// Contained joins dir and rel, a slash-separated path from a run or
// comparison file, when rel stays inside dir. Absolute paths, paths that
// climb out with "..", empty ones and (on Windows) device names are
// refused: run.json and comparison.json may come from someone else and
// following such a path would read or write any file on the machine.
func Contained(dir, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || !filepath.IsLocal(clean) {
		return "", fmt.Errorf("%q is outside %s", rel, dir)
	}
	return filepath.Join(dir, clean), nil
}

// Inside is Contained for a file that is read: it also refuses a path
// whose symlinks lead outside dir, so a run folder from someone else
// with shots/a.png linked to a private file can't pull that file into a
// report or a snapshot. A missing file isn't outside: opening it reports
// the error.
func Inside(dir, rel string) (string, error) {
	path, err := Contained(dir, rel)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path, nil //nolint:nilerr // missing: the caller's open says so
	}
	root, err := filepath.EvalSymlinks(cmp.Or(dir, "."))
	if err != nil {
		return "", err
	}
	if inside, err := filepath.Rel(root, real); err != nil || !filepath.IsLocal(inside) {
		return "", fmt.Errorf("%q links outside %s", rel, dir)
	}
	return path, nil
}

// SaveRun writes the run into dir: its pages to a pages file (see
// PagesFile), then run.json naming it. A run with pages whose visit was
// canceled is marked as interrupted, so it's never read back as a full
// result.
//
// Rewriting a stored run (accepting a change into a snapshot) writes
// the pages under the other name before run.json is replaced, so a
// crash leaves the old run or the new one, never a mix; the old pages
// file is removed last.
func SaveRun(dir string, r *model.Run) error {
	if slices.ContainsFunc(r.Pages, func(p model.Page) bool { return p.Canceled() }) {
		r.Interrupted = true
	}
	r.Complete()
	name := PagesFile
	if currentPagesFile(dir) == PagesFile {
		name = altPagesFile
	}
	if err := writePages(filepath.Join(dir, name), r.Pages); err != nil {
		return err
	}
	// The summary, groups and EAA summary are for command output;
	// run.json keeps what was recorded.
	stored := *r
	stored.Summary, stored.Groups, stored.EAA = nil, nil, nil
	stored.Pages = []model.Page{}
	if stored.Schema == model.Schema {
		// A run of another schema keeps it and reads back as such.
		stored.Schema = SplitSchema
	}
	if err := cache.WriteJSON(filepath.Join(dir, "run.json"), storedRun{Run: &stored, PagesFile: name, PageCount: len(r.Pages)}); err != nil {
		if name == altPagesFile {
			_ = os.Remove(filepath.Join(dir, name))
		}
		return err
	}
	old := altPagesFile
	if name == altPagesFile {
		old = PagesFile
	}
	_ = os.Remove(filepath.Join(dir, old))
	return nil
}

// LoadRun reads the run in dir: run.json and the pages it names, or a
// run.json from an older version that holds its pages itself.
func LoadRun(dir string) (*model.Run, error) {
	r, s, err := loadHead(dir)
	if err != nil || s.PagesFile == "" {
		return r, err
	}
	if r.Pages, err = readPages(dir, s.PagesFile, s.PageCount); err != nil {
		return nil, err
	}
	return r, nil
}

// loadHead reads run.json in dir. In the split layout the run comes back
// without its pages; the schema is set to model.Schema, the one the
// rest of Soundcheck knows.
func loadHead(dir string) (*model.Run, storedRun, error) {
	var r model.Run
	s := storedRun{Run: &r}
	if err := cache.ReadJSON(filepath.Join(dir, "run.json"), &s); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, s, fmt.Errorf("no run in %s", dir)
		}
		return nil, s, fmt.Errorf("%s: %w", dir, err)
	}
	switch {
	case r.Schema == SplitSchema && s.PagesFile != "":
		r.Schema = model.Schema
	case r.Schema == model.Schema:
		s.PagesFile = "" // pages are inline in this layout
	default:
		return nil, s, fmt.Errorf("%s was made by an incompatible Soundcheck (%s); take a new snapshot", dir, r.Schema)
	}
	return &r, s, nil
}

// SaveComparison writes comparison.json into dir, completed (kind,
// change IDs, summary) the way scripts expect it.
func SaveComparison(dir string, c *model.Comparison) error {
	c.Complete()
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
// replacing an older snapshot with that name and returns its new folder.
// It holds the store's lock, so two snapshots finishing at once each
// leave a whole snapshot, never a mix of both. A snapshot whose
// screenshots weren't all written (see verifyShots) is refused and the
// previous one stays.
func (s Store) Promote(dir, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("%q isn't a valid snapshot name", name)
	}
	if err := verifyShots(dir); err != nil {
		return "", err
	}
	unlock, err := s.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	snapshots := filepath.Join(s.Root, "snapshots")
	if err := os.MkdirAll(snapshots, dirMode); err != nil {
		return "", err
	}
	if err := caseClash(snapshots, name); err != nil {
		return "", err
	}
	_ = os.Remove(filepath.Join(dir, resumeFile)) // only --resume needs it
	final := filepath.Join(snapshots, name)
	if _, err := os.Lstat(final); err != nil {
		if err := move(dir, final); err != nil {
			return "", err
		}
		return final, Release(final)
	}
	// The old snapshot is moved aside, not deleted, until the new one is
	// in place: a failed rename then puts it back.
	tmp, err := os.MkdirTemp(snapshots, ".replaced-*")
	if err != nil {
		return "", err
	}
	aside := filepath.Join(tmp, name)
	if err := move(final, aside); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := move(dir, final); err != nil {
		if rerr := move(aside, final); rerr != nil {
			// Deleting tmp now would delete the old snapshot too, so it
			// stays where the user can move it back.
			return "", fmt.Errorf("%w; the previous snapshot %q couldn't be put back and is in %s", err, name, aside)
		}
		_ = os.Remove(tmp)
		return "", err
	}
	_ = os.RemoveAll(tmp) //nolint:gosec // our own temporary folder
	return final, Release(final)
}

var (
	// rename is os.Rename, swapped in tests to make it fail.
	rename = os.Rename
	// retryRenames is on where a rename can fail for a moment: on
	// Windows, a virus scanner or the search indexer briefly opens the
	// files of a folder that was just written and moving it is then
	// denied until they let go.
	retryRenames  = runtime.GOOS == "windows"
	renameBackoff = 50 * time.Millisecond
)

// renameTries is how often move tries when retryRenames is on.
const renameTries = 5

// move renames from to to, retrying with a growing pause where renames
// fail for a moment. A missing source is never retried.
func move(from, to string) error {
	err := rename(from, to)
	wait := renameBackoff
	for try := 1; retryRenames && err != nil && try < renameTries && !errors.Is(err, fs.ErrNotExist); try++ {
		time.Sleep(wait)
		wait *= 2
		err = rename(from, to)
	}
	return err
}

// SetLatest records name as the snapshot compare uses by default.
func (s Store) SetLatest(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("%q isn't a valid snapshot name", name)
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := cache.WriteFile(filepath.Join(s.Root, Latest), []byte(name+"\n")); err != nil {
		return err
	}
	// The old pointer would otherwise name a stale snapshot to an older
	// Soundcheck sharing this store and look like a snapshot to no one.
	if legacy := s.legacyLatest(); isFile(legacy) {
		_ = os.Remove(legacy)
	}
	return nil
}

// latest returns the name LATEST points to, or "" when there's none. A
// store from an older version has the pointer in snapshots/; it's read
// there once and moved to the root.
func (s Store) latest() string {
	if b, err := os.ReadFile(filepath.Join(s.Root, Latest)); err == nil {
		return strings.TrimSpace(string(b))
	}
	legacy := s.legacyLatest()
	if !isFile(legacy) {
		// A folder there is a snapshot named LATEST from before the
		// name was reserved, not a pointer.
		return ""
	}
	b, err := os.ReadFile(legacy) //nolint:gosec // the store's own pointer file
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(b))
	if ValidName(name) {
		// A read-only store can't be migrated; it's read from the old
		// place every time then, which works as well.
		_ = s.SetLatest(name)
	}
	return name
}

func (s Store) legacyLatest() string { return filepath.Join(s.Root, "snapshots", Latest) }

// isFile reports whether path is a regular file.
func isFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// Snapshot returns the directory of a named snapshot, or of the latest
// one when name is empty.
func (s Store) Snapshot(name string) (string, error) {
	if name == "" {
		if name = s.latest(); name == "" {
			return "", errors.New("no snapshot yet; take one first: rhythm soundcheck snapshot <url>")
		}
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
		// Dot folders are Promote's temporary ones. Other invalid names
		// (a snapshot named LATEST from before the name was reserved)
		// can't be opened or deleted by name, so they aren't listed.
		if e.IsDir() && ValidName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sortByTime(names)
	return names, err
}

// Prune keeps the newest keep runs and deletes older ones. Snapshots are
// never pruned and neither is a run another process is still writing,
// nor an --eaa run with an evidence pack (eaa/evidence.json): each pack
// is chained to the one before it, so deleting one would leave every
// later pack's chain with a gap. Such runs are deleted by hand.
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
		if inUse(filepath.Join(dir, owner)) || hasEvidence(dir) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}

// evidencePack is where an --eaa run keeps its evidence pack, relative
// to the run's folder (the evidence package's File).
const evidencePack = "eaa/evidence.json"

// hasEvidence reports whether the run in dir has an evidence pack.
func hasEvidence(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(evidencePack)))
	return err == nil
}

// realPath resolves symlinks and short names in p. The store folder may
// not exist yet, so it resolves the longest part that does and keeps the
// rest as it is.
func realPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}

// ExcludeFromGit adds the store's snapshots and runs to the repository's
// local exclude list (.git/info/exclude), so screenshots never get
// committed by accident and the project's .gitignore stays untouched.
// The LATEST pointer and the lock are local state and are excluded too.
// config.yaml and flows/ stay visible to git, to share with the team. It
// does nothing outside a git repository and adds only the lines that
// are missing, so stores excluded by an older version get the new ones.
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
	// Both sides resolved: macOS temp folders sit behind the /var symlink
	// and Windows ones can be 8.3 short names (RUNNER~1), which git
	// reports in full.
	rel, err := filepath.Rel(realPath(strings.TrimSpace(string(rootOut))), realPath(abs))
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	// Already ignored (e.g. inside a build or work folder): nothing to add.
	if exec.Command("git", "check-ignore", "-q", abs).Run() == nil { //nolint:gosec // our own store path
		return
	}
	base := "/" + filepath.ToSlash(rel) + "/"
	lines := []string{base + "snapshots/", base + "runs/", base + Incoming + "/", base + Latest, base + lockFile}
	existing, _ := os.ReadFile(path) //nolint:gosec // the repository's own exclude file
	have := strings.Split(string(existing), "\n")
	missing := slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return slices.Contains(have, l) })
	if len(missing) == 0 {
		return
	}
	text := strings.Join(missing, "\n") + "\n"
	if len(missing) == len(lines) {
		text = "# Soundcheck snapshots and reports\n" + text
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // the repository's own exclude file
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString(text)
}
