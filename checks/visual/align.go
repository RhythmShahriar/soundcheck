// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package visual

import (
	"bytes"
	"cmp"
	"hash/maphash"
	"slices"
	"sort"
)

// Pages change vertically: a plugin adds a banner at the top and a
// notice above the footer, a paragraph wraps onto one more line. Lining
// up the two screenshots row by row, the way a text diff lines up
// lines, keeps everything that only moved out of the comparison; what's
// left is blocks that were inserted or removed and bands that really
// changed in place.

// Band is a run of rows inserted (H > 0) or removed (H < 0) from the
// page. Y is where, in before coordinates; At is the same place in the
// after image.
type Band struct{ Y, H, At int }

// rowHashes hashes each row's first w pixels, with ignored pixels
// blanked, so rows that only differ inside masked regions still match.
func rowHashes(r *raster, w int, ignore mask, seed maphash.Seed) []uint64 {
	out := make([]uint64, r.h)
	buf := make([]uint8, w*4)
	for y := range out {
		row := r.row(y, w)
		if m := ignore.row(y); m != nil {
			copy(buf, row)
			for x, v := range m {
				if v {
					buf[x*4], buf[x*4+1], buf[x*4+2], buf[x*4+3] = 0, 0, 0, 0
				}
			}
			row = buf
		}
		out[y] = maphash.Bytes(seed, row)
	}
	return out
}

type pair struct{ b, a int }

// matchRows lines up two sequences of row hashes, patience-diff style:
// common leading and trailing rows match, then rows that occur exactly
// once on each side anchor the alignment (in order, via the longest
// increasing subsequence). The gaps between anchors are aligned the
// same way. Blank or repeated rows never anchor on their own; they only
// match next to unique rows, so a long white background can't pull
// content out of place. A stretch with no unique rows at all (patterned
// or repeating content) falls back to a minimal row diff when it's
// small enough. It returns matched rows in order.
func matchRows(hb, ha []uint64) []pair {
	var out []pair
	var align func(b0, b1, a0, a1 int)
	align = func(b0, b1, a0, a1 int) {
		for b0 < b1 && a0 < a1 && hb[b0] == ha[a0] {
			out = append(out, pair{b0, a0})
			b0++
			a0++
		}
		tail := 0
		for b1-tail > b0 && a1-tail > a0 && hb[b1-1-tail] == ha[a1-1-tail] {
			tail++
		}
		if b0 < b1-tail && a0 < a1-tail {
			anchors := uniqueAnchors(hb, ha, b0, b1-tail, a0, a1-tail)
			pb, pa := b0, a0
			for _, p := range anchors {
				align(pb, p.b, pa, p.a)
				out = append(out, p)
				pb, pa = p.b+1, p.a+1
			}
			if len(anchors) > 0 {
				align(pb, b1-tail, pa, a1-tail)
			} else {
				out = append(out, myers(hb, ha, b0, b1-tail, a0, a1-tail)...)
			}
		}
		for k := tail; k > 0; k-- {
			out = append(out, pair{b1 - k, a1 - k})
		}
	}
	align(0, len(hb), 0, len(ha))
	return out
}

// uniqueAnchors returns rows that occur exactly once in hb[b0:b1] and
// once in ha[a0:a1], keeping the longest run that's in the same order on
// both sides.
func uniqueAnchors(hb, ha []uint64, b0, b1, a0, a1 int) []pair {
	type seen struct{ nb, na, b, a int }
	m := make(map[uint64]*seen, b1-b0)
	for y := b0; y < b1; y++ {
		if s := m[hb[y]]; s != nil {
			s.nb++
		} else {
			m[hb[y]] = &seen{nb: 1, b: y}
		}
	}
	var cands []pair
	for y := a0; y < a1; y++ {
		if s := m[ha[y]]; s != nil {
			s.na++
			s.a = y
		}
	}
	for _, s := range m {
		if s.nb == 1 && s.na == 1 {
			cands = append(cands, pair{s.b, s.a})
		}
	}
	slices.SortFunc(cands, func(x, y pair) int { return cmp.Compare(x.b, y.b) })
	return increasing(cands)
}

// increasing returns the longest subsequence of ps (sorted by b) whose a
// also increases (patience sorting).
func increasing(ps []pair) []pair {
	if len(ps) == 0 {
		return nil
	}
	var tops []int // index into ps of the smallest tail of each pile
	prev := make([]int, len(ps))
	for i, p := range ps {
		k := sort.Search(len(tops), func(j int) bool { return ps[tops[j]].a >= p.a })
		prev[i] = -1
		if k > 0 {
			prev[i] = tops[k-1]
		}
		if k == len(tops) {
			tops = append(tops, i)
		} else {
			tops[k] = i
		}
	}
	out := make([]pair, len(tops))
	for i, k := len(tops)-1, tops[len(tops)-1]; i >= 0; i, k = i-1, prev[k] {
		out[i] = ps[k]
	}
	return out
}

// maxEdits bounds the fallback row diff: beyond this many inserted and
// removed rows in one stretch without unique rows, it stays unaligned
// (one band changed in place) rather than costing time and memory.
const maxEdits = 1500

// myers is Myers' O((N+M)·D) diff of hb[b0:b1] and ha[a0:a1], returning
// matched rows in order, or nil when more than maxEdits rows differ.
func myers(hb, ha []uint64, b0, b1, a0, a1 int) []pair {
	n, m := b1-b0, a1-a0
	limit := min(n+m, maxEdits)
	off := limit + 1
	v := make([]int, 2*limit+3)
	var trace [][]int // trace[d]: v for k in [-d-1, d+1] after round d
	for d := 0; d <= limit; d++ {
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && hb[b0+x] == ha[a0+y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				trace = append(trace, slices.Clone(v[off-d-1:off+d+2]))
				return backtrack(trace, b0, a0, n, m)
			}
		}
		trace = append(trace, slices.Clone(v[off-d-1:off+d+2]))
	}
	return nil
}

