// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// The HTML report opens from disk, from a CI artifact or from a mail,
// so it is locked down with a Content-Security-Policy: nothing may load
// from the network and only the report's own script may run. The
// script's hash is computed from the template when the package loads,
// so the policy can't fall out of step with it.

// scriptPattern finds an inline script.
var scriptPattern = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// ScriptHash is the CSP source of the report's inline script,
// "sha256-<base64>".
var ScriptHash = scriptHash()

// CSP is the Content-Security-Policy every HTML report carries in a
// meta element: no network at all, images only as data: URLs, inline
// styles and the one inline script by its hash.
var CSP = "default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src '" + ScriptHash + "'"

// scriptHash hashes the report's inline script as the template writes
// it, which isn't the template's text: html/template drops the
// script's comments. The script has no template actions, so every
// report holds the same one.
func scriptHash() string {
	if m := scriptPattern.FindStringSubmatch(htmlSource); m == nil || strings.Contains(m[1], "{{") {
		panic("report: the HTML template needs one inline script without template actions")
	}
	var b strings.Builder
	if err := htmlTemplate.Execute(&b, htmlData{}); err != nil {
		panic("report: the HTML template doesn't run: " + err.Error())
	}
	m := scriptPattern.FindStringSubmatch(b.String())
	if m == nil {
		panic("report: the HTML report has no inline script")
	}
	return CSPHash(m[1])
}

