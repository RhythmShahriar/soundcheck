// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package visual

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
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

func writePNG(t *testing.T, dir, name string, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := store.Saver(dir)(name, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// shotPage records a page with a desktop screenshot written to dir.
func shotPage(t *testing.T, dir, path string, img image.Image) model.Page {
	t.Helper()
	p := model.Page{ID: model.PageID(path), URL: "https://shop.example" + path, Path: path, Status: 200, Shots: map[string]model.Shot{}}
	file := "shots/" + p.ID + "-desktop.png"
	writePNG(t, dir, file, img)
	p.Shots["desktop"] = model.Shot{File: file, Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}
	return p
}

// pixelHeavy marks a test that diffs many or large screenshots: under
// -race each takes 5 to 20 seconds. It skips with -short and otherwise
// runs alongside the other heavy tests, so the package's -race run fits
// Go's 10 minute default timeout even with -count=3.
func pixelHeavy(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("pixel-heavy test")
	}
	t.Parallel()
}

func byTitle(cs []model.Change) map[string]model.Change {
	m := map[string]model.Change{}
	for _, c := range cs {
		m[c.Title] = c
	}
	return m
}

func TestCompareGroupsSharedChanges(t *testing.T) {
	pixelHeavy(t)
	beforeDir, afterDir, cmpDir := t.TempDir(), t.TempDir(), t.TempDir()
	paths := []string{"/", "/about/", "/blog/", "/contact/", "/shop/"}
	before, after := &model.Run{}, &model.Run{}
	for i, path := range paths {
		b := page(800, 1200)
		fill(b, image.Rect(100, 300+i*40, 300, 330+i*40), blue) // pages differ from each other
		before.Pages = append(before.Pages, shotPage(t, beforeDir, path, b))

		a := page(800, 1200)
		fill(a, image.Rect(100, 300+i*40, 300, 330+i*40), blue)
		fill(a, image.Rect(0, 0, 800, 60), red) // a new header color everywhere
		switch path {
		case "/about/":
			fill(a, image.Rect(100, 800, 500, 1000), red) // and a real change on one page
		case "/blog/":
			a = grow(a, 340)
		}
		after.Pages = append(after.Pages, shotPage(t, afterDir, path, a))
	}
	after.Pages = append(after.Pages, model.Page{Path: "/new/", Status: 200}) // not in the snapshot

	cfg := config.Default()
	cc := &check.CompareContext{Config: cfg, Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir,
		Dir: cmpDir, Save: store.Saver(cmpDir)}
	all := Module{}.Compare(context.Background(), cc)
	got := byTitle(all)

	header, ok := got["Header changed on 5 pages (desktop)"]
	if !ok {
		t.Fatalf("no shared header change in %v", keys(got))
	}
	if strings.Join(header.Pages, ",") != "/,/about/,/blog/,/contact/,/shop/" || header.Class != model.Changed || header.Check != "visual/shared" {
		t.Errorf("header = %+v", header)
	}
	if header.Score < 4 || header.Score > 6 { // 800×60 of 800×1200 = 5%
		t.Errorf("header score = %v", header.Score)
	}
	about, ok := find(all, "Page looks different (desktop)", "/about/")
	if !ok {
		t.Fatalf("no page change for /about/ in %v", keys(got))
	}
	if about.Score != 8.33 || !strings.Contains(about.Detail, "besides the changes shared") { // the block, 80000 of 960000 px; the header is reported once
		t.Errorf("about score = %v", about.Score)
	}
	for _, k := range []string{"before", "after", "diff"} {
		f := about.Files[k]
		if !strings.HasPrefix(f, "visual/") {
			t.Fatalf("files = %v", about.Files)
		}
		img, err := load(cmpDir, f)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if k == "diff" && img.Bounds().Dy() != 1200 {
			t.Errorf("diff is %v", img.Bounds())
		}
	}
	if header.Files == nil {
		t.Error("shared change has no images")
	}
	// /blog/ grew at the bottom: a block only it has, reported with the
	// page, not as a separate size change.
	blog, ok := find(all, "Page looks different (desktop)", "/blog/")
	if !ok || !strings.Contains(blog.Detail, "A 340 px tall block was inserted 1200 px down") || blog.Score != 28.33 {
		t.Errorf("blog = %+v", blog)
	}
	if len(all) != 3 {
		t.Errorf("changes: %v", all)
	}
	// Only pages that changed get images.
	entries, _ := os.ReadDir(filepath.Join(cmpDir, "visual"))
	if len(entries) != 3*5 {
		t.Errorf("%d files saved", len(entries))
	}
}

// A plugin inserted the same banner at the top of every page. Pages
// differ above and below it, and on one page something else changed
// too: one change for the banner, one for that page.
func TestCompareGroupsInsertedBanner(t *testing.T) {
	pixelHeavy(t)
	beforeDir, afterDir, cmpDir := t.TempDir(), t.TempDir(), t.TempDir()
	before, after := &model.Run{}, &model.Run{}
	for i, path := range []string{"/", "/a/", "/b/", "/c/"} {
		for _, vp := range []string{"desktop", "mobile"} {
			w := map[string]int{"desktop": 800, "mobile": 390}[vp]
			b := page(w, 1200+i*100) // pages differ in length and content
			fill(b, image.Rect(20, 300+i*40, 300, 330+i*40), blue)
			a := insertRows(b, 0, 320, color.NRGBA{255, 200, 100, 255})
			if path == "/b/" && vp == "desktop" {
				fill(a, image.Rect(400, 1000, 700, 1200), red) // below the banner, in shifted content
			}
			addShot(t, before, beforeDir, path, vp, b)
			addShot(t, after, afterDir, path, vp, a)
		}
	}
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir,
		Dir: cmpDir, Save: store.Saver(cmpDir)}
	got := byTitle(Module{}.Compare(context.Background(), cc))
	for _, title := range []string{"New block at the top of 4 pages (desktop)", "New block at the top of 4 pages (mobile)"} {
		c, ok := got[title]
		if !ok {
			t.Fatalf("missing %q in %v", title, keys(got))
		}
		if strings.Join(c.Pages, ",") != "/,/a/,/b/,/c/" || c.Files["diff"] == "" || c.Check != "visual/block" ||
			!strings.HasPrefix(c.Detail, "A 320 px tall block appeared at the top of each page listed") || !strings.Contains(c.Detail, "320 px taller in all") {
			t.Errorf("%s = %+v", title, c)
		}
	}
	p, ok := got["Page looks different (desktop)"]
	if !ok || strings.Join(p.Pages, ",") != "/b/" {
		t.Fatalf("leftover change = %+v (all %v)", p, keys(got))
	}
	if p.Score != 5.36 { // 300×200 of 800×1400 compared
		t.Errorf("leftover score %v: %s", p.Score, p.Detail)
	}
	if len(got) != 3 { // the size change is told with the blocks
		t.Errorf("changes: %v", keys(got))
	}
}

