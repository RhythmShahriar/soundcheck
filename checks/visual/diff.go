package visual

import (
	"bytes"
	"hash/maphash"
	"image"
	"image/color"
	"image/draw"
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/RhythmShahriar/soundcheck/model"
)

// DiffOptions tune a pixel comparison.
type DiffOptions struct {
	// Threshold is how different two colors must be to count, from 0
	// (any difference) to 1, in YIQ perceptual distance. 0.1 (the
	// default when zero) ignores compression and gamma noise.
	Threshold float64
	// IgnoreBefore and IgnoreAfter are regions left out of the comparison,
	// each in its own image's coordinates: elements hidden by ignore
	// selectors and dynamic regions (sliders, clocks, ads).
	IgnoreBefore, IgnoreAfter []model.Rect
	// CountAA counts anti-aliased pixels as changed. Off by default, so a
	// glyph that renders a subpixel differently isn't a change.
	CountAA bool
}

// Pixel classes recorded per after-image pixel.
const (
	same     uint8 = iota
	changed        // differs beyond the threshold
	aliased        // differs, but only as anti-aliasing noise
	masked         // inside an ignored region
	inserted       // a row with no counterpart in the before image
)

// Diff is the result of comparing two screenshots. Coordinates are in
// the after image.
type Diff struct {
	Width, Height int     // the after image, clipped to the narrower width
	Changed       int     // pixels that differ
	Compared      int     // pixels that had a counterpart and weren't ignored
	Percent       float64 // Changed / Compared × 100
	Regions       []Region
	// Blocks are runs of rows inserted into or removed from the page,
	// which moved the content below them. They aren't pixel changes.
	Blocks []Band
	// Padding is blank space added at the end of the page (negative when
	// removed): the document grew without content.
	Padding int

	after  *raster
	before *raster
	rows   []int // after row → before row, or -1 when inserted
	class  []uint8
}

// Region is a cluster of changed pixels.
type Region struct {
	model.Rect
	Pixels int
}

// raster is an image as straight-alpha RGBA bytes.
type raster struct {
	pix    []uint8
	stride int
	w, h   int
}

func toRaster(img image.Image) *raster {
	b := img.Bounds()
	switch m := img.(type) {
	case *image.NRGBA:
		if b.Min == (image.Point{}) {
			return &raster{pix: m.Pix, stride: m.Stride, w: b.Dx(), h: b.Dy()}
		}
	case *image.RGBA:
		// Premultiplied equals straight alpha when every pixel is opaque,
		// which is true of screenshots.
		if b.Min == (image.Point{}) && m.Opaque() {
			return &raster{pix: m.Pix, stride: m.Stride, w: b.Dx(), h: b.Dy()}
		}
	}
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
	return &raster{pix: n.Pix, stride: n.Stride, w: b.Dx(), h: b.Dy()}
}

func (r *raster) row(y, w int) []uint8 { return r.pix[y*r.stride : y*r.stride+w*4] }

// Compare diffs before and after in the style of pixelmatch: YIQ color
// distance with a threshold, and anti-aliased pixels ignored. First the
// rows of the two images are lined up like lines in a text diff (see
// matchRows), so content that only moved because a block was inserted or
// removed above it isn't a change; the inserted and removed blocks are
// reported in Blocks, and the pixel diff runs on the aligned rows.
func Compare(before, after image.Image, opts DiffOptions) *Diff {
	b, a := toRaster(before), toRaster(after)
	if opts.Threshold == 0 {
		opts.Threshold = 0.1
	}
	w := min(a.w, b.w)
	ignoreB := maskOf(opts.IgnoreBefore, w, b.h)
	ignoreA := maskOf(opts.IgnoreAfter, w, a.h)
	rows, blocks, padding := alignRows(b, a, w, ignoreB, opts)
	d := compareRows(b, a, w, rows, ignoreB, ignoreA, opts, 0)
	d.Blocks, d.Padding = blocks, padding
	if len(blocks) == 0 {
		return d
	}
	// Moving rows around can also "explain" a change in place (a red box
	// where text was, as text removed and a box inserted). Keep the
	// alignment only when it explains the page with fewer changed pixels
	// than lining up the tops, counting rows without a counterpart as
	// changed on both sides.
	cost := d.Changed
	for _, bl := range blocks {
		cost += max(bl.H, -bl.H) * w
	}
	extra := max(a.h-b.h, b.h-a.h) * w
	if cost <= extra {
		return d // lining up the tops can't do better
	}
	if top := compareRows(b, a, w, topRows(a.h, b.h), ignoreB, ignoreA, opts, cost-extra); top.Changed+extra < cost {
		return top
	}
	return d
}

