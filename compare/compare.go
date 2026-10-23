// Package compare turns two runs into a ranked list of what changed: new
// errors are BROKE, other new problems NEW, problems that went away
// FIXED, plus whatever the modules' comparers report (visual changes,
// slowdowns, lost content).
package compare

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Compare diffs cc.Before and cc.After.
func Compare(ctx context.Context, cc *check.CompareContext, modules []check.Module, version string) *model.Comparison {
	out := &model.Comparison{
		Schema: model.Schema, Tool: "rhythm-soundcheck", Version: version, Created: time.Now().UTC(),
		Before: ref(cc.Before, cc.BeforeDir), After: ref(cc.After, cc.AfterDir), Pages: len(cc.After.Pages),
	}
	out.Warnings = mismatch(cc.Before, cc.After)
	out.Changes = Issues(cc.Before, cc.After)
	for _, m := range modules {
		if c, ok := m.(check.Comparer); ok {
			out.Changes = append(out.Changes, c.Compare(ctx, cc)...)
		}
	}
	Sort(out.Changes)
	return out
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
	groups := map[string]*model.Change{}
	add := func(class string, x *at) {
		k := class + "|" + x.issue.Key
		g := groups[k]
		if g == nil {
			g = &model.Change{Class: class, Check: x.issue.Check, Title: x.issue.Title, Detail: x.issue.Detail,
				Source: x.issue.Source, Fix: x.issue.Fix}
			groups[k] = g
		}
		for _, p := range x.pages {
			if !slices.Contains(g.Pages, p) {
				g.Pages = append(g.Pages, p)
			}
		}
	}
	for k, x := range a {
		if _, ok := b[k]; !ok {
			class := model.New
			if x.issue.Level == model.Error {
				class = model.Broke
			}
			add(class, x)
		}
	}
	for k, x := range b {
		if _, ok := a[k]; !ok {
			add(model.Fixed, x)
		}
	}
	var out []model.Change
	for _, g := range groups {
		slices.Sort(g.Pages)
		g.Score = float64(len(g.Pages))
		out = append(out, *g)
	}
	return out
}

// Sort orders changes by class, then by score (biggest first), then title.
func Sort(cs []model.Change) {
	slices.SortStableFunc(cs, func(x, y model.Change) int {
		return cmp.Or(
			cmp.Compare(slices.Index(model.Classes, x.Class), slices.Index(model.Classes, y.Class)),
			cmp.Compare(y.Score, x.Score),
			strings.Compare(x.Title, y.Title))
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
		w = append(w, fmt.Sprintf("modules differ (snapshot %v, now %v)", before.Options.Modules, after.Options.Modules))
	}
	if !slices.Equal(before.Options.Levels, after.Options.Levels) {
		w = append(w, fmt.Sprintf("interaction levels differ (snapshot %v, now %v)", before.Options.Levels, after.Options.Levels))
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