// A plugin adds a banner at the top and a notice above the footer of
// every page: one change per block per viewport, nothing per page. The
// notice wraps onto more lines on mobile, and on some pages.
func TestCompareGroupsTwoBlocks(t *testing.T) {
	pixelHeavy(t)
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	before, after := &model.Run{}, &model.Run{}
	for i, path := range []string{"/", "/a/", "/b/", "/c/"} {
		for _, vp := range []string{"desktop", "mobile"} {
			w, notice := 800, 53
			if vp == "mobile" {
				w, notice = 390, 96
			}
			if i == 3 {
				notice += 8 // a longer page title wraps the notice once more
			}
			h := 1500 + i*200
			b := page(w, h)
			fill(b, image.Rect(20, 300+i*40, 300, 330+i*40), blue)
			a := insertRows(b, 0, 320, color.NRGBA{255, 200, 100, 255})
			a = insertRows(a, 320+h-80, notice, color.NRGBA{250, 250, 200, 255}) // above the footer
			addShot(t, before, beforeDir, path, vp, b)
			addShot(t, after, afterDir, path, vp, a)
		}
	}
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir}
	cs := Module{}.Compare(context.Background(), cc)
	got := byTitle(cs)
	for _, title := range []string{
		"New block at the top of 4 pages (desktop)", "New block at the top of 4 pages (mobile)",
		"New block near the bottom of 4 pages (desktop)", "New block near the bottom of 4 pages (mobile)",
	} {
		if c, ok := got[title]; !ok || len(c.Pages) != 4 {
			t.Errorf("%s: %+v", title, c)
		}
	}
	if d := got["New block near the bottom of 4 pages (mobile)"].Detail; !strings.HasPrefix(d, "A 96–104 px tall block appeared near the bottom") ||
		!strings.Contains(d, "416–424 px taller in all") {
		t.Errorf("mobile notice: %s", d)
	}
	if len(cs) != 4 {
		t.Errorf("changes: %v", keys(got))
	}
}

