// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package compare turns two runs into a ranked list of what changed: new
// errors are BROKE, other new problems NEW, problems that went away
// FIXED, plus whatever the modules' comparers report (visual changes,
// slowdowns, lost content).
package compare

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Compare diffs cc.Before and cc.After.
//
// Pages whose visit was canceled (the run was stopped) are left out:
// they recorded nothing real, and comparing them would report the site
// as broken. The comparison is then marked as interrupted.
func Compare(ctx context.Context, cc *check.CompareContext, modules []check.Module, version string) *model.Comparison {
	// Settings and pages missing from the run are judged on the runs as
	// given: a canceled page was reached, so it isn't counted as missing.
	warnings := mismatch(cc.Before, cc.After)
	before, droppedBefore := withoutCanceled(cc.Before)
	after, dropped := withoutCanceled(cc.After)
	if droppedBefore+dropped > 0 {
		c := *cc
		c.Before, c.After = before, after
		cc = &c
	}
	out := &model.Comparison{
		Schema: model.Schema, Tool: "rhythm-soundcheck", Version: version, Created: time.Now().UTC(),
		Before: ref(cc.Before, cc.BeforeDir), After: ref(cc.After, cc.AfterDir), Pages: len(cc.After.Pages),
	}
	// The run's own notes (a skipped leak test, unreadable flows) belong in
	// the comparison too, so JSON readers see them.
	out.Warnings = append(warnings, cc.After.Warnings...)
	var notes []string
	if out.Interrupted = cc.After.Interrupted || dropped > 0; out.Interrupted {
		note := "interrupted: the run was stopped before it finished; pages it didn't reach weren't checked"
		if dropped > 0 {
			note = "interrupted: " + pagesNotChecked(dropped)
		}
		notes = append(notes, note)
	}
	if droppedBefore > 0 {
		notes = append(notes, fmt.Sprintf("the snapshot was interrupted: %s in it weren't finished and aren't compared", plural(droppedBefore)))
	}
	out.Warnings = append(notes, out.Warnings...)
	out.Changes = Issues(cc.Before, cc.After)
	// A module that ran only once has nothing to compare against: all its
	// issues would look new (or fixed).
	if b, a := cc.Before.Options.Modules, cc.After.Options.Modules; len(b) > 0 && len(a) > 0 {
		out.Changes = slices.DeleteFunc(out.Changes, func(c model.Change) bool {
			mod, _, _ := strings.Cut(c.Check, "/")
			return !slices.Contains(b, mod) || !slices.Contains(a, mod)
		})
	}
	for _, m := range modules {
		if _, ok := m.(check.IssueOwner); ok {
			out.Changes = slices.DeleteFunc(out.Changes, func(c model.Change) bool {
				return strings.HasPrefix(c.Check, m.Name()+"/")
			})
		}
	}
	warn := func(msg string) { out.Warnings = append(out.Warnings, msg) }
	for _, m := range modules {
		if c, ok := m.(check.Comparer); ok {
			safe(m.Name(), "Compare", warn, func() { out.Changes = append(out.Changes, c.Compare(ctx, cc)...) })
		}
	}
	for _, m := range modules {
		if f, ok := m.(check.ChangeFilter); ok {
			// The filter gets a copy: one that panics halfway through
			// filtering in place would otherwise leave a mangled list.
			safe(m.Name(), "FilterChanges", warn, func() { out.Changes = f.FilterChanges(slices.Clone(out.Changes)) })
		}
	}
	Sort(out.Changes)
	return out
}

// withoutCanceled returns r without the pages whose visit was canceled,
// and how many it left out. r itself is returned when there are none.
func withoutCanceled(r *model.Run) (*model.Run, int) {
	n := 0
	for i := range r.Pages {
		if r.Pages[i].Canceled() {
			n++
		}
	}
	if n == 0 {
		return r, 0
	}
	cp := *r
	cp.Pages = slices.DeleteFunc(slices.Clone(r.Pages), func(p model.Page) bool { return p.Canceled() })
	cp.Interrupted = true
	return &cp, n
}

func pagesNotChecked(n int) string {
	if n == 1 {
		return "1 page was not checked"
	}
	return fmt.Sprintf("%d pages were not checked", n)
}

func plural(n int) string {
	if n == 1 {
		return "1 page"
	}
	return fmt.Sprintf("%d pages", n)
}

func ref(r *model.Run, dir string) model.RunRef {
	return model.RunRef{Target: r.Target, Name: r.Name, Dir: dir, Started: r.Started}
}

