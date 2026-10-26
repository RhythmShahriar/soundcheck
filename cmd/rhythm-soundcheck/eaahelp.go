// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// helpWidth is the width the generated help topics wrap at, so they fit
// an 80 column terminal with room for the command name.
const helpWidth = 76

// limits are the fixed sentences on what EAA mode can't tell, in the
// words help eaa, the README and the statement's method section share.
// They are text the docs quote, so they stay word for word.
var limits = []string{
	"Soundcheck's EAA mode gives you evidence about your site. It is not an audit, not a certification and not legal advice.",
	"Many success criteria need a person: meaningful alt text, captions, reading order, plain language. The draft marks them not tested: needs a person, never passed.",
	"Only the pages and flows Soundcheck visited, in the states it saw, in Chrome, with the keyboard walk at desktop width. Content in iframes from other sites, PDFs, videos, apps and emails was not tested.",
	"The keyboard walkthrough uses Chrome's keyboard events. It is not a screen reader test, not a voice control test and not a test with disabled users.",
	"The site decides what Soundcheck sees. A site that serves headless browsers a different page can pass while visitors fail. Overlay widgets don't make a site accessible.",
	"EAA mode presses keys on your pages. It holds requests that would change data, blocks downloads and follows no links except skip links. A site that changes data on a GET or on focus can still change. Run it on staging and list your live site under production.",
	"Reports contain what your pages showed. Soundcheck masks form values, credentials and tokens it recognises and emails, phone numbers and card numbers in text it writes. Masking is pattern-based: it is pseudonymised, not anonymised. Screenshots are not masked.", //nolint:misspell // fixed text the docs quote
	"A signed evidence pack proves who produced it and that it wasn't changed afterwards. It doesn't prove the site was in that state for every visitor.",
	"Nothing leaves your machine except requests to your own site and hosts you allow. No telemetry, no hosted service, no AI.",
}

// wcagEM is the W3C's method for a person's evaluation of a website,
// which help eaa points to for what the tests leave out.
const wcagEM = "https://www.w3.org/WAI/test-evaluate/conformance/wcag-em/"

// wrap breaks text into lines of at most width runes, each starting with
// indent; the first line starts with first instead. A word longer than a
// line (a URL) gets a line of its own.
//
// The command name goes in first (named), so a wrap never splits it
// where named would look for it.
func wrap(text, first, indent string, width int) string {
	text = named(text)
	var b strings.Builder
	line := first
	empty := true
	for _, w := range strings.Fields(text) {
		if !empty && len([]rune(line))+1+len([]rune(w)) > width {
			b.WriteString(line + "\n")
			line, empty = indent, true
		}
		if !empty {
			line += " "
		}
		line += w
		empty = false
	}
	b.WriteString(line + "\n")
	return b.String()
}

// para wraps one paragraph at helpWidth with no indent.
func para(text string) string { return wrap(text, "", "", helpWidth) }

// item wraps one list item: "  - text", continued under the text.
func item(text string) string { return wrap(text, "  - ", "    ", helpWidth) }

// coverage counts the criteria of a WCAG version that Soundcheck's tests
// can find failures of (coverage partly in the catalog) and those
// that need a person.
func coverage(version string) (tested, person, all int) {
	for _, c := range wcag.Criteria(version) {
		all++
		if c.Coverage != "" && c.Coverage != "none" {
			tested++
		}
	}
	return tested, all - tested, all
}

// eaaHelp is help eaa: what EAA mode tests, what it can't, why the
// draft has [TO DO]s and the fixed limits.
func eaaHelp() string {
	t21, p21, n21 := coverage("2.1")
	t22, _, n22 := coverage("2.2")
	var b strings.Builder
	b.WriteString(`Usage:
  rhythm soundcheck snapshot <url> --eaa     the baseline, with a draft
  rhythm soundcheck compare --eaa            what changed since
  rhythm soundcheck statement -o accessibility-statement.md

`)
	b.WriteString(para("EAA mode gathers accessibility evidence for the European Accessibility Act. " +
		"It runs the health, a11y and keyboard modules in Chrome with the WCAG 2.1 A and AA rules " +
		"(--wcag 2.2 for the newer ones). A list from --modules or the config file keeps its modules " +
		"and gains a11y and keyboard. Every run writes a statement draft (eaa/statement-draft.md) " +
		"and an evidence pack (eaa/evidence.json) into its folder."))
	b.WriteString("\nWhat the tests do:\n")
	b.WriteString(item("a11y: axe-core " + axeVersion + " on every page with the rules of the WCAG version, " +
		"plus fields labeled only by a placeholder, sideways scrolling at 320 px wide and known overlay widgets."))
	b.WriteString(item("keyboard: Tab and Shift+Tab through every page; menus, dialogs and disclosures " +
		"opened and closed by keyboard; your key flows at --level flows (help keyboard)."))
	b.WriteString(item("statement: the draft lists what the tests found per criterion, " +
		"with pages and counts (help statement); evidence verify and sign check the pack (help evidence)."))
	b.WriteString("\nWhat they can't do:\n")
	b.WriteString(para(fmt.Sprintf("Of the %d WCAG 2.1 A and AA success criteria, the tests look for failures of %d "+
		"(%d of the %d in WCAG 2.2). The other %d need a person. Even for a criterion the tests look at, "+
		"only a person can say it is met. A person's review can follow WCAG-EM, the W3C's method for "+
		"evaluating a website: %s", n21, t21, t22, n22, p21, wcagEM)))
	b.WriteString("\nWhy the draft has [TO DO]s:\n")
	b.WriteString(para("Only you can say who runs the site, how visitors reach you, which status fits, " +
		"what is out of scope and what a person checked. The draft marks each of these [TO DO]. " +
		"Put your details under statement: in .soundcheck/config.yaml (rhythm soundcheck init --eaa " +
		"writes the block) and run rhythm soundcheck statement again: your text lives there, so a new " +
		"draft never loses it."))
	b.WriteString("\nLimits:\n")
	b.WriteString(item(model.EvidenceNote))
	for _, s := range limits {
		b.WriteString(item(s))
	}
	return b.String()
}

