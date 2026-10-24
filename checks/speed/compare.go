// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package speed

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// metric is one lab measurement a comparison watches. A page is slower
// when the value got worse by more than speed.slower percent AND by at
// least floor: lab timings jitter by tens of milliseconds between loads,
// and a 50% rise from 20 ms to 30 ms is noise, not a regression.
type metric struct {
	key, name     string
	worse, better string // "slower"/"faster", "higher"/"lower"
	floor         float64
	get           func(model.Metrics) float64
	format        func(float64) string
	measured      func(model.Metrics) bool // false when the browser didn't report it
	fix           string
}

var metrics = []metric{
	{key: "lcp", name: "LCP", worse: "slower", better: "faster", floor: 300,
		get: func(m model.Metrics) float64 { return m.LCP }, format: ms,
		measured: func(m model.Metrics) bool { return m.LCP > 0 },
		fix:      "Find what grew the largest element's load: a bigger hero image, new render-blocking CSS/JS, or a slower server response."},
	// TBT's floor is high because it's CPU time: with four tabs loading
	// at once, and the machine doing other work, the same 250 ms of
	// script measured 190–390 ms. A real regression from a new plugin
	// script is usually bigger than that.
	{key: "tbt", name: "Total Blocking Time", worse: "higher", better: "lower", floor: 250,
		get: func(m model.Metrics) float64 { return m.TBT }, format: ms,
		measured: func(model.Metrics) bool { return true },
		fix:      "New or updated JavaScript runs longer tasks; check which plugin or theme scripts changed (see the per-plugin cost suggestions)."},
	{key: "cls", name: "Layout shift (CLS)", worse: "higher", better: "lower", floor: 0.05,
		get: func(m model.Metrics) float64 { return m.CLS }, format: func(v float64) string { return fmt.Sprintf("%.2f", v) },
		measured: func(model.Metrics) bool { return true },
		fix:      "Something now moves content after it renders: an image or embed without dimensions, a late banner, or a web font swap."},
	{key: "weight", name: "Page weight", worse: "higher", better: "lower", floor: 100 << 10,
		get: func(m model.Metrics) float64 { return float64(m.Bytes) }, format: func(v float64) string { return size(int64(v)) },
		measured: func(m model.Metrics) bool { return m.Requests > 0 },
		fix:      "Look for new or larger images, scripts and fonts (see the per-plugin cost suggestions)."},
	{key: "ttfb", name: "Server response (TTFB)", worse: "slower", better: "faster", floor: 200,
		get: func(m model.Metrics) float64 { return m.TTFB }, format: ms,
		measured: func(m model.Metrics) bool { return m.TTFB > 0 },
		fix:      "PHP takes longer to build the page: check page caching still works, then look for slow plugins or queries (Query Monitor)."},
}

// hit is one page whose metric moved past the bar.
type hit struct {
	path, group   string
	before, after float64
	pct           float64 // % worse (100 when it was 0 before), or % lower for improvements
}

// OwnsIssues keeps the generic issue diff away from speed issues: lab
// numbers wobble between runs, so a page near a threshold would flip
// between NEW and FIXED. Compare reports speed changes with noise floors.
func (*Module) OwnsIssues() {}

// Compare reports pages that got measurably slower (SLOWER) or faster
// (FIXED), grouped by page template, or as one site-wide change when most
// pages moved; and plugins or themes that now add much more weight.
func (*Module) Compare(_ context.Context, cmp *check.CompareContext) []model.Change {
	bar := cmp.Config.Speed.Slower
	if bar <= 0 {
		bar = 25
	}
	pairs := cmp.Pairs()
	var out []model.Change
	for _, m := range metrics {
		var slower, faster []hit
		n := 0
		for _, pair := range pairs {
			b, a := pair[0], pair[1]
			if !comparable(b) || !comparable(a) || !m.measured(b.Metrics) || !m.measured(a.Metrics) {
				continue
			}
			n++
			bv, av := m.get(b.Metrics), m.get(a.Metrics)
			group := a.Group
			if group == "" {
				group = b.Group
			}
			if pct, ok := moved(bv, av, m.floor, bar); ok {
				slower = append(slower, hit{a.Path, group, bv, av, pct})
			} else if _, ok := moved(av, bv, m.floor, bar); ok {
				faster = append(faster, hit{a.Path, group, bv, av, (bv - av) / bv * 100})
			}
		}
		out = append(out, grouped(m, slower, n, bar, true)...)
		out = append(out, grouped(m, faster, n, bar, false)...)
	}
	return append(out, pluginChanges(pairs, bar)...)
}

// comparable: pages that loaded fine have meaningful timings.
func comparable(p *model.Page) bool { return p.Error == "" && p.Status > 0 && p.Status < 400 }

// moved reports whether to grew past from by both the floor and pct
// percent, and by how many percent. Improvements use the same test with
// the values swapped, so a page must be as clearly faster as a slower one
// must be slower.
func moved(from, to, floor, pct float64) (float64, bool) {
	if to-from < floor {
		return 0, false
	}
	if from <= 0 {
		return 100, true
	}
	p := (to - from) / from * 100
	return p, p > pct
}

