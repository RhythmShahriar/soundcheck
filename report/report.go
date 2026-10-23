// Package report renders Soundcheck results: the live terminal view,
// plain text for logs and screen readers, JSON, Markdown, JUnit XML and a
// standalone HTML report with before/after sliders.
//
// A Result is either a comparison (what changed since a snapshot) or a
// single run (the problems a site has right now, e.g. a new snapshot).
package report

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Result is what a command produced. Run is always set; Comparison only
// when the run was compared with an earlier one.
type Result struct {
	Run        *model.Run
	Comparison *model.Comparison
	Dir        string // the folder Run (and Comparison) were saved in; Files paths are relative to it
}

// Compared reports whether the result is a comparison.
func (r Result) Compared() bool { return r.Comparison != nil }

// Path resolves a file a change refers to (relative to Dir).
func (r Result) Path(rel string) string {
	if rel == "" || filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(r.Dir, filepath.FromSlash(rel))
}

// classInfo describes how a change class is shown.
type classInfo struct {
	Label string // BROKE
	Glyph string
	Blurb string // one line under the section heading
}

var classes = map[string]classInfo{
	model.Broke:   {"BROKE", "✖", "worked in the snapshot, fails now"},
	model.New:     {"NEW", "▲", "problems that weren't there before"},
	model.Changed: {"CHANGED", "■", "looks or reads differently; check it's intended"},
	model.Slower:  {"SLOWER", "◆", "measurably slower than the snapshot"},
	model.Fixed:   {"FIXED", "✔", "problems from the snapshot that are gone"},
}

var levelGlyphs = map[model.Level]string{model.Error: "✖", model.Warning: "▲", model.Notice: "·"}

// Levels in report order.
var Levels = []model.Level{model.Error, model.Warning, model.Notice}

// FailOn reports whether a result should fail a CI build at threshold.
// For a comparison the threshold is a change class: "new" fails on BROKE
// or NEW, "changed" also on CHANGED, and so on (FIXED never fails). For a
// single run it's an issue level: "warning" fails on errors or warnings.
func FailOn(r Result, threshold string) bool {
	if threshold == "" {
		return false
	}
	if r.Compared() {
		limit := slices.Index(model.Classes, threshold)
		for _, c := range r.Comparison.Changes {
			if i := slices.Index(model.Classes, c.Class); i >= 0 && i <= limit && c.Class != model.Fixed {
				return true
			}
		}
		return false
	}
	limit := model.Level(threshold).Rank()
	for _, i := range Issues(r.Run) {
		if i.Level.Rank() <= limit {
			return true
		}
	}
	return false
}

// ValidFailOn checks a --fail-on value for a comparison or a single run.
func ValidFailOn(threshold string, compared bool) error {
	if compared {
		if !slices.Contains(model.Classes[:4], threshold) {
			return fmt.Errorf("--fail-on %q: want broke, new, changed or slower", threshold)
		}
		return nil
	}
	if !slices.Contains(Levels, model.Level(threshold)) {
		return fmt.Errorf("--fail-on %q: want error, warning or notice (or a class like broke when comparing)", threshold)
	}
	return nil
}

// Issues returns a run's issues with the same problem on several pages
// merged into one, most serious first, then by how many pages have it.
func Issues(run *model.Run) []model.Issue {
	var out []model.Issue
	at := map[string]int{}
	for _, i := range run.AllIssues() {
		k := i.Key
		if k == "" {
			i.Identify()
			k = i.Key
		}
		if n, ok := at[k]; ok {
			for _, p := range i.Pages {
				if !slices.Contains(out[n].Pages, p) {
					out[n].Pages = append(out[n].Pages, p)
				}
			}
			continue
		}
		at[k] = len(out)
		i.Pages = slices.Clone(i.Pages)
		out = append(out, i)
	}
	slices.SortStableFunc(out, func(a, b model.Issue) int {
		return cmp.Or(cmp.Compare(a.Level.Rank(), b.Level.Rank()), cmp.Compare(len(b.Pages), len(a.Pages)))
	})
	return out
}

// Counts tallies a run's grouped issues by level.
func Counts(issues []model.Issue) map[model.Level]int {
	m := map[model.Level]int{}
	for _, i := range issues {
		m[i.Level]++
	}
	return m
}

// byClass splits changes by class, keeping their order.
func byClass(cs []model.Change) map[string][]model.Change {
	m := map[string][]model.Change{}
	for _, c := range cs {
		m[c.Class] = append(m[c.Class], c)
	}
	return m
}

// pagesLabel says where something happens: one path, or "/a/, /b/ +3".
func pagesLabel(pages []string, show int) string {
	switch len(pages) {
	case 0:
		return "site-wide"
	case 1:
		return pages[0]
	}
	if len(pages) <= show {
		return strings.Join(pages, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(pages[:show], ", "), len(pages)-show)
}

// countLabel is "1 page", "42 pages" or "site-wide".
func countLabel(pages []string) string {
	switch len(pages) {
	case 0:
		return "site-wide"
	case 1:
		return "1 page"
	}
	return fmt.Sprintf("%d pages", len(pages))
}

// beforeAfter is "200 → 500", or "" when neither side is known.
func beforeAfter(c model.Change) string {
	if c.Before == "" && c.After == "" {
		return ""
	}
	return cmp.Or(c.Before, "–") + " → " + cmp.Or(c.After, "–")
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// plural is "1 page" or "3 pages".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
