// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"context"

	"github.com/RhythmShahriar/soundcheck/model"
)

// PageComparer is a Comparer that can also compare two runs a pair of
// pages at a time, so a big comparison (compare.Stream) never holds
// either run whole. A module implements it next to Compare and must
// report the same changes.
type PageComparer interface {
	Comparer
	// ComparePages starts a comparison. cc.Before and cc.After are the
	// runs without their pages (site issues, Echo flows, options); the
	// returned PairSink then gets the pairs Pairs would give, one at a
	// time and in that order.
	ComparePages(ctx context.Context, cc *CompareContext) PairSink
}

// PairSink takes the page pairs of one comparison and then gives its
// changes. It keeps what it needs of a pair, not the pages.
type PairSink interface {
	Pair(before, after *model.Page)
	Changes() []model.Change
}

// BeforeSink is a PairSink that also reads every page of the earlier
// run, paired or not, as Compare can through cc.Before.Pages. The
// comparison gives it those pages (without the canceled ones, in stored
// order) before the first pair.
type BeforeSink interface {
	PairSink
	Before(p *model.Page)
}
