// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package visual compares screenshots of each page before and after a
// change. A pure-Go pixel diff (pixelmatch's YIQ distance and
// anti-aliasing test) finds what changed; ignore selectors and regions
// that change on their own (learned by screenshotting a snapshot twice)
// are left out; identical changes on many pages are reported once.
package visual

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Module is the visual regression module.
type Module struct{}

// Name is the module's name in --modules and the config file.
func (Module) Name() string { return "visual" }

// Viewports compared, in report order.
var Viewports = []string{"desktop", "mobile"}

const (
	// dynamicPad widens learned dynamic regions so a rotating element's
	// soft edges and shadows stay inside them.
	dynamicPad = 6
	// minResize is the smallest height or width change reported, so a
	// rounding pixel isn't news.
	minResize = 8
	// edge is how close to the top or bottom a region must start to be
	// named the header or footer.
	edge = 150
)

// CheckPage learns dynamic regions in a snapshot: the desktop shot is
// taken twice a moment apart. Whatever differs between the two
// (carousels, rotating ads, clocks) is recorded on the shot so later
// comparisons ignore it.
func (Module) CheckPage(cc *check.Context, p *model.Page) {
	if !cc.Snapshot || cc.Dir == "" {
		return
	}
	for _, vp := range Viewports {
		s, ok := p.Shots[vp]
		if !ok || s.Second == "" {
			continue
		}
		first, err1 := load(cc.Dir, s.File)
		second, err2 := load(cc.Dir, s.Second)
		if err1 != nil || err2 != nil {
			continue
		}
		s.Dynamic = Dynamic(first, second, s.Masked)
		p.Shots[vp] = s
		w, h := first.Bounds().Dx(), first.Bounds().Dy()
		if area := coverage(s.Dynamic, w, h); area > 30 {
			cc.Warn(fmt.Sprintf("%s: %.0f%% of the page changes on its own (%s); visual changes there are ignored", p.Path, area, vp))
		}
	}
}

// Dynamic returns the regions that differ between two captures of the
// same page, padded a little.
func Dynamic(first, second image.Image, masked []model.Rect) []model.Rect {
	d := Compare(first, second, DiffOptions{IgnoreBefore: masked, IgnoreAfter: masked})
	var out []model.Rect
	for _, r := range d.Regions {
		out = append(out, pad(r.Rect, dynamicPad, d.Width, d.Height))
	}
	slices.SortFunc(out, func(a, b model.Rect) int { return cmp.Or(cmp.Compare(a.Y, b.Y), cmp.Compare(a.X, b.X)) })
	return out
}

// coverage is the percentage of a w × h image the rects cover (overlaps
// counted once).
func coverage(rects []model.Rect, w, h int) float64 {
	if w == 0 || h == 0 {
		return 0
	}
	m := maskOf(rects, w, h)
	n := 0
	for _, row := range m {
		for _, v := range row {
			if v {
				n++
			}
		}
	}
	return float64(n) * 100 / float64(w*h)
}

// shotDiff is one page at one viewport, compared.
type shotDiff struct {
	page     *model.Page
	viewport string
	diff     *Diff
	sizeB    image.Point
	sizeA    image.Point
	sigs     []uint64 // per region: where it is and what it looked like before and after
	report   bool     // over the threshold on its own
	files    map[string]string
}

// Compare diffs every page's screenshots, saves before/after/diff images
// for pages that changed and reports them. Areas that changed the same
// way on several pages (a new header) become one change listing those
// pages, so a site-wide tweak is one line instead of hundreds.
//
// Screenshots that can't be read (deleted, truncated, not a PNG, or a
// path that leaves the run's folder) are counted and reported as a
// warning, so a missing baseline never reads as "Nothing changed".
func (Module) Compare(ctx context.Context, cmp *check.CompareContext) []model.Change {
	out, warnings := compareShots(ctx, cmp)
	for _, msg := range warnings {
		cmp.Warn(msg)
	}
	return out
}

// ComparePages is Compare a pair of pages at a time, for runs too big to
// hold (check.PageComparer). It keeps where each pair's screenshots are
// and diffs them at the end, as Compare does.
func (Module) ComparePages(ctx context.Context, cmp *check.CompareContext) check.PairSink {
	return &pairSink{ctx: ctx, cmp: cmp}
}

