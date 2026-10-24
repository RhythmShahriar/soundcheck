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

	"github.com/RhythmShahriar/soundcheck/model"
)

// A stored run keeps its pages in PagesFile next to run.json, one JSON
// object per line (a page as model/schema/run.v1.json describes it), so
// a run of thousands of pages can be written while it's visited and read
// back a page at a time. run.json keeps the rest (options, site issues,
// warnings) with an empty pages list, names the file and says how many
// pages it holds.
const (
	// PagesFile is where a run's pages are stored, and where PageWriter
	// writes them while a snapshot is taken.
	PagesFile = "pages.jsonl"
	// altPagesFile takes the pages when run.json already names
	// PagesFile, so rewriting a run (accepting a change) never overwrites
	// the file the current run.json points at.
	altPagesFile = "pages.1.jsonl"
	// SplitSchema is the schema of a run.json whose pages are in a file
	// of their own. It differs from model.Schema on purpose: an older
	// Soundcheck that reads only run.json would see a run without pages
	// and report nothing; with an unknown schema it refuses the run.
	SplitSchema = model.Schema + "+jsonl"
)

// maxPrealloc bounds the pages slice made up front from page_count,
// which comes from a file that may not be ours.
const maxPrealloc = 1 << 16

// storedRun is run.json in the split layout.
type storedRun struct {
	*model.Run
	PagesFile string `json:"pages_file,omitempty"`
	PageCount int    `json:"page_count"`
}

// currentPagesFile returns the pages file run.json in dir names, or ""
// without one (no run.json yet, or one from an older version).
func currentPagesFile(dir string) string {
	var head struct {
		PagesFile string `json:"pages_file"`
	}
	f, err := os.Open(filepath.Join(dir, "run.json")) //nolint:gosec // our own run folder
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if json.NewDecoder(f).Decode(&head) != nil {
		return ""
	}
	return head.PagesFile
}

