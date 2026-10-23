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

func byTitle(cs []model.Change) map[string]model.Change {
	m := map[string]model.Change{}
	for _, c := range cs {
		m[c.Title] = c
	}
	return m
}

func TestCompareGroupsSharedChanges(t *testing.T) {
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
		img, err := load(filepath.Join(cmpDir, f))
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
