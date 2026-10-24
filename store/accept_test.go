// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/visual"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// screen is a plain page: a dark header on white, plus a red block when
// block isn't empty.
func screen(block image.Rectangle) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 800, 1200))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, 800, 60), image.NewUniform(color.NRGBA{20, 20, 30, 255}), image.Point{}, draw.Src)
	draw.Draw(img, block, image.NewUniform(color.NRGBA{200, 40, 40, 255}), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func php(title string) model.Issue {
	i := model.Issue{Check: "health/php-error", Level: model.Warning, Title: title, Source: "lib.php:6"}
	i.Identify()
	return i
}

// fixture is a snapshot and a later run of /a/, /b/ and /c/. In the run
// /a/ and /b/ gained a red block and /a/ a PHP warning; /c/ is slower
// and has a new title.
type fixture struct {
	snap, run string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".soundcheck")
	f := fixture{snap: filepath.Join(root, "snapshots", "good"), run: filepath.Join(root, "runs", "now")}
	write := func(dir string, blocks map[string]image.Rectangle, edit func(p *model.Page)) {
		r := &model.Run{Schema: model.Schema, Version: "test", Target: "http://site.test/",
			Options: model.Options{Viewports: []string{"desktop"}, Modules: []string{"health", "visual", "speed"}}}
		for _, path := range []string{"/a/", "/b/", "/c/"} {
			p := model.Page{ID: model.PageID(path), Path: path, Status: 200, Title: "Page " + path,
				Metrics: model.Metrics{TTFB: 100, Load: 500}}
			file := "shots/" + p.ID + "-desktop.png"
			if err := store.Saver(dir)(file, screen(blocks[path])); err != nil {
				t.Fatal(err)
			}
			p.Shots = map[string]model.Shot{"desktop": {File: file, Width: 800, Height: 1200}}
			edit(&p)
			r.Pages = append(r.Pages, p)
		}
		if err := store.SaveRun(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	write(f.snap, nil, func(p *model.Page) {
		if p.Path == "/a/" {
			p.Issues = []model.Issue{php("PHP Warning: old")}
		}
	})
	write(f.run, map[string]image.Rectangle{"/a/": image.Rect(100, 800, 300, 900), "/b/": image.Rect(500, 300, 700, 450)},
		func(p *model.Page) {
			switch p.Path {
			case "/a/":
				p.Issues = []model.Issue{php("PHP Warning: old"), php(`PHP Warning: Undefined array key "id"`)}
			case "/c/":
				p.Title = "Renamed"
				p.Metrics.Load = 2500
			}
		})
	return f
}

// compareAgain compares the run with the snapshot as it is on disk now.
func (f fixture) compareAgain(t *testing.T) *model.Comparison {
	t.Helper()
	before, err := store.LoadRun(f.snap)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.LoadRun(f.run)
	if err != nil {
		t.Fatal(err)
	}
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: f.snap, AfterDir: f.run}
	return compare.Compare(context.Background(), cc, []check.Module{visual.Module{}}, "test")
}

func onPage(c *model.Comparison, check, page string) []model.Change {
	var out []model.Change
	for _, ch := range c.Changes {
		if model.Glob(check, ch.Check) && slices.Contains(ch.Pages, page) {
			out = append(out, ch)
		}
	}
	return out
}

func TestAcceptVisualChangeOnOnePage(t *testing.T) {
	f := newFixture(t)
	c := f.compareAgain(t)
	if len(onPage(c, "visual/*", "/a/")) == 0 || len(onPage(c, "visual/*", "/b/")) == 0 {
		t.Fatalf("the fixture should change /a/ and /b/: %+v", c.Changes)
	}
	done, err := store.Accept(f.snap, f.run, c, store.Selector{Page: "/a/*", Check: "visual/*"}, "dana")
	if err != nil {
		t.Fatal(err)
	}
	if len(done) == 0 || done[0].Page != "/a/" || done[0].Who != "dana" || done[0].At.IsZero() || done[0].ID == "" {
		t.Errorf("accepted = %+v", done)
	}

	again := f.compareAgain(t)
	if got := onPage(again, "visual/*", "/a/"); len(got) != 0 {
		t.Errorf("/a/ still looks different after accepting it: %+v", got)
	}
	if len(onPage(again, "visual/*", "/b/")) == 0 {
		t.Errorf("/b/'s visual change went away too: %+v", again.Changes)
	}
	if len(onPage(again, "health/php-error", "/a/")) == 0 {
		t.Errorf("accepting /a/'s looks accepted its PHP warning too: %+v", again.Changes)
	}

	// The old screenshot is gone, the new one is a PNG in the snapshot
	// and the record says who accepted what.
	snap, err := store.LoadRun(f.snap)
	if err != nil {
		t.Fatal(err)
	}
	shot := snap.Page("/a/").Shots["desktop"]
	if !strings.HasPrefix(shot.File, "shots/") || shot.File == "shots/"+model.PageID("/a/")+"-desktop.png" {
		t.Errorf("new screenshot name %q", shot.File)
	}
	if _, err := os.Stat(filepath.Join(f.snap, "shots", model.PageID("/a/")+"-desktop.png")); !os.IsNotExist(err) {
		t.Errorf("the replaced screenshot is still there: %v", err)
	}
	if snap.Page("/b/").Shots["desktop"].File != "shots/"+model.PageID("/b/")+"-desktop.png" {
		t.Errorf("/b/'s screenshot changed: %+v", snap.Page("/b/").Shots)
	}
	info, err := store.LoadSnapshotInfo(f.snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Accepted) != len(done) || info.Accepted[0].Page != "/a/" || info.Kind != store.KindSnapshot {
		t.Errorf("snapshot.json = %+v", info)
	}
	// What a snapshot must pass to be promoted still holds.
	if err := store.VerifyShots(f.snap); err != nil {
		t.Errorf("the accepted snapshot fails the screenshot check: %v", err)
	}
}

func TestAcceptNewIssueByID(t *testing.T) {
	f := newFixture(t)
	c := f.compareAgain(t)
	news := onPage(c, "health/php-error", "/a/")
	if len(news) != 1 || news[0].Class != model.New {
		t.Fatalf("want one NEW PHP warning on /a/: %+v", c.Changes)
	}
	if _, err := store.Accept(f.snap, f.run, c, store.Selector{IDs: []string{news[0].ID}}, "dana"); err != nil {
		t.Fatal(err)
	}
	again := f.compareAgain(t)
	if got := onPage(again, "health/php-error", "/a/"); len(got) != 0 {
		t.Errorf("the accepted warning is still reported: %+v", got)
	}
	if len(onPage(again, "visual/*", "/a/")) == 0 {
		t.Errorf("accepting the warning accepted /a/'s looks too")
	}
	snap, _ := store.LoadRun(f.snap)
	if n := len(snap.Page("/a/").Issues); n != 2 {
		t.Errorf("/a/ has %d issues in the snapshot, want the old and the accepted one", n)
	}
}

func TestAcceptSpeedAndTitle(t *testing.T) {
	f := newFixture(t)
	c := &model.Comparison{Changes: []model.Change{
		{ID: "00000001", Class: model.Slower, Check: "speed/load", Title: "Load 5× slower", Pages: []string{"/c/"}},
		{ID: "00000002", Class: model.Changed, Check: "health/title", Title: "Page title changed", Pages: []string{"/c/"}},
	}}
	if _, err := store.Accept(f.snap, f.run, c, store.Selector{Page: "/c/"}, "dana"); err != nil {
		t.Fatal(err)
	}
	snap, _ := store.LoadRun(f.snap)
	if p := snap.Page("/c/"); p.Metrics.Load != 2500 || p.Title != "Renamed" {
		t.Errorf("/c/ in the snapshot: load %v, title %q", p.Metrics.Load, p.Title)
	}
	if p := snap.Page("/b/"); p.Title != "Page /b/" {
		t.Errorf("/b/ changed: %+v", p)
	}
}

func TestAcceptSelectors(t *testing.T) {
	f := newFixture(t)
	c := f.compareAgain(t)
	for _, tc := range []struct {
		sel  store.Selector
		want string
	}{
		{store.Selector{}, "say which changes to accept"},
		{store.Selector{IDs: []string{"deadbeef"}}, `no change "deadbeef" in this comparison`},
		{store.Selector{IDs: []string{c.Changes[0].ID}, Page: "/a/"}, "by id or by page and check, not both"},
		{store.Selector{Page: "/nowhere/*"}, `no change matches page "/nowhere/*"`},
		{store.Selector{Page: "/a/", Check: "speed/*"}, `no change matches page "/a/" and check "speed/*"`},
	} {
		_, err := store.Accept(f.snap, f.run, c, tc.sel, "dana")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Accept(%+v) = %v, want %q", tc.sel, err, tc.want)
		}
	}
	if info, _ := store.LoadSnapshotInfo(f.snap); len(info.Accepted) != 0 {
		t.Errorf("a refused accept recorded %+v", info.Accepted)
	}
}

