// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// Limits are the fixed sentences on what the tests can and can't tell,
// which the method section of every draft carries word for word after
// the facts of the run (PRD 15, sentences 1 to 9). help eaa and the
// README carry them too.
var Limits = []string{
	"Soundcheck's EAA mode gives you evidence about your site. It is not an audit, not a certification and not legal advice.",
	"Many success criteria need a person: meaningful alt text, captions, reading order, plain language. The draft marks them not tested: needs a person, never passed.",
	"Only the pages and flows Soundcheck visited, in the states it saw, in Chrome, with the keyboard walk at desktop width. Content in iframes from other sites, PDFs, videos, apps and emails was not tested.",
	"The keyboard walkthrough uses Chrome's keyboard events. It is not a screen reader test, not a voice control test and not a test with disabled users.",
	"The site decides what Soundcheck sees. A site that serves headless browsers a different page can pass while visitors fail. Overlay widgets don't make a site accessible.",
	"EAA mode presses keys on your pages. It holds requests that would change data, blocks downloads and follows no links except skip links. A site that changes data on a GET or on focus can still change. Run it on staging and list your live site under production.",
	//nolint:misspell // PRD 15 wording, word for word (British, like the rest of the copy)
	"Reports contain what your pages showed. Soundcheck masks form values, credentials and tokens it recognises and emails, phone numbers and card numbers in text it writes. Masking is pattern-based: it is pseudonymised, not anonymised. Screenshots are not masked.",
	"A signed evidence pack proves who produced it and that it wasn't changed afterwards. It doesn't prove the site was in that state for every visitor.",
	"Nothing leaves your machine except requests to your own site and hosts you allow. No telemetry, no hosted service, no AI.",
}

// Fixed text of the draft.
const (
	// BannerTitle opens the box the owner deletes once the draft is
	// theirs.
	BannerTitle = "Draft for you to review. Not ready to publish."
	// BannerEnd closes it.
	BannerEnd = `Fill in every [TO DO], test the criteria marked "needs a person", then delete this box.`
	// StatusOptions are the only words a draft has for a conformance
	// status. They sit inside the [TO DO] that asks the owner to choose
	// one: Soundcheck never does.
	StatusOptions = `choose one after your review: "fully compliant", "partially compliant" or "not compliant" with `
	// OutsideClauses names the parts of EN 301 549 a website check
	// doesn't reach.
	OutsideClauses = "EN 301 549 clauses 5 (generic requirements, such as closed functionality), 6 (two-way voice communication), 7 (video capabilities), 10 (non-web documents), 11 (software) and 12 (documentation and support services) are outside a website check and weren't tested."
)

// todoPrefix starts a marker in text that came in from outside the
// draft's own wording, such as a placeholder owner the CLI fills in.
const todoPrefix = "[TO DO:"

// The draft is built once as a small document of blocks and spans and
// then written as Markdown or HTML, so the two formats always say the
// same thing and count the same [TO DO] markers.

// span is a run of text in a line. Text is plain (see plain): the
// writers escape it for their format.
type span struct {
	Kind string // spanText, spanStrong or spanTodo
	Text string
}

const (
	spanText   = "text"
	spanStrong = "strong"
	spanTodo   = "todo" // written as [TO DO: Text]
)

// line is a paragraph, a list item or a table cell.
type line []span

// part is a heading, a paragraph, a list or a table.
type part struct {
	Kind  string // blockH2, blockH3, blockPara, blockList or blockTable
	Lines []line // the heading or paragraph (one line) or the list items
	Head  []string
	Rows  [][]line
}

const (
	blockH2    = "h2"
	blockH3    = "h3"
	blockPara  = "p"
	blockList  = "ul"
	blockTable = "table"
)

// doc is a draft ready to write.
type doc struct {
	Title  string
	Banner []line
	Blocks []part
	Todo   int
}

func text(s string) span   { return span{spanText, plain(s)} }
func strong(s string) span { return span{spanStrong, plain(s)} }
func todo(s string) span   { return span{spanTodo, plain(s)} }

// add appends a part and counts its markers: the draft's own and the
// ones in text from outside it.
func (d *doc) add(b part) {
	count := func(l line) {
		for _, s := range l {
			if s.Kind == spanTodo {
				d.Todo++
			} else {
				d.Todo += strings.Count(s.Text, todoPrefix)
			}
		}
	}
	for _, l := range b.Lines {
		count(l)
	}
	for _, r := range b.Rows {
		for _, c := range r {
			count(c)
		}
	}
	d.Blocks = append(d.Blocks, b)
}

