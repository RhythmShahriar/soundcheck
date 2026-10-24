// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// bigRuns builds two runs of n pages with ten issues each, all different,
// so every issue is new or fixed: the worst case for the issue diff. A
// few issues repeat on every page, to group across all of them.
func bigRuns(n int) (before, after *model.Run) {
	before, after = &model.Run{}, &model.Run{}
	for i := range n {
		path := fmt.Sprintf("/page-%d/", i)
		var b, a []model.Issue
		for j := range 10 {
			title := fmt.Sprintf("issue %d", j)
			if j >= 3 {
				title = fmt.Sprintf("issue %d on %d", j, i)
			}
			b = append(b, issue("health/php-error", model.Warning, "old "+title))
			a = append(a, issue("health/php-error", model.Warning, "new "+title))
		}
		before.Pages = append(before.Pages, model.Page{Path: path, Issues: b})
		after.Pages = append(after.Pages, model.Page{Path: path, Issues: a})
	}
	return before, after
}

func BenchmarkCompare(b *testing.B) {
	for _, n := range []int{300, 3000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			before, after := bigRuns(n)
			for b.Loop() {
				Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
			}
		})
	}
}

// timeCompare is the fastest of a few comparisons of n pages, to keep a
// busy machine's noise out of the ratio.
func timeCompare(n int) time.Duration {
	before, after := bigRuns(n)
	best := time.Duration(1<<63 - 1)
	for range 3 {
		start := time.Now()
		c := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
		best = min(best, time.Since(start))
		if len(c.Changes) != 2*(3+7*n) {
			panic(fmt.Sprintf("%d changes", len(c.Changes)))
		}
	}
	return best
}

func TestCompareIsLinear(t *testing.T) {
	// The race detector slows some steps far more than others, so the
	// ratio below would measure it rather than Compare.
	if testing.Short() || raceEnabled {
		t.Skip("timing test")
	}
	budget := 2 * time.Second
	small, big := timeCompare(1000), timeCompare(10000)
	if big > budget {
		t.Errorf("10,000 pages took %s, want under %s", big, budget)
	}
	// Ten times the pages should take about ten times as long; quadratic
	// code takes about a hundred.
	if ratio := float64(big) / float64(max(small, time.Millisecond)); ratio > 30 {
		t.Errorf("1,000 pages %s, 10,000 pages %s: %.0fx slower, want roughly linear", small, big, ratio)
	}
}
