// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// The --eaa additions every format shares: the accessibility summary,
// the criteria an issue fails and the keyboard path that led to it.

// eaaOf is the accessibility summary a result shows, or nil when the
// run wasn't made with --eaa: the comparison's for a comparison, else
// the run's.
func eaaOf(r Result) *model.EAASummary {
	if r.Comparison != nil && r.Comparison.EAA != nil {
		return r.Comparison.EAA
	}
	if r.Run != nil {
		return r.Run.EAA
	}
	return nil
}

// EAA is the accessibility summary a result shows, or nil when the run
// wasn't made with --eaa: the comparison's for a comparison, else the
// run's. Other packages (the client report) use it to pick the same
// summary every format of this package shows.
func EAA(r Result) *model.EAASummary { return eaaOf(r) }

// accessibilityCheck reports whether check is an accessibility or
// keyboard check, whose issues the EAA summary counts.
func accessibilityCheck(check string) bool {
	return strings.HasPrefix(check, "a11y/") || strings.HasPrefix(check, "keyboard/")
}

// whereLabel is pagesLabel for an issue or change of check. An
// accessibility or keyboard problem is often on most pages of a site,
// so past three pages its label also says how many: "/, /a/, /b/ +35
// more (38 pages)".
func whereLabel(check string, pages []string) string {
	label := pagesLabel(pages, 3)
	if accessibilityCheck(check) && len(pages) > 3 {
		label += fmt.Sprintf(" (%d pages)", len(pages))
	}
	return label
}

// wcagLine is "wcag: 1.3.1, 4.1.2 · EN 301 549 9.1.3.1, 9.4.1.2", or ""
// for an issue that maps to no criterion. The ids come from the run's
// file, so they are made safe like any other text from it.
func wcagLine(ids, en []string) string {
	if len(ids) == 0 {
		return ""
	}
	line := "wcag: " + strings.Join(termAll(ids), ", ")
	if len(en) > 0 {
		line += " · EN 301 549 " + strings.Join(termAll(en), ", ")
	}
	return line
}

// pathStops is how many of the last focus stops a path line shows.
const pathStops = 6

// pathLine is "path: #email → #phone → #card ×4": the last six focus
// stops before a keyboard problem, with a stop repeated in a row
// folded into one. Plain mode writes "path: #email then #phone then
// #card (4 times)" for screen readers and logs: words, as ">" and a
// trailing "x4" read as part of a CSS selector. It is "" without a
// path.
func pathLine(path []string, plain bool) string {
	if len(path) == 0 {
		return ""
	}
	path = path[max(0, len(path)-pathStops):]
	arrow, times, timesEnd := " → ", " ×", ""
	if plain {
		arrow, times, timesEnd = " then ", " (", " times)"
	}
	var parts []string
	for i := 0; i < len(path); {
		j := i + 1
		for j < len(path) && path[j] == path[i] {
			j++
		}
		stop := Term(path[i])
		if j-i > 1 {
			stop += fmt.Sprintf("%s%d%s", times, j-i, timesEnd)
		}
		parts = append(parts, stop)
		i = j
	}
	return "path: " + strings.Join(parts, arrow)
}