func (d *doc) h2(s string)        { d.add(part{Kind: blockH2, Lines: []line{{text(s)}}}) }
func (d *doc) h3(s string)        { d.add(part{Kind: blockH3, Lines: []line{{text(s)}}}) }
func (d *doc) para(spans ...span) { d.add(part{Kind: blockPara, Lines: []line{spans}}) }
func (d *doc) list(items ...line) { d.add(part{Kind: blockList, Lines: items}) }
func (d *doc) listIf(items ...line) {
	if len(items) > 0 {
		d.list(items...)
	}
}

// build lays out the draft in its layout: the model statement of
// Implementing Decision (EU) 2018/1523 or the service information of
// the European Accessibility Act, Annex V.
func build(dr *Draft) *doc {
	service := dr.Layout == LayoutService
	o := dr.Owner
	d := &doc{}
	noun := "statement"
	if service {
		noun = "information"
		d.Title = "Accessibility information for " + plain(o.Owner)
	} else {
		d.Title = "Accessibility statement for " + plain(o.Owner)
	}
	d.Todo += strings.Count(d.Title, todoPrefix)
	d.Banner = []line{
		{strong(BannerTitle)},
		{text(fmt.Sprintf("Rhythm Soundcheck %s wrote this on %s from a check of %s of %s.", dr.Version, dr.Date, count(dr.Pages, "page"), o.Website))},
		{text(dr.EvidenceNote)},
		{text(BannerEnd)},
	}
	for _, l := range d.Banner {
		d.Todo += strings.Count(l[0].Text, todoPrefix)
	}

	// Who and what.
	d.para(text(fmt.Sprintf("%s wants %s to be usable by as many people as possible.", o.Owner, o.Website)))
	if len(o.Scope) > 0 {
		d.para(text(fmt.Sprintf("This %s covers %s.", noun, joinAnd(o.Scope))))
	} else {
		d.para(text(fmt.Sprintf("This %s covers the %s Soundcheck checked, listed under \"Pages checked\". ", noun, count(dr.Pages, "page"))),
			todo("set statement.scope to the pages this "+noun+" covers, or check that these are all of them."))
	}
	if service {
		d.h2("About the service")
		if strings.TrimSpace(o.Service) != "" {
			d.para(text(o.Service))
		} else {
			d.para(todo("describe the service in general terms, in words people with disabilities can use; or set statement.service."))
		}
		d.h2("How the service meets the accessibility requirements")
	} else {
		d.h2("How accessible this website is")
	}
	d.para(todo(fmt.Sprintf("%s%s. Soundcheck does not choose this for you. Its tests found failures of %d of %d criteria and could not test %d of them.",
		StatusOptions, dr.Standard, dr.Failed, len(dr.Rows), dr.NotTested)))

	if !service {
		d.h2("What isn't accessible yet")
	}
	problems(d, dr)
	if len(dr.KnownIssues) > 0 {
		d.h3("Problems you know about")
		var items []line
		for _, k := range dr.KnownIssues {
			l := line{strong(k.Criterion + " " + k.Title), text(": " + k.Text)}
			if k.Until != "" {
				l = append(l, text(" Planned to be fixed by "+k.Until+"."))
			}
			items = append(items, l)
		}
		d.list(items...)
	}
	if len(dr.Ignored) > 0 {
		d.h3("Accepted or ignored in Soundcheck")
		d.para(text("These problems were found and left out of Soundcheck's results by your ignore rules or accepts. They still count against their criteria above."))
		var items []line
		for _, ig := range dr.Ignored {
			s := " (" + ig.Check + ")"
			if len(ig.Pages) > 0 {
				s += " on " + joinAnd(ig.Pages)
			}
			reason := ig.Reason
			if strings.TrimSpace(reason) == "" {
				reason = "no reason given"
			}
			items = append(items, line{strong(strings.Join(ig.Criteria, ", ")), text(s + ": " + reason)})
		}
		d.list(items...)
	}
	d.h3("Disproportionate burden")
	d.para(todo("say if fixing any of this would be a disproportionate burden and why, or delete this section."))
	if !service {
		d.h3("Content outside the scope of the law")
		d.para(todo("list content the law doesn't cover, such as third-party content you don't control, or delete this section."))
	} else {
		d.h2("Accessibility features")
		d.para(todo("describe how the service works for people with disabilities, such as keyboard use, captions, text alternatives or other ways to reach you."))
	}

	method(d, dr, noun)
	contact(d, o)
	d.h2("Enforcement")
	if strings.TrimSpace(o.Enforcement) != "" {
		d.para(todo("check this."), text(" If our answer doesn't satisfy you, you can contact: "+strings.TrimSpace(o.Enforcement)))
	} else {
		where := "where you operate"
		if o.Country != "" {
			where = "in " + o.Country
		}
		d.para(todo("name the authority that enforces accessibility law " + where + " and how to reach it; or set statement.enforcement."))
	}

	appendix(d, dr)
	return d
}

