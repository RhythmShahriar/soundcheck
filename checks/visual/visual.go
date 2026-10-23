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
	"fmt"
	"hash/fnv"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Module is the visual regression module.
type Module struct{}

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
// taken twice a moment apart, and whatever differs between the two
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
		first, err1 := load(filepath.Join(cc.Dir, filepath.FromSlash(s.File)))
		second, err2 := load(filepath.Join(cc.Dir, filepath.FromSlash(s.Second)))
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
// for pages that changed, and reports them. Areas that changed the same
// way on several pages (a new header) become one change listing those
// pages, so a site-wide tweak is one line instead of hundreds.
func (Module) Compare(ctx context.Context, cmp *check.CompareContext) []model.Change {
	threshold := cmp.Config.Visual.Threshold
	type job struct {
		pair [2]*model.Page
		vp   string
	}
	var jobs []job
	for _, pair := range cmp.Pairs() {
		b, a := pair[0], pair[1]
		if a.Error != "" || b.Error != "" || a.Status >= 400 || b.Status >= 400 {
			continue // health reports pages that fail; their pixels aren't news
		}
		for _, vp := range Viewports {
			if b.Shots[vp].File != "" && a.Shots[vp].File != "" {
				jobs = append(jobs, job{pair, vp})
			}
		}
	}

	// Decoding and diffing dominate; a few at a time keeps memory bounded
	// (a tall page is tens of MB decoded).
	results := make([]*shotDiff, len(jobs))
	sem := make(chan struct{}, min(runtime.GOMAXPROCS(0), 4))
	var wg sync.WaitGroup
	for i, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			results[i] = diffShot(cmp, j.pair[0], j.pair[1], j.vp, threshold)
		})
	}
	wg.Wait()
	results = slices.DeleteFunc(results, func(r *shotDiff) bool { return r == nil })

	out := resizes(results)
	return append(out, changes(results, threshold)...)
}

// diffShot loads and compares one page's screenshots at one viewport.
func diffShot(cmp *check.CompareContext, b, a *model.Page, vp string, threshold float64) *shotDiff {
	sb, sa := b.Shots[vp], a.Shots[vp]
	rawB, err := os.ReadFile(filepath.Join(cmp.BeforeDir, filepath.FromSlash(sb.File)))
	if err != nil {
		return nil
	}
	rawA, err := os.ReadFile(filepath.Join(cmp.AfterDir, filepath.FromSlash(sa.File)))
	if err != nil {
		return nil
	}
	imgB, err := png.Decode(bytes.NewReader(rawB))
	if err != nil {
		return nil
	}
	imgA, err := png.Decode(bytes.NewReader(rawA))
	if err != nil {
		return nil
	}
	d := Compare(imgB, imgA, DiffOptions{
		IgnoreBefore: concat(sb.Masked, sb.Dynamic, sa.Dynamic),
		IgnoreAfter:  concat(sa.Masked, sa.Dynamic, sb.Dynamic),
	})
	r := &shotDiff{page: a, viewport: vp, diff: d, sizeB: imgB.Bounds().Size(), sizeA: imgA.Bounds().Size(),
		report: d.Changed > 0 && d.Percent > threshold}
	for _, reg := range d.Regions {
		r.sigs = append(r.sigs, d.signature(reg.Rect))
	}
	if (r.report || r.banded()) && cmp.Save != nil {
		r.files = save(cmp, a, vp, rawB, rawA, d)
	}
	d.release() // keep only the summary; hundreds of pages don't fit in memory
	return r
}

