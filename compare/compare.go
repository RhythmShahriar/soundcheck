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
	out := start(cc, version, len(cc.After.Pages), warnings, droppedBefore, dropped)
	out.Changes = compared(Issues(cc.Before, cc.After), cc, modules)
	warn := func(msg string) { out.Warnings = append(out.Warnings, msg) }
	for _, m := range modules {
		if c, ok := m.(check.Comparer); ok {
			safe(m.Name(), "Compare", warn, func() { out.Changes = append(out.Changes, c.Compare(ctx, cc)...) })
		}
	}
	return finish(out, cc, modules)
}

// start begins the comparison of cc's runs, of which the current one has
// pages pages that were checked: the warnings about how the runs differ,
// and what was interrupted. droppedBefore and dropped count the canceled
// pages left out of each run.
func start(cc *check.CompareContext, version string, pages int, warnings []string, droppedBefore, dropped int) *model.Comparison {
	out := &model.Comparison{
		Schema: model.Schema, Tool: "rhythm-soundcheck", Version: version, Created: time.Now().UTC(),
		Before: ref(cc.Before, cc.BeforeDir), After: ref(cc.After, cc.AfterDir), Pages: pages,
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
	return out
}

// compared drops the issue changes of modules that didn't run in both
// runs and of modules that report their own (check.IssueOwner).
func compared(changes []model.Change, cc *check.CompareContext, modules []check.Module) []model.Change {
	// A module that ran only once has nothing to compare against: all its
	// issues would look new (or fixed).
	if b, a := cc.Before.Options.Modules, cc.After.Options.Modules; len(b) > 0 && len(a) > 0 {
		changes = slices.DeleteFunc(changes, func(c model.Change) bool {
			mod, _, _ := strings.Cut(c.Check, "/")
			return !slices.Contains(b, mod) || !slices.Contains(a, mod)
		})
	}
	for _, m := range modules {
		if _, ok := m.(check.IssueOwner); ok {
			changes = slices.DeleteFunc(changes, func(c model.Change) bool {
				return strings.HasPrefix(c.Check, m.Name()+"/")
			})
		}
	}
	return changes
}

// finish runs the modules' change filters over every change, keeps one
// line per cause, sorts and completes the comparison.
func finish(out *model.Comparison, cc *check.CompareContext, modules []check.Module) *model.Comparison {
	warn := func(msg string) { out.Warnings = append(out.Warnings, msg) }
	for _, m := range modules {
		if f, ok := m.(check.ChangeFilter); ok {
			// The filter gets a copy: one that panics halfway through
			// filtering in place would otherwise leave a mangled list.
			safe(m.Name(), "FilterChanges", warn, func() { out.Changes = f.FilterChanges(slices.Clone(out.Changes)) })
		}
	}
	out.Changes = oneCause(out.Changes, siteIssue(cc.Before, cc.After))
	Sort(out.Changes)
	out.Complete() // IDs follow the sorted order, so they're deterministic too
	return out
}

// siteIssue reports whether a change comes from a site-level issue of
// either run (a broken link, found once with the pages that link to it)
// rather than from something on the pages it lists.
func siteIssue(runs ...*model.Run) func(model.Change) bool {
	keys := map[string]bool{}
	for _, r := range runs {
		for _, i := range r.Issues {
			keys[i.Check+"|"+i.Title+"|"+i.Source] = true
		}
	}
	return func(c model.Change) bool { return keys[c.Check+"|"+c.Title+"|"+c.Source] }
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
	d := issueDiff{groups: map[string]*issueGroup{}}
	// Every page with a path counts, when a path is there twice.
	byPath := func(r *model.Run) map[string][]model.Issue {
		m := map[string][]model.Issue{}
		for _, p := range r.Pages {
			m[p.Path] = append(m[p.Path], p.Issues...)
		}
		return m
	}
	b, a := byPath(before), byPath(after)
	for path, ai := range a {
		if bi, ok := b[path]; ok {
			d.page(path, bi, ai)
		}
	}
	d.site(before.Issues, after.Issues)
	return d.changes()
}

// issueDiff collects the changes Issues reports a path at a time, so
// Stream can build them without holding either run. One PHP warning on
// 14 pages is one change. The result doesn't depend on the order paths
// come in: pages are a set; the detail comes from the lowest page
// path (a page's issue before a site issue listing the same page).
type issueDiff struct{ groups map[string]*issueGroup }

type issueGroup struct {
	change     model.Change
	pages      map[string]bool
	detailFrom string // the lowest page path seen, where the detail came from
	detailSite bool   // the detail came from a site issue
}

// issueAt is an issue found on pages, or on the site (a broken link,
// which re-visiting pages doesn't check again).
type issueAt struct {
	issue model.Issue
	pages []string
	site  bool
}

func (d *issueDiff) add(class string, x issueAt) {
	k := class + "|" + x.issue.Key
	g := d.groups[k]
	first := ""
	if len(x.pages) > 0 {
		first = slices.Min(x.pages)
	}
	if g == nil {
		g = &issueGroup{change: model.Change{Class: class, Check: x.issue.Check, Title: x.issue.Title, Detail: x.issue.Detail,
			Source: x.issue.Source, Fix: x.issue.Fix, FromPageIssue: !x.site}, pages: map[string]bool{}, detailFrom: first, detailSite: x.site}
		d.groups[k] = g
	} else if first < g.detailFrom || (first == g.detailFrom && g.detailSite && !x.site) {
		g.change.Detail, g.detailFrom, g.detailSite = x.issue.Detail, first, x.site
	}
	g.change.FromPageIssue = g.change.FromPageIssue && !x.site
	for _, p := range x.pages {
		g.pages[p] = true
	}
}

// page diffs the issues of every page with path in each run. The first
// issue with a key is the one reported.
func (d *issueDiff) page(path string, before, after []model.Issue) {
	byKey := func(is []model.Issue) map[string]model.Issue {
		m := make(map[string]model.Issue, len(is))
		for _, i := range is {
			if _, ok := m[i.Key]; !ok {
				m[i.Key] = i
			}
		}
		return m
	}
	b, a := byKey(before), byKey(after)
	for k, i := range a {
		if _, ok := b[k]; !ok {
			d.add(appeared(i), issueAt{issue: i, pages: []string{path}})
		}
	}
	for k, i := range b {
		if _, ok := a[k]; !ok {
			d.add(model.Fixed, issueAt{issue: i, pages: []string{path}})
		}
	}
}

// site diffs the site-level issues. The last issue with a key is the
// one reported.
func (d *issueDiff) site(before, after []model.Issue) {
	byKey := func(is []model.Issue) map[string]model.Issue {
		m := make(map[string]model.Issue, len(is))
		for _, i := range is {
			m[i.Key] = i
		}
		return m
	}
	b, a := byKey(before), byKey(after)
	for k, i := range a {
		if _, ok := b[k]; !ok {
			d.add(appeared(i), issueAt{issue: i, pages: i.Pages, site: true})
		}
	}
	for k, i := range b {
		if _, ok := a[k]; !ok {
			d.add(model.Fixed, issueAt{issue: i, pages: i.Pages, site: true})
		}
	}
}

// appeared is the class of an issue that wasn't there before: BROKE for
// an error, NEW for the rest.
func appeared(i model.Issue) string {
	if i.Level == model.Error {
		return model.Broke
	}
	return model.New
}

// changes returns the collected changes, in a fixed order.
func (d *issueDiff) changes() []model.Change {
	out := make([]model.Change, 0, len(d.groups))
	for _, k := range sortedKeys(d.groups) {
		g := d.groups[k]
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
	missing := 0
	for _, p := range before.Pages {
		if after.Page(p.Path) == nil {
			missing++
		}
	}
	return mismatchWith(before, after, missing)
}

// mismatchWith is mismatch for runs whose pages aren't in memory: missing
// is how many pages of before have no page in after.
func mismatchWith(before, after *model.Run, missing int) []string {
	var w []string
	if note := versionNote(before.Version, after.Version); note != "" {
		w = append(w, note)
	}
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
	if missing > 0 {
		w = append(w, fmt.Sprintf("%d pages from the snapshot weren't checked this time", missing))
	}
	return w
}