// pairSink collects the screenshots to diff, one pair at a time.
type pairSink struct {
	ctx  context.Context //nolint:containedctx // the comparison's, for Changes
	cmp  *check.CompareContext
	jobs []shotJob
}

// Pair notes the screenshots of one page found in both runs, keeping
// only what diffing them needs of each page.
func (s *pairSink) Pair(b, a *model.Page) {
	slim := func(p *model.Page) *model.Page {
		return &model.Page{ID: p.ID, URL: p.URL, Path: p.Path, Group: p.Group, Error: p.Error, Status: p.Status, Shots: p.Shots}
	}
	var sb, sa *model.Page
	for _, j := range shotJobs(b, a) {
		if sb == nil {
			sb, sa = slim(b), slim(a)
		}
		s.jobs = append(s.jobs, shotJob{[2]*model.Page{sb, sa}, j.vp})
	}
}

// Changes diffs the screenshots and returns the changes.
func (s *pairSink) Changes() []model.Change {
	out, warnings := diffJobs(s.ctx, s.cmp, s.jobs)
	for _, msg := range warnings {
		s.cmp.Warn(msg)
	}
	return out
}

// shotJob is one page's screenshots at one viewport, to diff.
type shotJob struct {
	pair [2]*model.Page
	vp   string
}

// shotJobs returns the viewports at which both pages have a screenshot
// to diff.
func shotJobs(b, a *model.Page) []shotJob {
	if a.Error != "" || b.Error != "" || a.Status >= 400 || b.Status >= 400 {
		return nil // health reports pages that fail; their pixels aren't news
	}
	var out []shotJob
	for _, vp := range Viewports {
		if b.Shots[vp].File != "" && a.Shots[vp].File != "" {
			out = append(out, shotJob{[2]*model.Page{b, a}, vp})
		}
	}
	return out
}

// compareShots is Compare, returning the warnings it would show.
func compareShots(ctx context.Context, cmp *check.CompareContext) ([]model.Change, []string) {
	var jobs []shotJob
	for _, pair := range cmp.Pairs() {
		jobs = append(jobs, shotJobs(pair[0], pair[1])...)
	}
	return diffJobs(ctx, cmp, jobs)
}

// diffJobs diffs the screenshots of jobs and returns the changes and
// the warnings to show.
func diffJobs(ctx context.Context, cmp *check.CompareContext, jobs []shotJob) ([]model.Change, []string) {
	threshold := cmp.Config.Visual.Threshold
	// Decoding and diffing dominate; a few at a time keeps memory bounded
	// (a tall page is tens of MB decoded).
	results := make([]*shotDiff, len(jobs))
	errs := make([]error, len(jobs))
	sem := make(chan struct{}, min(runtime.GOMAXPROCS(0), 4))
	var wg sync.WaitGroup
	for i, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			results[i], errs[i] = diffShot(cmp, j.pair[0], j.pair[1], j.vp, threshold)
		})
	}
	wg.Wait()
	results = slices.DeleteFunc(results, func(r *shotDiff) bool { return r == nil })

	told := map[*shotDiff]bool{} // pages whose height change a change already tells
	out := changes(results, threshold, told)
	fix := pageFix(cmp.Before)
	for i := range out {
		if out[i].Check == "visual/page" {
			out[i].Fix = fix
		}
	}
	return append(out, resizes(results, told)...), unreadable(errs)
}

// pageFix is the advice for a page that looks different. A snapshot
// takes an expected change with accept (or review); the other site of
// --against can't, so there the way to accept a difference is to hide
// the area. Only snapshots have a name.
func pageFix(before *model.Run) string {
	if before == nil || before.Name == "" {
		return "Open the report to compare the screenshots. If this difference is expected, hide the area with visual.ignore in .soundcheck/config.yaml."
	}
	return "Open the report to compare the screenshots. If the change is expected, accept it into the snapshot with accept <id> or review."
}

// shotError is a screenshot diffShot couldn't read.
type shotError struct {
	snapshot bool // the snapshot's file, not this run's
	err      error
}

func (e *shotError) Error() string { return e.err.Error() }
func (e *shotError) Unwrap() error { return e.err }