// A block removed from the middle of two pages is one change.
func TestCompareGroupsRemovedBlock(t *testing.T) {
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	before, after := &model.Run{}, &model.Run{}
	for i, path := range []string{"/x/", "/y/"} {
		b := page(800, 1500)
		fill(b, image.Rect(20, 300+i*30, 300, 320+i*30), blue)
		b = insertRows(b, 700, 200, red)
		a := removeRows(b, 700, 200)
		addShot(t, before, beforeDir, path, "desktop", b)
		addShot(t, after, afterDir, path, "desktop", a)
	}
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir}
	got := byTitle(Module{}.Compare(context.Background(), cc))
	if _, ok := got["Block removed 700 px down on 2 pages (desktop)"]; !ok || len(got) != 1 {
		t.Errorf("changes: %v", keys(got))
	}
}

// More than five pages that each changed differently are one change.
func TestCompareCollapsesManyPages(t *testing.T) {
	pixelHeavy(t)
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	before, after := &model.Run{}, &model.Run{}
	for i := range 7 {
		path := fmt.Sprintf("/p%d/", i)
		a := page(800, 1200)
		fill(a, image.Rect(500, 100+i*20, 700, 140+i*30+i*i*10), red) // a different change on each page
		addShot(t, before, beforeDir, path, "desktop", page(800, 1200))
		addShot(t, after, afterDir, path, "desktop", a)
	}
	cmpDir := t.TempDir()
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir,
		Dir: cmpDir, Save: store.Saver(cmpDir)}
	cs := Module{}.Compare(context.Background(), cc)
	if len(cs) != 1 {
		t.Fatalf("changes: %v", keys(byTitle(cs)))
	}
	c := cs[0]
	if c.Title != "7 pages look different (desktop)" || len(c.Pages) != 7 || c.Pages[0] != "/p0/" {
		t.Errorf("collapsed = %+v", c)
	}
	// Scores 0.83, 1.25, 2.08, 3.33, 5, 7.08 and 9.58% (/p6/: 200×460 px).
	if c.Score != 9.58 || c.Files["after"] != "visual/"+model.PageID("/p6/")+"-desktop-after.png" ||
		!strings.Contains(c.Detail, "at most 9.58% (/p6/, shown)") || !strings.HasPrefix(c.Detail, "Median 3.33%") {
		t.Errorf("collapsed = %+v", c)
	}

	// Five stay separate.
	before.Pages, after.Pages = before.Pages[:5], after.Pages[:5]
	if cs := (Module{}).Compare(context.Background(), cc); len(cs) != 5 {
		t.Errorf("%d changes for 5 pages", len(cs))
	}
}

func addShot(t *testing.T, r *model.Run, dir, path, vp string, img image.Image) {
	t.Helper()
	p := r.Page(path)
	if p == nil {
		r.Pages = append(r.Pages, model.Page{ID: model.PageID(path), Path: path, Status: 200, Shots: map[string]model.Shot{}})
		p = &r.Pages[len(r.Pages)-1]
	}
	file := "shots/" + p.ID + "-" + vp + ".png"
	writePNG(t, dir, file, img)
	p.Shots[vp] = model.Shot{File: file, Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}
}

