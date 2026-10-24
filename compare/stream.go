// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"context"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// PageSource is a run's pages, read one at a time: store.Pages for a
// stored run, InMemory for one that is in memory anyway (the run just
// made).
type PageSource interface {
	Len() int
	Path(i int) string
	Canceled(i int) bool // see model.Page.Canceled
	Page(i int) (*model.Page, error)
	// Lookup returns the positions of the pages with path, in order.
	Lookup(path string) []int
}

// PageComparer is a Comparer that can also compare two runs a pair of
// pages at a time, so Stream never holds either run whole. A module
// implements it next to Compare and must report the same changes.
type PageComparer interface {
	check.Comparer
	// ComparePages starts a comparison. cc.Before and cc.After are the
	// runs without their pages (site issues, Echo flows, options); the
	// returned PairSink then gets the pairs Pairs would give, one at a
	// time and in that order.
	ComparePages(ctx context.Context, cc *check.CompareContext) PairSink
}

// PairSink takes the page pairs of one comparison and then gives its
// changes. It keeps what it needs of a pair, not the pages.
type PairSink interface {
	Pair(before, after *model.Page)
	Changes() []model.Change
}

// Stream is Compare for runs too big to hold: cc.Before and cc.After are
// the runs without their pages (as store.OpenRun gives them), before and
// after their pages. It reads the pages pairwise by path and keeps only
// the changes, so its memory doesn't grow with the pages' size. The
// result is the one Compare gives for the same runs.
//
// Modules that compare pages must be PageComparers for that. When one
// isn't, Stream reads both runs whole and calls Compare: slower on
// memory, never different. An error is a page that couldn't be read.
func Stream(ctx context.Context, cc *check.CompareContext, before, after PageSource, modules []check.Module, version string) (*model.Comparison, error) {
	if !streamable(modules) {
		var err error
		c := *cc
		if c.Before, err = whole(cc.Before, before); err != nil {
			return nil, err
		}
		if c.After, err = whole(cc.After, after); err != nil {
			return nil, err
		}
		return Compare(ctx, &c, modules, version), nil
	}

	missing := 0
	for i := range before.Len() {
		if len(after.Lookup(before.Path(i))) == 0 {
			missing++
		}
	}
	warnings := mismatchWith(cc.Before, cc.After, missing)
	droppedBefore, dropped := canceled(before), canceled(after)
	c := *cc
	c.Before, c.After = head(cc.Before, droppedBefore), head(cc.After, dropped)
	cc = &c
	out := start(cc, version, after.Len()-dropped, warnings, droppedBefore, dropped)
	warn := func(msg string) { out.Warnings = append(out.Warnings, msg) }

	type sink struct {
		name string
		s    PairSink
	}
	var sinks []*sink
	for _, m := range modules {
		if pc, ok := m.(PageComparer); ok {
			safe(m.Name(), "ComparePages", warn, func() { sinks = append(sinks, &sink{m.Name(), pc.ComparePages(ctx, cc)}) })
		}
	}

	d := issueDiff{groups: map[string]*issueGroup{}}
	diffed := map[string]bool{}
	for i := range after.Len() {
		if after.Canceled(i) {
			continue
		}
		path := after.Path(i)
		bs, as := live(before, path), live(after, path)
		if len(bs) == 0 {
			continue
		}
		a, err := after.Page(i)
		if err != nil {
			return nil, err
		}
		b, err := before.Page(bs[0])
		if err != nil {
			return nil, err
		}
		if !diffed[path] {
			diffed[path] = true
			bi, err := issuesOf(before, bs, b)
			if err != nil {
				return nil, err
			}
			ai, err := issuesOf(after, as, a)
			if err != nil {
				return nil, err
			}
			d.page(path, bi, ai)
		}
		for _, s := range sinks {
			if s.s == nil {
				continue
			}
			ok := false
			safe(s.name, "Pair", warn, func() { s.s.Pair(b, a); ok = true })
			if !ok {
				s.s = nil // a panic costs the module its changes, as in Compare
			}
		}
	}
	d.site(cc.Before.Issues, cc.After.Issues)
	out.Changes = compared(d.changes(), cc, modules)
	for _, s := range sinks {
		if s.s != nil {
			safe(s.name, "Changes", warn, func() { out.Changes = append(out.Changes, s.s.Changes()...) })
		}
	}
	return finish(out, cc, modules), nil
}

// streamable reports whether every module that compares runs can do it a
// page pair at a time.
func streamable(modules []check.Module) bool {
	for _, m := range modules {
		if _, ok := m.(check.Comparer); ok {
			if _, ok := m.(PageComparer); !ok {
				return false
			}
		}
	}
	return true
}

// whole returns r with every page of src.
func whole(r *model.Run, src PageSource) (*model.Run, error) {
	cp := *r
	cp.Pages = make([]model.Page, 0, src.Len())
	for i := range src.Len() {
		p, err := src.Page(i)
		if err != nil {
			return nil, err
		}
		cp.Pages = append(cp.Pages, *p)
	}
	return &cp, nil
}

// head is a run without pages for the modules, marked as interrupted
// when dropped of its pages were canceled, as withoutCanceled marks it.
func head(r *model.Run, dropped int) *model.Run {
	cp := *r
	cp.Pages = nil
	if dropped > 0 {
		cp.Interrupted = true
	}
	return &cp
}

func canceled(src PageSource) int {
	n := 0
	for i := range src.Len() {
		if src.Canceled(i) {
			n++
		}
	}
	return n
}

// live returns the positions of the pages with path whose visit wasn't
// canceled.
func live(src PageSource, path string) []int {
	at := src.Lookup(path)
	for _, i := range at {
		if src.Canceled(i) {
			out := make([]int, 0, len(at))
			for _, j := range at {
				if !src.Canceled(j) {
					out = append(out, j)
				}
			}
			return out
		}
	}
	return at
}

// issuesOf returns the issues of the pages at positions at, of which the
// first is already read as first. Issues diffs every page with a path
// when it's there twice; nearly always it's there once.
func issuesOf(src PageSource, at []int, first *model.Page) ([]model.Issue, error) {
	if len(at) == 1 {
		return first.Issues, nil
	}
	issues := append([]model.Issue(nil), first.Issues...)
	for _, i := range at[1:] {
		p, err := src.Page(i)
		if err != nil {
			return nil, err
		}
		issues = append(issues, p.Issues...)
	}
	return issues, nil
}

// InMemory is the PageSource of pages already in memory.
func InMemory(pages []model.Page) PageSource {
	m := memPages{pages: pages, byPath: make(map[string][]int, len(pages))}
	for i := range pages {
		m.byPath[pages[i].Path] = append(m.byPath[pages[i].Path], i)
	}
	return m
}

type memPages struct {
	pages  []model.Page
	byPath map[string][]int
}

func (m memPages) Len() int                        { return len(m.pages) }
func (m memPages) Path(i int) string               { return m.pages[i].Path }
func (m memPages) Canceled(i int) bool             { return m.pages[i].Canceled() }
func (m memPages) Page(i int) (*model.Page, error) { return &m.pages[i], nil }
func (m memPages) Lookup(path string) []int        { return m.byPath[path] }
