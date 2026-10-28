// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package history

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RhythmShahriar/soundcheck/wcag"
)

// Value is an entry's metric: for the timings, in milliseconds, the
// site median or one page's value when page is set; for a11y and
// keyboard, the site's open problems or one page's. It's NaN when the
// entry has no value: the page wasn't among the ones recorded, the
// metric wasn't measured or the run didn't have the module on.
func (e Entry) Value(metric, page string) float64 {
	if count, ok := countMetrics[metric]; ok {
		n := count(e)
		if page != "" {
			n = nil
			for _, p := range e.PageSet {
				if p.Path == page {
					n = pageCounts[metric](p)
					break
				}
			}
		}
		if n != nil {
			return float64(*n)
		}
		return math.NaN()
	}
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

// countMetrics are the metrics that count problems instead of timing
// pages, with the entry's count of each.
var countMetrics = map[string]func(Entry) *int{
	"a11y":     func(e Entry) *int { return e.A11yOpen },
	"keyboard": func(e Entry) *int { return e.KeyboardOpen },
}

// pageCounts are countMetrics for one page of an entry.
var pageCounts = map[string]func(PageMetrics) *int{
	"a11y":     func(p PageMetrics) *int { return p.A11yOpen },
	"keyboard": func(p PageMetrics) *int { return p.KeyboardOpen },
}

// countColumns name the column of a count metric.
var countColumns = map[string]string{"a11y": "A11y open", "keyboard": "Keyboard open"}

func positive(v float64) float64 {
	if v > 0 {
		return v
	}
	return math.NaN()
}

// Render prints entries as a table, oldest first, with a column for the
// metric (one of Metrics; empty is lcp) and a sparkline of it at the
// end. With page set, the column is that page's value instead of the
// site median. plain (--plain, for screen readers and logs) leaves out
// the sparkline and writes every glyph as text: "160 ms to 276 ms". The
// a11y and keyboard metrics count open problems (see renderCounts).
func Render(w io.Writer, entries []Entry, page, metric string, plain bool) error {
	text := func(s string) string { return s }
	if plain {
		text = strings.NewReplacer("→", "to", "–", "-", "…", "...").Replace
	}
	if metric == "" {
		metric = "lcp"
	}
	if !slices.Contains(Metrics, metric) {
		return fmt.Errorf("metric %q: want %s or %s", metric, strings.Join(Metrics[:len(Metrics)-1], ", "), Metrics[len(Metrics)-1])
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
	_, count := countMetrics[metric]
	if page != "" && known == 0 {
		if count {
			return fmt.Errorf("no %s counts for %s in the history; it keeps the first %d pages of each run with the %s module on", metric, page, MaxPages, metric)
		}
		return fmt.Errorf("no timings for %s in the history; it keeps the first %d pages of each run", page, MaxPages)
	}
	if count {
		return renderCounts(w, entries, metric, page, values, plain)
	}
	col := strings.ToUpper(metric)
	if page == "" {
		col += " (median)"
	} else {
		col += " " + page
	}
	table(w, entries, col, func(i int) string { return text(ms(values[i])) }, text)
	fmt.Fprintf(w, "\n%s", strings.ToUpper(metric))
	if !plain {
		fmt.Fprintf(w, "  %s", Sparkline(values))
	}
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
		fmt.Fprintf(w, "  %s", text(ms(first)+" → "+ms(last)))
		if first > 0 {
			fmt.Fprintf(w, " (%+.0f%%)", (last-first)/first*100)
		}
	}
	fmt.Fprintln(w)
	return nil
}

// Series is what history --json prints: the metric's value in each
// entry, oldest first, then the entries themselves with every count
// (a11y_open, a11y_by_criterion, keyboard_open, the page timings).
type Series struct {
	Metric  string  `json:"metric"`
	Page    string  `json:"page,omitempty"`
	Points  []Point `json:"points"`
	Entries []Entry `json:"entries"`
}

// Point is one entry's value of the metric. Value is null when the entry
// has none (see Entry.Value).
type Point struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Name    string    `json:"name,omitempty"`
	Target  string    `json:"target"`
	Verdict string    `json:"verdict"`
	Value   *float64  `json:"value"`
}