// problems lists the criteria the tests or the owner found failures of.
func problems(d *doc, dr *Draft) {
	d.h3("Problems found")
	var items []line
	for _, r := range dr.Rows {
		if r.Tests != ResultFailures && (r.Manual == nil || r.Manual.Result != ManualFailures) {
			continue
		}
		l := line{strong(r.ID + " " + r.Title), text(" (EN 301 549 " + r.EN + ")")}
		if r.Tests == ResultFailures {
			s := ": " + count(r.Issues, "element") + " on " + count(r.Pages, "page")
			if len(r.Paths) > 0 {
				shown := append([]string(nil), r.Paths...)
				if more := r.Pages - len(r.Paths); more > 0 {
					shown = append(shown, count(more, "more page"))
				}
				s += ": " + joinAnd(shown)
			}
			l = append(l, text(s+"."))
		}
		if m := r.Manual; m != nil && r.Tests != ResultFailures {
			l = append(l, text(fmt.Sprintf(": found by your manual review (%s, %s).", m.By, m.Date)))
		} else if m != nil {
			l = append(l, text(fmt.Sprintf(" Your manual review (%s, %s): %s.", m.By, m.Date, m.Result)))
		}
		items = append(items, l)
	}
	if len(items) == 0 {
		d.para(text(fmt.Sprintf("Soundcheck's tests found no failures on %s. That doesn't mean there are none: %s need a person.", dr.Date, count(dr.NotTested, "criterion"))))
		return
	}
	d.para(text("Found by Soundcheck's tests on " + dr.Date + ", or by your own review:"))
	d.list(items...)
	d.para(todo("for each problem, say in plain words what doesn't work, for whom and what you offer instead."))
}

// method is the section on how the draft was prepared: the facts of
// the run, then the fixed limits.
func method(d *doc, dr *Draft, noun string) {
	d.h2("How this " + noun + " was prepared")
	d.para(text("This "+noun+" was prepared on "), todo("date"), text(" by "), todo("who"), text("."))
	axe := "axe-core"
	if dr.Axe != "" {
		axe += " " + dr.Axe
	}
	if dr.AxeRules > 0 {
		axe += ", " + count(dr.AxeRules, "rule")
	}
	s := fmt.Sprintf("Rhythm Soundcheck %s checked %s of %s on %s with automated tests (%s for WCAG %s A and AA)",
		dr.Version, count(dr.Pages, "page"), dr.Owner.Website, dr.Date, axe, dr.WCAG)
	if k := dr.Keyboard; k.PagesWalked > 0 {
		s += fmt.Sprintf(" and a keyboard walkthrough of %s (%s tested, %s run).", count(k.PagesWalked, "page"), count(k.WidgetsTested, "widget"), count(k.FlowsRun, "key flow"))
	} else {
		s += " only: the keyboard walkthrough didn't run."
	}
	d.para(text(s))
	d.para(text("It was also checked by "), todo("your manual review, such as a screen reader test of checkout"), text("."))
	manual := 0
	for _, r := range dr.Rows {
		if r.Manual != nil {
			manual++
		}
	}
	if manual > 0 {
		d.para(text(fmt.Sprintf("You recorded manual results for %s; the criteria table shows them with who checked and when.", count(manual, "criterion"))))
	}
	if len(dr.Notes) > 0 {
		d.para(text("Notes on this run:"))
		var notes []line
		for _, n := range dr.Notes {
			notes = append(notes, line{text(n)})
		}
		d.list(notes...)
	}
	d.para(text("It was last reviewed on "), todo("date"), text("."))
	d.h3("What these tests can and can't tell")
	var items []line
	for _, l := range Limits {
		items = append(items, line{text(l)})
	}
	d.list(items...)
}

