// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// titleModule is a module that compares page titles both ways: whole runs
// (Compare) and a pair at a time (ComparePages).
type titleModule struct{}

func (titleModule) Name() string { return "titles" }

func (titleModule) Compare(_ context.Context, cc *check.CompareContext) []model.Change {
	s := &titleSink{}
	for _, p := range cc.Pairs() {
		s.Pair(p[0], p[1])
	}
	return s.Changes()
}

func (titleModule) ComparePages(context.Context, *check.CompareContext) PairSink { return &titleSink{} }

type titleSink struct{ out []model.Change }

func (s *titleSink) Pair(b, a *model.Page) {
	if b.Title != a.Title {
		s.out = append(s.out, model.Change{Class: model.Changed, Check: "titles/changed", Title: "Title changed",
			Pages: []string{a.Path}, Before: b.Title, After: a.Title})
	}
}

func (s *titleSink) Changes() []model.Change { return s.out }

// wholeOnly compares whole runs only.
type wholeOnly struct{ pages *int }

func (wholeOnly) Name() string { return "whole" }

func (w wholeOnly) Compare(_ context.Context, cc *check.CompareContext) []model.Change {
	*w.pages = len(cc.After.Pages)
	return nil
}

// panickySink panics on the second pair.
type panickySink struct{ n int }

func (*panickySink) Name() string { return "panickysink" }

func (*panickySink) Compare(context.Context, *check.CompareContext) []model.Change { return nil }

func (p *panickySink) ComparePages(context.Context, *check.CompareContext) PairSink { return p }

func (p *panickySink) Pair(_, _ *model.Page) {
	if p.n++; p.n == 2 {
		panic("pair broke")
	}
}

func (p *panickySink) Changes() []model.Change {
	return []model.Change{{Class: model.Changed, Check: "panickysink/x", Title: "Must not show"}}
}

// messyRuns makes two runs with what Compare has to get right: pages in
// one run only, a path twice, canceled pages, errors and warnings that
// come and go plus site issues listing pages that have the same issue.
func messyRuns(seed uint64, n int) (before, after *model.Run) {
	rnd := rand.New(rand.NewPCG(seed, seed))
	keys := []model.Issue{
		issue("health/php-error", model.Warning, "PHP Warning"),
		issue("health/status", model.Error, "Server error 500"),
		issue("health/js-error", model.Warning, "JS error"),
		issue("links/broken", model.Error, "Broken link /gone/"),
		issue("titles/x", model.Notice, "Owned elsewhere"),
	}
	page := func(path string) model.Page {
		p := model.Page{Path: path, Title: fmt.Sprint("t", rnd.IntN(3))}
		for _, k := range keys {
			if rnd.IntN(3) == 0 {
				k.Detail = fmt.Sprintf("on %s, %d", path, rnd.IntN(5))
				p.Issues = append(p.Issues, k)
			}
		}
		if rnd.IntN(25) == 0 {
			p.Error = model.CanceledError
		}
		return p
	}
	run := func() *model.Run {
		r := &model.Run{Target: "http://messy.test/", Options: model.Options{Modules: []string{"health", "links", "titles"}}}
		for i := range n {
			if rnd.IntN(10) == 0 {
				continue // only in the other run
			}
			r.Pages = append(r.Pages, page(fmt.Sprintf("/p%d/", i)))
			if rnd.IntN(40) == 0 {
				r.Pages = append(r.Pages, page(fmt.Sprintf("/p%d/", i))) // the same path again
			}
		}
		rnd.Shuffle(len(r.Pages), func(i, j int) { r.Pages[i], r.Pages[j] = r.Pages[j], r.Pages[i] })
		for _, k := range keys[2:4] {
			if rnd.IntN(2) == 0 {
				k.Pages = []string{"/p1/", fmt.Sprintf("/p%d/", rnd.IntN(n))}
				r.Issues = append(r.Issues, k)
			}
		}
		return r
	}
	return run(), run()
}

