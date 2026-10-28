// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package evidence writes the evidence pack of an --eaa run:
// eaa/evidence.json with the SHA-256 of every file the run recorded,
// chained to the previous --eaa run of the same site, so a later reader
// can tell the results weren't changed after the run. A pack can be
// signed with a key of the owner's (Sign, Keygen) and checked (Verify).
//
// A pack proves the files are the ones the run wrote and, when signed,
// who signed them. It doesn't prove the site was in that state for
// every visitor.
package evidence

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cache"

	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// Schema is the schema of eaa/evidence.json
// (model/schema/evidence.v1.json).
const Schema = "rhythm-soundcheck/evidence/v1"

// Files of a pack, relative to the run's folder.
const (
	File    = "eaa/evidence.json"
	SigFile = "eaa/evidence.sig" // ed25519 signature of File, base64
	PubFile = "eaa/evidence.pub" // the public key that made SigFile, base64
)

// maxPack bounds the evidence.json Verify reads: a pack lists one line
// per recorded file, so even a run of thousands of pages stays far
// below.
const maxPack = 32 << 20

// Ref points at an evidence pack: its run and the SHA-256 of its
// evidence.json.
type Ref struct {
	Run    string `json:"run"`    // the run's folder in the store, such as runs/2026-10-26T09-00-00 or snapshots/eaa-1
	SHA256 string `json:"sha256"` // of its eaa/evidence.json, hex
}

// Pack is eaa/evidence.json.
type Pack struct {
	Schema      string           `json:"schema"`
	Tool        string           `json:"tool"`
	Version     string           `json:"version"` // Soundcheck's
	Run         string           `json:"run"`     // see Ref.Run
	Axe         Axe              `json:"axe"`
	Chrome      string           `json:"chrome"` // the browser's version, when the caller knew it
	Options     model.Options    `json:"options"`
	Started     time.Time        `json:"started"`
	Finished    time.Time        `json:"finished"`
	Interrupted bool             `json:"interrupted"`
	Insecure    bool             `json:"insecure"` // TLS certificates weren't verified (-k)
	Pages       []PageAudit      `json:"pages"`
	Files       []FileHash       `json:"files"`
	EAA         model.EAASummary `json:"eaa"`
	Previous    *Ref             `json:"previous"`
	Amends      *Amendment       `json:"amends,omitempty"`
}

// Amendment says a pack was recorded again after the run's files were
// changed on purpose (an accept). It names the pack it replaces. That pack
// and the files it lists are kept in the store (Keep), so packs chained
// to it can still be checked.
type Amendment struct {
	SHA256 string    `json:"sha256"` // of the evidence.json it replaces, kept in evidence/<sha256>/
	Reason string    `json:"reason"` // what changed the files, such as "accept"
	At     time.Time `json:"at"`
}

// Axe is the axe-core build the run used.
type Axe struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// PageAudit is whether each module's audit of a page finished.
type PageAudit struct {
	ID     string            `json:"id"`
	Path   string            `json:"path"`             // without its query string
	Audits map[string]string `json:"audits,omitempty"` // module: status
}

// FileHash is a file of the run and its SHA-256.
type FileHash struct {
	Path   string `json:"path"` // relative to the run's folder, with /
	SHA256 string `json:"sha256"`
}

// Extra is what a pack records that the run itself doesn't: the
// browser's version and whether TLS was verified.
type Extra struct {
	Chrome   string
	Insecure bool
}

// Write records the evidence pack of run in its folder dir, chained to
// the pack prev (nil for the first). run.json must be saved already.
func Write(dir string, run *model.Run, prev *Ref) error {
	return WriteWith(dir, run, prev, Extra{})
}

// WriteWith is Write with the browser's version and the -k setting
// recorded too. Sign refuses a pack whose run didn't verify TLS.
func WriteWith(dir string, run *model.Run, prev *Ref, extra Extra) error {
	return writePack(dir, run, prev, extra, nil)
}

