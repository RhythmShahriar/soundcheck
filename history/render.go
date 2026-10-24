// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package history

import (
	"fmt"
	"html/template"
	"io"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Metrics Render and Value accept.
var Metrics = []string{"lcp", "tbt", "ttfb"}

// Value is an entry's metric in milliseconds: the site median, or one
// page's value when page is set. It's NaN when the entry has no value:
// the page wasn't among the ones recorded, or the metric wasn't
// measured.
func (e Entry) Value(metric, page string) float64 {
	pick := func(lcp, tbt, ttfb float64) float64 {
		switch metric {
		case "lcp":
			return positive(lcp)
		case "ttfb":
			return positive(ttfb)
		case "tbt":
			return tbt
		}
		return math.NaN()
	}
	if page == "" {
		if metric == "tbt" && len(e.PageSet) == 0 {
			return math.NaN()
		}
		return pick(e.LCP, e.TBT, e.TTFB)
	}
	for _, p := range e.PageSet {
		if p.Path == page {
			return pick(p.LCP, p.TBT, p.TTFB)
		}
	}
	return math.NaN()
}

func positive(v float64) float64 {
	if v > 0 {
		return v
	}
	return math.NaN()
}

// Render prints entries as a table, oldest first, with a column for the
// metric (lcp, tbt or ttfb; empty is lcp) and a sparkline of it at the
// end. With page set, the column is that page's value instead of the
// site median.
func Render(w io.Writer, entries []Entry, page, metric string) error {
	if metric == "" {
		metric = "lcp"
	}
	if !slices.Contains(Metrics, metric) {
		return fmt.Errorf("metric %q: want lcp, tbt or ttfb", metric)
	}
	if len(entries) == 0 {
		fmt.Fprintln(w, "No history yet: it grows with every snapshot, run and comparison.")
		return nil
	}
	values := make([]float64, len(entries))
	known := 0
	for i, e := range entries {
		values[i] = e.Value(metric, page)
		if !math.IsNaN(values[i]) {
			known++
		}
	}
	if page != "" && known == 0 {
		return fmt.Errorf("no timings for %s in the history; it keeps the first %d pages of each run", page, MaxPages)
	}
	col := strings.ToUpper(metric)
	if page == "" {
		col += " (median)"
	} else {
		col += " " + page
	}
	nameW := 4
	for _, e := range entries {
		nameW = max(nameW, min(24, utf8.RuneCountInString(e.Name)))
	}
	row := func(when, kind, name, pages, errs, warns, verdict, value string) {
		fmt.Fprintf(w, "%-17s  %-10s  %-*s  %5s  %6s  %8s  %-8s  %s\n", when, kind, nameW, name, pages, errs, warns, verdict, value)
	}
	row("When", "Kind", "Name", "Pages", "Errors", "Warnings", "Verdict", col)
	for i, e := range entries {
		row(e.At.Local().Format("2006-01-02 15:04"), e.Kind, truncate(e.Name, nameW), fmt.Sprint(e.Pages),
			fmt.Sprint(e.Issues["error"]), fmt.Sprint(e.Issues["warning"]), e.Verdict, ms(values[i]))
	}
	fmt.Fprintf(w, "\n%s  %s", strings.ToUpper(metric), Sparkline(values))
	first, last := math.NaN(), math.NaN()
	for _, v := range values {
		if !math.IsNaN(v) {
			if math.IsNaN(first) {
				first = v
			}
			last = v
		}
	}
	if known > 1 {
		fmt.Fprintf(w, "  %s → %s", ms(first), ms(last))
		if first > 0 {
			fmt.Fprintf(w, " (%+.0f%%)", (last-first)/first*100)
		}
	}
	fmt.Fprintln(w)
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ms is "840 ms", "1.6 s", or "–" when unknown.
func ms(v float64) string {
	switch {
	case math.IsNaN(v):
		return "–"
	case v < 1000:
		return fmt.Sprintf("%.0f ms", v)
	}
	return fmt.Sprintf("%.1f s", (time.Duration(v) * time.Millisecond).Seconds())
}

var bars = []rune("▁▂▃▄▅▆▇█")

// Sparkline draws values as block characters, lowest to highest; NaN is
// a gap (a space).
func Sparkline(values []float64) string {
	lo, hi := bounds(values)
	var b strings.Builder
	for _, v := range values {
		switch {
		case math.IsNaN(v):
			b.WriteRune(' ')
		case hi == lo:
			b.WriteRune(bars[len(bars)/2])
		default:
			b.WriteRune(bars[int(math.Round((v-lo)/(hi-lo)*float64(len(bars)-1)))])
		}
	}
	return b.String()
}

func bounds(values []float64) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, v := range values {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			lo, hi = min(lo, v), max(hi, v)
		}
	}
	return lo, hi
}

// SVG sizes.
const (
	svgWidth  = 120
	svgHeight = 28
	svgPad    = 3
)

// SVG returns values as an inline sparkline for HTML pages: a line in
// the text color, broken where a value is NaN, with a dot on the last
// value. It holds only numbers, so it's safe to embed as is.
func SVG(values []float64) template.HTML {
	lo, hi := bounds(values)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" class="spark" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="trend">`,
		svgWidth, svgHeight, svgWidth, svgHeight)
	if math.IsInf(lo, 0) {
		b.WriteString(`</svg>`)
		return template.HTML(b.String()) //nolint:gosec // numbers only
	}
	step := 0.0
	if len(values) > 1 {
		step = float64(svgWidth-2*svgPad) / float64(len(values)-1)
	}
	point := func(i int, v float64) (float64, float64) {
		y := float64(svgHeight) / 2
		if hi > lo {
			y = float64(svgHeight-svgPad) - (v-lo)/(hi-lo)*float64(svgHeight-2*svgPad)
		}
		return math.Round((svgPad+float64(i)*step)*10) / 10, math.Round(y*10) / 10
	}
	var seg []string
	flush := func() {
		if len(seg) > 1 {
			fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round"/>`, strings.Join(seg, " "))
		}
		seg = seg[:0]
	}
	lastI := -1
	for i, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			flush()
			continue
		}
		x, y := point(i, v)
		seg = append(seg, fmt.Sprintf("%g,%g", x, y))
		lastI = i
	}
	flush()
	x, y := point(lastI, values[lastI])
	fmt.Fprintf(&b, `<circle cx="%g" cy="%g" r="2" fill="currentColor"/></svg>`, x, y)
	return template.HTML(b.String()) //nolint:gosec // numbers only
}