// insertRows returns img with n rows of c inserted at y.
func insertRows(img *image.NRGBA, y, n int, c color.Color) *image.NRGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := canvas(w, h+n)
	draw.Draw(out, image.Rect(0, 0, w, y), img, image.Point{}, draw.Src)
	fill(out, image.Rect(0, y, w, y+n), c)
	draw.Draw(out, image.Rect(0, y+n, w, h+n), img, image.Pt(0, y), draw.Src)
	return out
}

// removeRows returns img without rows [y, y+n).
func removeRows(img *image.NRGBA, y, n int) *image.NRGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := canvas(w, h-n)
	draw.Draw(out, image.Rect(0, 0, w, y), img, image.Point{}, draw.Src)
	draw.Draw(out, image.Rect(0, y, w, h-n), img, image.Pt(0, y+n), draw.Src)
	return out
}

func TestCompareThreshold(t *testing.T) {
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	a := page(800, 1200)
	fill(a, image.Rect(600, 300, 640, 340), red) // 1600 px, 0.17%
	before := &model.Run{Pages: []model.Page{shotPage(t, beforeDir, "/", page(800, 1200))}}
	after := &model.Run{Pages: []model.Page{shotPage(t, afterDir, "/", a)}}
	cfg := config.Default()
	cc := &check.CompareContext{Config: cfg, Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir}
	if got := (Module{}).Compare(context.Background(), cc); len(got) != 0 {
		t.Errorf("under the threshold: %+v", got)
	}
	cc.Config.Visual.Threshold = 0.1
	got := (Module{}).Compare(context.Background(), cc)
	if len(got) != 1 || got[0].Score != 0.17 || got[0].Files != nil { // no Save: no files
		t.Errorf("over the threshold: %+v", got)
	}
	// A failing page is health's business.
	after.Pages[0].Status = 500
	if got := (Module{}).Compare(context.Background(), cc); len(got) != 0 {
		t.Errorf("error page compared: %+v", got)
	}
}

func TestCompareUsesMasksAndDynamic(t *testing.T) {
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	a := page(800, 1200)
	fill(a, image.Rect(100, 300, 400, 400), red)  // a rotating banner, learned
	fill(a, image.Rect(500, 800, 700, 900), blue) // a chat widget, ignored by selector
	bp := shotPage(t, beforeDir, "/", page(800, 1200))
	s := bp.Shots["desktop"]
	s.Dynamic = []model.Rect{{X: 94, Y: 294, W: 312, H: 112}}
	bp.Shots["desktop"] = s
	ap := shotPage(t, afterDir, "/", a)
	s = ap.Shots["desktop"]
	s.Masked = []model.Rect{{X: 500, Y: 800, W: 200, H: 100}}
	ap.Shots["desktop"] = s
	cfg := config.Default()
	cfg.Visual.Threshold = 0
	cc := &check.CompareContext{Config: cfg, Before: &model.Run{Pages: []model.Page{bp}}, After: &model.Run{Pages: []model.Page{ap}},
		BeforeDir: beforeDir, AfterDir: afterDir}
	if got := (Module{}).Compare(context.Background(), cc); len(got) != 0 {
		t.Errorf("ignored regions reported: %+v", got)
	}
}