func comparisonJSON(t *testing.T, c *model.Comparison) string {
	t.Helper()
	c.Created = time.Time{}
	b, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func streamed(t *testing.T, before, after *model.Run, modules []check.Module) *model.Comparison {
	t.Helper()
	bh, ah := *before, *after
	bh.Pages, ah.Pages = nil, nil
	c, err := Stream(context.Background(), &check.CompareContext{Before: &bh, After: &ah}, InMemory(before.Pages), InMemory(after.Pages), modules, "test")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestStreamMatchesCompare(t *testing.T) {
	for seed := range uint64(30) {
		before, after := messyRuns(seed, 120)
		modules := []check.Module{titleModule{}}
		want := comparisonJSON(t, Compare(context.Background(), &check.CompareContext{Before: before, After: after}, modules, "test"))
		got := comparisonJSON(t, streamed(t, before, after, modules))
		if got != want {
			t.Fatalf("seed %d: Stream differs from Compare\nstream:\n%s\ncompare:\n%s", seed, got, want)
		}
		if seed == 0 && !strings.Contains(got, `"titles/changed"`) && !strings.Contains(got, `"broke"`) {
			t.Fatal("the runs gave nothing to compare; the test proves nothing")
		}
	}
}

func TestStreamFallsBackForWholeRunModules(t *testing.T) {
	before, after := messyRuns(7, 50)
	seen := 0
	modules := []check.Module{titleModule{}, wholeOnly{&seen}}
	want := comparisonJSON(t, Compare(context.Background(), &check.CompareContext{Before: before, After: after}, modules, "test"))
	seen = 0
	got := comparisonJSON(t, streamed(t, before, after, modules))
	if got != want {
		t.Errorf("fallback differs from Compare\nstream:\n%s\ncompare:\n%s", got, want)
	}
	if seen == 0 {
		t.Error("the whole-run module didn't get the pages")
	}
}

func TestStreamTurnsAPanickingSinkIntoAWarning(t *testing.T) {
	before, after := messyRuns(3, 30)
	c := streamed(t, before, after, []check.Module{&panickySink{}, titleModule{}})
	warned := false
	for _, w := range c.Warnings {
		warned = warned || strings.HasPrefix(w, "panickysink: panic in Pair: pair broke (compare.(*panickySink).Pair stream_test.go:")
	}
	if !warned {
		t.Errorf("warnings = %q", c.Warnings)
	}
	for _, ch := range c.Changes {
		if ch.Check == "panickysink/x" {
			t.Error("a module that panicked kept its changes")
		}
	}
}

// failing is a PageSource whose pages can't be read.
type failing struct{ PageSource }

func (failing) Page(int) (*model.Page, error) { return nil, errors.New("disk on fire") }

func TestStreamReportsUnreadablePages(t *testing.T) {
	before, after := messyRuns(1, 20)
	cc := &check.CompareContext{Before: &model.Run{}, After: &model.Run{}}
	if _, err := Stream(context.Background(), cc, failing{InMemory(before.Pages)}, InMemory(after.Pages), nil, "test"); err == nil || err.Error() != "disk on fire" {
		t.Errorf("err = %v", err)
	}
	if _, err := Stream(context.Background(), cc, InMemory(before.Pages), failing{InMemory(after.Pages)}, []check.Module{wholeOnly{new(int)}}, "test"); err == nil {
		t.Error("the fallback hid a read error")
	}
}

// fatPages makes n pages of about 2 KB of JSON each, with three issues
// on every page that all change between runs.
func fatPages(n int, version string) []model.Page {
	pages := make([]model.Page, n)
	for i := range pages {
		path := fmt.Sprintf("/product/%d/", i)
		p := model.Page{Path: path, URL: "http://shop.test" + path, Status: 200, Title: "Product " + version,
			Headers: map[string]string{"content-type": "text/html", "cache-control": "max-age=60"}}
		for j := range 12 {
			p.Resources = append(p.Resources, model.Resource{URL: fmt.Sprintf("http://shop.test/wp-content/plugins/p%d/x.js?ver=%s", j, version),
				Type: "Script", Status: 200, Bytes: int64(1000 * j), Cache: "max-age=3600"})
		}
		for j := range 3 {
			p.Issues = append(p.Issues, issue("health/php-error", model.Warning, fmt.Sprintf("PHP Warning %d in %s", j, version)))
		}
		pages[i] = p
	}
	return pages
}

// peakHeap runs fn and returns how far the live heap grew while it ran,
// sampled every few milliseconds with the collector kept busy, so the
// number is close to what fn held rather than garbage not yet collected.
func peakHeap(fn func()) uint64 {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base, peak := ms.HeapAlloc, ms.HeapAlloc
	var mu sync.Mutex
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				mu.Lock()
				peak = max(peak, ms.HeapAlloc)
				mu.Unlock()
			}
		}
	}()
	fn()
	close(stop)
	<-done
	return peak - base
}

func TestStreamKeepsBigRunsOutOfMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("writes two 20,000-page runs")
	}
	const n = 20000
	root := t.TempDir()
	open := func(name, version string) (*model.Run, *store.Pages, int64) {
		dir := filepath.Join(root, name)
		if err := store.SaveRun(dir, &model.Run{Target: "http://shop.test/", Pages: fatPages(n, version)}); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(dir, store.PagesFile))
		if err != nil {
			t.Fatal(err)
		}
		r, p, err := store.OpenRun(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = p.Close() })
		return r, p, info.Size()
	}
	bh, bp, size := open("before", "1.0")
	ah, ap, _ := open("after", "1.1")

	var c *model.Comparison
	var err error
	grew := peakHeap(func() {
		c, err = Stream(context.Background(), &check.CompareContext{Before: bh, After: ah}, bp, ap, []check.Module{titleModule{}}, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	// Six issue changes (three new, three fixed) and the title change,
	// each on every page (the title changes read the same, so they merge).
	if c.Pages != n || len(c.Changes) != 7 || len(c.Changes[0].Pages) != n {
		t.Fatalf("%d pages, %d changes", c.Pages, len(c.Changes))
	}
	// Each run's pages file is about 40 MB; read whole, the two runs
	// grow the heap by about 200 MB. Streaming holds the path index, the
	// changes and a pair of pages.
	const budget = 48 << 20
	t.Logf("pages file %d MB, heap grew %d MB", size>>20, grew>>20)
	if grew > budget {
		t.Errorf("comparing two %d-page runs grew the heap by %d MB, want under %d MB", n, grew>>20, budget>>20)
	}
	if int64(grew) > size {
		t.Errorf("the heap grew by %d MB, more than one run's pages file (%d MB)", grew>>20, size>>20)
	}
}