// unreadable turns diffShot's errors into warnings, one per side.
func unreadable(errs []error) []string {
	var snap, run int
	for _, err := range errs {
		var se *shotError
		switch {
		case errors.As(err, &se) && se.snapshot:
			snap++
		case err != nil:
			run++
		}
	}
	var out []string
	for _, x := range []struct {
		n     int
		where string
	}{{snap, "the snapshot"}, {run, "this run"}} {
		switch {
		case x.n == 1:
			out = append(out, fmt.Sprintf("visual: 1 screenshot from %s is missing or unreadable; visual changes on this page weren't checked", x.where))
		case x.n > 1:
			out = append(out, fmt.Sprintf("visual: %d screenshots from %s are missing or unreadable; visual changes on these pages weren't checked", x.n, x.where))
		}
	}
	return out
}

// diffShot loads and compares one page's screenshots at one viewport.
func diffShot(cmp *check.CompareContext, b, a *model.Page, vp string, threshold float64) (*shotDiff, error) {
	sb, sa := b.Shots[vp], a.Shots[vp]
	rawB, imgB, err := readShot(cmp.BeforeDir, sb.File)
	if err != nil {
		return nil, &shotError{snapshot: true, err: err}
	}
	rawA, imgA, err := readShot(cmp.AfterDir, sa.File)
	if err != nil {
		return nil, &shotError{err: err}
	}
	d := Compare(imgB, imgA, DiffOptions{
		// Rows are lined up before pixels are compared, so each side's
		// regions apply in its own coordinates.
		IgnoreBefore: concat(sb.Masked, sb.Dynamic),
		IgnoreAfter:  concat(sa.Masked, sa.Dynamic),
		Window:       capture.Viewports[vp].Height,
	})
	r := &shotDiff{page: a, viewport: vp, diff: d, sizeB: imgB.Bounds().Size(), sizeA: imgA.Bounds().Size(),
		report: d.Changed > 0 && d.Percent > threshold}
	for _, reg := range d.Regions {
		r.sigs = append(r.sigs, d.signature(reg.Rect))
	}
	if (r.report || len(r.blocks()) > 0) && cmp.Save != nil {
		r.files = save(cmp, a, vp, rawB, rawA, d)
	}
	d.release() // keep only the summary; hundreds of pages don't fit in memory
	return r, nil
}

// openShot opens a screenshot recorded in run.json. A run's files are
// always inside its folder; a path that is absolute or climbs out of it,
// or a symlink that points outside (a snapshot someone else made), is
// refused rather than read, since the bytes end up in the report.
func openShot(dir, file string) (*os.File, error) {
	rel := filepath.FromSlash(file)
	if !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("screenshot path %q is outside the run folder", file)
	}
	return os.OpenInRoot(dir, rel)
}

// readShot reads and decodes a screenshot, keeping the file's bytes for
// the report.
func readShot(dir, file string) ([]byte, image.Image, error) {
	f, err := openShot(dir, file)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, err
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	return raw, img, nil
}

// blocks returns the blocks inserted into or removed from the page that
// are big enough to report.
func (r *shotDiff) blocks() []Band {
	var out []Band
	for _, b := range r.diff.Blocks {
		if b.H >= minResize || -b.H >= minResize {
			out = append(out, b)
		}
	}
	return out
}

// save stores the before and after screenshots and the diff image in the
// comparison folder, so the report's sliders work on their own.
func save(cmp *check.CompareContext, a *model.Page, vp string, rawB, rawA []byte, d *Diff) map[string]string {
	base := fmt.Sprintf("visual/%s-%s", a.ID, vp)
	files := map[string]string{"before": base + "-before.png", "after": base + "-after.png", "diff": base + "-diff.png"}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, d.Image()); err != nil {
		return nil
	}
	for name, data := range map[string][]byte{"before": rawB, "after": rawA, "diff": buf.Bytes()} {
		if err := cmp.Save(files[name], data); err != nil {
			return nil
		}
	}
	return files
}

