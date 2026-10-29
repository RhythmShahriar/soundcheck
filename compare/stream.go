// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"context"
	"slices"

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

// PageComparer is check.PageComparer: a module that compares a pair of
// pages at a time.
type PageComparer = check.PageComparer

// PairSink is check.PairSink: what a PageComparer's comparison gets.
type PairSink = check.PairSink

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
	moduleWarn, moduleWarnings := collect()
	c.WarnFunc = moduleWarn
	cc = &c
	out := start(cc, version, after.Len()-dropped, warnings, droppedBefore, dropped)
	warn := func(msg string) { out.Warnings = append(out.Warnings, msg) }

	var sinks []*sink
	for _, m := range modules {
		if pc, ok := m.(PageComparer); ok {
			safe(m.Name(), "ComparePages", warn, func() { sinks = append(sinks, &sink{m.Name(), pc.ComparePages(ctx, cc)}) })
		}
	}

	if err := feedBefore(before, sinks, warn); err != nil {
		return nil, err
	}

	ran, listed := ranInBoth(cc.Before, cc.After), func(m string) bool { return slices.Contains(cc.After.Options.Modules, m) }
	d := newIssueDiff(rulesOf(cc.Before, cc.After, ran), keptFrom(cc.Before))
	diffedMod := diffedIn(cc.Before, cc.After)
	diffed := map[string]bool{}
	for i := range after.Len() {
		if after.Canceled(i) {
			continue
		}
		path := after.Path(i)
		bs, as := live(before, path), live(after, path)
		a, err := after.Page(i)
		if err != nil {
			return nil, err
		}
		if len(bs) == 0 {
			if !diffed[path] {
				diffed[path] = true
				_, aaud, err := issuesOf(after, as, a)
				if err != nil {
					return nil, err
				}
				d.newPage(path, aaud, diffedMod)
			}
			continue
		}
		b, err := before.Page(bs[0])
		if err != nil {
			return nil, err
		}
		if !diffed[path] {
			diffed[path] = true
			bi, baud, err := issuesOf(before, bs, b)
			if err != nil {
				return nil, err
			}
			ai, aaud, err := issuesOf(after, as, a)
			if err != nil {
				return nil, err
			}
			d.page(path, bi, ai, aaud, a.Keyboard, gaps(path, baud, aaud, diffedMod, listed, d.kept), d.guard(bi, baud, aaud))
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
	aside(out, compared(d.ignoredChanges(), cc, modules), d.sortedNotes())
	for _, s := range sinks {
		if s.s != nil {
			safe(s.name, "Changes", warn, func() { out.Changes = append(out.Changes, s.s.Changes()...) })
		}
	}
	out.Warnings = append(out.Warnings, moduleWarnings()...)
	return finish(out, cc, modules), nil
}

// sink is one module's PairSink in a Stream; s is nil once it panicked.
type sink struct {
	name string
	s    PairSink
}

// feedBefore gives every page of the earlier run that wasn't canceled to
// the sinks that read them all (check.BeforeSink), in stored order. A
// sink that panics is dropped with a warning, as in Stream.
func feedBefore(before PageSource, sinks []*sink, warn func(string)) error {
	var want []*sink
	for _, s := range sinks {
		if _, ok := s.s.(check.BeforeSink); ok {
			want = append(want, s)
		}
	}
	if len(want) == 0 {
		return nil
	}
	for i := range before.Len() {
		if before.Canceled(i) {
			continue
		}
		p, err := before.Page(i)
		if err != nil {
			return err
		}
		for _, s := range want {
			if s.s == nil {
				continue
			}
			ok := false
			safe(s.name, "Before", warn, func() { s.s.(check.BeforeSink).Before(p); ok = true })
			if !ok {
				s.s = nil
			}
		}
	}
	return nil
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

// issuesOf returns the issues and audits of the pages at positions at,
// of which the first is already read as first. Issues diffs every page
// with a path when it's there twice; nearly always it's there once.
func issuesOf(src PageSource, at []int, first *model.Page) ([]model.Issue, map[string]model.Audit, error) {
	if len(at) == 1 {
		return first.Issues, first.Audits, nil
	}
	issues := append([]model.Issue(nil), first.Issues...)
	audits := mergeAudits(nil, first.Audits)
	for _, i := range at[1:] {
		p, err := src.Page(i)
		if err != nil {
			return nil, nil, err
		}
		issues = append(issues, p.Issues...)
		audits = mergeAudits(audits, p.Audits)
	}
	return issues, audits, nil
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