func TestLearnDynamic(t *testing.T) {
	dir := t.TempDir()
	first, second := page(800, 1200), page(800, 1200)
	fill(first, image.Rect(100, 200, 700, 400), red)
	fill(second, image.Rect(100, 200, 700, 400), blue)
	p := shotPage(t, dir, "/", first)
	s := p.Shots["desktop"]
	s.Second = "shots/" + p.ID + "-desktop.2.png"
	writePNG(t, dir, s.Second, second)
	p.Shots["desktop"] = s

	cc := &check.Context{Run: &model.Run{}, Dir: dir}
	Module{}.CheckPage(cc, &p)
	if len(p.Shots["desktop"].Dynamic) != 0 {
		t.Fatal("learned outside a snapshot")
	}
	cc.Snapshot = true
	Module{}.CheckPage(cc, &p)
	if d := p.Shots["desktop"].Dynamic; len(d) != 1 || d[0] != (model.Rect{X: 94, Y: 194, W: 612, H: 212}) {
		t.Errorf("dynamic = %+v", d)
	}
	if len(cc.Run.Warnings) != 0 {
		t.Errorf("warnings: %v", cc.Run.Warnings)
	}

	// A page that's mostly moving gets a warning.
	fill(second, image.Rect(0, 0, 800, 1200), color.NRGBA{9, 9, 9, 255})
	writePNG(t, dir, s.Second, second)
	Module{}.CheckPage(cc, &p)
	if len(cc.Run.Warnings) != 1 || !strings.Contains(cc.Run.Warnings[0], "changes on its own") {
		t.Errorf("warnings: %v", cc.Run.Warnings)
	}
}

// grow returns img with extra rows of white at the bottom.
func grow(img *image.NRGBA, rows int) *image.NRGBA {
	b := img.Bounds()
	out := canvas(b.Dx(), b.Dy()+rows)
	copy(out.Pix, img.Pix)
	return out
}

func find(cs []model.Change, title, page string) (model.Change, bool) {
	for _, c := range cs {
		if c.Title == title && slices.Contains(c.Pages, page) {
			return c, true
		}
	}
	return model.Change{}, false
}

func keys(m map[string]model.Change) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// shortPage is a page shorter than the window, screenshotted window-tall:
// a header, some text, a gray footer, then white filler.
func shortPage(content int, footerExtra func(*image.NRGBA, int)) *image.NRGBA {
	img := canvas(800, 900)
	fill(img, image.Rect(0, 0, 800, 60), ink)
	text(img, 40, 100, (content-200)/18, 30)
	footer := content - 100
	fill(img, image.Rect(0, footer, 800, content), color.NRGBA{236, 236, 236, 255})
	text(img, 40, float64(footer+40), 1, 20)
	if footerExtra != nil {
		footerExtra(img, footer)
	}
	return img
}

// Pages shorter than the window: a banner at the top and a warning at
// the top of the footer push content into the filler. Each block is one
// change, however long each page is, and the filler isn't a change.
func TestCompareShortPages(t *testing.T) {
	pixelHeavy(t)
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	before, after := &model.Run{}, &model.Run{}
	gray := color.NRGBA{236, 236, 236, 255}
	for i, path := range []string{"/", "/a/", "/b/", "/c/"} {
		content := 420 + i*120 // the last page grows past the window
		b := shortPage(content, nil)
		warned := insertRows(b, content-100, 48, gray) // the warning, inside the footer
		text(warned, 40, float64(content-100+10), 1, 25)
		a := windowTall(insertRows(warned, 0, 48, color.NRGBA{255, 124, 82, 255}), content+96) // and the banner
		addShot(t, before, beforeDir, path, "desktop", b)
		addShot(t, after, afterDir, path, "desktop", a)
	}
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir}
	cs := Module{}.Compare(context.Background(), cc)
	got := byTitle(cs)
	for _, title := range []string{"New block at the top of 4 pages (desktop)", "New block near the bottom of 4 pages (desktop)"} {
		if c, ok := got[title]; !ok || len(c.Pages) != 4 {
			t.Errorf("%s: %+v", title, c)
		}
	}
	if len(cs) != 2 {
		for _, c := range cs {
			t.Logf("%s %v %s", c.Title, c.Pages, c.Detail)
		}
		t.Errorf("%d changes, want 2", len(cs))
	}
}