// Issues diffs the issues of two runs. Issues are matched by key on the
// same page (page issues) or by key alone (site issues). A page that only
// exists in one run doesn't make its issues new or fixed.
func Issues(before, after *model.Run) []model.Change {
	type at struct {
		issue model.Issue
		pages []string
	}
	index := func(r *model.Run, other *model.Run) map[string]*at {
		m := map[string]*at{}
		for _, p := range r.Pages {
			if other.Page(p.Path) == nil {
				continue
			}
			for _, i := range p.Issues {
				k := i.Key + "@" + p.Path
				if _, ok := m[k]; !ok {
					m[k] = &at{issue: i, pages: []string{p.Path}}
				}
			}
		}
		for _, i := range r.Issues {
			m[i.Key+"@site"] = &at{issue: i, pages: i.Pages}
		}
		return m
	}
	b, a := index(before, after), index(after, before)

	// Group by issue key, so one PHP warning on 14 pages is one change.
	// Maps are walked in sorted key order and the detail comes from the
	// lowest page path, so the same two runs always give the same output.
	type group struct {
		change     model.Change
		pages      map[string]bool
		detailFrom string // the page the detail came from
	}
	groups := map[string]*group{}
	add := func(class string, x *at) {
		k := class + "|" + x.issue.Key
		g := groups[k]
		first := ""
		if len(x.pages) > 0 {
			first = slices.Min(x.pages)
		}
		if g == nil {
			g = &group{change: model.Change{Class: class, Check: x.issue.Check, Title: x.issue.Title, Detail: x.issue.Detail,
				Source: x.issue.Source, Fix: x.issue.Fix}, pages: map[string]bool{}, detailFrom: first}
			groups[k] = g
		} else if first < g.detailFrom {
			g.change.Detail, g.detailFrom = x.issue.Detail, first
		}
		for _, p := range x.pages {
			g.pages[p] = true
		}
	}
	for _, k := range sortedKeys(a) {
		if _, ok := b[k]; !ok {
			x := a[k]
			class := model.New
			if x.issue.Level == model.Error {
				class = model.Broke
			}
			add(class, x)
		}
	}
	for _, k := range sortedKeys(b) {
		if _, ok := a[k]; !ok {
			add(model.Fixed, b[k])
		}
	}
	out := make([]model.Change, 0, len(groups))
	for _, k := range sortedKeys(groups) {
		g := groups[k]
		g.change.Pages = sortedKeys(g.pages)
		g.change.Score = float64(len(g.change.Pages))
		out = append(out, g.change)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// Sort orders changes by class, then by score (biggest first). Ties are
// broken by check, source, first page, title and detail, so the order
// never depends on the order the changes were found in.
func Sort(cs []model.Change) {
	firstPage := func(c model.Change) string {
		if len(c.Pages) == 0 {
			return ""
		}
		return c.Pages[0]
	}
	slices.SortStableFunc(cs, func(x, y model.Change) int {
		return cmp.Or(
			cmp.Compare(slices.Index(model.Classes, x.Class), slices.Index(model.Classes, y.Class)),
			cmp.Compare(y.Score, x.Score),
			strings.Compare(x.Check, y.Check),
			strings.Compare(x.Source, y.Source),
			strings.Compare(firstPage(x), firstPage(y)),
			strings.Compare(x.Title, y.Title),
			strings.Compare(x.Detail, y.Detail))
	})
}

// mismatch warns when two runs weren't made the same way, because their
// differences may then come from the settings, not the site.
func mismatch(before, after *model.Run) []string {
	var w []string
	if !slices.Equal(before.Options.Viewports, after.Options.Viewports) {
		w = append(w, fmt.Sprintf("viewports differ (snapshot %v, now %v)", before.Options.Viewports, after.Options.Viewports))
	}
	if !slices.Equal(before.Options.Modules, after.Options.Modules) {
		w = append(w, fmt.Sprintf("modules differ (snapshot %v, now %v); only modules in both are compared", before.Options.Modules, after.Options.Modules))
	}
	if !slices.Equal(before.Options.Levels, after.Options.Levels) {
		w = append(w, fmt.Sprintf("interaction levels differ (snapshot %v, now %v)", before.Options.Levels, after.Options.Levels))
	}
	if before.Options.Echo && after.Options.Echo && before.Options.EchoMode != after.Options.EchoMode {
		w = append(w, fmt.Sprintf("Echo's mode differs (snapshot %s, now %s); messages may differ only because of it", before.Options.EchoMode, after.Options.EchoMode))
	}
	if before.Options.LoggedIn != after.Options.LoggedIn {
		w = append(w, "one run was logged in and the other wasn't")
	}
	missing := 0
	for _, p := range before.Pages {
		if after.Page(p.Path) == nil {
			missing++
		}
	}
	if missing > 0 {
		w = append(w, fmt.Sprintf("%d pages from the snapshot weren't checked this time", missing))
	}
	return w
}
