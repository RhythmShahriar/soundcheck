// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package visual

import (
	"image"
	"image/color"
	"slices"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

func TestMatchRows(t *testing.T) {
	// 1 2 3 4 5  →  9 9 1 2 3 7 4 5: two rows inserted at the top, one in
	// the middle.
	pairs := matchRows([]uint64{1, 2, 3, 4, 5}, []uint64{9, 9, 1, 2, 3, 7, 4, 5})
	want := []pair{{0, 2}, {1, 3}, {2, 4}, {3, 6}, {4, 7}}
	if !slices.Equal(pairs, want) {
		t.Errorf("pairs = %v, want %v", pairs, want)
	}
	// Repeated rows (0 = blank) don't anchor on their own: the unique rows
	// decide. Blanks match next to them.
	b := []uint64{0, 0, 0, 1, 0, 0, 2, 0, 0, 0}
	a := []uint64{0, 0, 0, 1, 0, 0, 5, 5, 5, 0, 0, 2, 0, 0, 0}
	pairs = matchRows(b, a)
	if len(pairs) != len(b) || pairs[3] != (pair{3, 3}) || pairs[6] != (pair{6, 11}) {
		t.Errorf("pairs = %v", pairs)
	}
	if got := matchRows(nil, []uint64{1}); len(got) != 0 {
		t.Errorf("empty before: %v", got)
	}
	// Only repeated rows: the fallback diff still lines them up.
	b = []uint64{1, 2, 1, 2, 1, 2, 1, 2}
	a = []uint64{1, 2, 1, 2, 7, 7, 1, 2, 1, 2}
	if pairs = matchRows(b, a); len(pairs) != 8 {
		t.Errorf("repeated: %v", pairs)
	}
}

// stripes paints a repetitive background: 20 px bands of two shades, the
// kind of rows that repeat hundreds of times down a page.
func stripes(w, h int) *image.NRGBA {
	img := canvas(w, h)
	for y := 0; y < h; y += 40 {
		fill(img, image.Rect(0, y, w, y+20), color.NRGBA{240, 244, 248, 255})
	}
	return img
}

// A block inserted into a long run of repeating rows must not drag the
// unique content around it out of place.
func TestRepetitiveBackground(t *testing.T) {
	b := stripes(800, 3000)
	text(b, 40, 200, 3, 30)  // a heading near the top
	text(b, 40, 2500, 3, 30) // and a paragraph near the bottom
	a := insertRows(b, 1200, 40, red)
	d := Compare(b, a, DiffOptions{})
	if d.Changed != 0 || len(d.Blocks) != 1 || d.Blocks[0].H != 40 {
		t.Fatalf("changed %d, blocks %+v", d.Changed, d.Blocks)
	}
	// Where exactly in identical stripes the block went is ambiguous by a
	// stripe period; it must be in the stripes, not by the text.
	if y := d.Blocks[0].Y; y < 300 || y > 2400 {
		t.Errorf("block at %d", y)
	}

	// No unique rows at all: a plain page with a block.
	b = canvas(400, 2000)
	a = insertRows(b, 500, 100, blue)
	d = Compare(b, a, DiffOptions{})
	if d.Changed != 0 || len(d.Blocks) != 1 || d.Blocks[0].H != 100 {
		t.Errorf("plain page: changed %d, blocks %+v", d.Changed, d.Blocks)
	}
}

// Three blocks on one page: inserted at the top, removed mid-page and
// inserted above the footer; with a change in place elsewhere.
func TestSeveralBlocks(t *testing.T) {
	b := page(1000, 3000)
	text(b, 40, 1000, 60, 80)
	a := insertRows(b, 0, 320, red)
	a = removeRows(a, 320+1100, 150) // lines of text
	a = insertRows(a, 320-150+2900, 53, blue)
	fill(a, image.Rect(600, 2000, 900, 2100), red)
	d := Compare(b, a, DiffOptions{})
	want := []Band{{Y: 0, H: 320, At: 0}, {Y: 1100, H: -150, At: 1420}, {Y: 2900, H: 53, At: 3070}}
	if !slices.Equal(d.Blocks, want) {
		t.Errorf("blocks = %+v, want %+v", d.Blocks, want)
	}
	if len(d.Regions) != 1 || d.Regions[0].Y != 2000 || d.Changed < 25000 {
		t.Errorf("changed %d regions %+v", d.Changed, d.Regions)
	}
}

// A dynamic region (a carousel learned in the snapshot) still lines up
// and stays ignored when a block above it pushed it down.
func TestDynamicRegionMoved(t *testing.T) {
	b := page(800, 1500)
	text(b, 40, 900, 10, 30)
	fill(b, image.Rect(100, 600, 700, 800), red) // the carousel's first slide
	a := insertRows(b, 0, 120, blue)
	fill(a, image.Rect(100, 720, 700, 920), color.NRGBA{0, 160, 0, 255}) // another slide, moved down
	dyn := []model.Rect{{X: 94, Y: 594, W: 612, H: 212}}
	d := Compare(b, a, DiffOptions{IgnoreBefore: dyn})
	if d.Changed != 0 || !slices.Equal(d.Blocks, []Band{{Y: 0, H: 120, At: 0}}) {
		t.Errorf("changed %d, blocks %+v", d.Changed, d.Blocks)
	}
}

// A notice added at the end of a page whose document also grew blank
// space below it: the notice is the block, the blank space is padding.
func TestBlankPaddingAtEnd(t *testing.T) {
	b := page(600, 1000)
	b = insertRows(b, 1000, 40, color.White) // the page ends in white rows
	a := insertRows(b, 1000, 60, color.NRGBA{250, 220, 220, 255})
	text(a, 10, 1020, 1, 30)
	a = insertRows(a, a.Bounds().Dy(), 200, color.White)
	d := Compare(b, a, DiffOptions{})
	if d.Changed != 0 || len(d.Blocks) != 1 || d.Blocks[0].H != 60 || d.Padding != 200 {
		t.Errorf("changed %d, blocks %+v, padding %d", d.Changed, d.Blocks, d.Padding)
	}
}