// issueExtras are the wcag and path lines under an issue or change.
func issueExtras(check string, ids, en, path []string, plain bool) []string {
	var lines []string
	if l := wcagLine(ids, en); l != "" {
		if plain {
			// Commas already part the ids, so a semicolon parts the lists.
			l = strings.ReplaceAll(l, " · ", "; ")
		}
		lines = append(lines, l)
	}
	if strings.HasPrefix(check, "keyboard/") {
		if l := pathLine(path, plain); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// notCheckedLines say which pages a module couldn't audit in a run or
// snapshot. A comparison's warnings say it with "its issues are kept
// from the snapshot", which a run has none of.
func notCheckedLines(e *model.EAASummary) []string {
	var lines []string
	for _, n := range e.NotAudited {
		why := n.Reason
		if why == "" {
			why = n.Status
		}
		lines = append(lines, fmt.Sprintf("not checked: %s (%s: %s)", Term(n.Path), Term(n.Module), Term(why)))
	}
	return lines
}

// eaaVersion is the summary's WCAG version, 2.0 when it has none.
func eaaVersion(e *model.EAASummary) string {
	if e.WCAG == "" {
		return "2.0"
	}
	return e.WCAG
}

// eaaTotal is how many A and AA criteria the summary's WCAG version
// has. A version the catalog doesn't know counts what the summary
// sorted.
func eaaTotal(e *model.EAASummary) int {
	if n := len(wcag.Criteria(eaaVersion(e))); n > 0 {
		return n
	}
	return len(e.Criteria.Failed) + len(e.Criteria.NoFailuresFound) + len(e.Criteria.NotTested)
}

// accessibilityLine is the counts line of an --eaa result:
// "Accessibility: 6 of 50 WCAG 2.1 A and AA criteria have failures · 20
// had none found by these tests · 24 need a person". A comparison adds
// the criteria whose failures are all new and the open issues before
// and after. It is "" for a result without EAA data.
func accessibilityLine(r Result, plain bool) string {
	e := eaaOf(r)
	if e == nil {
		return ""
	}
	failed := fmt.Sprintf("%d of %d WCAG %s A and AA criteria have failures", len(e.Criteria.Failed), eaaTotal(e), Term(eaaVersion(e)))
	var open string
	if c := r.Comparison; c != nil {
		if ids := newCriteria(c, e); len(ids) > 0 {
			failed += " (" + strings.Join(termAll(ids), ", ") + " new)"
		}
		before, after := openBefore(c, e.Open), e.Open
		arrow := "→"
		if plain {
			arrow = "to"
		}
		open = fmt.Sprintf(" · open issues %d %s %d", before, arrow, after)
	}
	return fmt.Sprintf("Accessibility: %s · %s%s", failed, restCounts(e), open)
}

// restCounts is the criteria without failures of a summary: "20 had
// none found by these tests · 24 need a person". Criteria whose tests
// didn't finish (or didn't run) are counted apart from the ones that
// need a person: "· 4 not tested (their tests didn't finish)". An
// interrupted run leaves out the none found count, which comes from
// the pages it finished only.
func restCounts(e *model.EAASummary) string {
	unrun, person := wcag.SplitNotTested(eaaVersion(e), e.Criteria.NotTested)
	var parts []string
	if e.Interrupted {
		parts = append(parts, fmt.Sprintf("%d had none found on the pages checked before the stop", len(e.Criteria.NoFailuresFound)))
	} else {
		parts = append(parts, fmt.Sprintf("%d had none found by these tests", len(e.Criteria.NoFailuresFound)))
	}
	if len(unrun) > 0 {
		why := "didn't run"
		if len(e.NotAudited) > 0 {
			why = "didn't finish"
		}
		parts = append(parts, fmt.Sprintf("%d not tested (their tests %s)", len(unrun), why))
	}
	parts = append(parts, fmt.Sprintf("%d need a person", len(person)))
	// A page whose tests didn't finish is said even when other pages
	// tested the same criteria: its own problems may be missing.
	if n := notAuditedPages(e); n > 0 {
		parts = append(parts, plural(n, "page not fully tested", "pages not fully tested"))
	}
	return strings.Join(parts, " · ")
}

// notAuditedPages counts the pages with a module whose tests didn't
// finish.
func notAuditedPages(e *model.EAASummary) int {
	seen := map[string]bool{}
	for _, n := range e.NotAudited {
		seen[n.Path] = true
	}
	return len(seen)
}

// newCriteria lists the criteria with failures whose every counted
// issue is a NEW or BROKE change: failures the site didn't have at the
// snapshot. The summary counts distinct issue keys and a change is one
// key, so a criterion is new when its NEW and BROKE changes cover its
// count.
func newCriteria(c *model.Comparison, e *model.EAASummary) []string {
	added := map[string]int{}
	for _, ch := range c.Changes {
		if ch.Class != model.New && ch.Class != model.Broke {
			continue
		}
		for _, id := range ch.WCAG {
			added[id]++
		}
	}
	var out []string
	for _, f := range e.Criteria.Failed {
		if n := added[f.ID]; n > 0 && n >= f.Issues {
			out = append(out, f.ID)
		}
	}
	return out
}

// openBefore is how many accessibility and keyboard issues were open at
// the snapshot: the ones open now, less the NEW and BROKE ones, plus
// the FIXED ones. Issues found now on a page whose audit didn't finish
// are set aside as not checked (model.NotCheckedReason) but still
// counted as open now; the NEW and BROKE ones among them weren't open
// at the snapshot either.
func openBefore(c *model.Comparison, now int) int {
	n := now
	for _, ch := range c.Changes {
		if !accessibilityCheck(ch.Check) {
			continue
		}
		switch ch.Class {
		case model.New, model.Broke:
			n--
		case model.Fixed:
			n++
		}
	}
	for _, ch := range c.Ignored {
		if accessibilityCheck(ch.Check) && (ch.Class == model.New || ch.Class == model.Broke) &&
			strings.HasPrefix(ch.Reason, model.NotCheckedReason) {
			n--
		}
	}
	return max(0, n)
}

// noFailuresVerdict replaces "all clear" for an --eaa result with no
// issues: the tests found nothing, which isn't the same as nothing
// being wrong.
const noFailuresVerdict = "no failures found by these tests"

// eaaVerdict is what an --eaa result with no open problem says: no
// failures found by these tests when no criterion has one, else how
// many criteria still have failures, from findings set aside by ignore
// rules or kept from the snapshot for a page whose audit didn't finish.
func eaaVerdict(e *model.EAASummary) string {
	if n := len(e.Criteria.Failed); n > 0 {
		return plural(n, "WCAG criterion has failures", "WCAG criteria have failures")
	}
	return noFailuresVerdict
}

// keyboardLine is the -v line of a page the keyboard module walked:
// "keyboard: / 23 stops, 3 widgets (menu, dialog, details), 0.9s".
func keyboardLine(pg *model.Page) string {
	k := pg.Keyboard
	if k == nil {
		return ""
	}
	widgets := plural(k.Widgets, "widget", "widgets")
	if len(k.WidgetKinds) > 0 {
		widgets += " (" + strings.Join(termAll(k.WidgetKinds), ", ") + ")"
	}
	return fmt.Sprintf("keyboard: %s %s, %s, %.1fs", Term(pg.Path), plural(k.Stops, "stop", "stops"), widgets, float64(k.MS)/1000)
}

// defaultColumns is the width text is wrapped at when the terminal's
// isn't known.
const defaultColumns = 80

// keepTogether are names wrap never breaks across lines.
var keepTogether = strings.NewReplacer("EN 301 549", "EN\x00301\x00549")

// wrap breaks s into lines of at most cols characters at spaces. A
// word longer than a line gets a line of its own and "EN 301 549" stays
// on one line.
func wrap(s string, cols int) []string {
	cols = max(cols, 20)
	var lines []string
	line := ""
	for _, w := range strings.Fields(keepTogether.Replace(s)) {
		w = strings.ReplaceAll(w, "\x00", " ")
		switch {
		case line == "":
			line = w
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) <= cols:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// mdCriteria is " (1.3.1, 4.1.2)" after the title of an accessibility
// or keyboard row of a Markdown table, or "" for other rows.
func mdCriteria(check string, ids []string) string {
	if !accessibilityCheck(check) || len(ids) == 0 {
		return ""
	}
	return " (" + mdCell(strings.Join(ids, ", ")) + ")"
}

// writeAccessibility writes the Accessibility section of an --eaa
// result's Markdown. It may be posted as a PR comment, where anyone
// with access to the repository reads it, so it holds counts, check
// ids, criteria and paths only: never an issue's title, detail, source
// or a screenshot, which can carry what a visitor typed or a name.
func writeAccessibility(b *strings.Builder, r Result) {
	e := eaaOf(r)
	if e == nil {
		return
	}
	version := eaaVersion(e)
	fmt.Fprintf(b, "### Accessibility (WCAG %s A and AA)\n\n", mdCell(version))
	fmt.Fprintf(b, "%d of %d criteria have failures · %s · %s\n\n",
		len(e.Criteria.Failed), eaaTotal(e), restCounts(e), plural(e.Open, "open issue", "open issues"))
	if len(e.Criteria.Failed) > 0 {
		var fresh []string
		if r.Comparison != nil {
			fresh = newCriteria(r.Comparison, e)
		}
		titles := criteriaTitles(version)
		b.WriteString("<details><summary>Criteria with failures</summary>\n\n")
		b.WriteString("| Criterion | EN 301 549 | Issues | Pages |\n|---|---|---:|---:|\n")
		for _, f := range e.Criteria.Failed {
			name := f.ID
			if t := titles[f.ID]; t != "" {
				name += " " + t
			}
			issues := fmt.Sprint(f.Issues)
			if slices.Contains(fresh, f.ID) {
				issues += " (new)"
			}
			fmt.Fprintf(b, "| %s | %s | %s | %d |\n", mdCell(name), mdCell("9."+f.ID), issues, f.Pages)
		}
		b.WriteString("\n</details>\n\n")
	}
	if len(e.NotAudited) > 0 {
		// The status, not the reason: a reason can quote the page.
		if r.Compared() {
			b.WriteString("Not checked, so their issues are kept from the snapshot and not counted as fixed:\n\n")
		} else {
			b.WriteString("Not checked, so their issues are missing from these results:\n\n")
		}
		for _, n := range e.NotAudited {
			fmt.Fprintf(b, "- %s (%s: %s)\n", mdPages([]string{n.Path}), mdCell(n.Module), mdCell(n.Status))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "> %s\n\n", mdCell(model.EvidenceNote))
}

// criteriaTitles maps each criterion of a WCAG version to its short
// title.
func criteriaTitles(version string) map[string]string {
	m := map[string]string{}
	for _, c := range wcag.Criteria(version) {
		m[c.ID] = c.Title
	}
	return m
}
