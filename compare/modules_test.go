// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	echomodel "github.com/RhythmShahriar/echo/model"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/cache"
	"github.com/RhythmShahriar/soundcheck/checks/echo"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/checks/seo"
	"github.com/RhythmShahriar/soundcheck/checks/speed"
	"github.com/RhythmShahriar/soundcheck/checks/visual"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// builtIn are the modules that compare runs, as the CLI builds them.
func builtIn() []check.Module {
	return []check.Module{health.Module{}, visual.Module{}, cache.Module{}, speed.New(), seo.Module{}, echo.Module{}}
}

// Every built-in module that compares runs can do it a pair of pages at
// a time, so a big comparison never falls back to reading both runs.
func TestBuiltInModulesStream(t *testing.T) {
	for _, m := range builtIn() {
		if _, ok := m.(check.Comparer); !ok {
			continue
		}
		if _, ok := m.(compare.PageComparer); !ok {
			t.Errorf("%s compares whole runs only", m.Name())
		}
	}
}

// writeShot writes a w×h PNG of one color with a block of another,
// which moves with shift, into dir/name.
func writeShot(t *testing.T, dir, name string, shift int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 96))
	for y := range 96 {
		for x := range 64 {
			c := color.RGBA{240, 240, 240, 255}
			if y >= 20+shift && y < 40+shift && x < 48 {
				c = color.RGBA{20, 40, 200, 255}
			}
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// realRun stores a run in dir that gives every built-in comparer
// something to report: titles, redirects and lost text (health),
// screenshots (visual), cache hits (cache), timings and plugin weight
// (speed), robots and SEO facts (seo) and form messages (echo). Without
// cached no page comes from the cache.
func realRun(t *testing.T, dir string, rnd *rand.Rand, n int, later, cached bool) {
	t.Helper()
	modules := []string{"health", "visual", "cache", "speed", "seo", "echo"}
	r := &model.Run{Target: "http://shop.test/", Name: "good", Options: model.Options{Modules: modules}}
	if later {
		r.Name = ""
	}
	if err := os.MkdirAll(filepath.Join(dir, "shots"), 0o755); err != nil {
		t.Fatal(err)
	}
	pick := func(k int) bool { return rnd.IntN(k) == 0 }
	for i := range n {
		if pick(12) {
			continue // in one run only
		}
		path := fmt.Sprintf("/p%d/", i)
		id := fmt.Sprintf("p%d", i)
		p := model.Page{ID: id, URL: "http://shop.test" + path, Path: path, Status: 200, Group: fmt.Sprint("g", i%3),
			Title: fmt.Sprint("Page ", rnd.IntN(2)), TextLength: 500 + rnd.IntN(2)*400, Robots: []string{"", "noindex"}[rnd.IntN(2)],
			Metrics: model.Metrics{TTFB: float64(100 + rnd.IntN(4)*300), LCP: float64(800 + rnd.IntN(3)*900), TBT: float64(rnd.IntN(3) * 200)},
			Cache:   map[string]string{"status": []string{"hit", "miss", "hit"}[rnd.IntN(3)], "layer": "nginx"}}
		if !cached {
			p.Cache["status"] = "miss"
		}
		if pick(6) {
			p.FinalURL = "http://shop.test/elsewhere/"
		}
		if pick(15) {
			p.Status = 500
		}
		if pick(20) {
			p.Error = model.CanceledError
		}
		for j := range 3 {
			p.Resources = append(p.Resources, model.Resource{URL: fmt.Sprintf("http://shop.test/wp-content/plugins/plug%d/a.js", j),
				Type: "Script", Status: 200, Bytes: int64((1 + rnd.IntN(3)) * 200 << 10)})
		}
		p.Resources = append(p.Resources, model.Resource{URL: "http://shop.test/app.js", Bytes: 9000})
		shot := "shots/" + id + ".png"
		writeShot(t, dir, id+".png", rnd.IntN(2)*30)
		_ = os.Rename(filepath.Join(dir, id+".png"), filepath.Join(dir, shot))
		p.Shots = map[string]model.Shot{"desktop": {File: shot, Width: 64, Height: 96}}
		if pick(3) {
			var msgs []echomodel.Message
			if pick(2) {
				msgs = append(msgs, echomodel.Message{ID: "m1", Channel: "email", Subject: "Thanks", To: []string{"a@shop.test"}})
			}
			p.Echo = []model.EchoAction{{Action: "form:" + path + "#f", Label: "Contact", Kind: "form", Page: path, Messages: msgs}}
		}
		writeJSON(t, filepath.Join(dir, "seo", id+".json"), seo.Facts{Description: fmt.Sprint("d", rnd.IntN(2)), H1: 1})
		r.Pages = append(r.Pages, p)
		if pick(30) {
			r.Pages = append(r.Pages, p) // the same path twice
		}
	}
	rnd.Shuffle(len(r.Pages), func(i, j int) { r.Pages[i], r.Pages[j] = r.Pages[j], r.Pages[i] })
	writeJSON(t, filepath.Join(dir, "seo", "site.json"), seo.Site{Robots: seo.Robots{Status: 200, BlocksAll: later && pick(2)}})
	if err := store.SaveRun(dir, r); err != nil {
		t.Fatal(err)
	}
}

// Stream through the built-in modules gives what Compare gives for the
// whole runs.
func TestStreamMatchesCompareForBuiltInModules(t *testing.T) {
	ctx := context.Background()
	reported := map[string]bool{}
	for seed := range uint64(6) {
		rnd := rand.New(rand.NewPCG(seed, 99))
		root := t.TempDir()
		bd, ad := filepath.Join(root, "before"), filepath.Join(root, "after")
		realRun(t, bd, rnd, 40, false, true)
		realRun(t, ad, rnd, 40, true, seed%2 == 1) // even seeds: the cache stopped

		before, err := store.LoadRun(bd)
		if err != nil {
			t.Fatal(err)
		}
		after, err := store.LoadRun(ad)
		if err != nil {
			t.Fatal(err)
		}
		want := compare.Compare(ctx, &check.CompareContext{Before: before, After: after, BeforeDir: bd, AfterDir: ad}, builtIn(), "test")

		bh, bp, err := store.OpenRun(bd)
		if err != nil {
			t.Fatal(err)
		}
		ah, ap, err := store.OpenRun(ad)
		if err != nil {
			t.Fatal(err)
		}
		got, err := compare.Stream(ctx, &check.CompareContext{Before: bh, After: ah, BeforeDir: bd, AfterDir: ad}, bp, ap, builtIn(), "test")
		_, _ = bp.Close(), ap.Close()
		if err != nil {
			t.Fatal(err)
		}
		want.Created, got.Created = time.Time{}, time.Time{}
		w, _ := json.MarshalIndent(want, "", " ")
		g, _ := json.MarshalIndent(got, "", " ")
		if string(w) != string(g) {
			t.Fatalf("seed %d: Stream differs from Compare\nstream:\n%s\ncompare:\n%s", seed, g, w)
		}
		for _, ch := range got.Changes {
			reported[ch.Check[:max(0, indexSlash(ch.Check))]] = true
		}
	}
	for _, m := range []string{"health", "visual", "cache", "speed", "seo", "echo"} {
		if !reported[m] {
			t.Errorf("no %s change in any seed; the test proves nothing for it (got %v)", m, reported)
		}
	}
}

func indexSlash(s string) int {
	for i := range len(s) {
		if s[i] == '/' {
			return i
		}
	}
	return len(s)
}