// grouped turns per-page hits into changes: one site-wide change when
// most pages moved (at least 3), otherwise one per page template, and one
// per page for pages without a template.
func grouped(m metric, hits []hit, compared int, bar float64, worse bool) []model.Change {
	if len(hits) == 0 {
		return nil
	}
	if len(hits) >= 3 && len(hits)*2 > compared {
		return []model.Change{change(m, hits, bar, worse, fmt.Sprintf(" on %d pages", len(hits)))}
	}
	byGroup := map[string][]hit{}
	var groups []string
	for _, h := range hits {
		if _, ok := byGroup[h.group]; !ok {
			groups = append(groups, h.group)
		}
		byGroup[h.group] = append(byGroup[h.group], h)
	}
	slices.Sort(groups)
	var out []model.Change
	for _, g := range groups {
		hs := byGroup[g]
		if g == "" || len(hs) == 1 {
			for _, h := range hs {
				out = append(out, change(m, []hit{h}, bar, worse, ""))
			}
			continue
		}
		out = append(out, change(m, hs, bar, worse, fmt.Sprintf(" on %d %s pages", len(hs), g)))
	}
	return out
}

func change(m metric, hits []hit, bar float64, worse bool, scope string) model.Change {
	slices.SortFunc(hits, func(a, b hit) int { return strings.Compare(a.path, b.path) })
	var pages []string
	var bs, as, ps []float64
	for _, h := range hits {
		pages = append(pages, h.path)
		bs, as, ps = append(bs, h.before), append(as, h.after), append(ps, h.pct)
	}
	b, a, pct := median(bs), median(as), median(ps)
	class, verb := model.Slower, m.worse
	if !worse {
		class, verb = model.Fixed, m.better
	}
	title := fmt.Sprintf("%s %s %s%s", m.name, amount(pct, worse), verb, scope)
	if worse && b <= 0 {
		title = fmt.Sprintf("%s up from 0%s", m.name, scope)
	}
	detail := "Lab measurement, one load per page"
	if len(hits) > 1 {
		detail = fmt.Sprintf("Lab measurement, median of %d pages", len(hits))
	}
	detail += fmt.Sprintf("; a page counts when it's %s by more than %.0f%% and at least %s", verb, bar, m.format(m.floor))
	ch := model.Change{Class: class, Check: "speed/" + m.key, Title: title, Detail: detail,
		Pages: pages, Before: m.format(b), After: m.format(a), Score: math.Round(pct*10) / 10}
	if worse {
		ch.Fix = m.fix
	}
	return ch
}

// amount words a change: "40%" up to doubling, then "3×" ("LCP 3× slower"
// reads better than "LCP 200% slower").
func amount(pct float64, worse bool) string {
	if !worse || pct < 100 {
		return fmt.Sprintf("%.0f%%", pct)
	}
	x := 1 + pct/100
	if x >= 10 {
		return fmt.Sprintf("%.0f×", x)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", x), ".0") + "×"
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	if n := len(s); n%2 == 0 {
		return (s[n/2-1] + s[n/2]) / 2
	}
	return s[len(s)/2]
}

// pluginChanges reports plugins and themes whose assets got much heavier
// per page (or appeared), and ones that got much lighter, using the same
// bar and a 100 KB floor. This points straight at the plugin an update
// made heavier.
func pluginChanges(pairs [][2]*model.Page, bar float64) []model.Change {
	var before, after []model.Page
	for _, p := range pairs {
		if comparable(p[0]) && comparable(p[1]) {
			before, after = append(before, *p[0]), append(after, *p[1])
		}
	}
	key := func(r model.Resource) string { return component(r.URL) }
	bc, ac := costs(before, nil, key), costs(after, nil, key)
	names := map[string]bool{}
	for n := range bc {
		names[n] = true
	}
	for n := range ac {
		names[n] = true
	}
	var out []model.Change
	for n := range names {
		var bb, ab int64
		var pages []string
		if c := bc[n]; c != nil {
			bb, _, _ = c.perPage()
			pages = c.pages
		}
		if c := ac[n]; c != nil {
			ab, _, _ = c.perPage()
			pages = c.pages
		}
		kind, slug, _ := strings.Cut(n, " ")
		name := strings.ToUpper(kind[:1]) + kind[1:] + " " + slug
		if pct, ok := moved(float64(bb), float64(ab), 100<<10, bar); ok {
			title := fmt.Sprintf("%s got %s heavier per page", name, amount(pct, true))
			if bb == 0 {
				title = name + " now loads assets on pages"
			}
			out = append(out, model.Change{Class: model.Slower, Check: "speed/" + kind + "-cost", Title: title,
				Source: kind + "s/" + slug, Pages: pages, Before: size(bb), After: size(ab), Score: math.Round(pct*10) / 10,
				Detail: "Lab measurement: average transferred size of its files per page that loads them",
				Fix:    "Check what the " + kind + " update changed, or limit its assets to the pages that use them."})
		} else if _, ok := moved(float64(ab), float64(bb), 100<<10, bar); ok {
			title := fmt.Sprintf("%s got %.0f%% lighter per page", name, 100*float64(bb-ab)/float64(bb))
			if ab == 0 {
				title = name + " no longer loads assets on pages"
			}
			out = append(out, model.Change{Class: model.Fixed, Check: "speed/" + kind + "-cost", Title: title,
				Source: kind + "s/" + slug, Pages: pages, Before: size(bb), After: size(ab),
				Score:  math.Round(1000*float64(bb-ab)/float64(bb)) / 10,
				Detail: "Lab measurement: average transferred size of its files per page that loads them"})
		}
	}
	slices.SortFunc(out, func(a, b model.Change) int { return cmp.Compare(a.Title, b.Title) })
	return out
}
