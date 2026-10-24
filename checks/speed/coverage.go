// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package speed

import (
	"cmp"
	"slices"

	"github.com/go-rod/rod/lib/proto"
)

// usage is how much of one file the page used, in characters of its
// decoded text.
type usage struct{ used, total int }

// unusedShare returns the fraction of the file the page never used.
func (u usage) unusedShare() float64 {
	if u.total <= 0 {
		return 0
	}
	return float64(max(0, u.total-u.used)) / float64(u.total)
}

// jsUsage turns V8 block coverage into used/total characters per script
// URL. Inline and eval'd scripts (no URL, or the page's own URL) are
// left to the caller to ignore.
func jsUsage(scripts []*proto.ProfilerScriptCoverage) map[string]usage {
	out := map[string]usage{}
	for _, s := range scripts {
		if s == nil || s.URL == "" {
			continue
		}
		var ranges []*proto.ProfilerCoverageRange
		for _, f := range s.Functions {
			ranges = append(ranges, f.Ranges...)
		}
		used, total := usedChars(ranges)
		u := out[s.URL]
		u.used += used
		u.total += total
		out[s.URL] = u
	}
	return out
}

// usedChars counts characters covered by a range with a non-zero count.
// V8 reports nested ranges: the script's top-level function spans the
// whole file, and inner ranges override their parents (a function never
// called is a count-0 range inside a count-1 one). A sweep over range
// boundaries with a stack of open ranges resolves the innermost count at
// every offset, the way DevTools and Puppeteer do.
func usedChars(ranges []*proto.ProfilerCoverageRange) (used, total int) {
	type point struct {
		at    int
		end   bool
		size  int
		count int
	}
	var pts []point
	for _, r := range ranges {
		if r == nil || r.EndOffset <= r.StartOffset {
			continue
		}
		total = max(total, r.EndOffset)
		size := r.EndOffset - r.StartOffset
		pts = append(pts, point{at: r.StartOffset, size: size, count: r.Count}, point{at: r.EndOffset, end: true, size: size})
	}
	slices.SortFunc(pts, func(a, b point) int {
		if c := cmp.Compare(a.at, b.at); c != 0 {
			return c
		}
		if a.end != b.end { // close ranges before opening new ones at the same offset
			if a.end {
				return -1
			}
			return 1
		}
		if a.end { // inner (shorter) ranges close first
			return cmp.Compare(a.size, b.size)
		}
		return cmp.Compare(b.size, a.size) // outer (longer) ranges open first
	})
	var stack []int
	last := 0
	for _, p := range pts {
		if n := len(stack); n > 0 && stack[n-1] > 0 && p.at > last {
			used += p.at - last
		}
		last = p.at
		if p.end {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		} else {
			stack = append(stack, p.count)
		}
	}
	return used, total
}

// cssUsage turns rule usage into used/total characters per stylesheet
// URL. Like DevTools, everything outside a used rule (unused rules,
// comments, whitespace) counts as unused.
func cssUsage(sheets map[proto.CSSStyleSheetID]*proto.CSSCSSStyleSheetHeader, rules []*proto.CSSRuleUsage) map[string]usage {
	used := map[proto.CSSStyleSheetID]int{}
	for _, r := range rules {
		if r != nil && r.Used && r.EndOffset > r.StartOffset {
			used[r.StyleSheetID] += int(r.EndOffset - r.StartOffset)
		}
	}
	out := map[string]usage{}
	for id, h := range sheets {
		if h == nil || h.IsInline || h.SourceURL == "" || h.Length <= 0 {
			continue
		}
		u := out[h.SourceURL]
		u.total += int(h.Length)
		u.used += min(used[id], int(h.Length))
		out[h.SourceURL] = u
	}
	return out
}
