package visual

import (
	"cmp"
	"slices"

	"github.com/RhythmShahriar/soundcheck/model"
)

// gap is how close (in grid cells) changed pixels must be to belong to
// the same region: 3 cells joins the lines of a changed paragraph into
// one box without merging unrelated parts of the page.
const gap = 3

// grid counts changed pixels per cell and keeps each cell's tight bounds,
// so regions hug the changes instead of snapping to the grid.
type grid struct {
	cols, rows int
	cells      []gridCell
}

type gridCell struct {
	n              int
	x0, y0, x1, y1 int32 // inclusive bounds of changed pixels
}

func newGrid(w, h int) *grid {
	cols, rows := (w+cell-1)/cell, (h+cell-1)/cell
	return &grid{cols: cols, rows: rows, cells: make([]gridCell, cols*rows)}
}

// add records a changed pixel. Workers own whole cell rows, so no lock.
func (g *grid) add(x, y int) {
	c := &g.cells[(y/cell)*g.cols+x/cell]
	if c.n == 0 {
		c.x0, c.y0, c.x1, c.y1 = int32(x), int32(y), int32(x), int32(y) //nolint:gosec // image coordinates fit
	} else {
		c.x0, c.y0 = min(c.x0, int32(x)), min(c.y0, int32(y)) //nolint:gosec // image coordinates fit
		c.x1, c.y1 = max(c.x1, int32(x)), max(c.y1, int32(y)) //nolint:gosec // image coordinates fit
	}
	c.n++
}

// regions clusters changed cells that lie within gap cells of each other
// and returns their bounding boxes, biggest change first.
func (g *grid) regions() []Region {
	seen := make([]bool, len(g.cells))
	var out []Region
	var stack []int
	for start, c := range g.cells {
		if c.n == 0 || seen[start] {
			continue
		}
		r := box{x0: c.x0, y0: c.y0, x1: c.x1, y1: c.y1, n: c.n}
		seen[start] = true
		stack = append(stack[:0], start)
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			cx, cy := i%g.cols, i/g.cols
			for y := max(cy-gap, 0); y <= min(cy+gap, g.rows-1); y++ {
				for x := max(cx-gap, 0); x <= min(cx+gap, g.cols-1); x++ {
					j := y*g.cols + x
					if seen[j] || g.cells[j].n == 0 {
						continue
					}
					seen[j] = true
					r.grow(g.cells[j])
					stack = append(stack, j)
				}
			}
		}
		out = append(out, r.region())
	}
	out = mergeOverlapping(out)
	slices.SortFunc(out, func(a, b Region) int {
		return cmp.Or(cmp.Compare(b.Pixels, a.Pixels), cmp.Compare(a.Y, b.Y), cmp.Compare(a.X, b.X))
	})
	return out
}

type box struct {
	x0, y0, x1, y1 int32
	n              int
}

func (b *box) grow(c gridCell) {
	b.x0, b.y0 = min(b.x0, c.x0), min(b.y0, c.y0)
	b.x1, b.y1 = max(b.x1, c.x1), max(b.y1, c.y1)
	b.n += c.n
}

func (b box) region() Region {
	return Region{Rect: model.Rect{X: int(b.x0), Y: int(b.y0), W: int(b.x1-b.x0) + 1, H: int(b.y1-b.y0) + 1}, Pixels: b.n}
}

// mergeOverlapping joins boxes that overlap (clusters shaped like an L
// can produce overlapping boxes). Clusters never share pixels, so their
// counts add up.
func mergeOverlapping(rs []Region) []Region {
	if len(rs) > 500 { // noise everywhere; boxes are already a fair summary
		return rs
	}
	for merged := true; merged; {
		merged = false
		for i := 0; i < len(rs); i++ {
			for j := i + 1; j < len(rs); j++ {
				if overlaps(rs[i].Rect, rs[j].Rect) {
					rs[i] = Region{Rect: union(rs[i].Rect, rs[j].Rect), Pixels: rs[i].Pixels + rs[j].Pixels}
					rs = slices.Delete(rs, j, j+1)
					merged = true
					j--
				}
			}
		}
	}
	return rs
}

func overlaps(a, b model.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

func union(a, b model.Rect) model.Rect {
	x0, y0 := min(a.X, b.X), min(a.Y, b.Y)
	x1, y1 := max(a.X+a.W, b.X+b.W), max(a.Y+a.H, b.Y+b.H)
	return model.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// pad grows r by n pixels on every side, clipped to w × h.
func pad(r model.Rect, n, w, h int) model.Rect {
	x0, y0 := max(r.X-n, 0), max(r.Y-n, 0)
	x1, y1 := min(r.X+r.W+n, w), min(r.Y+r.H+n, h)
	return model.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}
