// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// bigPages makes n pages of about 2 KB of JSON each, with what every
// built-in comparer reads: timings, plugin files, cache facts, a title
// and three issues that all change between versions.
func bigPages(n int, version string, slow float64) []model.Page {
	pages := make([]model.Page, n)
	for i := range pages {
		path := fmt.Sprintf("/product/%d/", i)
		p := model.Page{ID: fmt.Sprint("p", i), Path: path, URL: "http://shop.test" + path, Status: 200, Title: "Product " + version,
			TextLength: 2000, Group: "product", Cache: map[string]string{"status": "hit", "layer": "nginx"},
			Metrics: model.Metrics{TTFB: 100 * slow, LCP: 900 * slow, TBT: 50},
			Headers: map[string]string{"content-type": "text/html", "cache-control": "max-age=60"}}
		for j := range 12 {
			p.Resources = append(p.Resources, model.Resource{URL: fmt.Sprintf("http://shop.test/wp-content/plugins/p%d/x.js?ver=%s", j, version),
				Type: "Script", Status: 200, Bytes: int64(1000 * j), Cache: "max-age=3600"})
		}
		for j := range 3 {
			p.Issues = append(p.Issues, model.Issue{Check: "health/php-error", Level: model.Warning, Title: fmt.Sprintf("PHP Warning %d in %s", j, version)})
			p.Issues[j].Identify()
		}
		pages[i] = p
	}
	return pages
}

// heapGrowth runs fn and returns how far the live heap grew while it ran,
// sampled with the collector kept busy.
func heapGrowth(fn func()) uint64 {
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

// compare's comparison step on a 20,000-page snapshot with every module
// that compares runs: the snapshot's pages are read a pair at a time, so
// the heap grows by far less than reading the snapshot whole.
func TestCompareBigSnapshotHeap(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("writes a 20,000-page snapshot")
	}
	const n = 20000
	cfg := config.Default()
	cfg.Modules = slices.Clone(config.Modules)
	snap := filepath.Join(t.TempDir(), "snap")
	opts := model.Options{Modules: []string{"health", "visual", "cache", "speed", "seo", "echo"}}
	if err := store.SaveRun(snap, &model.Run{Target: "http://shop.test/", Name: "good", Options: opts, Pages: bigPages(n, "1.0", 1)}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(snap, store.PagesFile))
	if err != nil {
		t.Fatal(err)
	}
	before, pages, err := store.OpenRun(snap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pages.Close() }()
	after := &model.Run{Target: "http://shop.test/", Options: opts, Pages: bigPages(n, "1.1", 3)}
	s := &session{plan: &plan{cfg: cfg}, ctx: context.Background()}

	var c *model.Comparison
	grew := heapGrowth(func() { c, err = s.compareStored(before, pages, snap, after, t.TempDir()) })
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]bool{}
	for _, ch := range c.Changes {
		checks[ch.Check] = true
	}
	if c.Pages != n || !checks["health/php-error"] || !checks["speed/lcp"] {
		t.Fatalf("%d pages, changes %v", c.Pages, checks)
	}
	// Read whole and compared (store.LoadRun and compare.Compare), the
	// snapshot grows the heap by about 130 MB. Streamed it holds the path
	// index and the changes, which are about 40 MB here: every page has
	// its own title change.
	const budget = 64 << 20
	t.Logf("pages file %d MB, heap grew %d MB", info.Size()>>20, grew>>20)
	if grew > budget {
		t.Errorf("comparing with a %d-page snapshot grew the heap by %d MB, want under %d MB", n, grew>>20, budget>>20)
	}
}