// alignRows lines up the rows of the two images. Regions ignored in the
// before image (dynamic ones, learned in the snapshot) are blanked in
// the after image too, where they ended up: first assuming they didn't
// move, then, if the first alignment shows content above them moved,
// at their new place.
func alignRows(b, a *raster, w int, ignoreB mask, opts DiffOptions) ([]int, []Band, int) {
	seed := maphash.MakeSeed()
	hb := rowHashes(b, w, ignoreB, seed)
	align := func(extra []model.Rect) ([]int, []Band, int) {
		ignore := maskOf(append(slices.Clone(opts.IgnoreAfter), extra...), w, a.h)
		return plan(b, a, w, matchRows(hb, rowHashes(a, w, ignore, seed)))
	}
	rows, blocks, padding := align(opts.IgnoreBefore)
	if len(opts.IgnoreBefore) == 0 || len(blocks) == 0 {
		return rows, blocks, padding
	}
	// Only rows that occur once are trusted to say how far content moved;
	// a repeated row (blank, a solid band) may be matched anywhere.
	count := make(map[uint64]int, len(hb))
	for _, h := range hb {
		count[h]++
	}
	moved := false
	shifted := make([]model.Rect, len(opts.IgnoreBefore))
	for i, r := range opts.IgnoreBefore {
		off := 0
		for ya, yb := range rows { // the nearest unique matched row above the region
			if yb >= 0 && yb <= r.Y && count[hb[yb]] == 1 {
				off = ya - yb
			}
		}
		shifted[i] = r
		shifted[i].Y += off
		moved = moved || off != 0
	}
	if !moved {
		return rows, blocks, padding
	}
	return align(shifted)
}

// topRows lines rows up from the top.
func topRows(ah, bh int) []int {
	rows := make([]int, ah)
	for y := range rows {
		rows[y] = y
		if y >= bh {
			rows[y] = -1
		}
	}
	return rows
}

// cell is the clustering grid size in pixels.
const cell = 8

// band is how many rows a worker diffs at once; a multiple of cell so no
// grid cell is shared between workers.
const band = cell * 4

// compareRows diffs after rows against the before rows they're mapped
// to. With limit > 0 it gives up once more than limit pixels changed
// (the result then only says Changed > limit).
func compareRows(b, a *raster, w int, rows []int, ignoreB, ignoreA mask, opts DiffOptions, limit int) *Diff {
	d := &Diff{Width: w, Height: a.h, after: a, before: b, rows: rows, class: make([]uint8, w*a.h)}
	maxDelta := 35215 * opts.Threshold * opts.Threshold
	g := newGrid(w, a.h)

	type tally struct{ changed, compared int }
	bands := (a.h + band - 1) / band
	tallies := make([]tally, bands)
	jobs := make(chan int)
	var total atomic.Int64 // changed so far, when there's a limit
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), max(bands, 1)) {
		wg.Go(func() {
			for i := range jobs {
				var t tally
				if limit > 0 && total.Load() > int64(limit) {
					tallies[i] = tally{changed: limit + 1}
					continue
				}
				for ya := i * band; ya < min((i+1)*band, a.h); ya++ {
					cls := d.class[ya*w : (ya+1)*w]
					yb := rows[ya]
					if yb < 0 {
						for x := range cls {
							cls[x] = inserted
						}
						continue
					}
					mb, ma := ignoreB.row(yb), ignoreA.row(ya)
					if mb == nil && ma == nil && bytes.Equal(a.row(ya, w), b.row(yb, w)) {
						t.compared += w
						continue
					}
					ra, rb := a.row(ya, w), b.row(yb, w)
					for x := range w {
						if (mb != nil && mb[x]) || (ma != nil && ma[x]) {
							cls[x] = masked
							continue
						}
						t.compared++
						k := x * 4
						if ra[k] == rb[k] && ra[k+1] == rb[k+1] && ra[k+2] == rb[k+2] && ra[k+3] == rb[k+3] {
							continue
						}
						if math.Abs(delta(rb[k:k+4], ra[k:k+4], false)) <= maxDelta {
							continue
						}
						if !opts.CountAA && (antialiased(b, x, yb, w, a, ya-yb) || antialiased(a, x, ya, w, b, yb-ya) ||
							shifted(b, a, x, yb, ya, w, maxDelta)) {
							cls[x] = aliased
							continue
						}
						cls[x] = changed
						t.changed++
						g.add(x, ya)
					}
				}
				tallies[i] = t
				if limit > 0 {
					total.Add(int64(t.changed))
				}
			}
		})
	}
	for i := range bands {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, t := range tallies {
		d.Changed += t.changed
		d.Compared += t.compared
	}
	if d.Compared > 0 {
		d.Percent = float64(d.Changed) * 100 / float64(d.Compared)
	}
	d.Regions = g.regions()
	return d
}