// writePack is WriteWith recording amends too.
func writePack(dir string, run *model.Run, prev *Ref, extra Extra, amends *Amendment) error {
	if run == nil {
		return errors.New("evidence: no run")
	}
	files, err := hashFiles(dir)
	if err != nil {
		return err
	}
	p := Pack{
		Schema: Schema, Tool: "rhythm-soundcheck", Version: run.Version, Run: runID(dir), Chrome: extra.Chrome,
		Options: run.Options, Started: run.Started, Finished: run.Started.Add(time.Duration(run.Duration * float64(time.Millisecond))),
		Interrupted: run.Interrupted, Insecure: extra.Insecure, Pages: []PageAudit{}, Files: files, Previous: prev,
		Amends: amends,
	}
	if a := run.Options.A11y; a != nil {
		p.Axe = Axe{a.Axe, a.AxeSHA256}
	}
	pages, done, err := pagesOf(dir, run)
	if err != nil {
		return err
	}
	pages(func(pg *model.Page) bool {
		pa := PageAudit{ID: pg.ID, Path: cleanPath(pg.Path)}
		for m, a := range pg.Audits {
			if pa.Audits == nil {
				pa.Audits = map[string]string{}
			}
			pa.Audits[m] = a.Status
		}
		p.Pages = append(p.Pages, pa)
		return true
	})
	if run.EAA != nil {
		p.EAA = *run.EAA
	} else {
		p.EAA = wcag.Summarize(run, pages, run.Issues, nil, nil)
	}
	p.EAA.NotAudited = slices.Clone(p.EAA.NotAudited)
	for i := range p.EAA.NotAudited {
		p.EAA.NotAudited[i].Path = cleanPath(p.EAA.NotAudited[i].Path)
	}
	if err := done(); err != nil {
		return fmt.Errorf("evidence: %w", err)
	}
	if err := noSymlink(dir, "eaa"); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return cache.WriteFile(filepath.Join(dir, filepath.FromSlash(File)), append(data, '\n'))
}

// pagesOf gives the run's pages: run.Pages, or streamed from the run's
// folder when the caller passed the run without them. done closes the
// folder's pages file and returns the first error reading a page.
func pagesOf(dir string, run *model.Run) (pages func(func(*model.Page) bool), done func() error, err error) {
	if len(run.Pages) > 0 {
		return func(yield func(*model.Page) bool) {
			for i := range run.Pages {
				if !yield(&run.Pages[i]) {
					return
				}
			}
		}, func() error { return nil }, nil
	}
	_, ps, err := store.OpenRun(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("evidence: %w", err)
	}
	var readErr error
	pages = func(yield func(*model.Page) bool) {
		for i := range ps.Len() {
			pg, err := ps.Page(i)
			if err != nil {
				readErr = cmp.Or(readErr, err)
				return
			}
			if !yield(pg) {
				return
			}
		}
	}
	return pages, func() error { return cmp.Or(readErr, ps.Close()) }, nil
}

// recorded reports whether a file of the run's folder belongs in the
// pack: run.json, the pages file, keyboard traces and focus crops.
func recorded(rel string) bool {
	switch {
	case rel == "run.json", strings.HasPrefix(rel, "pages") && strings.HasSuffix(rel, ".jsonl") && !strings.Contains(rel, "/"):
		return true
	case strings.HasPrefix(rel, "keyboard/"), strings.HasPrefix(rel, "eaa/focus/"):
		return true
	}
	return false
}

// hashFiles hashes every recorded file of dir, sorted by path. run.json
// is hashed in canonical form (see canonical), the rest as they are.
func hashFiles(dir string) ([]FileHash, error) {
	if _, err := os.Stat(filepath.Join(dir, "run.json")); err != nil {
		return nil, fmt.Errorf("evidence: %w", err)
	}
	var out []FileHash
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if e.IsDir() || !recorded(rel) {
			return nil
		}
		if !e.Type().IsRegular() {
			return fmt.Errorf("evidence: %s isn't a regular file", rel)
		}
		sum, err := hashFile(dir, rel)
		if err != nil {
			return err
		}
		out = append(out, FileHash{rel, sum})
		return nil
	})
	slices.SortFunc(out, func(a, b FileHash) int { return strings.Compare(a.Path, b.Path) })
	return out, err
}

// hashFile is the SHA-256 of the run's file rel, hex.
func hashFile(dir, rel string) (string, error) {
	// A listed path swapped for a FIFO or device would block the open
	// or the read, so only a regular file is opened.
	fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("evidence: %s isn't a regular file", rel)
	}
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel))) //nolint:gosec // rel is checked by local or found by WalkDir
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if rel == "run.json" {
		data, err := io.ReadAll(f)
		if err != nil {
			return "", err
		}
		data, err = canonical(data)
		if err != nil {
			return "", fmt.Errorf("evidence: run.json: %w", err)
		}
		return sum(data), nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// canonical is run.json decoded and written again by encoding/json:
// keys sorted, no spacing, numbers as they were. A run.json rewritten
// with other spacing or key order hashes the same; a changed value
// doesn't.
func canonical(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("more than one JSON value")
	}
	return json.Marshal(v)
}

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// runID names the run folder dir in its store: its kind and name.
func runID(dir string) string {
	dir = filepath.Clean(dir)
	return filepath.Base(filepath.Dir(dir)) + "/" + filepath.Base(dir)
}