func TestAcceptRefusesBrokenRunScreenshot(t *testing.T) {
	f := newFixture(t)
	c := f.compareAgain(t)
	if err := os.WriteFile(filepath.Join(f.run, "shots", model.PageID("/a/")+"-desktop.png"), []byte("not a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := store.Accept(f.snap, f.run, c, store.Selector{Page: "/a/", Check: "visual/*"}, "dana")
	if err == nil || !strings.Contains(err.Error(), "isn't a PNG") {
		t.Fatalf("err = %v", err)
	}
	snap, _ := store.LoadRun(f.snap)
	if snap.Page("/a/").Shots["desktop"].File != "shots/"+model.PageID("/a/")+"-desktop.png" {
		t.Errorf("a refused accept changed the snapshot")
	}
	entries, _ := os.ReadDir(filepath.Join(f.snap, "shots"))
	if len(entries) != 3 {
		t.Errorf("a refused accept left files: %d in shots/", len(entries))
	}
}

func TestAcceptSiteIssue(t *testing.T) {
	f := newFixture(t)
	link := model.Issue{Check: "health/broken-link", Level: model.Warning, Title: "Broken link to /old (404)", Pages: []string{"/a/"}}
	link.Identify()
	run, _ := store.LoadRun(f.run)
	run.Issues = []model.Issue{link}
	if err := store.SaveRun(f.run, run); err != nil {
		t.Fatal(err)
	}
	c := f.compareAgain(t)
	i := slices.IndexFunc(c.Changes, func(ch model.Change) bool { return ch.Check == "health/broken-link" })
	if i < 0 {
		t.Fatalf("no broken link change: %+v", c.Changes)
	}
	if _, err := store.Accept(f.snap, f.run, c, store.Selector{IDs: []string{c.Changes[i].ID}}, "dana"); err != nil {
		t.Fatal(err)
	}
	if again := f.compareAgain(t); slices.ContainsFunc(again.Changes, func(ch model.Change) bool { return ch.Check == "health/broken-link" }) {
		t.Errorf("the accepted broken link is still reported")
	}
}

func TestAcceptRefusesScreenshotLinkedOutsideTheRun(t *testing.T) {
	if runtime.GOOS == "windows" { // symlinks need privileges there
		t.Skip("no symlinks")
	}
	f := newFixture(t)
	c := f.compareAgain(t)
	private := filepath.Join(t.TempDir(), "private.png")
	if err := os.WriteFile(private, screen(image.Rect(0, 0, 10, 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(f.run, "shots", model.PageID("/a/")+"-desktop.png")
	if err := os.Remove(shot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, shot); err != nil {
		t.Fatal(err)
	}
	_, err := store.Accept(f.snap, f.run, c, store.Selector{Page: "/a/", Check: "visual/*"}, "dana")
	if err == nil || !strings.Contains(err.Error(), "is outside the run") {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(f.snap, "shots"))
	if len(entries) != 3 {
		t.Errorf("the linked file was copied: %d in shots/", len(entries))
	}
}

func TestAcceptLocksTheStoreWithATrailingSlash(t *testing.T) {
	f := newFixture(t)
	c := f.compareAgain(t)
	wait := *store.LockWait
	*store.LockWait = 100 * time.Millisecond
	t.Cleanup(func() { *store.LockWait = wait })
	// Another live process holds the store's lock.
	root := filepath.Dir(filepath.Dir(f.snap))
	held := fmt.Sprintf("%d\n%s\n", os.Getppid(), time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(root, store.LockFile), []byte(held), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := store.Accept(f.snap+string(filepath.Separator), f.run, c, store.Selector{Page: "/a/", Check: "visual/*"}, "dana")
	if err == nil || !strings.Contains(err.Error(), "another Soundcheck") {
		t.Errorf("Accept ignored the store's lock: %v", err)
	}
}
