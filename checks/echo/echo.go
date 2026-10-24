// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package echo is the echo module: with Echo running (--echo), every
// successful form submit and every flow records the emails, SMS and
// webhooks it made the site send (see checks/interact and echolink). This
// module compares them with the snapshot's, action by action, using
// Echo's own comparison: a message that's no longer sent is BROKE, a new
// one NEW, a different recipient, subject, text or payload CHANGED.
//
// It isn't an IssueOwner: the problems Echo finds in messages are
// ordinary page issues (echo/…), and the generic issue diff reports them
// the usual way, a new error as BROKE. The one overlap, a form that now
// sends nothing, is tidied by FilterChanges.
package echo

import (
	"context"
	"slices"
	"strings"

	echocompare "github.com/RhythmShahriar/echo/compare"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Module is the echo module.
type Module struct{}

func (Module) Name() string { return "echo" }

// Compare diffs the messages of each action recorded in both runs: form
// submits on the same page, and flows.
func (m Module) Compare(ctx context.Context, cc *check.CompareContext) []model.Change {
	s := m.ComparePages(ctx, cc)
	for _, pair := range cc.Pairs() {
		s.Pair(pair[0], pair[1])
	}
	return s.Changes()
}

// ComparePages is Compare a pair of pages at a time, for runs too big to
// hold (check.PageComparer). The flows are compared at the end.
func (Module) ComparePages(_ context.Context, cc *check.CompareContext) check.PairSink {
	return &pairSink{cc: cc, g: &grouper{}}
}

// pairSink diffs each pair's form submits as it comes.
type pairSink struct {
	cc *check.CompareContext
	g  *grouper
}

// Pair compares the form submits of one page found in both runs.
func (s *pairSink) Pair(b, a *model.Page) { diff(s.g, b.Echo, a.Echo) }

// Changes compares the flows and returns every change.
func (s *pairSink) Changes() []model.Change {
	diff(s.g, s.cc.Before.Echo, s.cc.After.Echo)
	return s.g.list()
}

// diff compares actions matched by ID. An action only in one run (a form
// that didn't submit this time) is left to the interact module's issues.
func diff(g *grouper, before, after []model.EchoAction) {
	for _, a := range after {
		i := slices.IndexFunc(before, func(b model.EchoAction) bool { return b.Action == a.Action })
		if i < 0 {
			continue
		}
		// With several actions on one page, the title says which form,
		// so their changes don't merge.
		prefix := ""
		if n := count(after, a.Page); n > 1 {
			prefix = a.Label + ": "
		}
		for _, c := range echocompare.Messages(before[i].Messages, a.Messages) {
			g.add(model.Change{Class: c.Class, Check: "echo/" + c.Check, Title: prefix + c.Title, Detail: c.Detail,
				Source: a.Label, Fix: c.Fix, Before: c.Before, After: c.After}, a.Page)
		}
	}
}

func count(acts []model.EchoAction, page string) int {
	n := 0
	for _, a := range acts {
		if a.Page == page {
			n++
		}
	}
	return n
}

// FilterChanges drops the NEW "Form says sent, but no message went out"
// notice where this module already reports the lost messages as BROKE:
// it's the same news, and the BROKE lines say which messages.
func (Module) FilterChanges(cs []model.Change) []model.Change {
	lost := map[string]bool{}
	for _, c := range cs {
		if c.Class == model.Broke && c.Check == "echo/message/missing" {
			for _, p := range c.Pages {
				lost[p+"|"+c.Source] = true
			}
		}
	}
	if len(lost) == 0 {
		return cs
	}
	var out []model.Change
	for _, c := range cs {
		if c.Check == "echo/no-message" && c.Class == model.New {
			c.Pages = slices.DeleteFunc(slices.Clone(c.Pages), func(p string) bool { return lost[p+"|"+c.Source] })
			if len(c.Pages) == 0 {
				continue
			}
			c.Score = float64(len(c.Pages))
		}
		out = append(out, c)
	}
	return out
}

// grouper merges the same change on several pages (a newsletter form in
// every footer) into one, as the issue diff does.
type grouper struct {
	order []string
	by    map[string]*model.Change
}

func (g *grouper) add(c model.Change, page string) {
	if g.by == nil {
		g.by = map[string]*model.Change{}
	}
	k := strings.Join([]string{c.Class, c.Check, c.Title, c.Source, c.Before, c.After}, "|")
	have, ok := g.by[k]
	if !ok {
		c.Pages = nil
		have = &c
		g.by[k] = have
		g.order = append(g.order, k)
	}
	if !slices.Contains(have.Pages, page) {
		have.Pages = append(have.Pages, page)
	}
}

func (g *grouper) list() []model.Change {
	out := make([]model.Change, 0, len(g.order))
	for _, k := range g.order {
		c := *g.by[k]
		slices.Sort(c.Pages)
		c.Score = float64(len(c.Pages))
		out = append(out, c)
	}
	return out
}
