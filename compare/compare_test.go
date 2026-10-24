// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func issue(chk string, level model.Level, title string) model.Issue {
	i := model.Issue{Check: chk, Level: level, Title: title}
	i.Identify()
	return i
}

func TestIssues(t *testing.T) {
	warn := issue("health/php-error", model.Warning, `PHP Warning: Undefined array key "id"`)
	fatal := issue("health/status", model.Error, "Server error 500")
	gone := issue("health/js-error", model.Warning, "JS error: x is not defined")
	before := &model.Run{Pages: []model.Page{
		{Path: "/", Issues: []model.Issue{gone}},
		{Path: "/a"}, {Path: "/b"},
		{Path: "/only-before", Issues: []model.Issue{fatal}},
	}}
	after := &model.Run{Pages: []model.Page{
		{Path: "/", Issues: []model.Issue{warn}},
		{Path: "/a", Issues: []model.Issue{warn}},
		{Path: "/b", Issues: []model.Issue{fatal}},
		{Path: "/only-after", Issues: []model.Issue{fatal}},
	}}
	cmp := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	byClass := map[string][]model.Change{}
	for _, c := range cmp.Changes {
		byClass[c.Class] = append(byClass[c.Class], c)
	}
	if b := byClass[model.Broke]; len(b) != 1 || b[0].Title != "Server error 500" || len(b[0].Pages) != 1 || b[0].Pages[0] != "/b" {
		t.Errorf("broke = %+v", b)
	}
	if n := byClass[model.New]; len(n) != 1 || len(n[0].Pages) != 2 {
		t.Errorf("new = %+v (one warning grouped across / and /a)", n)
	}
	if f := byClass[model.Fixed]; len(f) != 1 || f[0].Title != "JS error: x is not defined" {
		t.Errorf("fixed = %+v", f)
	}
	if cmp.Changes[0].Class != model.Broke {
		t.Error("broke sorts first")
	}
	if len(cmp.Warnings) != 1 {
		t.Errorf("warnings = %v (one page from the snapshot wasn't checked)", cmp.Warnings)
	}
}

// owner reports its own changes, so its issues must not also come out of
// the generic diff.
type owner struct{}

func (owner) Name() string { return "speed" }
func (owner) OwnsIssues()  {}
func (owner) Compare(context.Context, *check.CompareContext) []model.Change {
	return []model.Change{{Class: model.Slower, Check: "speed/lcp", Title: "LCP 40% slower"}}
}

func TestIssueOwnerSkipsGenericDiff(t *testing.T) {
	slow := issue("speed/lcp", model.Error, "Largest Contentful Paint over 4 s (lab)")
	php := issue("health/php-error", model.Warning, "PHP Warning: x")
	before := &model.Run{Pages: []model.Page{{Path: "/"}}}
	after := &model.Run{Pages: []model.Page{{Path: "/", Issues: []model.Issue{slow, php}}}}
	cmp := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, []check.Module{owner{}}, "test")
	var got []string
	for _, c := range cmp.Changes {
		got = append(got, c.Class+":"+c.Title)
	}
	if len(got) != 2 || got[0] != "new:PHP Warning: x" || got[1] != "slower:LCP 40% slower" {
		t.Errorf("changes = %v", got)
	}
}

func TestOnlyModulesInBothRunsCompare(t *testing.T) {
	speed := issue("speed/lcp", model.Warning, "LCP over 2.5 s")
	php := issue("health/php-error", model.Warning, "PHP Warning: x")
	before := &model.Run{Options: model.Options{Modules: []string{"health"}}, Pages: []model.Page{{Path: "/"}}}
	after := &model.Run{Options: model.Options{Modules: []string{"health", "speed"}}, Pages: []model.Page{{Path: "/", Issues: []model.Issue{speed, php}}}}
	cmp := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	if len(cmp.Changes) != 1 || cmp.Changes[0].Check != "health/php-error" {
		t.Errorf("changes = %+v", cmp.Changes)
	}
	if len(cmp.Warnings) == 0 || !strings.Contains(cmp.Warnings[0], "only modules in both") {
		t.Errorf("warnings = %v", cmp.Warnings)
	}
}

func TestMismatchEchoMode(t *testing.T) {
	before, after := &model.Run{}, &model.Run{}
	before.Options.Echo, before.Options.EchoMode = true, "seal"
	after.Options.Echo, after.Options.EchoMode = true, "observe"
	if w := strings.Join(mismatch(before, after), "\n"); !strings.Contains(w, "Echo's mode differs (snapshot seal, now observe)") {
		t.Errorf("want an Echo mode warning, got %q", w)
	}
	after.Options.EchoMode = "seal"
	if w := mismatch(before, after); len(w) != 0 {
		t.Errorf("same mode warned: %q", w)
	}
}

// deterministicRuns has one issue on six pages with a different detail on
// each, plus changes that tie on class, score and title.
func deterministicRuns() (before, after *model.Run) {
	before, after = &model.Run{}, &model.Run{}
	for _, path := range []string{"/f", "/c", "/a", "/e", "/b", "/d"} {
		warn := issue("health/php-error", model.Warning, "PHP Warning: x")
		warn.Detail = "seen on " + path
		js := issue("health/js-error", model.Warning, "Same title")
		js.Source = "app.js"
		other := issue("health/console", model.Warning, "Same title")
		other.Source = "lib.js"
		before.Pages = append(before.Pages, model.Page{Path: path})
		after.Pages = append(after.Pages, model.Page{Path: path, Issues: []model.Issue{other, js, warn}})
	}
	return before, after
}

