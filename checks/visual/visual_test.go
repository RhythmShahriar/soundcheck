package visual

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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
	got := byTitle(Module{}.Compare(context.Background(), cc))

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
	about, ok := got["Page looks different (desktop)"]
	if !ok || len(about.Pages) != 1 || about.Pages[0] != "/about/" {
		t.Fatalf("page change = %+v (all %v)", about, keys(got))
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
	taller, ok := got["Page got 340 px taller (desktop)"]
	if !ok || strings.Join(taller.Pages, ",") != "/blog/" || taller.Before != "800×1200 px" || taller.After != "800×1540 px" {
		t.Errorf("size change = %+v (all %v)", taller, keys(got))
	}
	if len(got) != 3 {
		t.Errorf("changes: %v", keys(got))
	}
	// Only pages that changed get images.
	entries, _ := os.ReadDir(filepath.Join(cmpDir, "visual"))
	if len(entries) != 3*5 {
		t.Errorf("%d files saved", len(entries))
	}
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

func keys(m map[string]model.Change) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