func backtrack(trace [][]int, b0, a0, x, y int) []pair {
	var out []pair
	for d := len(trace) - 1; d > 0; d-- {
		prev := trace[d-1] // k from -d to d
		get := func(k int) int { return prev[k+d] }
		k := x - y
		pk := k - 1
		if k == -d || (k != d && get(k-1) < get(k+1)) {
			pk = k + 1
		}
		px := get(pk)
		py := px - pk
		for x > px && y > py {
			out = append(out, pair{b0 + x - 1, a0 + y - 1})
			x--
			y--
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		out = append(out, pair{b0 + x - 1, a0 + y - 1})
		x--
		y--
	}
	slices.Reverse(out)
	return out
}

// plan turns matched rows into a row map (after row → before row, -1
// when it has no counterpart) and the blocks inserted or removed. Rows
// between matches that exist on both sides are a band that changed in
// place: they're lined up (at the top or the bottom of the band,
// whichever fits better) for the pixel diff. When one side of such a
// band is mostly extra rows, those rows are a block of their own. The
// last value is blank space added at the end of the page (negative when
// removed), which isn't a block.
func plan(b, a *raster, w int, pairs []pair, window int) ([]int, []Band, int) {
	rows := make([]int, a.h)
	for i := range rows {
		rows[i] = -1
	}
	var blocks []Band
	// Blank space added or lost at the very end of the page (a document a
	// little taller than its content) isn't part of a block: one-color
	// rows that run to the end of the page are cut from a block that has
	// content before them and counted as padding.
	padding := 0
	// A page shorter than the window is screenshotted window-tall, so its
	// last rows are filler. Content pushed down eats into it. Filler
	// rows (a window-tall shot, or both pages ending in the same color)
	// are always padding; other one-color rows at the end only when
	// content is left in the block.
	tailA, tailB := blankTail(a, w), blankTail(b, w)
	sameEnd := tailA < a.h && tailB < b.h && bytes.Equal(a.row(a.h-1, w), b.row(b.h-1, w))
	fillA, fillB := sameEnd || (a.h == window && tailA < a.h), sameEnd || (b.h == window && tailB < b.h)
	hunk := func(b0, b1, a0, a1 int) {
		if a1 >= tailA && a1-a0 > b1-b0 {
			if end := max(tailA, a0); fillA || end-a0-(b1-b0) >= minResize {
				padding += a1 - end
				a1 = end
			}
		}
		if b1 >= tailB && b1-b0 > a1-a0 {
			if end := max(tailB, b0); fillB || end-b0-(a1-a0) >= minResize {
				padding -= b1 - end
				b1 = end
			}
		}
		nb, na := b1-b0, a1-a0
		switch {
		case nb == 0 && na == 0:
			return
		case nb == 0:
			blocks = append(blocks, Band{Y: b0, H: na, At: a0})
			return
		case na == 0:
			blocks = append(blocks, Band{Y: b0, H: -nb, At: a0})
			return
		}
		m := min(nb, na)
		extra := max(nb, na) - m
		// Line the shorter side up with the top or the bottom of the other.
		atTop := extra == 0 || rowCost(b, a, w, b0, a0, m) <= rowCost(b, a, w, b1-m, a1-m, m)
		ob, oa := b0, a0
		if !atTop {
			ob, oa = b1-m, a1-m
		}
		for k := range m {
			rows[oa+k] = ob + k
		}
		if extra < minResize || extra < m {
			return // a band that grew or shrank a little, e.g. text that wraps
		}
		switch {
		case na > nb && atTop:
			blocks = append(blocks, Band{Y: b0 + m, H: extra, At: a0 + m})
		case na > nb:
			blocks = append(blocks, Band{Y: b0, H: extra, At: a0})
		case atTop:
			blocks = append(blocks, Band{Y: b0 + m, H: -extra, At: a0 + m})
		default:
			blocks = append(blocks, Band{Y: b0, H: -extra, At: a0})
		}
	}
	pb, pa := 0, 0
	for _, p := range pairs {
		hunk(pb, p.b, pa, p.a)
		rows[p.a] = p.b
		pb, pa = p.b+1, p.a+1
	}
	hunk(pb, b.h, pa, a.h)
	return rows, blocks, padding
}

// blankTail returns the first row of the one-color rows that end the
// image (h when the last row isn't one color).
func blankTail(r *raster, w int) int {
	if r.h == 0 || !blankRow(r, r.h-1, w) {
		return r.h
	}
	last := r.row(r.h-1, w)
	y := r.h - 1
	for y > 0 && bytes.Equal(r.row(y-1, w), last) {
		y--
	}
	return y
}

// blankRow reports whether a row is a single color.
func blankRow(r *raster, y, w int) bool {
	row := r.row(y, w)
	for x := 4; x < len(row); x += 4 {
		if row[x] != row[0] || row[x+1] != row[1] || row[x+2] != row[2] || row[x+3] != row[3] {
			return false
		}
	}
	return true
}

// rowCost estimates how different m rows are when before row yb lines up
// with after row ya, sampling every fourth pixel.
func rowCost(b, a *raster, w, yb, ya, m int) int {
	n := 0
	for k := range m {
		rb, ra := b.row(yb+k, w), a.row(ya+k, w)
		for x := 0; x < w*4; x += 16 {
			if rb[x] != ra[x] || rb[x+1] != ra[x+1] || rb[x+2] != ra[x+2] {
				n++
			}
		}
	}
	return n
}