// storeRoot is the store dir's run folder belongs to.
func storeRoot(dir string) string { return filepath.Dir(filepath.Dir(filepath.Clean(dir))) }

// noQuery is a path without its query string or fragment, which can
// hold session ids and reset keys.
// cleanPath is a page path as the pack lists it: no query string or
// fragment, and a token in the path (/reset/<token>/) or a one-time
// code masked the way statement drafts and keyboard traces mask them.
func cleanPath(p string) string {
	if p == "" {
		return p
	}
	return echolink.RedactURL(noQuery(p))
}

func noQuery(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		return p[:i]
	}
	return p
}

// noSymlink refuses a symbolic link at dir/rel: the pack would be
// written wherever it points.
func noSymlink(dir, rel string) error {
	fi, err := os.Lstat(filepath.Join(dir, rel))
	if err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("evidence: %s is a symbolic link; refusing to write through it", rel)
	}
	return nil
}

// Decode reads an evidence.json strictly: the schema must match and
// every file it lists must be one a pack records, inside the run's
// folder, so a crafted pack can't make Verify read other files.
func Decode(data []byte) (*Pack, error) {
	if len(data) > maxPack {
		return nil, fmt.Errorf("evidence: %s is over %d MB", File, maxPack>>20)
	}
	var p Pack
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("evidence: %s: %w", File, err)
	}
	if p.Schema != Schema {
		return nil, fmt.Errorf("evidence: %s has schema %q, not %s", File, p.Schema, Schema)
	}
	for _, f := range p.Files {
		if !fs.ValidPath(f.Path) || !recorded(f.Path) || path.Clean(f.Path) != f.Path {
			return nil, fmt.Errorf("evidence: %s lists %q, which isn't a file of the run", File, f.Path)
		}
		if _, err := hex.DecodeString(f.SHA256); err != nil || len(f.SHA256) != 64 {
			return nil, fmt.Errorf("evidence: %s: %q isn't a SHA-256", File, f.SHA256)
		}
	}
	if a := p.Amends; a != nil {
		if _, err := hex.DecodeString(a.SHA256); err != nil || len(a.SHA256) != 64 {
			return nil, fmt.Errorf("evidence: %s: amends %q, which isn't a SHA-256", File, a.SHA256)
		}
	}
	if p.Previous != nil && !validRun(p.Previous.Run) {
		return nil, fmt.Errorf("evidence: %s: previous run %q isn't a run of the store", File, p.Previous.Run)
	}
	return &p, nil
}

// validRun reports whether id names a run or snapshot folder.
func validRun(id string) bool {
	kind, name, ok := strings.Cut(id, "/")
	return ok && (kind == "runs" || kind == "snapshots") && fs.ValidPath(id) && name != "" && !strings.Contains(name, "/") && name != "." && name != ".."
}

// readPack reads and decodes the pack of the run folder dir.
func readPack(dir string) ([]byte, *Pack, error) {
	data, err := readRaw(dir)
	if err != nil {
		return nil, nil, err
	}
	p, err := Decode(data)
	return data, p, err
}

// readRaw reads the evidence.json of the run folder dir, up to just
// over the size Decode accepts.
func readRaw(dir string) ([]byte, error) {
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(File))) //nolint:gosec // the caller's run folder
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxPack+1))
}