// windowTall crops or pads (with white filler) img to what a capture of
// a page with this much content gives: at least the 900 px window.
func windowTall(img *image.NRGBA, content int) *image.NRGBA {
	h := max(content, 900)
	out := canvas(800, h)
	draw.Draw(out, image.Rect(0, 0, 800, min(content, img.Bounds().Dy())), img, image.Point{}, draw.Src)
	return out
}

// The same footer change sits lower on longer pages; it's one change.
// A second, different footer change on other pages shares its line, and
// the height the footers gained is told with it, not on its own.
func TestCompareFooterAtDifferentHeights(t *testing.T) {
	pixelHeavy(t)
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	before, after := &model.Run{}, &model.Run{}
	for i, path := range []string{"/1/", "/2/", "/3/", "/4/", "/5/"} {
		h := 1500 + i*137
		b := page(800, h)
		text(b, 40, 600, 10+i*3, 30)
		// The footer is redrawn 25 px taller, gray instead of dark…
		a := canvas(800, h+25)
		draw.Draw(a, image.Rect(0, 0, 800, h-80), b, image.Point{}, draw.Src)
		fill(a, image.Rect(0, h-80, 800, h+25), color.NRGBA{236, 236, 236, 255})
		if i < 3 {
			text(a, 40, float64(h-60), 2, 20) // …with a notice
		} else {
			fill(a, image.Rect(300, h-60, 500, h-20), red) // …or a badge
		}
		addShot(t, before, beforeDir, path, "desktop", b)
		addShot(t, after, afterDir, path, "desktop", a)
	}
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir}
	cs := Module{}.Compare(context.Background(), cc)
	if len(cs) != 1 {
		for _, c := range cs {
			t.Logf("%s %v %s", c.Title, c.Pages, c.Detail)
		}
		t.Fatalf("%d changes, want 1", len(cs))
	}
	c := cs[0]
	if c.Title != "Footer changed on 5 pages (desktop)" || !strings.Contains(c.Detail, "2 different changes") ||
		!strings.Contains(c.Detail, "The pages are 25 px taller.") {
		t.Errorf("footer = %+v", c)
	}
}

// twoViewportRuns is a snapshot and a run of two pages, each with a
// desktop and a mobile screenshot, where the after pages changed.
func twoViewportRuns(t *testing.T) (*check.CompareContext, string) {
	t.Helper()
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	before, after := &model.Run{}, &model.Run{}
	for _, path := range []string{"/", "/about/"} {
		for _, vp := range Viewports {
			addShot(t, before, beforeDir, path, vp, page(400, 600))
			a := page(400, 600)
			fill(a, image.Rect(50, 200, 350, 400), red)
			addShot(t, after, afterDir, path, vp, a)
		}
	}
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir}
	return cc, beforeDir
}

