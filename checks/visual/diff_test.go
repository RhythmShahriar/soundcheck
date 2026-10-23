package visual

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

var (
	white = color.NRGBA{255, 255, 255, 255}
	ink   = color.NRGBA{20, 20, 30, 255}
	blue  = color.NRGBA{30, 80, 200, 255}
	red   = color.NRGBA{200, 40, 40, 255}
)

func canvas(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(white), image.Point{}, draw.Src)
	return img
}

func fill(img draw.Image, r image.Rectangle, c color.Color) {
	draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src)
}

// text draws rows of anti-aliased "glyphs" (rings and slanted strokes,
// coverage from 4×4 supersampling) starting at (ox, oy), the way a
// browser renders type: soft gray edges around dark strokes.
func text(img *image.NRGBA, ox, oy float64, lines, cols int) { glyphs(img, ox, oy, lines, cols, 1) }

// glyphs draws text whose edge coverage is raised to gamma, as different
// font rasterizers and gamma settings do.
func glyphs(img *image.NRGBA, ox, oy float64, lines, cols int, gamma float64) {
	shapes := []func(x, y float64) bool{
		func(x, y float64) bool { d := math.Hypot(x-5, y-6); return d > 3 && d < 4.6 },       // o
		func(x, y float64) bool { return math.Abs((x-2)-(y*0.45)) < 0.8 && y > 0 && y < 12 }, // /
		func(x, y float64) bool { return x > 3 && x < 4.6 && y > 0 && y < 12 },               // l
	}
	for l := range lines {
		for c := range cols {
			shape := shapes[(l+c)%len(shapes)]
			gx, gy := ox+float64(c*11), oy+float64(l*18)
			for py := int(gy) - 1; py < int(gy)+14; py++ {
				for px := int(gx) - 1; px < int(gx)+12; px++ {
					cov := 0
					for sy := range 4 {
						for sx := range 4 {
							if shape(float64(px)+(float64(sx)+0.5)/4-gx, float64(py)+(float64(sy)+0.5)/4-gy) {
								cov++
							}
						}
					}
					if cov > 0 {
						f := math.Pow(float64(cov)/16, gamma)
						v := uint8(255 - f*(255-20))
						img.SetNRGBA(px, py, color.NRGBA{v, v, uint8(255 - f*(255-30)), 255})
					}
				}
			}
		}
	}
}

func page(w, h int) *image.NRGBA {
	img := canvas(w, h)
	fill(img, image.Rect(0, 0, w, 60), ink) // header
	text(img, 40, 100, 20, 40)
	fill(img, image.Rect(40, 500, 600, 700), blue)
	fill(img, image.Rect(0, h-80, w, h), ink) // footer
	return img
}

func TestIdentical(t *testing.T) {
	d := Compare(page(800, 1200), page(800, 1200), DiffOptions{})
	if d.Changed != 0 || d.Percent != 0 || len(d.Regions) != 0 || d.Compared != 800*1200 {
		t.Fatalf("identical images: %+v", d)
	}
}

func TestBlockChange(t *testing.T) {
	a := page(800, 1200)
	fill(a, image.Rect(100, 800, 300, 900), red)
	d := Compare(page(800, 1200), a, DiffOptions{})
	if d.Changed != 200*100 {
		t.Errorf("changed = %d, want %d", d.Changed, 200*100)
	}
	if len(d.Regions) != 1 || d.Regions[0].Rect != (model.Rect{X: 100, Y: 800, W: 200, H: 100}) || d.Regions[0].Pixels != 20000 {
		t.Errorf("regions = %+v", d.Regions)
	}
	if want := 20000.0 * 100 / (800 * 1200); math.Abs(d.Percent-want) > 1e-9 {
		t.Errorf("percent = %v, want %v", d.Percent, want)
	}
	img := d.Image()
	if img.NRGBAAt(150, 850) != changeColor {
		t.Errorf("changed pixel drawn as %v", img.NRGBAAt(150, 850))
	}
	if c := img.NRGBAAt(5, 5); c.R < 200 || c.R != c.G { // the dark header, faded to light gray
		t.Errorf("unchanged pixel drawn as %v", c)
	}
}