// delta is the squared YIQ distance between two straight-alpha pixels
// blended over white, signed by which is brighter (pixelmatch's
// colorDelta). With yOnly it returns just the brightness difference.
func delta(p, q []uint8, yOnly bool) float64 {
	r1, g1, b1 := blend(p)
	r2, g2, b2 := blend(q)
	y1, y2 := rgb2y(r1, g1, b1), rgb2y(r2, g2, b2)
	y := y1 - y2
	if yOnly {
		return y
	}
	i := rgb2i(r1, g1, b1) - rgb2i(r2, g2, b2)
	qq := rgb2q(r1, g1, b1) - rgb2q(r2, g2, b2)
	dd := 0.5053*y*y + 0.299*i*i + 0.1957*qq*qq
	if y1 > y2 {
		return -dd
	}
	return dd
}

func blend(p []uint8) (r, g, b float64) {
	r, g, b = float64(p[0]), float64(p[1]), float64(p[2])
	if a := p[3]; a < 255 {
		f := float64(a) / 255
		r, g, b = 255+(r-255)*f, 255+(g-255)*f, 255+(b-255)*f
	}
	return r, g, b
}

func rgb2y(r, g, b float64) float64 { return r*0.29889531 + g*0.58662247 + b*0.11448223 }
func rgb2i(r, g, b float64) float64 { return r*0.59597799 - g*0.27417610 - b*0.32180189 }
func rgb2q(r, g, b float64) float64 { return r*0.21147017 - g*0.52261711 + b*0.31114694 }

// antialiased reports whether the pixel at (x, y) of img looks like an
// anti-aliased edge: it sits between a darker and a brighter neighbor,
// has at most two neighbors of equal brightness, and the darkest or
// brightest neighbor lies inside a flat area in both images. other is
// the second image; oy maps img rows to its rows.
func antialiased(img *raster, x1, y1, w int, other *raster, oy int) bool {
	x0, y0 := max(x1-1, 0), max(y1-1, 0)
	x2, y2 := min(x1+1, w-1), min(y1+1, img.h-1)
	zeroes := 0
	if x1 == x0 || x1 == x2 || y1 == y0 || y1 == y2 {
		zeroes = 1
	}
	var lo, hi float64
	var loX, loY, hiX, hiY int
	p := img.pix[y1*img.stride+x1*4:][:4]
	for x := x0; x <= x2; x++ {
		for y := y0; y <= y2; y++ {
			if x == x1 && y == y1 {
				continue
			}
			dd := delta(p, img.pix[y*img.stride+x*4:][:4], true)
			switch {
			case dd == 0:
				zeroes++
				if zeroes > 2 {
					return false
				}
			case dd < lo:
				lo, loX, loY = dd, x, y
			case dd > hi:
				hi, hiX, hiY = dd, x, y
			}
		}
	}
	if lo == 0 || hi == 0 {
		return false
	}
	return (manySiblings(img, loX, loY, w) && manySiblings(other, loX, loY+oy, w)) ||
		(manySiblings(img, hiX, hiY, w) && manySiblings(other, hiX, hiY+oy, w))
}

// shifted reports whether the 3×3 patch around (x, ya) in a matches the
// patch one pixel away in b: the same pixels, just nudged by a pixel, as
// when text or an icon lands a pixel to the side. pixelmatch's
// anti-aliasing test alone misses this for thin strokes, whose cores have
// no flat neighbors.
func shifted(b, a *raster, x, yb, ya, w int, maxDelta float64) bool {
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if (dx != 0 || dy != 0) && samePatch(b, a, x, yb, ya, dx, dy, w, maxDelta) {
				return true
			}
		}
	}
	return false
}