// Verify checks the evidence pack in dir: every file it lists has the
// SHA-256 it recorded, no recorded kind of file was added, a signature
// next to it is valid and the chain of previous packs is whole back to
// the first run, each of them checked the same way.
//
// A link whose run was changed on purpose since (an accept into a
// snapshot, a snapshot taken again or deleted) is checked against the
// copy kept when it changed (Keep). The notes say where that happened.
func Verify(dir string) (notes []string, err error) {
	root := storeRoot(dir)
	_, p, err := readPack(dir)
	if err != nil {
		return nil, err
	}
	if err := verifyPack(dir, p, ""); err != nil {
		return nil, err
	}
	note, err := amendNote(root, runID(dir), p)
	if err != nil {
		return nil, err
	}
	if note != "" {
		notes = append(notes, note)
	}
	seen := map[string]bool{runID(dir): true}
	for id := runID(dir); p.Previous != nil; {
		prev := p.Previous
		if seen[prev.Run] {
			return notes, fmt.Errorf("evidence: the chain loops at %s", prev.Run)
		}
		seen[prev.Run] = true
		pdir, prefix := filepath.Join(root, filepath.FromSlash(prev.Run)), prev.Run+"/"
		data, pp, err := readPack(pdir)
		if errors.Is(err, fs.ErrNotExist) || err == nil && sum(data) != prev.SHA256 {
			how := "was deleted or taken again"
			if err == nil {
				how = "changed"
				if pp.Amends != nil {
					how = "changed by " + pp.Amends.Reason
				}
			}
			kdir, kdata, kp, kerr := kept(root, prev.SHA256)
			switch {
			case kerr == nil:
				notes = append(notes, fmt.Sprintf("%s %s after %s was chained to it; checked the copy kept in %s", prev.Run, how, id, keptName(prev.SHA256)))
				pdir, prefix, data, pp, err = kdir, keptName(prev.SHA256)+"/", kdata, kp, nil
			case !errors.Is(kerr, fs.ErrNotExist):
				return notes, kerr
			}
		}
		if errors.Is(err, fs.ErrNotExist) {
			return notes, fmt.Errorf("evidence: the run before %s is missing from the chain", id)
		}
		if err != nil {
			return notes, err
		}
		if sum(data) != prev.SHA256 {
			return notes, fmt.Errorf("evidence: %s/%s changed after it was recorded (sha256 differs)", prev.Run, File)
		}
		if err := verifyPack(pdir, pp, prefix); err != nil {
			return notes, err
		}
		id, p = prev.Run, pp
	}
	return notes, nil
}

// verifyPack checks the files and signature of one pack. Errors name
// files with prefix, the run's folder in the store for an earlier run.
func verifyPack(dir string, p *Pack, prefix string) error {
	listed := map[string]bool{}
	for _, f := range p.Files {
		listed[f.Path] = true
		got, err := hashFile(dir, f.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("evidence: %s%s was deleted after it was recorded", prefix, f.Path)
		}
		if err != nil {
			return err
		}
		if got != f.SHA256 {
			return fmt.Errorf("evidence: %s%s changed after it was recorded (sha256 differs)", prefix, f.Path)
		}
	}
	now, err := hashFiles(dir)
	if err != nil {
		return err
	}
	for _, f := range now {
		if !listed[f.Path] {
			return fmt.Errorf("evidence: %s%s was added after the pack was recorded", prefix, f.Path)
		}
	}
	return checkSignature(dir, prefix)
}

// Previous finds the evidence pack of the last --eaa run of target in
// the store at storeRoot, for Write to chain to: the run or snapshot
// with the latest start that was made with --eaa against the same
// target and has a pack. A run without a pack yet (the one being
// written) is skipped. It is nil when there is none.
//
// Previous is for a run being written now, the newest of its target. To
// write the pack of an older run again, use PreviousBefore with its
// start, or the chain would point forward.
func Previous(storeRoot, target string) (*Ref, error) {
	return PreviousBefore(storeRoot, target, time.Time{})
}

// PreviousBefore is Previous for a run that started at before: it only
// considers runs that started earlier. A zero before considers them
// all.
func PreviousBefore(storeRoot, target string, before time.Time) (*Ref, error) {
	var best *Ref
	var bestStart time.Time
	for _, kind := range []string{"runs", "snapshots"} {
		entries, err := os.ReadDir(filepath.Join(storeRoot, kind))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := filepath.Join(storeRoot, kind, e.Name())
			head, err := readHead(dir)
			if err != nil || !head.Options.EAA || !sameTarget(head.Target, target) ||
				(!before.IsZero() && !head.Started.Before(before)) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(File))) //nolint:gosec // a folder of the store
			if err != nil {
				continue
			}
			if best == nil || head.Started.After(bestStart) {
				best, bestStart = &Ref{Run: kind + "/" + e.Name(), SHA256: sum(data)}, head.Started
			}
		}
	}
	return best, nil
}

// head is the part of run.json Previous needs.
type head struct {
	Target  string    `json:"target"`
	Started time.Time `json:"started"`
	Options struct {
		EAA bool `json:"eaa"`
	} `json:"options"`
}

// readHead reads run.json's target, start and --eaa setting without
// its pages.
func readHead(dir string) (head, error) {
	var h head
	f, err := os.Open(filepath.Join(dir, "run.json")) //nolint:gosec // a folder of the store
	if err != nil {
		return h, err
	}
	defer func() { _ = f.Close() }()
	err = json.NewDecoder(io.LimitReader(f, maxPack)).Decode(&h)
	return h, err
}

// sameTarget compares targets as the CLI writes them, ignoring a
// trailing slash.
func sameTarget(a, b string) bool {
	return strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/")
}