// contact is how visitors reach the owner.
func contact(d *doc, o Owner) {
	d.h2("Feedback and contact")
	ways := []string{"email " + o.Contact.Email}
	if o.Contact.Phone != "" {
		ways = append(ways, "phone "+o.Contact.Phone)
	}
	if o.Contact.Address != "" {
		ways = append(ways, "post to "+o.Contact.Address)
	}
	d.para(text("If something on this website doesn't work for you, tell us: " + joinAnd(ways) + "."))
	if o.ResponseDays > 0 {
		d.para(text("We reply within " + count(o.ResponseDays, "day") + "."))
	} else {
		d.para(text("We reply within "), todo("days; or set statement.response_days"), text(" days."))
	}
}

// appendix is the criteria table, what wasn't tested and the pages.
func appendix(d *doc, dr *Draft) {
	d.h2("Appendix: every WCAG " + dr.WCAG + " A and AA criterion")
	t := part{Kind: blockTable, Head: []string{"Criterion", "Level", "EN 301 549", "How Soundcheck tests it", "Result"}}
	for _, r := range dr.Rows {
		t.Rows = append(t.Rows, []line{{text(r.ID + " " + r.Title)}, {text(r.Level)}, {text(r.EN)}, {text(r.HowTested())}, {text(ResultText(r))}})
	}
	d.add(t)

	d.h2("Not tested")
	var items []line
	for _, n := range dr.NotAudited {
		where := n.Path
		if where == "" {
			where = "A page behind the login"
		}
		s := where + ": " + n.Module + " " + auditWords(n.Status)
		if n.Reason != "" {
			s += " (" + n.Reason + ")"
		}
		items = append(items, line{text(s + "; its criteria count as not tested there.")})
	}
	if len(dr.Frames) > 0 {
		items = append(items, line{text("Content in frames from " + joinAnd(dr.Frames) + " wasn't tested.")})
	}
	for _, f := range dr.Keyboard.FlowsSkipped {
		items = append(items, line{text("Key flow " + f.File + " didn't run: " + f.Reason + ".")})
	}
	var person []string
	for _, r := range dr.Rows {
		if r.Result == ResultNotTested {
			person = append(person, r.ID)
		}
	}
	if len(person) > 0 {
		items = append(items, line{text(fmt.Sprintf("%s need a person: %s.", count(len(person), "criterion"), strings.Join(person, ", ")))})
	}
	items = append(items,
		line{text("Only Chrome was used. The keyboard walk ran at desktop width.")},
		line{text("No screen reader, voice control or test with disabled users.")},
		line{text(OutsideClauses)})
	d.list(items...)

	d.h2("Pages checked")
	if dr.PrivatePages > 0 {
		d.para(text(fmt.Sprintf("%s checked logged in, so their paths aren't listed. Set statement.list_private_paths to true to list them.", countWere(dr.PrivatePages))))
	}
	var pages []line
	for _, p := range dr.Checked {
		pages = append(pages, line{text(p)})
	}
	d.listIf(pages...)
}

// ResultText is a criterion's result in the criteria table: "failures
// found (3 elements, 2 pages)", "no failures found by these tests",
// "not tested: needs a person", the criterion's fixed note or the
// owner's manual result with who and when.
func ResultText(r Row) string {
	if m := r.Manual; m != nil {
		return fmt.Sprintf("manual: %s (%s, %s); the tests: %s", m.Result, m.By, m.Date, testsText(r))
	}
	return testsText(r)
}

// testsText is what the tests found for r.
func testsText(r Row) string {
	switch r.Tests {
	case ResultFailures:
		return fmt.Sprintf("failures found (%s, %s)", count(r.Issues, "element"), count(r.Pages, "page"))
	case ResultNoFailures:
		return "no failures found by these tests"
	}
	switch {
	case r.Note != "":
		return r.Note
	case r.Coverage == wcag.CoveragePartly:
		return "not tested: its tests didn't run"
	}
	return "not tested: needs a person"
}

// auditWords says what became of an audit.
func auditWords(status string) string {
	switch status {
	case model.AuditTimeout:
		return "timed out"
	case model.AuditError:
		return "failed"
	case model.AuditSkipped:
		return "was skipped"
	}
	return status
}

// count is n with its noun, as "1 page" or "3 pages".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	switch {
	case strings.HasSuffix(noun, "criterion"):
		noun = strings.TrimSuffix(noun, "criterion") + "criteria"
	default:
		noun += "s"
	}
	return strconv.Itoa(n) + " " + noun
}

// countWere is "1 page was" or "3 pages were".
func countWere(n int) string {
	if n == 1 {
		return "1 page was"
	}
	return count(n, "page") + " were"
}

// joinAnd lists items as "a, b and c".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
