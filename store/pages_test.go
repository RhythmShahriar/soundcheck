// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/soundcheck/model"
)

// synthPages makes n pages with a little of everything a real one
// records.
func synthPages(n int) []model.Page {
	pages := make([]model.Page, n)
	for i := range pages {
		path := fmt.Sprintf("/page-%d/", i)
		pages[i] = model.Page{
			ID: model.PageID(path), URL: "http://big.test" + path, Path: path, Status: 200, Title: fmt.Sprintf("Page %d", i),
			TextLength: 1000 + i, Headers: map[string]string{"content-type": "text/html"},
			Resources: []model.Resource{{URL: "http://big.test/app.js", Type: "Script", Status: 200, Bytes: 2048}},
			Links:     []string{"/", fmt.Sprintf("/page-%d/", (i+1)%n)},
			Metrics:   model.Metrics{TTFB: 120, Load: 900, Bytes: 40960, Requests: 12},
			Shots:     map[string]model.Shot{"desktop": {File: "shots/" + model.PageID(path) + "-desktop.png", Width: 1280, Height: 2000}},
			Issues:    []model.Issue{{Check: "health/php-error", Level: model.Warning, Title: "PHP Warning", Key: "health/php-error|PHP Warning|"}},
		}
	}
	return pages
}

func TestSaveRunRoundTripsBigRuns(t *testing.T) {
	dir := t.TempDir()
	pages := synthPages(20000)
	pages[7].Error = model.CanceledError
	if err := SaveRun(dir, &model.Run{Target: "http://big.test/", Name: "big", Pages: pages, Issues: []model.Issue{{Check: "links/broken", Key: "k"}}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "run.json"))
	if err != nil || info.Size() > 4096 {
		t.Errorf("run.json is %d bytes, want a small file without the pages (%v)", info.Size(), err)
	}
	r, err := LoadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Schema != model.Schema || !r.Interrupted || r.Name != "big" || len(r.Issues) != 1 {
		t.Errorf("run = schema %q, interrupted %v, name %q, issues %v", r.Schema, r.Interrupted, r.Name, r.Issues)
	}
	if !reflect.DeepEqual(r.Pages, pages) {
		t.Fatalf("pages didn't round-trip: %d read, %d written", len(r.Pages), len(pages))
	}

	head, p, err := OpenRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if len(head.Pages) != 0 || head.Name != "big" || p.Len() != len(pages) {
		t.Fatalf("OpenRun: %d pages in the head, %d indexed", len(head.Pages), p.Len())
	}
	for _, i := range []int{0, 7, 12345, 19999} {
		got, err := p.Page(i)
		if err != nil || !reflect.DeepEqual(*got, pages[i]) {
			t.Errorf("Page(%d) = %v, %v", i, got.Path, err)
		}
		if p.Path(i) != pages[i].Path || p.Canceled(i) != (i == 7) {
			t.Errorf("index %d: path %q canceled %v", i, p.Path(i), p.Canceled(i))
		}
		if at := p.Lookup(pages[i].Path); len(at) != 1 || at[0] != i {
			t.Errorf("Lookup(%q) = %v", pages[i].Path, at)
		}
	}
	if p.Lookup("/nowhere/") != nil {
		t.Error("Lookup found a page that isn't there")
	}
}

func TestSaveRunCreatesItsFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run", "against")
	if err := SaveRun(dir, &model.Run{Pages: synthPages(1)}); err != nil {
		t.Fatal(err)
	}
	if r, err := LoadRun(dir); err != nil || len(r.Pages) != 1 {
		t.Errorf("LoadRun: %v", err)
	}
}

func TestOldSingleFileRunsStillLoad(t *testing.T) {
	dir := t.TempDir()
	pages := synthPages(3)
	pages = append(pages, pages[1]) // a path twice
	old := &model.Run{Schema: model.Schema, Kind: model.KindRun, Target: "http://old.test/", Pages: pages, Warnings: []string{}}
	if err := cache.WriteJSON(filepath.Join(dir, "run.json"), old); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRun(dir)
	if err != nil || !reflect.DeepEqual(r.Pages, pages) || r.Target != "http://old.test/" {
		t.Fatalf("LoadRun of the old layout: %v", err)
	}
	head, p, err := OpenRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(head.Pages) != 0 || p.Len() != 4 || !reflect.DeepEqual(p.Lookup(pages[1].Path), []int{1, 3}) {
		t.Errorf("OpenRun of the old layout: %d head pages, %d indexed, %v", len(head.Pages), p.Len(), p.Lookup(pages[1].Path))
	}
	if got, err := p.Page(2); err != nil || got.Path != pages[2].Path {
		t.Errorf("Page(2) = %v, %v", got, err)
	}
	if err := p.Close(); err != nil {
		t.Error(err)
	}

	// Saving it again (accepting a change) moves it to the new layout.
	if err := SaveRun(dir, r); err != nil {
		t.Fatal(err)
	}
	if currentPagesFile(dir) != PagesFile {
		t.Errorf("pages file = %q", currentPagesFile(dir))
	}
	if again, err := LoadRun(dir); err != nil || !reflect.DeepEqual(again.Pages, pages) {
		t.Errorf("after saving: %v", err)
	}
}

