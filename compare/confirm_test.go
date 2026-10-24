// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"context"
	"slices"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// flaky compares a snapshot with a run where /flaky/ and /down/ answered
// 500 and /js/ logged a new JS error. again is the second visit:
// /down/ still fails, /flaky/ recovered and /js/ wasn't visited again.
func flaky(t *testing.T) (c *model.Comparison, before, again *model.Run) {
	t.Helper()
	fatal := issue("health/status", model.Error, "Server error 500")
	js := issue("health/js-error", model.Warning, "JS error: x is not defined")
	ok := func(path string, issues ...model.Issue) model.Page {
		return model.Page{Path: path, Status: 200, Issues: issues}
	}
	before = &model.Run{Pages: []model.Page{ok("/"), ok("/flaky/"), ok("/down/"), ok("/js/")}}
	after := &model.Run{Pages: []model.Page{ok("/"), ok("/flaky/", fatal), ok("/down/", fatal), ok("/js/", js)}}
	c = Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	again = &model.Run{Pages: []model.Page{ok("/flaky/"), ok("/down/", fatal)}}
	return c, before, again
}

func byClassTitle(c *model.Comparison) map[string][]string {
	m := map[string][]string{}
	for _, ch := range c.Changes {
		m[ch.Class+" "+ch.Title] = ch.Pages
	}
	return m
}

func TestConfirmSplitsUnreproduced(t *testing.T) {
	c, before, again := flaky(t)
	Confirm(c, before, again)
	got := byClassTitle(c)
	if p := got["broke Server error 500"]; !slices.Equal(p, []string{"/down/"}) {
		t.Errorf("BROKE stays only where it reproduced: %v", got)
	}
	if p := got["unstable Server error 500"]; !slices.Equal(p, []string{"/flaky/"}) {
		t.Errorf("/flaky/ should be UNSTABLE: %v", got)
	}
	if p := got["new JS error: x is not defined"]; !slices.Equal(p, []string{"/js/"}) {
		t.Errorf("a page that wasn't re-visited keeps its change: %v", got)
	}
	if last := c.Changes[len(c.Changes)-1]; last.Class != model.Unstable {
		t.Errorf("UNSTABLE sorts last: %+v", c.Changes)
	}
	if c.Summary.Unstable != 1 || c.Summary.Broke != 1 || c.Summary.Verdict != model.VerdictBroke {
		t.Errorf("summary = %+v", c.Summary)
	}
}

func TestConfirmKeepsIDWhenWholeChangeIsUnstable(t *testing.T) {
	c, before, _ := flaky(t)
	i := slices.IndexFunc(c.Changes, func(ch model.Change) bool { return ch.Class == model.Broke })
	id := c.Changes[i].ID
	Confirm(c, before, &model.Run{Pages: []model.Page{{Path: "/flaky/", Status: 200}, {Path: "/down/", Status: 200}}})
	i = slices.IndexFunc(c.Changes, func(ch model.Change) bool { return ch.Class == model.Unstable })
	if i < 0 || c.Changes[i].ID != id || len(c.Changes[i].Pages) != 2 || c.Summary.Broke != 0 {
		t.Errorf("want the whole 500 UNSTABLE with id %s: %+v", id, c.Changes)
	}
}

func TestConfirmCantTell(t *testing.T) {
	for name, again := range map[string]*model.Run{
		"nothing re-visited":       nil,
		"second visit canceled":    {Pages: []model.Page{{Path: "/flaky/", Error: model.CanceledError}, {Path: "/down/", Error: model.CanceledError}}},
		"second visit didn't load": {Pages: []model.Page{{Path: "/flaky/", Error: "net::ERR_TIMED_OUT"}, {Path: "/down/", Error: "net::ERR_TIMED_OUT"}}},
		"other pages re-visited":   {Pages: []model.Page{{Path: "/", Status: 200}}},
		"no pages in the re-visit": {},
	} {
		c, before, _ := flaky(t)
		Confirm(c, before, again)
		if c.Summary.Unstable != 0 || c.Count(model.Unstable) != 0 {
			t.Errorf("%s: %+v", name, c.Changes)
		}
	}
}

