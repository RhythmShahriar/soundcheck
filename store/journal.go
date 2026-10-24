// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

// keepResumable is how long an abandoned snapshot that finished some
// pages is kept in Incoming for --resume, counted from its last page.
var keepResumable = 24 * time.Hour

// PageWriter appends pages to a snapshot's pages file as they're
// visited (the runner's OnPage callback), so a snapshot that is stopped
// or killed keeps the pages it finished and --resume can skip them (see
// Resume). Each page is one write of one line: a crash loses at most the
// page being written, as a cut-short last line. SaveRun later replaces
// the file with the run's final pages.
//
// It's safe for concurrent use.
type PageWriter struct {
	mu  sync.Mutex
	f   *os.File
	n   int
	err error // the first failed write; every later Append returns it
}

// NewPageWriter opens the pages file in dir for appending. A file left
// by an earlier, stopped snapshot keeps its pages; a last line it cut
// short is dropped first, so the next page starts on a line of its own.
func NewPageWriter(dir string) (*PageWriter, error) {
	path := filepath.Join(dir, PagesFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // our own snapshot folder
	if err != nil {
		return nil, err
	}
	end, n, err := wholeLines(f)
	if err == nil {
		err = f.Truncate(end)
	}
	if err == nil {
		_, err = f.Seek(end, io.SeekStart)
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &PageWriter{f: f, n: n}, nil
}

// wholeLines returns where the last whole line of f ends and how many
// whole lines there are.
func wholeLines(f *os.File) (end int64, n int, err error) {
	r := bufio.NewReaderSize(f, 1<<16)
	var buf []byte
	for {
		line, complete, err := nextLine(r, buf)
		if errors.Is(err, io.EOF) || (err == nil && !complete) {
			return end, n, nil
		}
		if err != nil {
			return 0, 0, err
		}
		buf = line
		end += int64(len(line)) + 1
		n++
	}
}

// Append writes p as one line.
func (w *PageWriter) Append(p *model.Page) error {
	line, err := json.Marshal(p)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.f == nil {
		return errors.New("page writer is closed")
	}
	// A short write leaves part of a line; appending after it would glue
	// the next page to it, so the writer stops at the first failure.
	if _, err := w.f.Write(line); err != nil {
		w.err = err
		return err
	}
	w.n++
	return nil
}

// Len returns how many pages the file holds, from before and since.
func (w *PageWriter) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.n
}

// Close syncs the file to disk and closes it. Closing twice is fine.
func (w *PageWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Sync()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	w.f = nil
	return err
}

// Resume returns the pages a stopped snapshot in incomingDir finished,
// for --resume to keep and not visit again. Pages whose visit was
// canceled weren't finished and are left out; a page written twice
// counts once, as its last visit. A last line cut short by a crash is
// skipped. A folder without pages gives none.
//
// The pages have no Text: it isn't stored (see model.Page.Text).
func Resume(incomingDir string) ([]model.Page, error) {
	var pages []model.Page
	if _, err := os.Stat(filepath.Join(incomingDir, "run.json")); err == nil {
		// SaveRun ran but the snapshot wasn't promoted (a screenshot was
		// missing): the run itself is whole.
		r, err := LoadRun(incomingDir)
		if err != nil {
			return nil, err
		}
		pages = r.Pages
	} else if pages, err = journal(filepath.Join(incomingDir, PagesFile)); err != nil {
		return nil, err
	}
	last := map[string]int{}
	for i, p := range pages {
		if !p.Canceled() {
			last[p.Path] = i
		}
	}
	var out []model.Page
	for i, p := range pages {
		if j, ok := last[p.Path]; ok && j == i {
			out = append(out, p)
		}
	}
	return out, nil
}

// journal reads a PageWriter's file. Only its last line may be cut
// short; a broken line before it means the file is damaged.
func journal(path string) ([]model.Page, error) {
	f, err := os.Open(path) //nolint:gosec // our own snapshot folder
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1<<16)
	var pages []model.Page
	var buf []byte
	for n := 1; ; n++ {
		line, complete, err := nextLine(r, buf)
		if errors.Is(err, io.EOF) || (err == nil && !complete) {
			return pages, nil
		}
		if err != nil {
			return nil, err
		}
		buf = line
		var p model.Page
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		pages = append(pages, p)
	}
}

// Resumable finds a stopped snapshot called name in Incoming that no
// process is writing any more, claims it for this process and returns
// its folder with the pages it finished (see Resume). When there are
// several (name, name-2…), it takes the one that wrote a page last. It
// returns "" when there's none; the caller then starts afresh with
// NewDir.
func (s Store) Resumable(name string) (string, []model.Page, error) {
	if err := CheckName(name); err != nil {
		return "", nil, err
	}
	unlock, err := s.lock()
	if err != nil {
		return "", nil, err
	}
	defer unlock()
	entries, err := os.ReadDir(filepath.Join(s.Root, Incoming))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	var best string
	var bestAt time.Time
	for _, e := range entries {
		if !e.IsDir() || !sameSnapshot(e.Name(), name) {
			continue
		}
		dir := filepath.Join(s.Root, Incoming, e.Name())
		at, ok := resumable(dir)
		if ok && !held(dir) && (best == "" || at.After(bestAt)) {
			best, bestAt = dir, at
		}
	}
	if best == "" {
		return "", nil, nil
	}
	pages, err := Resume(best)
	if err != nil {
		return "", nil, err
	}
	return best, pages, writeOwner(filepath.Join(best, owner))
}

// sameSnapshot reports whether folder is name or one of NewDir's
// "name-2", "name-3"… folders for it.
func sameSnapshot(folder, name string) bool {
	rest, ok := strings.CutPrefix(folder, name)
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	n, ok := strings.CutPrefix(rest, "-")
	return ok && n != "" && n[0] != '0' && strings.Trim(n, "0123456789") == ""
}

// resumable reports whether an Incoming folder has finished pages to
// resume from and when it wrote the last one.
func resumable(dir string) (time.Time, bool) {
	info, err := os.Stat(filepath.Join(dir, PagesFile))
	if err != nil || info.Size() == 0 {
		return time.Time{}, false
	}
	return info.ModTime(), true
}