// samePatch compares a's 3×3 patch at (x, ya) with b's at (x+dx, yb+dy),
// center first so most candidates fail on the first pixel.
func samePatch(b, a *raster, x, yb, ya, dx, dy, w int, maxDelta float64) bool {
	for _, o := range patch {
		xa, ra := x+o[0], ya+o[1]
		xb, rb := xa+dx, yb+o[1]+dy
		if xa < 0 || xa >= w || ra < 0 || ra >= a.h {
			continue
		}
		if xb < 0 || xb >= w || rb < 0 || rb >= b.h {
			return false
		}
		p, q := a.pix[ra*a.stride+xa*4:][:4], b.pix[rb*b.stride+xb*4:][:4]
		if !bytes.Equal(p, q) && math.Abs(delta(q, p, false)) > maxDelta {
			return false
		}
	}
	return true
}

// patch lists 3×3 offsets, center first.
var patch = [9][2]int{{0, 0}, {-1, -1}, {0, -1}, {1, -1}, {-1, 0}, {1, 0}, {-1, 1}, {0, 1}, {1, 1}}

// manySiblings reports whether the pixel has three or more neighbors of
// exactly its color.
func manySiblings(img *raster, x1, y1, w int) bool {
	if y1 < 0 || y1 >= img.h {
		return false
	}
	x0, y0 := max(x1-1, 0), max(y1-1, 0)
	x2, y2 := min(x1+1, w-1), min(y1+1, img.h-1)
	zeroes := 0
	if x1 == x0 || x1 == x2 || y1 == y0 || y1 == y2 {
		zeroes = 1
	}
	p := img.pix[y1*img.stride+x1*4:][:4]
	for x := x0; x <= x2; x++ {
		for y := y0; y <= y2; y++ {
			if x == x1 && y == y1 {
				continue
			}
			if bytes.Equal(p, img.pix[y*img.stride+x*4:][:4]) {
				zeroes++
				if zeroes > 2 {
					return true
				}
			}
		}
	}
	return false
}

// mask marks ignored pixels; rows without any are nil.
type mask [][]bool

func maskOf(rects []model.Rect, w, h int) mask {
	if len(rects) == 0 {
		return nil
	}
	m := make(mask, h)
	for _, r := range rects {
		x0, y0 := max(r.X, 0), max(r.Y, 0)
		x1, y1 := min(r.X+r.W, w), min(r.Y+r.H, h)
		for y := y0; y < y1; y++ {
			if m[y] == nil {
				m[y] = make([]bool, w)
			}
			for x := x0; x < x1; x++ {
				m[y][x] = true
			}
		}
	}
	return m
}

func (m mask) row(y int) []bool {
	if m == nil {
		return nil
	}
	return m[y]
}

// Diff colors: changes in the brand's deeper orange, inserted rows in a
// light tint of it, ignored regions in a cool gray.
var (
	changeColor = color.NRGBA{0xE8, 0x60, 0x3A, 0xFF}
	insertColor = color.NRGBA{0xFF, 0xD9, 0xCC, 0xFF}
	maskColor   = color.NRGBA{0xC8, 0xCD, 0xD6, 0xFF}
)

// Image renders the diff: the after image faded to a light gray, with
// changed pixels in orange, ignored regions in gray and rows that only
// exist in the after image tinted.
func (d *Diff) Image() *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, d.Width, d.Height))
	for y := range d.Height {
		src := d.after.row(y, d.Width)
		dst := out.Pix[y*out.Stride : y*out.Stride+d.Width*4]
		cls := d.class[y*d.Width : (y+1)*d.Width]
		for x, c := range cls {
			k := x * 4
			var px color.NRGBA
			switch c {
			case changed:
				px = changeColor
			case masked:
				px = maskColor
			case inserted:
				px = mix(insertColor, src[k:k+4], 0.15)
			default:
				px = faded(src[k : k+4])
			}
			dst[k], dst[k+1], dst[k+2], dst[k+3] = px.R, px.G, px.B, 0xFF
		}
	}
	return out
}

// faded is the pixel's brightness at 10% over white, as pixelmatch draws
// unchanged pixels.
func faded(p []uint8) color.NRGBA {
	r, g, b := blend(p)
	v := uint8(255 + (rgb2y(r, g, b)-255)*0.1)
	return color.NRGBA{v, v, v, 0xFF}
}

func mix(c color.NRGBA, p []uint8, f float64) color.NRGBA {
	g := faded(p)
	m := func(a, b uint8) uint8 { return uint8(float64(a)*(1-f) + float64(b)*f) }
	return color.NRGBA{m(c.R, g.R), m(c.G, g.G), m(c.B, g.B), 0xFF}
}