// JSON writes the series of metric (lcp when empty), for page when it
// is set, as Render would show it. It fails as Render does on a metric
// it doesn't know and on a page with no values.
func JSON(w io.Writer, entries []Entry, page, metric string) error {
	if metric == "" {
		metric = "lcp"
	}
	if !slices.Contains(Metrics, metric) {
		return fmt.Errorf("metric %q: want %s or %s", metric, strings.Join(Metrics[:len(Metrics)-1], ", "), Metrics[len(Metrics)-1])
	}
	out := Series{Metric: metric, Page: page, Points: []Point{}, Entries: entries}
	if out.Entries == nil {
		out.Entries = []Entry{}
	}
	known := 0
	for _, e := range entries {
		pt := Point{At: e.At, Kind: e.Kind, Name: e.Name, Target: e.Target, Verdict: e.Verdict}
		if v := e.Value(metric, page); !math.IsNaN(v) {
			pt.Value = &v
			known++
		}
		out.Points = append(out.Points, pt)
	}
	if page != "" && known == 0 && len(entries) > 0 {
		if _, count := countMetrics[metric]; count {
			return fmt.Errorf("no %s counts for %s in the history; it keeps the first %d pages of each run with the %s module on", metric, page, MaxPages, metric)
		}
		return fmt.Errorf("no timings for %s in the history; it keeps the first %d pages of each run", page, MaxPages)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// table prints the entries' rows with the metric's column last.
func table(w io.Writer, entries []Entry, col string, value func(int) string, text func(string) string) {
	nameW := 4
	for _, e := range entries {
		nameW = max(nameW, min(24, utf8.RuneCountInString(e.Name)))
	}
	row := func(when, kind, name, pages, errs, warns, verdict, value string) {
		fmt.Fprintf(w, "%-17s  %-10s  %-*s  %5s  %6s  %8s  %-8s  %s\n", when, kind, nameW, name, pages, errs, warns, verdict, value)
	}
	row("When", "Kind", "Name", "Pages", "Errors", "Warnings", "Verdict", col)
	for i, e := range entries {
		row(e.At.Local().Format("2006-01-02 15:04"), e.Kind, text(truncate(e.Name, nameW)), fmt.Sprint(e.Pages),
			fmt.Sprint(e.Issues["error"]), fmt.Sprint(e.Issues["warning"]), e.Verdict, value(i))
	}
}

// renderCounts is Render for a count metric (a11y or keyboard): the
// open problems per entry, "-" for a run without the module, then
// "A11y open  188 to 64 (-66%)  ▇▇▆▆▁" and, for a11y, the criteria with
// the most problems in the latest entry that has them:
// "Worst criteria now: 1.4.3 Contrast (31), 4.1.2 Name, Role, Value (12)".
// With page set the counts are that page's ("A11y open /shop/") and
// the criteria, which are counted for the whole site, are left out.
func renderCounts(w io.Writer, entries []Entry, metric, page string, values []float64, plain bool) error {
	col := countColumns[metric]
	if page != "" {
		col += " " + page
	}
	text := func(s string) string { return s }
	if plain {
		text = strings.NewReplacer("…", "...").Replace
	}
	table(w, entries, col, func(i int) string {
		if math.IsNaN(values[i]) {
			return "-"
		}
		return fmt.Sprintf("%.0f", values[i])
	}, text)
	var known []float64
	for _, v := range values {
		if !math.IsNaN(v) {
			known = append(known, v)
		}
	}
	fmt.Fprintf(w, "\n%s", col)
	switch len(known) {
	case 0:
		fmt.Fprintf(w, "  not measured: no run had the %s module on (--eaa turns it on)", metric)
	case 1:
		fmt.Fprintf(w, "  %.0f", known[0])
	default:
		first, last := known[0], known[len(known)-1]
		fmt.Fprintf(w, "  %.0f to %.0f", first, last)
		if first > 0 {
			fmt.Fprintf(w, " (%+.0f%%)", (last-first)/first*100)
		}
	}
	if !plain && len(known) > 0 {
		fmt.Fprintf(w, "  %s", Sparkline(values))
	}
	fmt.Fprintln(w)
	if metric == "a11y" && page == "" {
		if worst := worstCriteria(entries, 3); worst != "" {
			fmt.Fprintf(w, "Worst criteria now: %s\n", worst)
		}
	}
	return nil
}

// worstCriteria names the n criteria with the most open problems in the
// latest entry that counted them, most first (ties by id), titled from
// the WCAG catalog: "1.4.3 Contrast (31)". An entry doesn't record its
// WCAG version, so the titles come from every version (a criterion
// keeps its number and title across them).
func worstCriteria(entries []Entry, n int) string {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.A11yOpen == nil {
			continue
		}
		type crit struct {
			id string
			n  int
		}
		var list []crit
		for id, c := range e.A11yByCriterion {
			// The file is ours; still, an id is printed as it is, so only
			// numbers and dots make it to the terminal.
			if c > 0 && id != "" && strings.Trim(id, "0123456789.") == "" {
				list = append(list, crit{id, c})
			}
		}
		slices.SortFunc(list, func(a, b crit) int {
			if a.n != b.n {
				return b.n - a.n
			}
			return wcag.Compare(a.id, b.id)
		})
		titles := map[string]string{}
		for _, v := range wcag.Versions {
			for _, c := range wcag.Criteria(v) {
				titles[c.ID] = shortTitle(c.Title)
			}
		}
		var parts []string
		for _, c := range list[:min(n, len(list))] {
			name := c.id
			if t := titles[c.id]; t != "" {
				name += " " + t
			}
			parts = append(parts, fmt.Sprintf("%s (%d)", name, c.n))
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

// shortTitle drops a criterion title's trailing note in brackets:
// "Contrast (Minimum)" is "Contrast".
func shortTitle(t string) string {
	if i := strings.LastIndex(t, " ("); i > 0 && strings.HasSuffix(t, ")") {
		return t[:i]
	}
	return t
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
