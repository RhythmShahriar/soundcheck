// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// oneCause is the last filter of a comparison: it keeps one line per
// cause, so a report counts breaks rather than their echoes.
//
// A page that is BROKE because it no longer loads or now answers 4xx/5xx
// shows the server's error page and whatever health notices that page
// raises (no favicon, a JS error in the error template) say nothing about
// the site. They are dropped for that page: NEW and CHANGED health/*
// changes lose it from their pages and go away when no page is left.
// Other BROKE changes stay and so do site-level issues such as broken
// links (site reports whether a change came from one).
//
// Then changes that read the same (same class, check, title, source,
// before, after and fix) are merged into one with the union of their
// pages. Changes with files (screenshots of one page) are never merged:
// the files would no longer match the pages.
func oneCause(cs []model.Change, site func(model.Change) bool) []model.Change {
	broken := map[string]bool{}
	for _, c := range cs {
		if c.Class == model.Broke && (c.Check == "health/unreachable" || c.Check == "health/status") {
			for _, p := range c.Pages {
				broken[p] = true
			}
		}
	}
	if len(broken) > 0 {
		cs = slices.DeleteFunc(cs, func(c model.Change) bool {
			return (c.Class == model.New || c.Class == model.Changed) && strings.HasPrefix(c.Check, "health/") && !site(c) &&
				len(c.Pages) > 0 && !slices.ContainsFunc(c.Pages, func(p string) bool { return !broken[p] })
		})
		for i, c := range cs {
			if (c.Class == model.New || c.Class == model.Changed) && strings.HasPrefix(c.Check, "health/") && !site(c) &&
				slices.ContainsFunc(c.Pages, func(p string) bool { return broken[p] }) {
				cs[i].Pages = slices.DeleteFunc(slices.Clone(c.Pages), func(p string) bool { return broken[p] })
				if cs[i].Score == float64(len(c.Pages)) { // a page count, as Issues sets it
					cs[i].Score = float64(len(cs[i].Pages))
				}
			}
		}
	}
	return merge(cs)
}

// merge folds changes that differ only in their pages, detail and score
// into the first of them. The detail comes from the lowest page, as in
// Issues and the score is the highest, or the page count when every
// part was scored by its page count.
func merge(cs []model.Change) []model.Change {
	type key struct{ class, check, title, source, before, after, fix string }
	at := map[key]int{}
	counted := map[int]bool{} // merged changes whose score is a page count
	out := cs[:0:0]
	for _, c := range cs {
		if len(c.Files) > 0 {
			out = append(out, c)
			continue
		}
		k := key{c.Class, c.Check, c.Title, c.Source, c.Before, c.After, c.Fix}
		i, ok := at[k]
		if !ok {
			at[k] = len(out)
			counted[len(out)] = c.Score == float64(len(c.Pages))
			out = append(out, c)
			continue
		}
		m := &out[i]
		if first(c.Pages) < first(m.Pages) {
			m.Detail = c.Detail
		}
		counted[i] = counted[i] && c.Score == float64(len(c.Pages))
		m.Score = max(m.Score, c.Score)
		pages := slices.Concat(m.Pages, c.Pages)
		slices.Sort(pages)
		m.Pages = slices.Compact(pages)
		if counted[i] {
			m.Score = float64(len(m.Pages))
		}
	}
	return out
}

// first returns the lowest page path, or "" for a change without pages.
func first(pages []string) string {
	if len(pages) == 0 {
		return ""
	}
	return slices.Min(pages)
}