func TestSeparateRegions(t *testing.T) {
	a := page(800, 1200)
	fill(a, image.Rect(10, 300, 60, 320), red)
	fill(a, image.Rect(500, 900, 700, 1000), red)
	// Two changes a few pixels apart are one region.
	fill(a, image.Rect(100, 1050, 140, 1060), red)
	fill(a, image.Rect(100, 1066, 140, 1076), red)
	d := Compare(page(800, 1200), a, DiffOptions{})
	if len(d.Regions) != 3 {
		t.Fatalf("regions = %+v", d.Regions)
	}
	if d.Regions[0].Rect != (model.Rect{X: 500, Y: 900, W: 200, H: 100}) {
		t.Errorf("largest region first: %+v", d.Regions[0])
	}
	want := map[model.Rect]bool{{X: 10, Y: 300, W: 50, H: 20}: true, {X: 100, Y: 1050, W: 40, H: 26}: true}
	for _, r := range d.Regions[1:] {
		if !want[r.Rect] {
			t.Errorf("unexpected region %+v", r)
		}
	}
}

// Text rendered with a different rasterizer gamma differs only in its
// anti-aliased edges, which aren't changes.
func TestAntialiasedEdges(t *testing.T) {
	b, a := canvas(600, 500), canvas(600, 500)
	glyphs(b, 20, 20, 20, 40, 1)
	glyphs(a, 20, 20, 20, 40, 1.6)
	d := Compare(b, a, DiffOptions{})
	raw := Compare(b, a, DiffOptions{CountAA: true})
	if raw.Changed < 1000 {
		t.Fatalf("the gamma change should touch many pixels, got %d", raw.Changed)
	}
	if d.Changed > raw.Changed/50 {
		t.Errorf("anti-aliasing counted: %d of %d differing pixels", d.Changed, raw.Changed)
	}
}

// Text or an icon that lands a pixel to the side is the same content
// nudged, not a change.
func TestShiftedOnePixel(t *testing.T) {
	for _, off := range [][2]float64{{1, 0}, {0, 1}, {-1, 1}} {
		b, a := canvas(600, 500), canvas(600, 500)
		text(b, 20, 20, 20, 40)
		text(a, 20+off[0], 20+off[1], 20, 40)
		fill(b, image.Rect(400, 420, 500, 470), blue)
		fill(a, image.Rect(400+int(off[0]), 420+int(off[1]), 500+int(off[0]), 470+int(off[1])), blue)
		d := Compare(b, a, DiffOptions{})
		raw := Compare(b, a, DiffOptions{CountAA: true})
		if raw.Changed < 1000 || d.Changed > 0 {
			t.Errorf("shift %v: %d of %d differing pixels counted", off, d.Changed, raw.Changed)
		}
	}
	// A half-pixel shift blends every edge differently; most of it is
	// still recognized as anti-aliasing.
	b, a := canvas(600, 500), canvas(600, 500)
	text(b, 20, 20, 20, 40)
	text(a, 20.5, 20, 20, 40)
	d := Compare(b, a, DiffOptions{})
	raw := Compare(b, a, DiffOptions{CountAA: true})
	if d.Changed*5 > raw.Changed {
		t.Errorf("half-pixel shift: %d of %d differing pixels counted", d.Changed, raw.Changed)
	}
}

// Different text in the same place is a change, even though its strokes
// look like the old ones nudged.
func TestTextChange(t *testing.T) {
	b, a := canvas(600, 500), canvas(600, 500)
	text(b, 20, 20, 20, 40)
	text(a, 20, 20, 20, 40)
	fill(a, image.Rect(20, 200, 460, 236), white)
	text(a, 31, 200, 2, 40) // the same glyphs, each moved one place along
	d := Compare(b, a, DiffOptions{})
	raw := Compare(b, a, DiffOptions{CountAA: true})
	if d.Changed*3 < raw.Changed || len(d.Regions) != 1 {
		t.Errorf("text change: %d of %d differing pixels counted, regions %+v", d.Changed, raw.Changed, d.Regions)
	}
}