// signature identifies a changed region by its size, its x position and
// its pixels before and after, so the same change on two pages matches
// exactly even when it sits lower on longer pages (a footer).
func (d *Diff) signature(r model.Rect) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d,%d,%d|", r.X, r.W, r.H)
	for y := r.Y; y < r.Y+r.H; y++ {
		_, _ = h.Write(d.after.pix[y*d.after.stride+r.X*4 : y*d.after.stride+(r.X+r.W)*4])
		if yb := d.rows[y]; yb >= 0 {
			_, _ = h.Write(d.before.pix[yb*d.before.stride+r.X*4 : yb*d.before.stride+(r.X+r.W)*4])
		} else {
			_, _ = h.Write([]byte{0})
		}
	}
	return h.Sum64()
}

// release drops the pixel buffers once a diff is summarized.
func (d *Diff) release() { d.after, d.before, d.rows, d.class = nil, nil, nil, nil }

// resizes reports pages whose screenshots got taller, shorter, wider or
// narrower. Pages that changed by the same amount are one change. A
// height change that inserted or removed blocks account for isn't
// repeated here: the blocks are reported with it.
func resizes(results []*shotDiff, told map[*shotDiff]bool) []model.Change {
	groups := map[string]*model.Change{}
	var order []string
	for _, r := range results {
		explained := r.diff.Padding
		for _, b := range r.blocks() {
			explained += b.H
		}
		for _, s := range sizeChanges(r.sizeB, image.Pt(r.sizeA.X, r.sizeA.Y-explained)) {
			if strings.HasSuffix(s, "taller") || strings.HasSuffix(s, "shorter") {
				if told[r] {
					continue
				}
				s = sizeChanges(image.Pt(0, r.sizeB.Y), image.Pt(0, r.sizeA.Y))[0] // the page's real change
			}
			title := fmt.Sprintf("Page got %s (%s)", s, r.viewport)
			g := groups[title]
			if g == nil {
				g = &model.Change{Class: model.Changed, Check: "visual/size", Title: title, Viewport: r.viewport,
					Before: fmt.Sprintf("%d×%d px", r.sizeB.X, r.sizeB.Y), After: fmt.Sprintf("%d×%d px", r.sizeA.X, r.sizeA.Y),
					Fix: "Expected if content was added or removed; otherwise look for a new notice, banner or element pushing the layout."}
				groups[title] = g
				order = append(order, title)
			}
			g.Pages = append(g.Pages, r.page.Path)
			if g.Files == nil {
				g.Files = r.files // the screenshots, when the page was saved for a visual change
			}
			g.Score = max(g.Score, round2(max(pct(r.sizeB.Y, r.sizeA.Y), pct(r.sizeB.X, r.sizeA.X))))
		}
	}
	var out []model.Change
	for _, t := range order {
		g := groups[t]
		slices.Sort(g.Pages)
		if len(g.Pages) > 1 {
			g.Detail = fmt.Sprintf("%d pages changed size by the same amount.", len(g.Pages))
			g.Before, g.After = "", ""
		}
		out = append(out, *g)
	}
	return out
}

func sizeChanges(b, a image.Point) []string {
	var out []string
	if dh := a.Y - b.Y; dh >= minResize {
		out = append(out, fmt.Sprintf("%d px taller", dh))
	} else if -dh >= minResize {
		out = append(out, fmt.Sprintf("%d px shorter", -dh))
	}
	if dw := a.X - b.X; dw >= minResize {
		out = append(out, fmt.Sprintf("%d px wider", dw))
	} else if -dw >= minResize {
		out = append(out, fmt.Sprintf("%d px narrower", -dw))
	}
	return out
}

func pct(before, after int) float64 {
	if before == 0 {
		return 0
	}
	d := float64(after-before) * 100 / float64(before)
	if d < 0 {
		return -d
	}
	return d
}