func TestCompareWarnsAboutUnreadableShots(t *testing.T) {
	pixelHeavy(t)
	const four = "visual: 4 screenshots from the snapshot are missing or unreadable; visual changes on these pages weren't checked"

	t.Run("intact", func(t *testing.T) {
		cc, _ := twoViewportRuns(t)
		cs, warnings := compareShots(context.Background(), cc)
		if len(warnings) != 0 || len(cs) == 0 {
			t.Errorf("an intact snapshot: changes %d, warnings %q", len(cs), warnings)
		}
	})
	t.Run("shots deleted", func(t *testing.T) {
		cc, dir := twoViewportRuns(t)
		if err := os.RemoveAll(filepath.Join(dir, "shots")); err != nil {
			t.Fatal(err)
		}
		cs, warnings := compareShots(context.Background(), cc)
		if len(cs) != 0 || !slices.Equal(warnings, []string{four}) {
			t.Errorf("changes %+v, warnings %q", cs, warnings)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		cc, dir := twoViewportRuns(t)
		f := filepath.Join(dir, filepath.FromSlash(cc.Before.Pages[0].Shots["desktop"].File))
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, raw[:len(raw)/2], 0o600); err != nil {
			t.Fatal(err)
		}
		_, warnings := compareShots(context.Background(), cc)
		want := "visual: 1 screenshot from the snapshot is missing or unreadable; visual changes on this page weren't checked"
		if !slices.Equal(warnings, []string{want}) {
			t.Errorf("warnings %q", warnings)
		}
	})
	t.Run("not a png", func(t *testing.T) {
		cc, dir := twoViewportRuns(t)
		f := filepath.Join(dir, filepath.FromSlash(cc.Before.Pages[1].Shots["mobile"].File))
		lfs := "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 1234\n"
		if err := os.WriteFile(f, []byte(lfs), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, warnings := compareShots(context.Background(), cc); len(warnings) != 1 || !strings.Contains(warnings[0], "1 screenshot from the snapshot") {
			t.Errorf("warnings %q", warnings)
		}
	})
	t.Run("paths outside the run folder", func(t *testing.T) {
		cc, dir := twoViewportRuns(t)
		// A real PNG sits outside the snapshot's folder; run.json points
		// at it with an absolute path and with ../ paths.
		outside := filepath.Join(t.TempDir(), "private.png")
		writePNG(t, filepath.Dir(outside), "private.png", page(400, 600))
		rel, err := filepath.Rel(dir, outside)
		if err != nil {
			t.Fatal(err)
		}
		set := func(i int, vp, file string) {
			s := cc.Before.Pages[i].Shots[vp]
			s.File = file
			cc.Before.Pages[i].Shots[vp] = s
		}
		set(0, "desktop", outside)
		set(0, "mobile", filepath.ToSlash(rel))
		set(1, "desktop", "shots/../../"+filepath.Base(dir)+"/shots/"+cc.Before.Pages[1].ID+"-desktop.png")
		set(1, "mobile", "../x.png")
		cs, warnings := compareShots(context.Background(), cc)
		if len(cs) != 0 || !slices.Equal(warnings, []string{four}) {
			t.Errorf("changes %+v, warnings %q", cs, warnings)
		}
	})
	t.Run("this run's shot", func(t *testing.T) {
		cc, _ := twoViewportRuns(t)
		if err := os.RemoveAll(filepath.Join(cc.AfterDir, "shots")); err != nil {
			t.Fatal(err)
		}
		_, warnings := compareShots(context.Background(), cc)
		want := "visual: 4 screenshots from this run are missing or unreadable; visual changes on these pages weren't checked"
		if !slices.Equal(warnings, []string{want}) {
			t.Errorf("warnings %q", warnings)
		}
	})
}

func TestShotPath(t *testing.T) {
	for file, ok := range map[string]bool{
		"shots/a-desktop.png": true,
		"/etc/passwd":         false,
		"../shots/a.png":      false,
		"shots/../../a.png":   false,
		"":                    false,
	} {
		if _, err := shotPath("/run", file); (err == nil) != ok {
			t.Errorf("shotPath(%q) error = %v", file, err)
		}
	}
}

// notice inserts a 40 px notice above the footer of a page h px tall,
// and with split also a 16 px piece 2 px below it, the way row alignment
// can cut one block around a couple of rows that happen to match.
func notice(b *image.NRGBA, h int, split bool) *image.NRGBA {
	a := b
	if split {
		a = insertRows(a, h-78, 16, color.NRGBA{240, 240, 190, 255})
	}
	return insertRows(a, h-80, 40, color.NRGBA{250, 250, 200, 255})
}

// The compare from users.md N5: a notice near the bottom of three pages,
// and on /shop/ also a block the alignment cut in two pieces 2 px apart.
func TestCompareOneLinePerCause(t *testing.T) {
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	before, after := &model.Run{}, &model.Run{}
	for i, path := range []string{"/", "/contact/", "/shop/"} {
		h := 1200 + i*100
		b := page(390, h)
		fill(b, image.Rect(20, 300+i*40, 300, 330+i*40), blue)
		a := notice(b, h, path == "/shop/")
		if path == "/shop/" {
			a = insertRows(insertRows(a, 649, 16, red), 647, 16, blue)
		}
		addShot(t, before, beforeDir, path, "mobile", b)
		addShot(t, after, afterDir, path, "mobile", a)
	}
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir}
	got := byTitle(Module{}.Compare(context.Background(), cc))
	if c, ok := got["New block near the bottom of 3 pages (mobile)"]; !ok || strings.Join(c.Pages, ",") != "/,/contact/,/shop/" {
		t.Fatalf("grouped notice = %+v (all %v)", c, keys(got))
	}
	p, ok := got["Page looks different (mobile)"]
	if !ok || strings.Join(p.Pages, ",") != "/shop/" {
		t.Fatalf("shop's own change = %+v (all %v)", p, keys(got))
	}
	// The two pieces are one block, and the notice's piece is told with
	// the grouped notice, not again here.
	if n := strings.Count(p.Detail, "block was inserted"); n != 1 || !strings.Contains(p.Detail, "A 32 px tall block was inserted 647 px down") {
		t.Errorf("detail = %q", p.Detail)
	}
	if len(got) != 2 {
		t.Errorf("changes: %v", keys(got))
	}
}

