// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/model"
)

// twice is a comparer that reports the same change once per page, the
// way a module looping over pages does.
type twice struct{}

func (twice) Name() string { return "visual" }
func (twice) Compare(context.Context, *check.CompareContext) []model.Change {
	return []model.Change{
		{Class: model.Changed, Check: "visual/block", Title: "New block near the bottom", Pages: []string{"/contact.php"}, Score: 3},
		{Class: model.Changed, Check: "visual/block", Title: "New block near the bottom", Pages: []string{"/"}, Score: 5},
		{Class: model.Changed, Check: "visual/block", Title: "New block near the bottom", Pages: []string{"/contact.php"}, Score: 3},
	}
}

// n5Runs rebuilds the compare of users.md N5 with the real health
// checks: /promo.php became a 404 whose error page throws a JS error,
// while / and /contact.php gained a PHP warning and the favicon went
// missing (a site-level issue listing the pages that asked for it).
func n5Runs(t *testing.T) (before, after *model.Run) {
	t.Helper()
	base, _ := url.Parse("http://127.0.0.1:20300/")
	cc := &check.Context{Base: base}
	page := func(path string, status int, text string, failed []model.Request, console []string) model.Page {
		p := model.Page{Path: path, URL: base.String() + strings.TrimPrefix(path, "/"), Status: status, Title: "T " + path,
			Text: text, TextLength: len(text), Failed: failed, Console: console}
		health.Module{}.CheckPage(cc, &p)
		return p
	}
	ok := "Welcome to the shop, everything is fine here."
	warn := "Warning: Undefined variable $x in /var/www/html/lib.php on line 6\n" + ok
	favicon := []model.Request{{URL: base.String() + "favicon.ico", Status: 404}}
	opts := model.Options{Modules: []string{"health", "visual"}}
	before = &model.Run{Options: opts, Pages: []model.Page{
		page("/", 200, ok, nil, nil),
		page("/about.php", 200, ok, nil, nil),
		page("/promo.php", 200, ok, nil, nil),
		page("/contact.php", 200, ok, nil, nil),
	}}
	after = &model.Run{Options: opts, Pages: []model.Page{
		page("/", 200, warn, nil, nil),
		page("/about.php", 200, ok, nil, nil),
		page("/promo.php", 404, "Not Found", favicon, []string{"Uncaught ReferenceError: theme is not defined"}),
		page("/contact.php", 200, warn, favicon, nil),
	}}
	// A broken link found on the 404 page is a site issue: it stays.
	link := model.Issue{Check: "health/broken-link", Level: model.Warning, Title: "Broken link to /old (404)", Pages: []string{"/promo.php"}}
	link.Identify()
	after.Issues = []model.Issue{link}
	for _, r := range []*model.Run{before, after} {
		health.Module{}.CheckSite(context.Background(), &check.Context{Base: base, Run: r})
	}
	return before, after
}

func lines(cs []model.Change) []string {
	var out []string
	for _, c := range cs {
		out = append(out, fmt.Sprintf("%s %s %v", c.Class, c.Title, c.Pages))
	}
	return out
}

func TestOneCausePerLine(t *testing.T) {
	before, after := n5Runs(t)
	cmp := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, []check.Module{health.Module{}, twice{}}, "test")
	got := lines(cmp.Changes)
	want := []string{
		"broke Page not found (404) [/promo.php]",
		"new No favicon [/contact.php /promo.php]",
		"new PHP Warning: Undefined variable $x [/ /contact.php]",
		"new Broken link to /old (404) [/promo.php]",
		"changed New block near the bottom [/ /contact.php]",
	}
	if !slices.Equal(got, want) {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, c := range cmp.Changes {
		if c.Title == "New block near the bottom" && c.Score != 5 {
			t.Errorf("merged score = %v, want the highest (5)", c.Score)
		}
		// A site-level issue keeps every page, the error page too: the
		// favicon is missing for the whole site, not for one page.
		if c.Title == "No favicon" && c.Score != 2 {
			t.Errorf("favicon score = %v, want the page count (2)", c.Score)
		}
	}
}

// Without a broken page nothing is dropped and different before/after
// values or files keep changes apart.
func TestOneCauseKeepsDistinctChanges(t *testing.T) {
	cs := []model.Change{
		{Class: model.New, Check: "health/favicon", Title: "No favicon", Pages: []string{"/a"}, Score: 1},
		{Class: model.Broke, Check: "health/white-screen", Title: "Blank page (white screen)", Pages: []string{"/a"}, Score: 1},
		{Class: model.Changed, Check: "health/title", Title: "Page title changed", Pages: []string{"/a"}, Before: "A", After: "B"},
		{Class: model.Changed, Check: "health/title", Title: "Page title changed", Pages: []string{"/b"}, Before: "C", After: "D"},
		{Class: model.Changed, Check: "visual/page", Title: "Page looks different", Pages: []string{"/a"}, Files: map[string]string{"diff": "a.png"}},
		{Class: model.Changed, Check: "visual/page", Title: "Page looks different", Pages: []string{"/b"}, Files: map[string]string{"diff": "b.png"}},
		{Class: model.New, Check: "speed/lcp", Title: "LCP over 4 s", Pages: []string{"/b"}, Detail: "on b", Score: 1},
		{Class: model.New, Check: "speed/lcp", Title: "LCP over 4 s", Pages: []string{"/a"}, Detail: "on a", Score: 1},
	}
	got := oneCause(slices.Clone(cs), func(model.Change) bool { return false })
	if len(got) != 7 {
		t.Fatalf("got %d changes, want 7 (only the two LCP lines merge):\n%s", len(got), strings.Join(lines(got), "\n"))
	}
	lcp := got[6]
	if !slices.Equal(lcp.Pages, []string{"/a", "/b"}) || lcp.Detail != "on a" || lcp.Score != 2 {
		t.Errorf("merged = %+v, want pages /a /b, the detail of /a and score 2", lcp)
	}
	if got[0].Title != "No favicon" {
		t.Error("a white screen isn't a page that failed to load: its notices stay")
	}
}

// A broken page leaves health changes of other pages alone and other
// modules' changes on it too.
func TestOneCauseOnlyHealthOnBrokenPages(t *testing.T) {
	cs := []model.Change{
		{Class: model.Broke, Check: "health/unreachable", Title: "Page didn't load: net::ERR_CONNECTION_REFUSED", Pages: []string{"/x"}},
		{Class: model.New, Check: "health/js-error", Title: "JS error: a", Pages: []string{"/x", "/y"}, Score: 2},
		{Class: model.Changed, Check: "health/redirect", Title: "Page now ends somewhere else", Pages: []string{"/x"}},
		// The page is gone, not fixed: its old issues don't read as FIXED.
		{Class: model.Fixed, Check: "health/no-title", Title: "Page has no <title>", Pages: []string{"/x"}},
		{Class: model.Fixed, Check: "health/broken-asset", Title: "Broken image: /missing.png (404)", Pages: []string{"/x", "/z"}, Score: 2},
		{Class: model.Changed, Check: "visual/page", Title: "Page looks different", Pages: []string{"/x"}},
	}
	got := lines(oneCause(cs, func(model.Change) bool { return false }))
	want := []string{
		"broke Page didn't load: net::ERR_CONNECTION_REFUSED [/x]",
		"new JS error: a [/y]",
		"fixed Broken image: /missing.png (404) [/z]",
		"changed Page looks different [/x]",
	}
	if !slices.Equal(got, want) {
		t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