// changes turns diffs into changes: blocks inserted or removed at the
// same place on several pages, regions that changed identically on
// several pages, then at most one change per page for what's left
// (collapsed into one change per viewport when there are many).
func changes(results []*shotDiff, threshold float64, told map[*shotDiff]bool) []model.Change {
	out, grouped := blockChanges(results)
	type member struct {
		r      *shotDiff
		region Region
		index  int
	}
	bySig := map[string][]member{}
	for _, r := range results {
		for i, reg := range r.diff.Regions {
			k := fmt.Sprintf("%s|%x", r.viewport, r.sigs[i])
			bySig[k] = append(bySig[k], member{r, reg, i})
		}
	}
	keys := make([]string, 0, len(bySig))
	for k := range bySig {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	// Regions that changed identically on several pages, merged by where
	// they are (a footer is "Footer" whether it sits at 400 or 4000 px).
	type shared struct {
		viewport, label string
		groups          int
		members         []member
	}
	byLabel := map[string]*shared{}
	var labels []string
	for _, k := range keys {
		ms := bySig[k]
		if len(ms) < 2 || !slices.ContainsFunc(ms, func(m member) bool { return m.r.report }) {
			continue
		}
		slices.SortFunc(ms, func(a, b member) int { return strings.Compare(a.r.page.Path, b.r.page.Path) })
		first := ms[0]
		label := areaName(first.region.Rect, contentHeight(first.r))
		lk := first.r.viewport + "|" + label
		sh := byLabel[lk]
		if sh == nil {
			sh = &shared{viewport: first.r.viewport, label: label}
			byLabel[lk] = sh
			labels = append(labels, lk)
		}
		sh.groups++
		sh.members = append(sh.members, ms...)
	}
	explained := map[*shotDiff]map[int]bool{} // regions covered by a shared change
	for _, lk := range labels {
		sh := byLabel[lk]
		ch := model.Change{Class: model.Changed, Check: "visual/shared", Viewport: sh.viewport}
		var heights []int
		for _, m := range sh.members {
			if !slices.Contains(ch.Pages, m.r.page.Path) {
				ch.Pages = append(ch.Pages, m.r.page.Path)
				if h := heightLeft(m.r); abs(h) >= minResize && !told[m.r] {
					heights = append(heights, h)
					told[m.r] = true
				}
			}
			if explained[m.r] == nil {
				explained[m.r] = map[int]bool{}
			}
			explained[m.r][m.index] = true
			if m.r.diff.Compared > 0 {
				ch.Score = max(ch.Score, float64(m.region.Pixels)*100/float64(m.r.diff.Compared))
			}
		}
		slices.Sort(ch.Pages)
		for _, m := range sh.members { // images from the first page listed
			if ch.Files == nil && m.r.page.Path == ch.Pages[0] {
				ch.Files = m.r.files
			}
		}
		ch.Title = fmt.Sprintf("%s changed on %d pages (%s)", sh.label, len(ch.Pages), sh.viewport)
		r := sh.members[0].region
		if sh.groups == 1 {
			ch.Detail = fmt.Sprintf("The same area (%s, on %s) changed the same way on every page listed.",
				placeOf(r.Rect, sh.members[0].r.sizeA.X), sh.members[0].r.page.Path)
		} else {
			ch.Detail = fmt.Sprintf("%d different changes in this part of the pages listed, each the same on several pages.", sh.groups)
		}
		if len(heights) > 0 {
			ch.Detail += " " + grewBy(heights, len(ch.Pages))
		}
		ch.Score = round2(ch.Score)
		out = append(out, ch)
	}

	pages := map[string][]model.Change{} // by viewport
	for _, r := range results {
		if c, ok := pageChange(r, explained[r], grouped[r], threshold, told); ok {
			pages[r.viewport] = append(pages[r.viewport], c)
		}
	}
	for _, vp := range Viewports {
		out = append(out, collapse(pages[vp], vp)...)
	}
	return out
}

// heightLeft is how much taller (or shorter, negative) the page got
// beyond its blocks and end-of-page padding.
func heightLeft(r *shotDiff) int {
	h := r.sizeA.Y - r.sizeB.Y - r.diff.Padding
	for _, b := range r.blocks() {
		h -= b.H
	}
	return h
}

// grewBy says how much pages grew, e.g. "13 of the pages are 25 px
// taller."
func grewBy(heights []int, pages int) string {
	var up, down []int
	for _, h := range heights {
		if h > 0 {
			up = append(up, h)
		} else {
			down = append(down, -h)
		}
	}
	var parts []string
	for _, x := range []struct {
		hs  []int
		dir string
	}{{up, "taller"}, {down, "shorter"}} {
		if len(x.hs) == 0 {
			continue
		}
		who := "The pages are"
		if len(x.hs) < pages {
			who = fmt.Sprintf("%d of the pages are", len(x.hs))
		}
		parts = append(parts, fmt.Sprintf("%s %s %s.", who, span(x.hs), x.dir))
	}
	return strings.Join(parts, " ")
}

// contentHeight is where the page's content ends in the after image,
// above any filler of a page shorter than the window.
func contentHeight(r *shotDiff) int {
	if r.diff.Content > 0 {
		return r.diff.Content
	}
	return r.sizeA.Y
}

// pageChange is what's left on one page once shared changes are taken
// out: changed regions and blocks no other page shares.
func pageChange(r *shotDiff, explained, grouped map[int]bool, threshold float64, told map[*shotDiff]bool) (model.Change, bool) {
	d := r.diff
	var rest []Region // largest first, as in d.Regions
	pixels := 0
	if r.report {
		for i, reg := range d.Regions {
			if !explained[i] {
				rest = append(rest, reg)
				pixels += reg.Pixels
			}
		}
	}
	blocks := looseBlocks(r.blocks(), grouped)
	score := 0.0
	if d.Compared > 0 {
		score = float64(pixels) * 100 / float64(d.Compared)
	}
	for _, b := range blocks {
		score += pct(r.sizeB.Y, r.sizeB.Y+b.H)
	}
	if (pixels == 0 && len(blocks) == 0) || score <= threshold {
		return model.Change{}, false
	}
	var parts []string
	if pixels > 0 {
		areas := "1 area"
		if n := len(rest); n != 1 {
			areas = fmt.Sprintf("%d areas", n)
		}
		p := fmt.Sprintf("%.2f%% of the page changed, in %s", float64(pixels)*100/float64(d.Compared), areas)
		if len(rest) < len(d.Regions) {
			p += " besides the changes shared with other pages"
		}
		big := rest[0]
		parts = append(parts, p+"; the largest "+placeOf(big.Rect, r.sizeA.X))
	}
	for _, b := range blocks {
		parts = append(parts, bandDetail(b))
	}
	if h := heightLeft(r); abs(h) >= minResize && !told[r] {
		told[r] = true
		dir := "taller"
		if h < 0 {
			dir = "shorter"
		}
		parts = append(parts, fmt.Sprintf("The page is %d px %s", abs(h), dir))
	}
	detail := strings.Join(parts, ". ")
	return model.Change{Class: model.Changed, Check: "visual/page",
		Title: fmt.Sprintf("Page looks different (%s)", r.viewport), Detail: detail + ".",
		Pages: []string{r.page.Path}, Score: round2(score), Files: r.files, Viewport: r.viewport}, true
}

// placeOf tells an area's size and where it is, e.g. "377×314 px, 143 px
// from the top and 8 px from the left". The left offset is left out for
// an area as wide as the page. Coordinates as "8,143" read as one number.
func placeOf(r model.Rect, width int) string {
	top := fmt.Sprintf("%d px from the top", r.Y)
	if r.Y == 0 {
		top = "at the top"
	}
	s := fmt.Sprintf("%d×%d px, %s", r.W, r.H, top)
	switch {
	case r.X == 0 && r.W >= width:
		return s
	case r.X == 0:
		return s + " at the left edge"
	}
	return s + fmt.Sprintf(" and %d px from the left", r.X)
}

// nearBlock is how far apart (in px) two blocks may be and still be one
// block: row alignment can split a notice around a couple of rows that
// happen to match. The pieces have one cause.
const nearBlock = 8

// bandRows is where a block's rows are: in the after image for an
// inserted block, in the before image for a removed one.
func bandRows(b Band) (start, end int) {
	if b.H > 0 {
		return b.At, b.At + b.H
	}
	return b.Y, b.Y - b.H
}

// looseBlocks returns the blocks a grouped change doesn't tell, with
// pieces within nearBlock of each other merged into one. A piece next to
// a grouped block is part of it and is left out with it.
func looseBlocks(blocks []Band, grouped map[int]bool) []Band {
	order := make([]int, len(blocks))
	for i := range order {
		order[i] = i
	}
	removed := func(i int) int { // inserted blocks first, then removed ones
		if blocks[i].H < 0 {
			return 1
		}
		return 0
	}
	slices.SortFunc(order, func(x, y int) int {
		sx, _ := bandRows(blocks[x])
		sy, _ := bandRows(blocks[y])
		return cmp.Or(cmp.Compare(removed(x), removed(y)), cmp.Compare(sx, sy))
	})
	var out []Band
	for i := 0; i < len(order); {
		merged, told := blocks[order[i]], grouped[order[i]]
		first, end := bandRows(merged)
		j := i + 1
		for ; j < len(order); j++ {
			b := blocks[order[j]]
			start, e := bandRows(b)
			if (b.H < 0) != (merged.H < 0) || start-end > nearBlock {
				break
			}
			end = max(end, e)
			told = told || grouped[order[j]]
		}
		// The merged block spans its pieces and the rows between them,
		// which only matched by chance.
		if merged.H > 0 {
			merged.H = end - first
		} else {
			merged.H = first - end
		}
		if !told {
			out = append(out, merged)
		}
		i = j
	}
	slices.SortStableFunc(out, func(x, y Band) int { return cmp.Compare(x.Y, y.Y) })
	return out
}

// maxPages is how many pages may each get their own "looks different"
// change in one viewport; beyond it they're one change, because a list
// of hundreds hides the point (something site-wide changed).
const maxPages = 5

// collapse folds more than maxPages page changes into one, keeping the
// pages, the worst page's images and the median and highest scores.
func collapse(cs []model.Change, vp string) []model.Change {
	if len(cs) <= maxPages {
		return cs
	}
	slices.SortFunc(cs, func(a, b model.Change) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), strings.Compare(a.Pages[0], b.Pages[0]))
	})
	scores := make([]float64, len(cs))
	var pages []string
	for i, c := range cs {
		scores[i] = c.Score
		pages = append(pages, c.Pages...)
	}
	slices.Sort(scores)
	slices.Sort(pages)
	median := scores[len(scores)/2]
	if len(scores)%2 == 0 {
		median = (scores[len(scores)/2-1] + median) / 2
	}
	worst := cs[0]
	return []model.Change{{Class: model.Changed, Check: "visual/page",
		Title:  fmt.Sprintf("%d pages look different (%s)", len(cs), vp),
		Detail: fmt.Sprintf("Median %.2f%% of each page changed, at most %.2f%% (%s, shown).", round2(median), worst.Score, worst.Pages[0]),
		Pages:  pages, Score: worst.Score, Files: worst.Files, Viewport: vp}}
}