func TestThreshold(t *testing.T) {
	b, a := canvas(100, 100), canvas(100, 100)
	fill(a, image.Rect(0, 0, 50, 50), color.NRGBA{250, 250, 250, 255}) // barely off-white
	if d := Compare(b, a, DiffOptions{}); d.Changed != 0 {
		t.Errorf("faint change counted: %d", d.Changed)
	}
	if d := Compare(b, a, DiffOptions{Threshold: 0.01}); d.Changed != 2500 {
		t.Errorf("strict threshold: %d", d.Changed)
	}
}

func TestMasks(t *testing.T) {
	a := page(800, 1200)
	fill(a, image.Rect(100, 800, 300, 900), red)
	mask := []model.Rect{{X: 90, Y: 790, W: 220, H: 120}}
	d := Compare(page(800, 1200), a, DiffOptions{IgnoreBefore: mask, IgnoreAfter: mask})
	if d.Changed != 0 || d.Compared != 800*1200-220*120 {
		t.Errorf("masked change: changed %d compared %d", d.Changed, d.Compared)
	}
	if d.Image().NRGBAAt(150, 850) != maskColor {
		t.Error("masked area not drawn gray")
	}
	// A mask on one side only still hides the pair.
	d = Compare(page(800, 1200), a, DiffOptions{IgnoreAfter: mask})
	if d.Changed != 0 {
		t.Errorf("after-only mask: changed %d", d.Changed)
	}
}

// A banner pushed the page down: the rest of the page is the same, just
// lower, so it isn't a change; the banner rows have no counterpart.
func TestInsertedBanner(t *testing.T) {
	b := page(800, 1200)
	a := canvas(800, 1260)
	draw.Draw(a, image.Rect(0, 0, 800, 60), b, image.Point{}, draw.Src)
	fill(a, image.Rect(0, 60, 800, 120), red) // the banner
	draw.Draw(a, image.Rect(0, 120, 800, 1260), b, image.Pt(0, 60), draw.Src)
	d := Compare(b, a, DiffOptions{})
	if d.Shift != 60 || d.Changed != 0 {
		t.Fatalf("shift %d, changed %d (%.2f%%)", d.Shift, d.Changed, d.Percent)
	}
	if d.Height != 1260 || d.Compared != 800*1200 {
		t.Errorf("size %dx%d compared %d", d.Width, d.Height, d.Compared)
	}
	if got := sizeChanges(b.Bounds().Size(), a.Bounds().Size()); len(got) != 1 || got[0] != "60 px taller" {
		t.Errorf("size changes %v", got)
	}
	img := d.Image()
	if c := img.NRGBAAt(10, 1250); c.R != c.B { // footer, aligned: unchanged gray
		t.Errorf("footer drawn as %v", c)
	}
	if c := img.NRGBAAt(10, 90); c.R == c.B { // inserted rows tinted
		t.Errorf("inserted rows drawn as %v", c)
	}
}

// Content appended at the end: top-aligned matches best.
func TestTallerAtBottom(t *testing.T) {
	b := page(800, 1200)
	a := canvas(800, 1500)
	draw.Draw(a, b.Bounds(), b, image.Point{}, draw.Src)
	fill(a, image.Rect(0, 1200, 800, 1500), blue)
	d := Compare(b, a, DiffOptions{})
	if d.Shift != 0 || d.Changed != 0 || d.Compared != 800*1200 {
		t.Errorf("shift %d changed %d compared %d", d.Shift, d.Changed, d.Compared)
	}
	if got := sizeChanges(image.Pt(800, 1500), image.Pt(780, 1200)); len(got) != 2 || got[0] != "300 px shorter" || got[1] != "20 px narrower" {
		t.Errorf("size changes %v", got)
	}
}