// criteriaText names the WCAG criteria a check fails, for --list-checks
// and help keyboard: "wcag 2.1.2", "wcag 2.4.11 with --wcag 2.2" or
// "advisory" for a check that fails none.
func criteriaText(id string) string {
	if strings.Contains(id, "<") { // a pattern such as a11y/<axe rule id>
		return "wcag from the rule's tags"
	}
	c21, c22 := checkCriteria(id, "2.1"), checkCriteria(id, "2.2")
	switch {
	case len(c21) > 0:
		return "wcag " + strings.Join(c21, ", ")
	case len(c22) > 0:
		return "wcag " + strings.Join(c22, ", ") + " with --wcag 2.2"
	}
	return "advisory"
}

// checkCriteria returns the criteria a check fails under a WCAG version:
// the catalogue's (wcag.ForCheck), else the keyboard check's own.
func checkCriteria(id, version string) []string {
	if c := wcag.ForCheck(id, version); len(c) > 0 {
		return c
	}
	if info, ok := ids.Lookup(id); ok {
		return info.WCAG(version)
	}
	return nil
}

// keyboardHelp is help keyboard: every keyboard check with its level and
// criteria, the settings and what the walk never does.
func keyboardHelp() string {
	var b strings.Builder
	b.WriteString(`Usage:
  rhythm soundcheck snapshot <url> --eaa
  rhythm soundcheck run <url> --modules +keyboard

`)
	b.WriteString(para("The keyboard module walks each page the way a keyboard user would, in Chrome only. " +
		"It presses Tab until focus comes back to the start (up to keyboard.max_tabs), then Shift+Tab back, " +
		"and records every stop. On the first page of each URL group and on keyboard.pages it opens and " +
		"closes menus, dialogs and disclosures by keyboard and checks where focus goes. Key flows " +
		"(flows with keyboard: true or send) run at --level flows; rhythm soundcheck flows init --keyboard " +
		"writes four to edit."))
	b.WriteString("\nChecks:\n")
	for _, c := range ids.Checks {
		b.WriteString(wrap(fmt.Sprintf("%s (%s, %s): %s.", c.ID, c.Level, criteriaText(c.ID), c.Title), "  ", "      ", helpWidth))
	}
	b.WriteString("\nSettings (keyboard: in .soundcheck/config.yaml):\n")
	for _, s := range []string{
		"max_tabs: Tab presses per page before the walk stops, 10-2000 (default 150); a walk that stops there is capped, never a trap.",
		"widgets: menus, dialogs and disclosures tested per page, 0-200 (default 10).",
		"trap_after: presses on the same element or region that count as a trap, 3-20 (default 4).",
		"skip_link: expected (a missing skip link is a notice), required (a warning) or off.",
		"pages: globs of the pages whose widgets are tested besides the first of each URL group.",
		"ignore: CSS selectors the walk steps over, such as a cookie banner.",
	} {
		b.WriteString(item(s))
	}
	b.WriteString("\nWhat the walk never does:\n")
	for _, s := range []string{
		"press Enter on a link that leaves the page, or Enter or Space on a submit control or anything that looks destructive (delete, remove, log out, unsubscribe, place order, pay, buy, checkout, empty cart);",
		"type into fields, act inside frames from other sites or on keyboard.ignore selectors;",
		"run on /wp-admin or /wp-login.php.",
	} {
		b.WriteString(item(s))
	}
	b.WriteString("\n")
	b.WriteString(para("A key flow with keyboard: true holds every request that would send data: the step that " +
		"would send counts as done and the rest are listed as not run. keyboard: send really submits, " +
		"only on this machine or a private address or with --staging. It never submits on a site listed under production."))
	return b.String()
}

// generatedTopics are the help topics built from the catalogs, so
// their counts and check lists can't drift from the code.
var generatedTopics = map[string]func() string{"eaa": eaaHelp, "keyboard": keyboardHelp}

// listedCriteria reports whether --list-checks names the criteria of a
// module's checks: the modules whose issues map to WCAG.
func listedCriteria(module string) bool { return slices.Contains(eaaNeeds, module) }