// banded reports whether a block was inserted or removed mid-page.
func (r *shotDiff) banded() bool {
	return r.diff.Band.H >= minResize || -r.diff.Band.H >= minResize
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

// signature identifies a changed region by its place and its pixels
// before and after, so the same change on two pages matches exactly.
func (d *Diff) signature(r model.Rect) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d,%d,%d,%d|", r.X, r.Y, r.W, r.H)
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
// narrower. Pages that changed by the same amount are one change.
func resizes(results []*shotDiff) []model.Change {
	groups := map[string]*model.Change{}
	bandOf := map[string]Band{}
	var order []string
	for _, r := range results {
		for _, s := range sizeChanges(r.sizeB, r.sizeA) {
			title := fmt.Sprintf("Page got %s (%s)", s, r.viewport)
			if r.banded() && (strings.HasSuffix(s, "taller") || strings.HasSuffix(s, "shorter")) {
				bandOf[title] = r.diff.Band
			}
			g := groups[title]
			if g == nil {
				g = &model.Change{Class: model.Changed, Check: "visual/size", Title: title,
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
		} else if b := bandOf[t]; b.H != 0 {
			g.Detail = bandDetail(b) + "."
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
// several pages, then pages whose remaining change is still over the
// threshold (collapsed into one change per viewport when there are many).
func changes(results []*shotDiff, threshold float64) []model.Change {
	out := bands(results)
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
	explained := map[*shotDiff]map[int]bool{} // regions covered by a shared change
	keys := make([]string, 0, len(bySig))
	for k := range bySig {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		ms := bySig[k]
		if len(ms) < 2 || !slices.ContainsFunc(ms, func(m member) bool { return m.r.report }) {
			continue
		}
		slices.SortFunc(ms, func(a, b member) int { return strings.Compare(a.r.page.Path, b.r.page.Path) })
		first := ms[0]
		ch := model.Change{Class: model.Changed, Check: "visual/shared",
			Title: fmt.Sprintf("%s changed on %d pages (%s)", areaName(first.region.Rect, first.r.sizeA.Y), len(ms), first.r.viewport),
			Detail: fmt.Sprintf("The same area (%d,%d, %d×%d px) changed the same way on every page listed.",
				first.region.X, first.region.Y, first.region.W, first.region.H),
		}
		for _, m := range ms {
			ch.Pages = append(ch.Pages, m.r.page.Path)
			if explained[m.r] == nil {
				explained[m.r] = map[int]bool{}
			}
			explained[m.r][m.index] = true
			if m.r.diff.Compared > 0 {
				ch.Score = max(ch.Score, float64(m.region.Pixels)*100/float64(m.r.diff.Compared))
			}
			if ch.Files == nil && m.r.files != nil {
				ch.Files = m.r.files
			}
		}
		ch.Score = round2(ch.Score)
		out = append(out, ch)
	}

	pages := map[string][]model.Change{} // by viewport
	for _, r := range results {
		if !r.report {
			continue
		}
		d := r.diff
		var rest []Region // largest first, as in d.Regions
		pixels := 0
		for i, reg := range d.Regions {
			if !explained[r][i] {
				rest = append(rest, reg)
				pixels += reg.Pixels
			}
		}
		score := float64(pixels) * 100 / float64(d.Compared)
		if pixels == 0 || score <= threshold {
			continue
		}
		areas := "1 area"
		if n := len(rest); n != 1 {
			areas = fmt.Sprintf("%d areas", n)
		}
		detail := fmt.Sprintf("%.2f%% of the page changed, in %s", score, areas)
		if len(rest) < len(d.Regions) {
			detail += " besides the changes shared with other pages"
		}
		big := rest[0]
		detail += fmt.Sprintf("; the largest at %d,%d (%d×%d px)", big.X, big.Y, big.W, big.H)
		if r.banded() {
			detail += "; " + lower(bandDetail(d.Band))
		}
		pages[r.viewport] = append(pages[r.viewport], model.Change{Class: model.Changed, Check: "visual/page",
			Title: fmt.Sprintf("Page looks different (%s)", r.viewport), Detail: detail + ".",
			Pages: []string{r.page.Path}, Score: round2(score), Files: r.files,
			Fix: "Open the report to compare the screenshots. If the change is expected, take a new snapshot."})
	}
	for _, vp := range Viewports {
		out = append(out, collapse(pages[vp], vp)...)
	}
	return out
}

// maxPages is how many pages may each get their own "looks different"
// change in one viewport; beyond it they're one change, because a list
// of hundreds hides the point (something site-wide changed).
const maxPages = 5

// collapse folds more than maxPages page changes into one, keeping the
// pages, the worst page's images, and the median and highest scores.
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
		Pages:  pages, Score: worst.Score, Files: worst.Files, Fix: worst.Fix}}
}

// bandTolerance is how far apart (px) the same block may start on
// different pages, which differ a little above it (titles, breadcrumbs).
const bandTolerance = 16

// bands reports blocks inserted or removed at the same place on several
// pages, e.g. a banner a plugin added to the top of every page. Pages
// match when the block has the same height and starts within
// bandTolerance px.
func bands(results []*shotDiff) []model.Change {
	byKey := map[string][]*shotDiff{}
	var keys []string
	for _, r := range results {
		if !r.banded() {
			continue
		}
		k := fmt.Sprintf("%s|%d", r.viewport, r.diff.Band.H)
		if byKey[k] == nil {
			keys = append(keys, k)
		}
		byKey[k] = append(byKey[k], r)
	}
	slices.Sort(keys)
	var out []model.Change
	for _, k := range keys {
		rs := byKey[k]
		slices.SortFunc(rs, func(a, b *shotDiff) int {
			return cmp.Or(cmp.Compare(a.diff.Band.Y, b.diff.Band.Y), strings.Compare(a.page.Path, b.page.Path))
		})
		for len(rs) > 0 {
			n := 1
			for n < len(rs) && rs[n].diff.Band.Y-rs[0].diff.Band.Y <= bandTolerance {
				n++
			}
			if n >= 2 {
				out = append(out, bandChange(rs[:n]))
			}
			rs = rs[n:]
		}
	}
	return out
}

func bandChange(rs []*shotDiff) model.Change {
	first := rs[0]
	b := first.diff.Band
	where := fmt.Sprintf("%d px down on", b.Y/10*10)
	switch {
	case b.Y < edge:
		where = "at the top of"
	case b.Y > first.sizeB.Y-edge:
		where = "at the bottom of"
	}
	title := fmt.Sprintf("New block %s %d pages (%s)", where, len(rs), first.viewport)
	if b.H < 0 {
		title = fmt.Sprintf("Block removed %s %d pages (%s)", strings.Replace(where, "at the", "from the", 1), len(rs), first.viewport)
	}
	ch := model.Change{Class: model.Changed, Check: "visual/shared", Title: title,
		Detail: "On every page listed: " + lower(bandDetail(b)) + ".",
		Fix:    "Expected if you added or removed it; otherwise look for a plugin or theme notice, banner or element that appeared."}
	paths := make([]string, 0, len(rs))
	for _, r := range rs {
		paths = append(paths, r.page.Path)
		ch.Score = max(ch.Score, round2(pct(r.sizeB.Y, r.sizeB.Y+r.diff.Band.H)))
	}
	slices.Sort(paths)
	ch.Pages = paths
	for _, r := range rs {
		if r.page.Path == paths[0] {
			ch.Files = r.files
		}
	}
	return ch
}

// bandDetail describes an inserted or removed block.
func bandDetail(b Band) string {
	if b.H < 0 {
		return fmt.Sprintf("A %d px tall block was removed %d px down; the content below it moved up", -b.H, b.Y)
	}
	return fmt.Sprintf("A %d px tall block was inserted %d px down; the content below it moved down", b.H, b.Y)
}

func lower(s string) string { return strings.ToLower(s[:1]) + s[1:] }

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

func load(path string) (image.Image, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return png.Decode(f)
}
