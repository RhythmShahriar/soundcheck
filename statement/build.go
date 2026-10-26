// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// maxPaths is how many pages a row or an ignored problem lists.
const maxPaths = 3

// NoteInsecure is the note of a draft from a run made with -k.
const NoteInsecure = "TLS certificate not verified for this run"

// Build makes the draft. It refuses an owner without statement.owner,
// statement.website and statement.contact.email (ErrMissingOwner), a
// run without --eaa results (ErrNoEAAData) and an interrupted run
// (ErrInterrupted), whose draft would leave problems out.
//
// The draft names the site by Owner.Website only: the run's host and
// every Input.Hide string are masked wherever a path holds them. A run
// made logged in lists no paths at all unless the owner sets
// list_private_paths; it counts them instead.
func Build(in Input) (*Draft, error) {
	o := in.Owner
	if strings.TrimSpace(o.Owner) == "" || strings.TrimSpace(o.Website) == "" || strings.TrimSpace(o.Contact.Email) == "" {
		return nil, ErrMissingOwner
	}
	if in.Run == nil || !in.Run.Options.EAA {
		return nil, ErrNoEAAData
	}
	if in.Run.Interrupted || in.Summary.Interrupted {
		return nil, ErrInterrupted
	}
	layout := in.Layout
	if layout == "" {
		layout = LayoutModel
	}
	if err := config.OneOf(layout, Layouts); err != nil {
		return nil, fmt.Errorf("--layout: %w", err)
	}
	version := in.Summary.WCAG
	if version == "" {
		version = in.Run.Options.WCAG
	}
	d := &Draft{
		Layout: layout, Version: in.Version, Date: in.Now.Format(time.DateOnly), Owner: o,
		WCAG: version, EN301549: wcag.EN301549(version), Standard: wcag.Standard(version), Axe: in.Summary.Axe,
		Keyboard: in.Summary.Keyboard, EvidenceNote: model.EvidenceNote,
	}
	if a := in.Run.Options.A11y; a != nil {
		d.AxeRules = len(a.Rules)
		if d.Axe == "" {
			d.Axe = a.Axe
		}
	}
	hide := hidden(in)
	// A logged-in run fetched every page with the account's session:
	// its paths can name customers and orders.
	private := in.Run.Options.LoggedIn && !o.ListPrivatePaths
	clean := func(p string) string { return cleanPath(p, hide) }
	// A skipped flow's file and reason can name the staging host or the
	// login account. A copy keeps the run's summary as it was.
	d.Keyboard.FlowsSkipped = slices.Clone(d.Keyboard.FlowsSkipped)
	for i, f := range d.Keyboard.FlowsSkipped {
		d.Keyboard.FlowsSkipped[i] = model.SkippedFlow{File: cleanText(f.File, hide), Reason: cleanText(f.Reason, hide)}
	}

	// One pass over the pages: the paths checked, up to 3 per failed
	// criterion and the hosts of frames the audits didn't enter.
	failing := map[string]bool{}
	for _, f := range in.Summary.Criteria.Failed {
		failing[f.ID] = true
	}
	paths := map[string][]string{}
	note := func(crit []string, path string) {
		for _, c := range crit {
			if failing[c] && len(paths[c]) < maxPaths && !slices.Contains(paths[c], path) {
				paths[c] = append(paths[c], path)
			}
		}
	}
	site := in.Site
	if site == nil {
		site = in.Run.Issues
	}
	for _, i := range site {
		crit := wcag.IssueCriteria(i, version)
		for _, p := range i.Pages {
			note(crit, clean(p))
		}
	}
	checked, frames := map[string]bool{}, map[string]bool{}
	pages := in.Pages
	if pages == nil {
		pages = func(yield func(*model.Page) bool) {
			for i := range in.Run.Pages {
				if !yield(&in.Run.Pages[i]) {
					return
				}
			}
		}
	}
	pages(func(p *model.Page) bool {
		d.Pages++
		path := clean(p.Path)
		checked[path] = true
		for _, i := range p.Issues {
			note(wcag.IssueCriteria(i, version), path)
		}
		for _, a := range p.Audits {
			for _, h := range a.Frames {
				if h = strings.ToLower(h); h != "" && hideAll(h, hide) == h {
					frames[h] = true
				}
			}
		}
		return true
	})
	d.Frames = slices.Sorted(maps.Keys(frames))
	if private {
		d.PrivatePages = d.Pages
	} else {
		d.Checked = slices.Sorted(maps.Keys(checked))
	}

	manual := map[string]ManualCheck{}
	for _, m := range o.Manual {
		manual[m.Criterion] = m // a later entry for the same criterion wins
	}
	counts := map[string]model.CriterionCount{}
	for _, f := range in.Summary.Criteria.Failed {
		counts[f.ID] = f
	}
	noFailures := map[string]bool{}
	for _, id := range in.Summary.Criteria.NoFailuresFound {
		noFailures[id] = true
	}
	for _, c := range wcag.Criteria(version) {
		r := Row{Criterion: c, Result: ResultNotTested}
		if f, ok := counts[c.ID]; ok {
			r.Result, r.Issues, r.Pages = ResultFailures, f.Issues, f.Pages
			if !private {
				r.Paths = slices.Sorted(slices.Values(paths[c.ID]))
			}
		} else if noFailures[c.ID] {
			r.Result = ResultNoFailures
		}
		r.Tests = r.Result
		switch r.Tests {
		case ResultFailures:
			d.Failed++
		case ResultNotTested:
			d.NotTested++
		}
		// The owner's own result wins. The tests' counts stay on the row,
		// so a renderer can still say what they found.
		if m, ok := manual[c.ID]; ok {
			r.Manual = &Manual{Result: m.Result, By: m.By, Date: m.Date}
			r.Result = ResultManual
			delete(manual, c.ID)
		}
		d.Rows = append(d.Rows, r)
	}
	for _, id := range slices.SortedFunc(maps.Keys(manual), wcag.Compare) {
		d.Notes = append(d.Notes, fmt.Sprintf("Your manual result for %s is left out: WCAG %s has no criterion %s.", id, version, id))
	}

	for _, k := range o.KnownIssues {
		c, _ := wcag.Lookup(k.Criterion)
		d.KnownIssues = append(d.KnownIssues, Known{KnownIssue: k, Title: c.Title})
	}
	for _, c := range in.Ignored {
		if strings.HasPrefix(c.Reason, notCheckedReason) {
			continue // a page whose audit didn't finish: listed under NotAudited
		}
		crit := wcag.IssueCriteria(model.Issue{Check: c.Check, WCAG: c.WCAG}, version)
		if len(crit) == 0 {
			continue
		}
		ig := Ignored{Criteria: crit, Check: c.Check, Reason: cleanText(c.Reason, hide)}
		if !private {
			for _, p := range c.Pages {
				if p = clean(p); len(ig.Pages) < maxPaths && !slices.Contains(ig.Pages, p) {
					ig.Pages = append(ig.Pages, p)
				}
			}
		}
		d.Ignored = append(d.Ignored, ig)
	}
	for _, n := range in.Summary.NotAudited {
		n.Path = clean(n.Path)
		if private {
			n.Path = ""
		}
		n.Reason = cleanText(n.Reason, hide)
		d.NotAudited = append(d.NotAudited, n)
	}
	if in.Insecure {
		d.Notes = append(d.Notes, NoteInsecure)
	}
	return d, nil
}

// notCheckedReason starts the reason a comparison gives a page issue it
// kept because the page's audit didn't finish. Those aren't the owner's
// choices, so the draft doesn't list them as ignored.
const notCheckedReason = "not checked on this page"