// writePages writes pages to path, one per line, through a temporary
// file that is synced and renamed into place, so path holds the old
// pages or all the new ones.
func writePages(path string, pages []model.Page) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pages-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	w := bufio.NewWriterSize(tmp, 1<<16)
	enc := json.NewEncoder(w)
	for i := range pages {
		if err := enc.Encode(&pages[i]); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// readPages reads the pages file name in dir, which run.json says holds
// want pages. A finished run's file is whole: a line cut short or a
// count that doesn't match is an error, not a shorter run.
func readPages(dir, name string, want int) ([]model.Page, error) {
	f, path, err := openPages(dir, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	pages := make([]model.Page, 0, min(max(want, 0), maxPrealloc))
	r := bufio.NewReaderSize(f, 1<<16)
	var buf []byte
	for n := 1; ; n++ {
		line, complete, err := nextLine(r, buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if !complete {
			return nil, fmt.Errorf("%s is cut short at line %d; take a new snapshot", path, n)
		}
		buf = line
		var p model.Page
		if err := json.Unmarshal(line, &p); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		pages = append(pages, p)
	}
	if len(pages) != want {
		return nil, fmt.Errorf("%s holds %d pages, run.json says %d; take a new snapshot", path, len(pages), want)
	}
	return pages, nil
}

// openPages opens the pages file name in dir. The name comes from
// run.json, which may be someone else's, so it must stay inside dir.
func openPages(dir, name string) (*os.File, string, error) {
	path, err := Contained(dir, name)
	if err != nil {
		return nil, "", fmt.Errorf("%s: run.json names pages file %q outside the run folder", dir, name)
	}
	f, err := os.Open(path) //nolint:gosec // inside the run folder, checked above
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("%s: the run's pages (%s) are missing; take a new snapshot", dir, name)
	}
	return f, path, err
}

// nextLine reads the next line of r without its newline, reusing buf's
// storage, and says whether it ended with a newline: one that doesn't is
// the end of a file a crash cut short. It returns io.EOF when nothing is
// left. Lines may be longer than r's buffer.
func nextLine(r *bufio.Reader, buf []byte) (line []byte, complete bool, err error) {
	buf = buf[:0]
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		switch {
		case err == nil:
			return buf[:len(buf)-1], true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(buf) > 0:
			return buf, false, nil
		default:
			return nil, false, err
		}
	}
}

// Pages reads a stored run's pages one at a time, so a comparison of
// two big runs never holds either whole (see compare.Stream). OpenRun
// returns it with the run's other fields. A run from an older version,
// with its pages inside run.json, is read whole and served from memory.
//
// It's safe for concurrent use. Close it when done.
type Pages struct {
	f      *os.File     // the pages file, or nil for inline pages
	inline []model.Page // the pages of an older run
	spans  []span       // every page, in stored order
	byPath map[string][]int
}

// span is where one page's line is in the pages file.
type span struct {
	off      int64
	n        int
	path     string
	canceled bool
}

// OpenRun reads run.json in dir without its pages (the run's Pages is
// empty) and indexes them for reading one at a time: the index holds
// each page's path and offset, not the page.
func OpenRun(dir string) (*model.Run, *Pages, error) {
	r, s, err := loadHead(dir)
	if err != nil {
		return nil, nil, err
	}
	if s.PagesFile == "" {
		p := &Pages{inline: r.Pages, byPath: map[string][]int{}}
		for i := range r.Pages {
			p.spans = append(p.spans, span{path: r.Pages[i].Path, canceled: r.Pages[i].Canceled()})
			p.byPath[r.Pages[i].Path] = append(p.byPath[r.Pages[i].Path], i)
		}
		head := *r
		head.Pages = []model.Page{}
		return &head, p, nil
	}
	p, err := indexPages(dir, s.PagesFile, s.PageCount)
	if err != nil {
		return nil, nil, err
	}
	return r, p, nil
}

// indexPages opens the pages file and records where each page is.
func indexPages(dir, name string, want int) (*Pages, error) {
	f, path, err := openPages(dir, name)
	if err != nil {
		return nil, err
	}
	p := &Pages{f: f, byPath: map[string][]int{}, spans: make([]span, 0, min(max(want, 0), maxPrealloc))}
	r := bufio.NewReaderSize(f, 1<<16)
	var buf []byte
	var off int64
	for n := 1; ; n++ {
		line, complete, err := nextLine(r, buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err == nil && !complete {
			err = fmt.Errorf("%s is cut short at line %d; take a new snapshot", path, n)
		}
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		buf = line
		// Only what pairing pages needs is decoded here; the rest of the
		// line is read again when the page is.
		var head struct {
			Path  string `json:"path"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		pg := model.Page{Error: head.Error}
		p.byPath[head.Path] = append(p.byPath[head.Path], len(p.spans))
		p.spans = append(p.spans, span{off: off, n: len(line), path: head.Path, canceled: pg.Canceled()})
		off += int64(len(line)) + 1
	}
	if len(p.spans) != want {
		_ = f.Close()
		return nil, fmt.Errorf("%s holds %d pages, run.json says %d; take a new snapshot", path, len(p.spans), want)
	}
	return p, nil
}

// Len returns how many pages the run has.
func (p *Pages) Len() int { return len(p.spans) }

// Path returns the path of page i, in stored order.
func (p *Pages) Path(i int) string { return p.spans[i].path }

// Canceled reports whether page i's visit was canceled (see
// model.Page.Canceled).
func (p *Pages) Canceled(i int) bool { return p.spans[i].canceled }

// Lookup returns the positions of the pages with path, in stored order.
// The slice belongs to p.
func (p *Pages) Lookup(path string) []int { return p.byPath[path] }

// Page reads page i.
func (p *Pages) Page(i int) (*model.Page, error) {
	if p.f == nil {
		pg := p.inline[i]
		return &pg, nil
	}
	s := p.spans[i]
	line := make([]byte, s.n)
	if _, err := p.f.ReadAt(line, s.off); err != nil {
		return nil, fmt.Errorf("%s: page %d: %w", p.f.Name(), i+1, err)
	}
	var pg model.Page
	if err := json.Unmarshal(line, &pg); err != nil {
		return nil, fmt.Errorf("%s: page %d: %w", p.f.Name(), i+1, err)
	}
	return &pg, nil
}

// Close closes the pages file.
func (p *Pages) Close() error {
	if p.f == nil {
		return nil
	}
	return p.f.Close()
}