func TestIssuesAreDeterministic(t *testing.T) {
	marshal := func(before, after *model.Run) string {
		c := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
		c.Created = time.Time{}
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	before, after := deterministicRuns()
	want := marshal(before, after)
	for range 50 {
		if got := marshal(before, after); got != want {
			t.Fatalf("output changed between identical calls:\n%s\n%s", want, got)
		}
	}
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // shuffling test input
	for range 20 {
		rng.Shuffle(len(after.Pages), func(i, j int) { after.Pages[i], after.Pages[j] = after.Pages[j], after.Pages[i] })
		rng.Shuffle(len(before.Pages), func(i, j int) { before.Pages[i], before.Pages[j] = before.Pages[j], before.Pages[i] })
		if got := marshal(before, after); got != want {
			t.Fatalf("page order changed the output:\n%s\n%s", want, got)
		}
	}
	c := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	var got []string
	for _, ch := range c.Changes {
		got = append(got, ch.Check+":"+ch.Detail)
	}
	// Ties on class and score go by check; the detail is the lowest page's.
	if strings.Join(got, ",") != "health/console:,health/js-error:,health/php-error:seen on /a" {
		t.Errorf("changes = %v", got)
	}
}

func TestSortTieBreaks(t *testing.T) {
	cs := []model.Change{
		{Class: model.New, Check: "b", Title: "t", Pages: []string{"/"}},
		{Class: model.New, Check: "a", Source: "z", Title: "t", Pages: []string{"/"}},
		{Class: model.New, Check: "a", Source: "y", Title: "t", Pages: []string{"/b"}},
		{Class: model.New, Check: "a", Source: "y", Title: "t", Pages: []string{"/a"}},
		{Class: model.New, Check: "a", Source: "y", Title: "s", Pages: []string{"/a"}},
		{Class: model.Broke, Check: "z", Title: "t", Pages: []string{"/"}},
	}
	Sort(cs)
	var got []string
	for _, c := range cs {
		got = append(got, c.Check+c.Source+c.Pages[0]+c.Title)
	}
	want := "z/t,ay/as,ay/at,ay/bt,az/t,b/t"
	if strings.Join(got, ",") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}

// titles is a Comparer that reports a changed title on every pair of
// pages, like the content module.
type titles struct{}

func (titles) Name() string { return "content" }
func (titles) Compare(_ context.Context, cc *check.CompareContext) []model.Change {
	var out []model.Change
	for _, pair := range cc.Pairs() {
		if pair[0].Title != pair[1].Title {
			out = append(out, model.Change{Class: model.Changed, Check: "content/title", Title: "Page title changed",
				Pages: []string{pair[1].Path}, Before: pair[0].Title, After: pair[1].Title})
		}
	}
	return out
}

// interruptedRuns is a snapshot of three pages and a run stopped while
// two of them were loading: their visits ended with "context canceled"
// and empty facts.
func interruptedRuns() (before, after *model.Run) {
	before = &model.Run{Pages: []model.Page{
		{Path: "/", Title: "Home"}, {Path: "/slow", Title: "Slow"}, {Path: "/shop/", Title: "Shop"},
	}}
	canceled := func(path string) model.Page {
		return model.Page{Path: path, Error: model.CanceledError, Issues: []model.Issue{
			issue("health/unreachable", model.Error, "Page didn't load: context canceled"),
			issue("health/white-screen", model.Error, "Blank page (white screen)"),
		}}
	}
	after = &model.Run{Pages: []model.Page{{Path: "/", Title: "Home"}, canceled("/slow"), canceled("/shop/")}}
	return before, after
}

func TestInterruptedRunDropsCanceledPages(t *testing.T) {
	before, after := interruptedRuns()
	c := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, []check.Module{titles{}}, "test")
	if len(c.Changes) != 0 {
		t.Errorf("canceled pages were compared: %+v", c.Changes)
	}
	if !c.Interrupted {
		t.Error("the comparison must be marked interrupted")
	}
	if c.Pages != 1 {
		t.Errorf("pages = %d, want the 1 that was checked", c.Pages)
	}
	if len(c.Warnings) == 0 || c.Warnings[0] != "interrupted: 2 pages were not checked" {
		t.Errorf("warnings = %q", c.Warnings)
	}
	if len(after.Pages) != 3 || after.Interrupted {
		t.Error("Compare must not change the run it was given")
	}

	// A canceled page in the snapshot isn't compared either, but the
	// current run is complete.
	before, after = interruptedRuns()
	before.Pages, after.Pages = after.Pages, before.Pages
	c = Compare(context.Background(), &check.CompareContext{Before: before, After: after}, []check.Module{titles{}}, "test")
	if len(c.Changes) != 0 || c.Interrupted {
		t.Errorf("changes = %+v, interrupted = %v", c.Changes, c.Interrupted)
	}
	if len(c.Warnings) == 0 || !strings.Contains(c.Warnings[0], "the snapshot was interrupted: 2 pages") {
		t.Errorf("warnings = %q", c.Warnings)
	}
}

func TestCompleteRunIsNotInterrupted(t *testing.T) {
	before, _ := interruptedRuns()
	after := &model.Run{Pages: slices.Clone(before.Pages)}
	after.Pages[1].Error = "net::ERR_CONNECTION_REFUSED" // a real failure is compared
	after.Pages[1].Issues = []model.Issue{issue("health/unreachable", model.Error, "Page didn't load: net::ERR_CONNECTION_REFUSED")}
	c := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	if c.Interrupted || len(c.Changes) != 1 || c.Changes[0].Class != model.Broke {
		t.Errorf("interrupted = %v, changes = %+v", c.Interrupted, c.Changes)
	}
	after.Interrupted = true // stopped between pages: nothing to drop, still partial
	c = Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	if !c.Interrupted || !strings.HasPrefix(c.Warnings[0], "interrupted: the run was stopped") {
		t.Errorf("interrupted = %v, warnings = %q", c.Interrupted, c.Warnings)
	}
}
