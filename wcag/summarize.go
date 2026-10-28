// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// labelChecks report a form control without a name a visitor can read.
// One missing label can show up as several of them (axe's label and
// Soundcheck's interact/unlabeled both fire on it), so they count once
// per element.
var labelChecks = []string{"a11y/label", "a11y/select-name", "a11y/placeholder-label", "interact/unlabeled", "interact/unnamed-field"}

// FindingKey is what Summarize counts an issue as: its issue key, or for
// the checks that report a missing label one key per element whichever
// of them reported it.
func FindingKey(i model.Issue) string {
	if slices.Contains(labelChecks, i.Check) {
		if sel := selectorOf(i); sel != "" {
			return "label|" + sel
		}
	}
	if i.Key != "" {
		return i.Key
	}
	return model.IssueKey(i.Check, i.Title, i.Source)
}

// selectorOf returns the element an issue is about. axe's issues have it
// as their Source; interact's put it in Detail: "#billing_email" for
// unlabeled and "in <form> (#billing_email)" for unnamed-field.
func selectorOf(i model.Issue) string {
	if i.Source != "" {
		return i.Source
	}
	switch i.Check {
	case "interact/unlabeled":
		return strings.TrimSpace(i.Detail)
	case "interact/unnamed-field":
		d := strings.TrimSpace(i.Detail)
		if strings.HasSuffix(d, ")") {
			if at := strings.LastIndex(d, " ("); at >= 0 {
				return d[at+2 : len(d)-1]
			}
		}
	}
	return ""
}

// auditing are the modules that record a Page.Audits entry for each page
// they test. Summarize counts one as run only when an audit finished.
var auditing = []string{"a11y", "keyboard"}

// moduleOf returns the module a test of TestedBy belongs to: a11y for
// the axe rules, else the part of the check id before the slash.
func moduleOf(test string) string {
	if strings.HasPrefix(test, "axe:") {
		return "a11y"
	}
	m, _, _ := strings.Cut(test, "/")
	return m
}

// tally is what Summarize keeps for one criterion while pages stream by.
type tally struct {
	keys  map[string]bool // finding keys
	pages int             // pages with one of them
	site  map[string]bool // paths of site issues' pages not yet seen
}

// Summarize sums up an --eaa run against the criteria of its WCAG
// version (run.Options.WCAG): pages yields the run's pages (they may
// come from disk one by one; each is read once), site its site-level
// issues and ignored the changes ignore rules and accepts left out. For
// a comparison, run is the current run and unstable its UNSTABLE
// changes.
//
// A criterion is failed when an issue that maps to it was found, each
// finding counted once (see FindingKey). It is no failures found when a
// test of it ran and found none. Any other is not tested: nothing tests
// it, or the modules whose checks test it didn't run. Ignored findings
// still count against their criteria (ignoring a problem doesn't fix
// it) but not as open. An UNSTABLE finding (seen once, gone when its
// page was checked again) isn't counted at all. Memory grows with the
// criteria and the findings, not the pages.
func Summarize(run *model.Run, pages func(func(*model.Page) bool), site []model.Issue, ignored []model.Change, unstable model.UnstableSet) model.EAASummary {
	version := orDefault(run.Options.WCAG)
	t := tables()[version]
	s := model.EAASummary{WCAG: version, EN301549: EN301549(version), Interrupted: run.Interrupted, EvidenceNote: model.EvidenceNote,
		Criteria: model.EAACriteria{Failed: []model.CriterionCount{}, NoFailuresFound: []string{}, NotTested: []string{}}}
	if run.Options.A11y != nil {
		s.Axe = run.Options.A11y.Axe
	}
	if run.KeyFlows != nil {
		s.Keyboard.FlowsRun = run.KeyFlows.Run
		s.Keyboard.FlowsSkipped = slices.Clone(run.KeyFlows.Skipped)
	}

	skip := map[string]bool{}
	for _, c := range ignored {
		skip[FindingKey(model.Issue{Check: c.Check, Title: c.Title, Source: c.Source, Detail: c.Detail, Key: c.Key})] = true
	}
	tallies := map[string]*tally{}
	get := func(id string) *tally {
		if tallies[id] == nil {
			tallies[id] = &tally{keys: map[string]bool{}, site: map[string]bool{}}
		}
		return tallies[id]
	}
	open := map[string]bool{}
	add := func(i model.Issue, path string) []string {
		if unstable.Has(i, path) {
			return nil
		}
		key := FindingKey(i)
		if (strings.HasPrefix(i.Check, "a11y/") || strings.HasPrefix(i.Check, "keyboard/")) && !skip[key] {
			open[key] = true
		}
		crit := IssueCriteria(i, version)
		for _, c := range crit {
			get(c).keys[key] = true
		}
		return crit
	}
	for _, i := range site {
		for _, c := range add(i, "") {
			for _, p := range i.Pages {
				get(c).site[p] = true
			}
		}
	}

	// A module that records audits ran when one page's audit finished;
	// one that doesn't (interact) ran when the run lists it and checked
	// at least one page.
	finished := map[string]bool{}
	checked := 0
	onPage := map[string]bool{}
	pages(func(p *model.Page) bool {
		checked++
		clear(onPage)
		for _, i := range p.Issues {
			for _, c := range add(i, p.Path) {
				onPage[c] = true
			}
		}
		for id, tl := range tallies {
			if tl.site[p.Path] {
				delete(tl.site, p.Path)
				onPage[id] = true
			}
		}
		for id := range onPage {
			tallies[id].pages++
		}
		for _, m := range slices.Sorted(maps.Keys(p.Audits)) {
			a := p.Audits[m]
			if a.Finished() {
				finished[m] = true
				continue
			}
			s.NotAudited = append(s.NotAudited, model.NotAudited{Path: p.Path, Module: m, Status: a.Status, Reason: a.Reason})
		}
		if p.Keyboard != nil {
			s.Keyboard.PagesWalked++
			s.Keyboard.WidgetsTested += p.Keyboard.Widgets
		}
		return true
	})
	slices.SortStableFunc(s.NotAudited, func(a, b model.NotAudited) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Module, b.Module))
	})

	ran := func(m string) bool {
		switch {
		case len(run.Options.Modules) > 0 && !slices.Contains(run.Options.Modules, m):
			return false
		case finished[m]:
			return true
		case m == "keyboard":
			// A walk counts even if the keyboard lane wrote no audit.
			return s.Keyboard.PagesWalked > 0
		case slices.Contains(auditing, m):
			// Listed but no page's audit finished, or none was
			// recorded: its tests didn't run.
			return false
		}
		return checked > 0
	}
	for _, c := range t.criteria {
		if tl := tallies[c.ID]; tl != nil && len(tl.keys) > 0 {
			// Pages of site issues that weren't among the pages streamed
			// still count.
			s.Criteria.Failed = append(s.Criteria.Failed, model.CriterionCount{ID: c.ID, Issues: len(tl.keys), Pages: tl.pages + len(tl.site)})
			continue
		}
		if c.Coverage == CoveragePartly && slices.ContainsFunc(c.TestedBy, func(test string) bool { return ran(moduleOf(test)) }) {
			s.Criteria.NoFailuresFound = append(s.Criteria.NoFailuresFound, c.ID)
			continue
		}
		s.Criteria.NotTested = append(s.Criteria.NotTested, c.ID)
	}
	s.Open = len(open)
	return s
}
