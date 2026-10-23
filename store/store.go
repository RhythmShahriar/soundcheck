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

// Store is a .soundcheck folder.
type Store struct{ Root string }

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidName reports whether name can be used as a snapshot name.
func ValidName(name string) bool { return safeName.MatchString(name) }

// Stamp is the default name for snapshots and runs.
func Stamp(t time.Time) string { return t.Format("2006-01-02T15-04-05") }

// NewDir creates kind/name (kind is "snapshots" or "runs") and returns it.
// An existing snapshot with the same name is replaced.
func (s Store) NewDir(kind, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("%q isn't a valid name: use letters, digits, dots, dashes and underscores", name)
	}
	dir := filepath.Join(s.Root, kind, name)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0o755)
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

// SaveRun writes run.json into dir.
func SaveRun(dir string, r *model.Run) error {
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

// SetLatest records name as the snapshot compare uses by default.
func (s Store) SetLatest(name string) error {
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
	if _, err := os.Stat(filepath.Join(dir, "run.json")); err != nil {
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
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names) // stamps sort chronologically
	return names, err
}

// Prune keeps the newest keep runs and deletes older ones. Snapshots are
// never pruned.
func (s Store) Prune(keep int) error {
	runs, err := s.list("runs")
	if err != nil {
		return err
	}
	for len(runs) > keep {
		if err := os.RemoveAll(filepath.Join(s.Root, "runs", runs[0])); err != nil {
			return err
		}
		runs = runs[1:]
	}
	return nil
}

// ExcludeFromGit adds the store to the repository's local exclude list
// (.git/info/exclude), so snapshots never get committed by accident and
// the project's .gitignore stays untouched. It does nothing outside a
// git repository.
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
	line := "/" + filepath.ToSlash(rel) + "/"
	existing, _ := os.ReadFile(path) //nolint:gosec // the repository's own exclude file
	if slices.Contains(strings.Split(string(existing), "\n"), line) {
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
	_, _ = f.WriteString("# Soundcheck snapshots and reports\n" + line + "\n")
}