func TestConfirmLeavesModuleChanges(t *testing.T) {
	c := &model.Comparison{Changes: []model.Change{
		{Class: model.Broke, Check: "echo/email", Title: "Form stopped sending email", Pages: []string{"/contact/"}},
		{Class: model.Changed, Check: "visual/page", Title: "Page looks different (desktop)", Pages: []string{"/contact/"}},
	}}
	Confirm(c, &model.Run{Pages: []model.Page{{Path: "/contact/"}}}, &model.Run{Pages: []model.Page{{Path: "/contact/", Status: 200}}})
	if c.Changes[0].Class != model.Broke || c.Changes[1].Class != model.Changed {
		t.Errorf("module changes were reclassified: %+v", c.Changes)
	}
}

func TestConfirmSiteIssuesStay(t *testing.T) {
	link := issue("health/broken-link", model.Error, "Broken link to /old (404)")
	link.Pages = []string{"/flaky/"}
	before := &model.Run{Pages: []model.Page{{Path: "/flaky/", Status: 200}}}
	after := &model.Run{Pages: []model.Page{{Path: "/flaky/", Status: 200}}, Issues: []model.Issue{link}}
	c := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	Confirm(c, before, &model.Run{Pages: []model.Page{{Path: "/flaky/", Status: 200}}})
	if c.Count(model.Broke) != 1 || c.Count(model.Unstable) != 0 {
		t.Errorf("a site issue can't be confirmed by re-visiting pages: %+v", c.Changes)
	}
}

// A page issue a site-wide check added (the page cache tests, which fetch
// the page outside the browser) can't be confirmed by re-visiting the
// page, which doesn't run those checks: a cache leak stays BROKE.
func TestConfirmSiteCheckPageIssuesStay(t *testing.T) {
	leak := issue("cache/leak", model.Error, "Cache leaks logged-in content to visitors")
	leak.FromSite = true
	fatal := issue("health/status", model.Error, "Server error 500")
	before := &model.Run{Pages: []model.Page{{Path: "/account/", Status: 200}}}
	after := &model.Run{Pages: []model.Page{{Path: "/account/", Status: 200, Issues: []model.Issue{leak, fatal}}}}
	c := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	Confirm(c, before, &model.Run{Pages: []model.Page{{Path: "/account/", Status: 200}}})
	got := byClassTitle(c)
	if p := got["broke Cache leaks logged-in content to visitors"]; !slices.Equal(p, []string{"/account/"}) {
		t.Errorf("the cache leak should stay BROKE: %v", got)
	}
	if p := got["unstable Server error 500"]; !slices.Equal(p, []string{"/account/"}) {
		t.Errorf("a page issue the re-visit didn't see again is still UNSTABLE: %v", got)
	}
}

// Confirmable lists the pages a second visit can still judge: those of
// BROKE and NEW page issues, not UNSTABLE ones, site issues or module
// changes.
func TestConfirmable(t *testing.T) {
	c, before, again := flaky(t)
	link := model.Change{Class: model.New, Check: "health/broken-link", Pages: []string{"/links/"}}
	email := model.Change{Class: model.Broke, Check: "echo/email", Pages: []string{"/contact/"}}
	c.Changes = append(c.Changes, link, email)
	if got := Confirmable(c); !slices.Equal(sorted(got), []string{"/down/", "/flaky/", "/js/"}) {
		t.Errorf("before confirming: %v", got)
	}
	Confirm(c, before, again)
	if got := Confirmable(c); !slices.Equal(sorted(got), []string{"/down/", "/js/"}) {
		t.Errorf("after confirming: %v", got)
	}
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}