func TestDifferentWidths(t *testing.T) {
	b, a := page(800, 600), page(820, 600)
	d := Compare(b, a, DiffOptions{})
	if d.Width != 800 || d.Height != 600 {
		t.Fatalf("compared %dx%d", d.Width, d.Height)
	}
}

func TestDynamicRegions(t *testing.T) {
	shot := func(slide color.Color, clock int) *image.NRGBA {
		img := page(800, 1200)
		fill(img, image.Rect(100, 200, 700, 400), slide)                        // carousel
		fill(img, image.Rect(700+clock, 20, 720+clock, 40), color.White)        // ticking clock in the header
		fill(img, image.Rect(50, 1000, 150, 1050), color.NRGBA{0, 150, 0, 255}) // static
		return img
	}
	masked := []model.Rect{{X: 0, Y: 1150, W: 800, H: 50}}
	dyn := Dynamic(shot(red, 0), shot(blue, 30), masked)
	want := []model.Rect{{X: 694, Y: 14, W: 62, H: 32}, {X: 94, Y: 194, W: 612, H: 212}}
	if len(dyn) != 2 || dyn[0] != want[0] || dyn[1] != want[1] {
		t.Fatalf("dynamic = %+v, want %+v", dyn, want)
	}
	// Later the carousel shows a third slide and the clock moved again;
	// with the learned regions ignored, nothing changed.
	d := Compare(shot(red, 0), shot(color.NRGBA{200, 200, 0, 255}, 10), DiffOptions{IgnoreBefore: dyn, IgnoreAfter: dyn})
	if d.Changed != 0 {
		t.Errorf("dynamic change counted: %d", d.Changed)
	}
	// A real change elsewhere still counts.
	a := shot(blue, 10)
	fill(a, image.Rect(50, 1000, 150, 1050), red)
	if d := Compare(shot(red, 0), a, DiffOptions{IgnoreBefore: dyn, IgnoreAfter: dyn}); d.Changed != 5000 {
		t.Errorf("real change next to dynamic regions: %d", d.Changed)
	}
	if c := coverage(dyn, 800, 1200); c < 13 || c > 14 {
		t.Errorf("coverage = %.2f", c)
	}
}

func TestRGBAInput(t *testing.T) {
	n := page(300, 300)
	r := image.NewRGBA(n.Bounds())
	draw.Draw(r, r.Bounds(), n, image.Point{}, draw.Src)
	sub := n.SubImage(image.Rect(0, 0, 300, 300))
	if d := Compare(r, sub, DiffOptions{}); d.Changed != 0 {
		t.Errorf("RGBA vs NRGBA: %d", d.Changed)
	}
}

func BenchmarkCompare(b *testing.B) {
	before := page(1366, 5000)
	after := page(1366, 5000)
	text(after, 40, 1200, 30, 60) // a new block of text
	fill(after, image.Rect(0, 0, 1366, 60), blue)
	fill(after, image.Rect(200, 3000, 1100, 3600), red)
	b.ReportAllocs()
	for b.Loop() {
		d := Compare(before, after, DiffOptions{})
		if d.Changed == 0 {
			b.Fatal("no change found")
		}
	}
}

func BenchmarkCompareShifted(b *testing.B) {
	before := page(1366, 5000)
	after := canvas(1366, 5340)
	draw.Draw(after, image.Rect(0, 0, 1366, 400), before, image.Point{}, draw.Src)
	fill(after, image.Rect(0, 400, 1366, 740), red)
	draw.Draw(after, image.Rect(0, 740, 1366, 5340), before, image.Pt(0, 400), draw.Src)
	b.ReportAllocs()
	for b.Loop() {
		if d := Compare(before, after, DiffOptions{}); d.Shift != 340 {
			b.Fatalf("shift %d", d.Shift)
		}
	}
}