// Where a block sits, for grouping: pages differ in length, so a block
// above the footer is "near the bottom" on every page rather than at one
// y position.
const (
	atTop    = "at the top of"
	nearEnd  = "near the bottom of"
	midPage  = "down on"
	nearRows = 600 // a block starting this close to the end is near the bottom
)

func place(b Band, height int) string {
	switch {
	case b.Y < edge:
		return atTop
	case height-b.Y <= max(nearRows, height/4) && b.Y >= height/2:
		return nearEnd
	}
	return midPage
}

// sameBlock reports whether two blocks look like the same block on two
// pages: inserted or removed alike, about the same height (text inside
// may wrap differently) and, mid-page, about the same place.
func sameBlock(x, y Band, where string) bool {
	if (x.H > 0) != (y.H > 0) {
		return false
	}
	hx, hy := abs(x.H), abs(y.H)
	if abs(hx-hy) > max(minResize, max(hx, hy)*15/100) {
		return false
	}
	return where != midPage || abs(x.Y-y.Y) <= max(32, x.Y/20)
}

// blockChanges reports blocks inserted or removed in the same place on
// several pages, e.g. a banner a plugin added to the top of every page,
// as one change each. It returns which of each page's blocks it covered.
func blockChanges(results []*shotDiff) ([]model.Change, map[*shotDiff]map[int]bool) {
	type at struct {
		r     *shotDiff
		b     Band
		index int
	}
	byKey := map[string][]at{}
	for _, r := range results {
		for i, b := range r.blocks() {
			k := fmt.Sprintf("%s|%t|%s", r.viewport, b.H > 0, place(b, r.diff.ContentBefore))
			byKey[k] = append(byKey[k], at{r, b, i})
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	grouped := map[*shotDiff]map[int]bool{}
	var out []model.Change
	for _, k := range keys {
		rest := byKey[k]
		slices.SortFunc(rest, func(x, y at) int {
			return cmp.Or(cmp.Compare(abs(y.b.H), abs(x.b.H)), cmp.Compare(x.b.Y, y.b.Y), strings.Compare(x.r.page.Path, y.r.page.Path))
		})
		for len(rest) > 0 {
			seed := rest[0]
			where := place(seed.b, seed.r.diff.ContentBefore)
			var group, left []at
			for _, x := range rest {
				if sameBlock(seed.b, x.b, where) {
					group = append(group, x)
				} else {
					left = append(left, x)
				}
			}
			rest = left
			pages := map[string]bool{}
			for _, x := range group {
				pages[x.r.page.Path] = true
			}
			if len(pages) < 2 {
				continue
			}
			var rs []*shotDiff
			var bs []Band
			for _, x := range group {
				if grouped[x.r] == nil {
					grouped[x.r] = map[int]bool{}
				}
				grouped[x.r][x.index] = true
				rs = append(rs, x.r)
				bs = append(bs, x.b)
			}
			out = append(out, blockChange(rs, bs, where))
		}
	}
	return out, grouped
}

func blockChange(rs []*shotDiff, bs []Band, where string) model.Change {
	vp := rs[0].viewport
	var paths []string
	var heights, totals []int
	score := 0.0
	for i, r := range rs {
		if !slices.Contains(paths, r.page.Path) {
			paths = append(paths, r.page.Path)
			totals = append(totals, abs(r.sizeA.Y-r.sizeB.Y))
		}
		heights = append(heights, abs(bs[i].H))
		score = max(score, pct(r.sizeB.Y, r.sizeB.Y+bs[i].H))
	}
	slices.Sort(paths)
	removed := bs[0].H < 0
	pos := where
	if where == midPage {
		pos = fmt.Sprintf("%d px down on", bs[0].Y/10*10)
	}
	title := fmt.Sprintf("New block %s %d pages (%s)", pos, len(paths), vp)
	verb := "appeared"
	if removed {
		title = fmt.Sprintf("Block removed %s %d pages (%s)", strings.Replace(pos, "at the top of", "from the top of", 1), len(paths), vp)
		verb = "was removed"
	}
	where = strings.TrimSuffix(strings.TrimSuffix(where, " of"), " on")
	if where == "down" {
		where = fmt.Sprintf("about %d px down", bs[0].Y/10*10)
	}
	detail := fmt.Sprintf("A %s tall block %s %s of each page listed, moving the content below it; that content is unchanged.",
		span(heights), verb, where)
	if t := span(totals); slices.Min(totals) > 0 {
		dir := "taller"
		if rs[0].sizeA.Y < rs[0].sizeB.Y {
			dir = "shorter"
		}
		detail += fmt.Sprintf(" The pages are %s %s in all.", t, dir)
	}
	ch := model.Change{Class: model.Changed, Check: "visual/block", Title: title, Detail: detail, Pages: paths, Score: round2(score), Viewport: vp,
		Fix: "Expected if you added or removed it; otherwise look for a plugin or theme notice, banner or element that appeared."}
	for _, p := range paths {
		for _, r := range rs {
			if r.page.Path == p && ch.Files == nil {
				ch.Files = r.files
			}
		}
	}
	return ch
}

// span is "320 px" or "300–340 px".
func span(vs []int) string {
	lo, hi := slices.Min(vs), slices.Max(vs)
	if lo == hi {
		return fmt.Sprintf("%d px", lo)
	}
	return fmt.Sprintf("%d–%d px", lo, hi)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// bandDetail describes an inserted or removed block.
func bandDetail(b Band) string {
	if b.H < 0 {
		return fmt.Sprintf("A %d px tall block was removed %d px down; the content below it moved up", -b.H, b.Y)
	}
	return fmt.Sprintf("A %d px tall block was inserted %d px down; the content below it moved down", b.H, b.Y)
}

// areaName names a region by where it sits on the page.
func areaName(r model.Rect, height int) string {
	switch {
	case r.Y < edge:
		return "Header"
	case r.Y+r.H > height-edge:
		return "Footer"
	}
	return fmt.Sprintf("Area %d px down", r.Y/10*10)
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func concat(rs ...[]model.Rect) []model.Rect {
	var out []model.Rect
	for _, r := range rs {
		out = append(out, r...)
	}
	return out
}

func load(dir, file string) (image.Image, error) {
	f, err := openShot(dir, file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return png.Decode(f)
}
