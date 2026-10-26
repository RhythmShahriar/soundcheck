// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

// JSON writes the comparison, or the run when there's none, in the
// versioned rhythm-soundcheck/v1 schema (model.Schemas describes both).
// Lists are [] when empty, every change has an ID and a summary counts
// what the terminal shows; a run also gets its issues grouped across
// pages. An --eaa result also gets its accessibility summary as eaa,
// with the evidence note always in it. The result itself isn't changed.
func JSON(w io.Writer, r Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	var failed *bool
	if r.FailOn != "" {
		f := FailOn(r, r.FailOn)
		failed = &f
	}
	if r.Compared() {
		c := *r.Comparison
		c.Changes = slices.Clone(c.Changes)
		c.Complete()
		c.Summary.Failed = failed
		c.EAA = eaaJSON(r)
		return enc.Encode(&c)
	}
	run := *r.Run
	run.EAA = eaaJSON(r)
	run.Complete()
	issues := Issues(&run)
	counts := Counts(issues)
	s := &model.RunSummary{Errors: counts[model.Error], Warnings: counts[model.Warning], Notices: counts[model.Notice], Failed: failed}
	switch {
	case Partial(r):
		s.Verdict = model.VerdictInterrupted
	case s.Errors > 0:
		s.Verdict = model.VerdictErrors
	case s.Warnings > 0:
		s.Verdict = model.VerdictWarnings
	default:
		s.Verdict = model.VerdictClean
	}
	run.Summary = s
	run.Groups = make([]model.Group, 0, len(issues))
	for _, i := range issues {
		run.Groups = append(run.Groups, model.Group{Key: i.Key, Check: i.Check, Level: i.Level, Title: i.Title, Pages: i.Pages})
	}
	return enc.Encode(&run)
}

// eaaJSON is the eaa block of r's JSON: a copy of its accessibility
// summary that always carries model.EvidenceNote, so a tool that shows
// the counts downstream has the sentence too. It is nil without --eaa.
func eaaJSON(r Result) *model.EAASummary {
	e := eaaOf(r)
	if e == nil {
		return nil
	}
	out := *e
	out.EvidenceNote = model.EvidenceNote
	// Lists are [] when empty, as everywhere in the JSON.
	if out.Criteria.Failed == nil {
		out.Criteria.Failed = []model.CriterionCount{}
	}
	out.Criteria.NoFailuresFound = nonNil(out.Criteria.NoFailuresFound)
	out.Criteria.NotTested = nonNil(out.Criteria.NotTested)
	return &out
}

// nonNil is ss, or an empty list for nil.
func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