// A page whose only leftover is a piece of a grouped block has no change
// of its own.
func TestComparePieceOfGroupedBlock(t *testing.T) {
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	before, after := &model.Run{}, &model.Run{}
	for i, path := range []string{"/", "/contact/", "/shop/"} {
		h := 1200 + i*100
		b := page(390, h)
		fill(b, image.Rect(20, 300+i*40, 300, 330+i*40), blue)
		addShot(t, before, beforeDir, path, "mobile", b)
		addShot(t, after, afterDir, path, "mobile", notice(b, h, path == "/shop/"))
	}
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir}
	got := byTitle(Module{}.Compare(context.Background(), cc))
	if _, ok := got["New block near the bottom of 3 pages (mobile)"]; !ok || len(got) != 1 {
		t.Errorf("changes: %v", keys(got))
	}
}

// Two blocks far apart on one page are two causes and stay two.
func TestCompareDistinctBlocksStaySeparate(t *testing.T) {
	beforeDir, afterDir := t.TempDir(), t.TempDir()
	before, after := &model.Run{}, &model.Run{}
	b := page(800, 1500)
	a := insertRows(insertRows(b, 900, 30, red), 300, 40, blue)
	addShot(t, before, beforeDir, "/", "desktop", b)
	addShot(t, after, afterDir, "/", "desktop", a)
	cc := &check.CompareContext{Config: config.Default(), Before: before, After: after, BeforeDir: beforeDir, AfterDir: afterDir}
	got := byTitle(Module{}.Compare(context.Background(), cc))
	p := got["Page looks different (desktop)"]
	if !strings.Contains(p.Detail, "A 40 px tall block was inserted 300 px down") || !strings.Contains(p.Detail, "A 30 px tall block was inserted 900 px down") {
		t.Errorf("detail = %q (all %v)", p.Detail, keys(got))
	}
}

func TestLooseBlocks(t *testing.T) {
	blocks := []Band{
		{Y: 147, H: 16, At: 147}, {Y: 149, H: 16, At: 165}, // pieces of one block
		{Y: 600, H: -20, At: 632}, {Y: 625, H: -10, At: 632}, // two removed blocks 5 px apart
		{Y: 900, H: 30, At: 902}, // grouped elsewhere
		{Y: 900, H: 8, At: 934},  // its piece
		{Y: 1400, H: 12, At: 1442},
	}
	got := looseBlocks(blocks, map[int]bool{4: true})
	want := []Band{{Y: 147, H: 32, At: 147}, {Y: 600, H: -30, At: 632}, {Y: 1400, H: 12, At: 1442}}
	if !slices.Equal(got, want) {
		t.Errorf("looseBlocks = %+v, want %+v", got, want)
	}
	// An inserted and a removed block side by side aren't one block.
	if got := looseBlocks([]Band{{Y: 100, H: 20, At: 100}, {Y: 100, H: -20, At: 120}}, nil); len(got) != 2 {
		t.Errorf("mixed = %+v", got)
	}
}
