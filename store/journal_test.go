// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

func TestPageWriterAppendsFromManyGoroutines(t *testing.T) {
	dir := t.TempDir()
	w, err := NewPageWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	pages := synthPages(20000)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := g; i < len(pages); i += 8 {
				if err := w.Append(&pages[i]); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := w.Append(&pages[0]); err == nil {
		t.Error("Append after Close worked")
	}
	if w.Len() != len(pages) {
		t.Errorf("Len = %d", w.Len())
	}
	got, err := Resume(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(pages) {
		t.Fatalf("Resume returned %d pages, want %d", len(got), len(pages))
	}
	slices.SortFunc(got, func(a, b model.Page) int { return strings.Compare(a.Path, b.Path) })
	want := slices.Clone(pages)
	slices.SortFunc(want, func(a, b model.Page) int { return strings.Compare(a.Path, b.Path) })
	for i := range want {
		if got[i].Path != want[i].Path || got[i].Title != want[i].Title {
			t.Fatalf("page %d = %s, want %s", i, got[i].Path, want[i].Path)
		}
	}
}

// journalOf writes pages as a PageWriter would, then tail as a last line
// a crash cut short.
func journalOf(t *testing.T, pages []model.Page, tail string) string {
	t.Helper()
	dir := t.TempDir()
	w, err := NewPageWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range pages {
		if err := w.Append(&pages[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, PagesFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(tail)
	_ = f.Close()
	return dir
}

func TestResumeSkipsACutShortLastLine(t *testing.T) {
	pages := synthPages(3)
	dir := journalOf(t, pages, `{"id":"abc","url":"http://big.test/page-3/","path":"/page-3/","sta`)
	got, err := Resume(dir)
	if err != nil {
		t.Fatal(err)
	}
	if paths(got) != "/page-0/,/page-1/,/page-2/" {
		t.Errorf("Resume = %s", paths(got))
	}

	// Appending again drops the broken line first, so the next page
	// starts on its own line.
	w, err := NewPageWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.Len() != 3 {
		t.Errorf("Len after reopening = %d", w.Len())
	}
	more := synthPages(5)[3:]
	for i := range more {
		if err := w.Append(&more[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got, err = Resume(dir)
	if err != nil || paths(got) != "/page-0/,/page-1/,/page-2/,/page-3/,/page-4/" {
		t.Errorf("after resuming: %s, %v", paths(got), err)
	}
}

func TestResumeLeavesOutCanceledAndRepeatedPages(t *testing.T) {
	pages := synthPages(3)
	canceled := pages[1]
	canceled.Error = "navigate: " + model.CanceledError
	again := pages[0]
	again.Title = "second visit"
	dir := journalOf(t, []model.Page{pages[0], canceled, pages[2], again}, "")
	got, err := Resume(dir)
	if err != nil {
		t.Fatal(err)
	}
	if paths(got) != "/page-2/,/page-0/" || got[1].Title != "second visit" {
		t.Errorf("Resume = %s (%q)", paths(got), got[len(got)-1].Title)
	}
	// A page that failed for a real reason was checked: it stays.
	failed := pages[1]
	failed.Error = "net::ERR_CONNECTION_REFUSED"
	got, _ = Resume(journalOf(t, []model.Page{failed}, ""))
	if len(got) != 1 {
		t.Errorf("a failed page was left out: %v", got)
	}
}

func TestResumeRefusesADamagedJournal(t *testing.T) {
	dir := journalOf(t, synthPages(1), "not json\n")
	f, _ := os.OpenFile(filepath.Join(dir, PagesFile), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"path":"/after/"}` + "\n")
	_ = f.Close()
	if _, err := Resume(dir); err == nil || !strings.Contains(err.Error(), ":2:") {
		t.Errorf("Resume of a damaged file: %v", err)
	}
}

func TestResumeWithoutPagesOrWithASavedRun(t *testing.T) {
	if got, err := Resume(t.TempDir()); err != nil || got != nil {
		t.Errorf("an empty folder: %v, %v", got, err)
	}
	// SaveRun ran but Promote refused: the saved run is what's resumed.
	dir := journalOf(t, synthPages(1), "")
	pages := synthPages(2)
	pages[1].Error = model.CanceledError
	if err := SaveRun(dir, &model.Run{Pages: pages}); err != nil {
		t.Fatal(err)
	}
	if got, err := Resume(dir); err != nil || paths(got) != "/page-0/" {
		t.Errorf("with run.json: %s, %v", paths(got), err)
	}
}

func TestResumableClaimsAStoppedSnapshot(t *testing.T) {
	s := Store{Root: t.TempDir()}
	in := filepath.Join(s.Root, Incoming)
	folder := func(name string, pid int, pages int, last time.Time) string {
		dir := filepath.Join(in, name)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		ownerFile(t, filepath.Join(dir, owner), pid, time.Now())
		if pages > 0 {
			w, err := NewPageWriter(dir)
			if err != nil {
				t.Fatal(err)
			}
			ps := synthPages(pages)
			for i := range ps {
				_ = w.Append(&ps[i])
			}
			_ = w.Close()
			if err := os.Chtimes(filepath.Join(dir, PagesFile), last, last); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	if dir, pages, err := s.Resumable("big"); err != nil || dir != "" || pages != nil {
		t.Errorf("no incoming folder: %q %v %v", dir, pages, err)
	}
	dead := deadPID(t)
	hour := time.Now().Add(-time.Hour)
	folder("big", dead, 2, hour.Add(-time.Hour))
	newest := folder("big-2", dead, 3, hour)
	folder("big-3", os.Getppid(), 4, time.Now()) // still running
	folder("big-4", dead, 0, time.Now())         // nothing finished
	folder("bigger", dead, 5, time.Now())        // another snapshot
	folder("big-02", dead, 5, time.Now())        // not a NewDir suffix

	dir, pages, err := s.Resumable("big")
	if err != nil {
		t.Fatal(err)
	}
	if dir != newest || len(pages) != 3 {
		t.Errorf("Resumable = %s with %d pages; want %s with 3", dir, len(pages), newest)
	}
	if pid, alive := holder(filepath.Join(dir, owner), staleOwner); pid != os.Getpid() || !alive {
		t.Errorf("owner after claiming = %d", pid)
	}
	if _, _, err := s.Resumable("../x"); err == nil {
		t.Error("an invalid name was accepted")
	}
}

func TestCleanIncomingKeepsResumableSnapshotsForADay(t *testing.T) {
	s := Store{Root: t.TempDir()}
	in := filepath.Join(s.Root, Incoming)
	dead := deadPID(t)
	for name, last := range map[string]time.Time{"recent": time.Now().Add(-time.Hour), "old": time.Now().Add(-25 * time.Hour), "empty": {}} {
		dir := filepath.Join(in, name)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		ownerFile(t, filepath.Join(dir, owner), dead, time.Now())
		if !last.IsZero() {
			_ = os.WriteFile(filepath.Join(dir, PagesFile), []byte(`{"path":"/"}`+"\n"), 0o600)
			_ = os.Chtimes(filepath.Join(dir, PagesFile), last, last)
		}
	}
	if _, err := s.NewDir(Incoming, "next"); err != nil {
		t.Fatal(err)
	}
	for name, stays := range map[string]bool{"recent": true, "old": false, "empty": false} {
		if _, err := os.Stat(filepath.Join(in, name)); (err == nil) != stays {
			t.Errorf("%s: stays = %v, want %v", name, err == nil, stays)
		}
	}
}

func TestSameSnapshot(t *testing.T) {
	for folder, want := range map[string]bool{"big": true, "big-2": true, "big-10": true, "big-": false, "big-02": false, "big-x": false, "bigger": false, "bi": false} {
		if got := sameSnapshot(folder, "big"); got != want {
			t.Errorf("sameSnapshot(%q) = %v", folder, got)
		}
	}
}

func paths(pages []model.Page) string {
	var out []string
	for _, p := range pages {
		out = append(out, p.Path)
	}
	return strings.Join(out, ",")
}