// CSPHash is the CSP hash source of a script's text: "sha256-<base64>".
func CSPHash(script string) string {
	sum := sha256.Sum256([]byte(script))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// The Accessibility tab of an --eaa report.

// tileStates say how a criterion tile shows each result: never by
// color alone, always with a word and an icon.
var tileStates = map[string]struct{ Label, Icon string }{
	"failed": {"failures", "✕"},
	"none":   {"none found", "○"},
	"manual": {"needs a person", "?"},
}

type htmlEAA struct {
	Version, Counts, Note string
	NotChecked            []string
	// Compared marks a comparison, whose unchecked pages keep their
	// issues from the snapshot.
	Compared   bool
	Principles []htmlPrinciple
	Keyboard   []htmlKeyboard
	Criteria   []htmlCriterion
	Statement  string
}

type htmlPrinciple struct {
	Name  string
	Tiles []htmlTile
}

type htmlTile struct {
	ID, Title, State, Label, Icon string
	Issues                        int
}

type htmlKeyboard struct {
	Path, Summary string
	Stops         []string // nil: the trace couldn't be read
	More          int      // stops past the ones listed
	Crops         []htmlCrop
}

type htmlCrop struct {
	URL          template.URL
	Alt, Caption string
}

type htmlCriterion struct {
	ID, Title, EN string
	Pages         []htmlCriterionPage
	MorePages     int
}

type htmlCriterionPage struct {
	Path  string
	Items []htmlCriterionItem
}

type htmlCriterionItem struct {
	Key, Label, Title, Anchor string
}

// statementCommand is how to make the statement draft, as the report's
// box gives it.
const statementCommand = "rhythm soundcheck statement -o accessibility-statement.md"

// Limits that keep the tab readable on a large site.
const (
	maxTraceStops   = 150     // focus stops listed per page
	maxTraceBytes   = 8 << 20 // a trace file bigger than this isn't read
	maxCriterionPgs = 10      // pages listed under a criterion
)

// eaaTab builds the Accessibility tab, or nil without --eaa. items are
// the report's issues or changes in the order the Results tab shows
// them, with their anchors.
func (r Result) eaaTab(items []htmlItem, src []eaaSource) *htmlEAA {
	e := eaaOf(r)
	if e == nil {
		return nil
	}
	version := eaaVersion(e)
	t := &htmlEAA{Version: version, Note: model.EvidenceNote, Statement: statementCommand, Compared: r.Compared(),
		Counts: strings.TrimPrefix(accessibilityLine(r, false), "Accessibility: ")}
	for _, n := range e.NotAudited {
		why := n.Reason
		if why == "" {
			why = n.Status
		}
		t.NotChecked = append(t.NotChecked, fmt.Sprintf("%s (%s: %s)", n.Path, n.Module, why))
	}
	failed := map[string]int{}
	for _, f := range e.Criteria.Failed {
		failed[f.ID] = f.Issues
	}
	for _, c := range wcag.Criteria(version) {
		state := "manual"
		switch {
		case failed[c.ID] > 0 || slices.ContainsFunc(e.Criteria.Failed, func(f model.CriterionCount) bool { return f.ID == c.ID }):
			state = "failed"
		case slices.Contains(e.Criteria.NoFailuresFound, c.ID):
			state = "none"
		}
		if len(t.Principles) == 0 || t.Principles[len(t.Principles)-1].Name != c.Principle {
			t.Principles = append(t.Principles, htmlPrinciple{Name: c.Principle})
		}
		p := &t.Principles[len(t.Principles)-1]
		s := tileStates[state]
		p.Tiles = append(p.Tiles, htmlTile{ID: c.ID, Title: c.Title, State: state, Label: s.Label, Icon: s.Icon, Issues: failed[c.ID]})
	}
	t.Criteria = criteriaGroups(version, items, src)
	for i := range r.Run.Pages {
		if pg := &r.Run.Pages[i]; pg.Keyboard != nil && !pg.Canceled() {
			t.Keyboard = append(t.Keyboard, r.keyboardSection(pg))
		}
	}
	return t
}

// eaaSource is what the tab needs of an issue or change beside its card.
type eaaSource struct {
	Check string
	WCAG  []string
	Pages []string
}

// criteriaGroups lists the accessibility and keyboard items under each
// criterion they fail, then by page, in criterion order.
func criteriaGroups(version string, items []htmlItem, src []eaaSource) []htmlCriterion {
	titles := criteriaTitles(version)
	by := map[string]*htmlCriterion{}
	var ids []string
	for n, s := range src {
		if !accessibilityCheck(s.Check) {
			continue
		}
		it := items[n]
		for _, id := range s.WCAG {
			c := by[id]
			if c == nil {
				c = &htmlCriterion{ID: id, Title: titles[id], EN: "9." + id}
				by[id] = c
				ids = append(ids, id)
			}
			pages := s.Pages
			if len(pages) == 0 {
				pages = []string{"site-wide"}
			}
			for _, path := range pages {
				at := slices.IndexFunc(c.Pages, func(p htmlCriterionPage) bool { return p.Path == path })
				if at < 0 {
					c.Pages = append(c.Pages, htmlCriterionPage{Path: path})
					at = len(c.Pages) - 1
				}
				c.Pages[at].Items = append(c.Pages[at].Items, htmlCriterionItem{Key: it.Key, Label: it.Label, Title: it.Title, Anchor: it.Anchor})
			}
		}
	}
	var out []htmlCriterion
	for _, id := range sortedCriteria(ids) {
		c := *by[id]
		if len(c.Pages) > maxCriterionPgs {
			c.MorePages = len(c.Pages) - maxCriterionPgs
			c.Pages = c.Pages[:maxCriterionPgs]
		}
		out = append(out, c)
	}
	return out
}

// sortedCriteria returns ids in criterion order: 1.4.3 before 1.4.10.
func sortedCriteria(ids []string) []string {
	ids = slices.Clone(ids)
	slices.SortFunc(ids, wcag.Compare)
	return slices.Compact(ids)
}

// keyboardSection is a page's keyboard walk: its focus path from the
// trace file and the crops of elements with no focus ring or a trap.
func (r Result) keyboardSection(pg *model.Page) htmlKeyboard {
	k := pg.Keyboard
	h := htmlKeyboard{Path: pg.Path, Summary: strings.TrimPrefix(keyboardLine(pg), "keyboard: "+Term(pg.Path)+" ")}
	h.Stops, h.More = r.focusPath(k.Evidence)
	keys := make([]string, 0, len(k.Crops))
	for key := range k.Crops {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		path, err := r.Path(k.Crops[key])
		if err != nil {
			continue
		}
		u, err := embedImage(path)
		if err != nil {
			continue
		}
		check, source, _ := strings.Cut(key, "|")
		h.Crops = append(h.Crops, htmlCrop{URL: u, Alt: "Focused element " + source + " (" + check + ")", Caption: check + ": " + source})
	}
	return h
}

// focusPath reads the forward focus path from a keyboard trace file,
// first stops first and how many stops it left out. It reads only each
// stop's selector and whether the page itself had focus: the trace's
// other fields can hold accessible names. Nil means the trace is
// missing or unreadable.
func (r Result) focusPath(rel string) ([]string, int) {
	if rel == "" {
		return nil, 0
	}
	path, err := r.Path(rel)
	if err != nil {
		return nil, 0
	}
	f, err := os.Open(path) //nolint:gosec // a trace file inside the run folder
	if err != nil {
		return nil, 0
	}
	defer func() { _ = f.Close() }()
	var trace struct {
		Forward []struct {
			Selector string `json:"selector"`
			IsBody   bool   `json:"is_body"`
		} `json:"forward"`
	}
	if err := json.NewDecoder(io.LimitReader(f, maxTraceBytes)).Decode(&trace); err != nil {
		return nil, 0
	}
	stops := []string{}
	for _, s := range trace.Forward[:min(len(trace.Forward), maxTraceStops)] {
		if s.IsBody {
			stops = append(stops, "(page)")
		} else {
			stops = append(stops, s.Selector)
		}
	}
	return stops, max(0, len(trace.Forward)-maxTraceStops)
}