// Markdown writes a report for pull requests, issues and CI summaries.
func Markdown(w io.Writer, r Result) error {
	var b strings.Builder
	run := r.Run
	b.WriteString("## ♪ Soundcheck\n\n")
	if Partial(r) {
		fmt.Fprintf(&b, "> [!WARNING]\n> **Interrupted.** %s.\n\n", capitalize(partialNote))
	}
	if r.Compared() {
		c := r.Comparison
		fmt.Fprintf(&b, "**%s.** %s of %s compared with %s.\n\n", verdict(c), plural(c.Pages, "page", "pages"), MarkdownText(run.Target), MarkdownText(snapshotAt(c.Before)))
		shown := shownClasses(c)
		b.WriteString("|")
		for _, class := range shown {
			fmt.Fprintf(&b, " %s |", capitalize(class))
		}
		b.WriteString("\n|" + strings.Repeat("---:|", len(shown)) + "\n|")
		for _, class := range shown {
			fmt.Fprintf(&b, " %d |", c.Count(class))
		}
		b.WriteString("\n\n")
		groups := byClass(c.Changes)
		for _, class := range model.Classes {
			cs := groups[class]
			if len(cs) == 0 {
				continue
			}
			fmt.Fprintf(&b, "### %s %s (%d)\n\n_%s._\n\n", classes[class].Glyph, classes[class].Label, len(cs), capitalize(classes[class].Blurb))
			b.WriteString("| Change | Where | Before → after | Fix |\n|---|---|---|---|\n")
			for _, ch := range cs {
				title := mdCell(ch.Title) + mdCriteria(ch.Check, ch.WCAG)
				if ch.Source != "" {
					title += "<br><sub>" + mdCell(ch.Source) + "</sub>"
				}
				fix := ""
				if class != model.Fixed {
					fix = mdCell(firstLine(ch.Fix))
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", title, mdPages(ch.Pages), mdCell(beforeAfter(ch)), fix)
			}
			b.WriteString("\n")
		}
		writeAccessibility(&b, r)
		writeNotes(&b, c.Warnings)
	} else {
		issues := Issues(run)
		counts := Counts(issues)
		fmt.Fprintf(&b, "**%s.** %s of %s checked.\n\n", MarkdownText(capitalize(runVerdict(run, counts))), plural(len(run.Pages), "page", "pages"), MarkdownText(run.Target))
		b.WriteString("| Errors | Warnings | Notices |\n|---:|---:|---:|\n")
		fmt.Fprintf(&b, "| %d | %d | %d |\n\n", counts[model.Error], counts[model.Warning], counts[model.Notice])
		if len(issues) > 0 {
			b.WriteString("| Level | Issue | Where | Fix |\n|---|---|---|---|\n")
			for _, i := range issues {
				title := mdCell(i.Title) + mdCriteria(i.Check, i.WCAG)
				if i.Source != "" {
					title += "<br><sub>" + mdCell(i.Source) + "</sub>"
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", i.Level, title, mdPages(i.Pages), mdCell(firstLine(i.Fix)))
			}
			b.WriteString("\n")
		}
		writeAccessibility(&b, r)
		writeNotes(&b, run.Warnings)
	}
	fmt.Fprintf(&b, "<sub>rhythm-soundcheck %s · %s · %s</sub>\n", MarkdownText(run.Version), r.stats(), run.Started.Format("2006-01-02 15:04 MST"))
	_, err := io.WriteString(w, b.String())
	return err
}

func writeNotes(b *strings.Builder, notes []string) {
	if len(notes) == 0 {
		return
	}
	fmt.Fprintf(b, "<details><summary>%s</summary>\n\n", plural(len(notes), "note", "notes"))
	for _, n := range notes {
		fmt.Fprintf(b, "- %s\n", mdCell(n))
	}
	b.WriteString("\n</details>\n\n")
}

// mdPages shows up to three paths as code, where Markdown is inert. A
// code span can't escape a backtick, so backticks are dropped and GitHub
// splits table cells on a pipe even inside one, so pipes are escaped.
func mdPages(pages []string) string {
	if len(pages) == 0 {
		return "site-wide"
	}
	var codes []string
	for _, p := range pages[:min(len(pages), 3)] {
		p = strings.NewReplacer("`", "", "|", `\|`).Replace(Term(p))
		codes = append(codes, "`"+p+"`")
	}
	s := strings.Join(codes, " ")
	if len(pages) > 3 {
		s += fmt.Sprintf(" +%d", len(pages)-3)
	}
	return s
}

// mdCell makes text safe inside a Markdown table cell; see MarkdownText.
func mdCell(s string) string { return MarkdownText(s) }

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// JUnit XML: one test suite per change class (or issue level), one test
// case per change. Cases at or above failOn fail; with no failOn, BROKE
// and NEW changes (or errors, for a single run) fail.
type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Time     float64      `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name      string `xml:"name,attr"`
	Tests     int    `xml:"tests,attr"`
	Failures  int    `xml:"failures,attr"`
	Errors    int    `xml:"errors,attr"`
	Skipped   int    `xml:"skipped,attr"`
	Timestamp string `xml:"timestamp,attr"`
	// Properties carry facts about the whole run, e.g. interrupted=true.
	Properties *junitProperties `xml:"properties,omitempty"`
	Cases      []junitCase      `xml:"testcase"`
}

type junitProperties struct {
	List []junitProperty `xml:"property"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Error     *junitFailure `xml:"error,omitempty"`
	Skipped   *struct{}     `xml:"skipped,omitempty"`
	Out       string        `xml:"system-out,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

// JUnit writes r as JUnit XML for CI test reports: one test case per
// change (or per issue of a run), failed when it's at or above failOn
// (new, or error for a run, when failOn is empty).
func JUnit(w io.Writer, r Result, failOn string) error {
	out := junitSuites{Name: "soundcheck", Time: r.Run.Duration / 1000}
	stamp := r.Run.Started.UTC().Format(time.RFC3339)
	// A partial result's findings are written as output, never as
	// failures: FailOn doesn't fail it either. The one error case below
	// says the run didn't finish.
	partial := Partial(r)
	if r.Compared() {
		limit := slices.Index(model.Classes, failOn)
		if failOn == "" {
			limit = slices.Index(model.Classes, model.New)
		}
		groups := byClass(r.Comparison.Changes)
		for i, class := range shownClasses(r.Comparison) {
			s := junitSuite{Name: "soundcheck." + class, Timestamp: stamp}
			for _, ch := range groups[class] {
				tc := junitCase{Name: xmlText(ch.Title), Classname: xmlText(strings.ReplaceAll(ch.Check, "/", ".")),
					Out: caseText(ch.Pages, beforeAfter(ch), ch.Source, ch.Detail, ch.Fix)}
				// An unstable change didn't reproduce: CI shows it as
				// skipped, neither a pass nor a failure.
				if class == model.Unstable {
					tc.Skipped = &struct{}{}
					s.Skipped++
				}
				if !partial && i <= limit && class != model.Fixed && class != model.Unstable {
					tc.Failure = &junitFailure{Message: classes[class].Label + ": " + tc.Name, Type: class, Text: tc.Out}
					tc.Out = ""
					s.Failures++
				}
				s.Cases = append(s.Cases, tc)
			}
			s.Tests = len(s.Cases)
			out.Suites = append(out.Suites, s)
		}
	} else {
		limit := model.Level(failOn).Rank()
		if failOn == "" {
			limit = model.Error.Rank()
		}
		issues := Issues(r.Run)
		for _, level := range Levels {
			s := junitSuite{Name: "soundcheck." + string(level), Timestamp: stamp}
			for _, i := range issues {
				if i.Level != level {
					continue
				}
				tc := junitCase{Name: xmlText(i.Title), Classname: xmlText(strings.ReplaceAll(i.Check, "/", ".")),
					Out: caseText(i.Pages, "", i.Source, i.Detail, i.Fix)}
				if !partial && level.Rank() <= limit {
					tc.Failure = &junitFailure{Message: tc.Name, Type: string(level), Text: tc.Out}
					tc.Out = ""
					s.Failures++
				}
				s.Cases = append(s.Cases, tc)
			}
			s.Tests = len(s.Cases)
			out.Suites = append(out.Suites, s)
		}
	}
	// A run with nothing to report still shows as one passing case, so CI
	// dashboards record that the check ran.
	pages := junitSuite{Name: "soundcheck.pages", Timestamp: stamp,
		Cases: []junitCase{{Name: fmt.Sprintf("%d pages checked", len(r.Run.Pages)), Classname: "soundcheck"}}}
	// A partial result is an error, not a pass or a failure: CI shows
	// the check didn't finish and --fail-on findings don't count.
	if partial {
		pages.Properties = &junitProperties{List: []junitProperty{{Name: "interrupted", Value: "true"}}}
		pages.Cases = append(pages.Cases, junitCase{Name: "Run was interrupted", Classname: "soundcheck",
			Error: &junitFailure{Message: capitalize(partialNote), Type: "interrupted"}})
		pages.Errors = 1
	}
	pages.Tests = len(pages.Cases)
	out.Suites = append(out.Suites, pages)
	for _, s := range out.Suites {
		out.Tests += s.Tests
		out.Failures += s.Failures
		out.Errors += s.Errors
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// caseText is a test case's text, made safe for XML: control characters
// shown as text, characters XML forbids dropped.
func caseText(pages []string, ba, source, detail, fix string) string {
	var lines []string
	lines = append(lines, "where: "+xmlText(pagesLabel(pages, 10)))
	for _, kv := range [][2]string{{"before → after", ba}, {"source", source}, {"detail", detail}, {"fix", fix}} {
		if kv[1] != "" {
			lines = append(lines, kv[0]+": "+xmlText(kv[1]))
		}
	}
	return strings.Join(lines, "\n")
}