func TestSaveRunNeverOverwritesTheFileRunJSONNames(t *testing.T) {
	dir := t.TempDir()
	pages := synthPages(5)
	want := []string{PagesFile, altPagesFile, PagesFile}
	for i, name := range want {
		pages[0].Title = fmt.Sprint("version ", i)
		if err := SaveRun(dir, &model.Run{Pages: pages}); err != nil {
			t.Fatal(err)
		}
		if got := currentPagesFile(dir); got != name {
			t.Errorf("save %d: run.json names %q, want %q", i, got, name)
		}
		entries, _ := os.ReadDir(dir)
		var files []string
		for _, e := range entries {
			files = append(files, e.Name())
		}
		if strings.Join(files, ",") != name+",run.json" { // ReadDir sorts by name
			t.Errorf("save %d left %v; want only run.json and %s", i, files, name)
		}
		r, err := LoadRun(dir)
		if err != nil || r.Pages[0].Title != pages[0].Title {
			t.Errorf("save %d read back %v, %v", i, r, err)
		}
	}
}

func TestLoadRunRefusesBrokenPages(t *testing.T) {
	write := func(t *testing.T, pagesFile string, count int, lines string) string {
		t.Helper()
		dir := t.TempDir()
		head := storedRun{Run: &model.Run{Schema: SplitSchema, Pages: []model.Page{}}, PagesFile: pagesFile, PageCount: count}
		if err := cache.WriteJSON(filepath.Join(dir, "run.json"), head); err != nil {
			t.Fatal(err)
		}
		if lines != "" {
			if err := os.WriteFile(filepath.Join(dir, PagesFile), []byte(lines), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	for name, c := range map[string]struct {
		file  string
		count int
		lines string
		want  string
	}{
		"cut short":    {PagesFile, 2, `{"path":"/"}` + "\n" + `{"path":"/a`, "cut short at line 2"},
		"fewer":        {PagesFile, 3, `{"path":"/"}` + "\n", "holds 1 pages, run.json says 3"},
		"more":         {PagesFile, 0, `{"path":"/"}` + "\n", "holds 1 pages, run.json says 0"},
		"not json":     {PagesFile, 1, "nope\n", ":1:"},
		"missing":      {PagesFile, 1, "", "pages (pages.jsonl) are missing"},
		"outside":      {"../pages.jsonl", 1, `{"path":"/"}` + "\n", "outside the run folder"},
		"absolute":     {"/etc/passwd", 1, "", "outside the run folder"},
		"no file name": {"", 0, "", "incompatible"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := write(t, c.file, c.count, c.lines)
			if _, err := LoadRun(dir); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("LoadRun: %v; want %q", err, c.want)
			}
			if _, _, err := OpenRun(dir); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("OpenRun: %v; want %q", err, c.want)
			}
		})
	}
	// The same file with the right count loads.
	dir := write(t, PagesFile, 1, `{"path":"/"}`+"\n")
	if r, err := LoadRun(dir); err != nil || len(r.Pages) != 1 {
		t.Errorf("a whole file: %v", err)
	}
}

func TestNextLineReadsLongLines(t *testing.T) {
	dir := t.TempDir()
	pages := synthPages(2)
	pages[0].Console = []string{strings.Repeat("x", 300<<10)} // longer than the reader's buffer
	if err := SaveRun(dir, &model.Run{Pages: pages}); err != nil {
		t.Fatal(err)
	}
	r, err := LoadRun(dir)
	if err != nil || !reflect.DeepEqual(r.Pages, pages) {
		t.Fatalf("long line: %v", err)
	}
	_, p, err := OpenRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if got, err := p.Page(1); err != nil || got.Path != pages[1].Path {
		t.Errorf("the page after a long line: %v, %v", got, err)
	}
}
