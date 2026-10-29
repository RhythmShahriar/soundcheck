// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/history"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Masked is r as an --eaa result shows it: the token-like segments and
// every query value of its page paths and URLs masked (security M3), as the statement draft,
// the evidence pack and the history mask them. A link can carry a
// reset key or a magic login token in its path while the terminal, the
// Markdown posted as a PR comment, the HTML report, JUnit and JSON all
// end up in logs and CI artifacts. Every place a masked path shows up
// (pages, issues, changes, warnings, the summary) is masked the same
// way. r itself isn't changed: the stored run keeps the paths compare
// and resume visit again. A result without --eaa is returned as it is.
//
// The start URL (the run's target and the comparison's run references)
// is shown without its query string and with a token in its path masked
// (see echolink.RedactTarget): it heads every report and the PR comment.
func Masked(r Result) Result {
	if r.Run == nil || (eaaOf(r) == nil && !r.Run.Options.EAA) {
		return r
	}
	mask := masker(pathMasker(r))
	run := *r.Run
	run.Target = echolink.RedactTarget(run.Target)
	run.Pages = slices.Clone(run.Pages)
	for i := range run.Pages {
		maskPage(&run.Pages[i], mask)
	}
	run.Issues = maskIssues(run.Issues, mask)
	run.Warnings = maskAll(run.Warnings, mask)
	if run.EAA != nil {
		run.EAA = maskEAA(run.EAA, mask)
	}
	r.Run = &run
	if c := r.Comparison; c != nil {
		cc := *c
		cc.Before.Target, cc.After.Target = echolink.RedactTarget(c.Before.Target), echolink.RedactTarget(c.After.Target)
		cc.Changes = maskChanges(c.Changes, mask)
		cc.Ignored = maskChanges(c.Ignored, mask)
		cc.Warnings = maskAll(c.Warnings, mask)
		if cc.EAA != nil {
			cc.EAA = maskEAA(cc.EAA, mask)
		}
		r.Comparison = &cc
	}
	return r
}

// ShownTarget is run's start URL as its outputs show it: masked as
// Masked masks it for an --eaa run, as it is otherwise.
func ShownTarget(run *model.Run) string { return history.Target(run) }

// pathMasker replaces each page path and URL of r that has a token or
// a query value in it with its masked form (echolink.RedactPage), or is
// nil when none has: the run's pages and the pages its issues and
// changes name (a FIXED change can name a page only the snapshot had). Longer ones go first, so a URL is replaced
// whole before the path inside it.
func pathMasker(r Result) *strings.Replacer {
	run := r.Run
	seen := map[string]bool{}
	var raw []string
	add := func(s string) {
		if s != "" && !seen[s] && echolink.RedactPage(s) != s {
			seen[s] = true
			raw = append(raw, s)
		}
	}
	for i := range run.Pages {
		p := &run.Pages[i]
		add(p.Path)
		add(p.Group)
		add(p.URL)
		add(p.FinalURL)
		for _, r := range p.Redirects {
			add(r)
		}
		for _, is := range p.Issues {
			for _, path := range is.Pages {
				add(path)
			}
		}
	}
	for _, is := range run.Issues {
		for _, path := range is.Pages {
			add(path)
		}
	}
	if c := r.Comparison; c != nil {
		for _, list := range [][]model.Change{c.Changes, c.Ignored} {
			for _, ch := range list {
				for _, path := range ch.Pages {
					add(path)
				}
			}
		}
	}
	if len(raw) == 0 {
		return nil
	}
	slices.SortFunc(raw, func(a, b string) int { return len(b) - len(a) })
	pairs := make([]string, 0, 2*len(raw))
	for _, s := range raw {
		pairs = append(pairs, s, echolink.RedactPage(s))
	}
	return strings.NewReplacer(pairs...)
}

// maskFunc masks one string of an --eaa result.
type maskFunc func(string) string

// masker is the mask of an --eaa result: its page paths replaced by
// rep (when it has any to mask), then the URLs, path tokens and secrets
// left in free text (a console line, a JS error title, an issue key
// that embeds a title) masked by echolink.RedactLine. Text a page sends
// to its console can carry a reset link no page path names.
func masker(rep *strings.Replacer) maskFunc {
	return func(s string) string {
		if rep != nil {
			s = rep.Replace(s)
		}
		return echolink.RedactLine(s)
	}
}

func maskPage(p *model.Page, rep maskFunc) {
	p.Path, p.Group, p.URL, p.FinalURL = rep(p.Path), rep(p.Group), rep(p.URL), rep(p.FinalURL)
	p.Redirects = maskAll(p.Redirects, rep)
	p.Links = maskAll(p.Links, rep)
	p.Issues = maskIssues(p.Issues, rep)
	p.Console = maskAll(p.Console, rep)
}

func maskIssues(issues []model.Issue, rep maskFunc) []model.Issue {
	if issues == nil {
		return nil
	}
	out := slices.Clone(issues)
	for i := range out {
		is := &out[i]
		is.Title, is.Detail, is.Source, is.Key = rep(is.Title), rep(is.Detail), rep(is.Source), rep(is.Key)
		is.Pages = maskAll(is.Pages, rep)
	}
	return out
}

func maskChanges(changes []model.Change, rep maskFunc) []model.Change {
	if changes == nil {
		return nil
	}
	out := slices.Clone(changes)
	for i := range out {
		c := &out[i]
		c.Title, c.Detail, c.Source, c.Reason = rep(c.Title), rep(c.Detail), rep(c.Source), rep(c.Reason)
		c.Before, c.After = rep(c.Before), rep(c.After)
		c.Pages = maskAll(c.Pages, rep)
	}
	return out
}

func maskEAA(e *model.EAASummary, rep maskFunc) *model.EAASummary {
	m := *e
	m.NotAudited = slices.Clone(e.NotAudited)
	for i := range m.NotAudited {
		n := &m.NotAudited[i]
		n.Path, n.Reason = rep(n.Path), rep(n.Reason)
	}
	return &m
}

func maskAll(list []string, rep maskFunc) []string {
	if list == nil {
		return nil
	}
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = rep(s)
	}
	return out
}

// MaskedPage is a page of an --eaa run as the live view shows it (the
// -v and --plain page lines): its path and URLs masked as Masked masks
// them. pg itself isn't changed.
func MaskedPage(pg *model.Page) *model.Page {
	q := *pg
	maskPage(&q, masker(pathMasker(Result{Run: &model.Run{Pages: []model.Page{*pg}}})))
	return &q
}
