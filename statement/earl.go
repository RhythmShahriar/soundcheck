// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// EARL is the JSON-LD the W3C WCAG-EM Report Tool (version 4) saves and
// opens: an evaluation with its scope, its sample of pages and one EARL
// assertion per criterion. The shape is the tool's own; see
// testdata/wcagem-empty.jsonld, an empty evaluation the tool saved.

// wcagemContext is the tool's JSON-LD context (appContext.js
// exportContext) with the prefixes it lacks for WCAG 2.2 test ids and
// the assertor added.
const wcagemContext = `{"reporter":"http://github.com/w3c/wai-wcag-em-report-tool/","wcagem":"http://www.w3.org/TR/WCAG-EM/#","Evaluation":"wcagem:procedure","defineScope":"wcagem:step1","scope":"wcagem:step1a","step1b":{"@id":"wcagem:step1b","@type":"@id"},"conformanceTarget":"step1b","accessibilitySupportBaseline":"wcagem:step1c","additionalEvaluationRequirements":"wcagem:step1d","exploreTarget":"wcagem:step2","essentialFunctionality":"wcagem:step2b","pageTypeVariety":"wcagem:step2c","technologiesReliedUpon":"wcagem:step2d","selectSample":"wcagem:step3","structuredSample":"wcagem:step3a","randomSample":"wcagem:step3b","Website":"wcagem:website","Webpage":"wcagem:webpage","auditSample":"wcagem:step4","reportFindings":"wcagem:step5","documentSteps":"wcagem:step5a","commissioner":"wcagem:commissioner","evaluator":"wcagem:evaluator","evaluationSpecifics":"wcagem:step5b","WCAG":"http://www.w3.org/TR/WCAG/#","WCAG20":"http://www.w3.org/TR/WCAG20/#","WCAG21":"http://www.w3.org/TR/WCAG21/#","WCAG22":"http://www.w3.org/TR/WCAG22/#","WAI":"http://www.w3.org/WAI/","A":"WAI:WCAG2A-Conformance","AA":"WAI:WCAG2AA-Conformance","AAA":"WAI:WCAG2AAA-Conformance","wcagVersion":"WAI:standards-guidelines/wcag/#versions","reportToolVersion":"wcagem:reportToolVersion","earl":"http://www.w3.org/ns/earl#","Assertion":"earl:Assertion","Assertor":"earl:Assertor","Software":"earl:Software","TestMode":"earl:TestMode","TestCriterion":"earl:TestCriterion","TestCase":"earl:TestCase","TestRequirement":"earl:TestRequirement","TestSubject":"earl:TestSubject","TestResult":"earl:TestResult","OutcomeValue":"earl:OutcomeValue","Pass":"earl:Pass","Fail":"earl:Fail","CannotTell":"earl:CannotTell","NotApplicable":"earl:NotApplicable","NotTested":"earl:NotTested","assertedBy":"earl:assertedBy","mode":"earl:mode","result":"earl:result","subject":"earl:subject","test":"earl:test","outcome":"earl:outcome","dcterms":"http://purl.org/dc/terms/","title":"dcterms:title","description":"dcterms:description","summary":"dcterms:summary","date":"dcterms:date","hasPart":"dcterms:hasPart","isPartOf":"dcterms:isPartOf","id":"@id","type":"@type","language":"@language"}`

// wcagemVersion is the Report Tool version whose files EARL writes.
const wcagemVersion = "4.1.0"

// EARL outcomes. A criterion the tests found no failures of is
// cantTell, never passed: automated tests can't show a criterion is
// met.
const (
	outcomeFailed   = "earl:failed"
	outcomeCantTell = "earl:cantTell"
	outcomeUntested = "earl:untested"
)

var outcomeTypes = map[string][]string{
	outcomeFailed:   {"OutcomeValue", "Fail"},
	outcomeCantTell: {"OutcomeValue", "CannotTell"},
	outcomeUntested: {"OutcomeValue", "NotTested"},
}

type earlDoc struct {
	Context           json.RawMessage `json:"@context"`
	Language          string          `json:"language"`
	Type              string          `json:"type"`
	ReportToolVersion string          `json:"reportToolVersion"`
	DefineScope       earlScope       `json:"defineScope"`
	ExploreTarget     earlExplore     `json:"exploreTarget"`
	SelectSample      earlSelect      `json:"selectSample"`
	AuditSample       []earlAssertion `json:"auditSample"`
	ReportFindings    earlFindings    `json:"reportFindings"`
}

type earlScope struct {
	ID                               string      `json:"id"`
	Scope                            earlSubject `json:"scope"`
	ConformanceTarget                string      `json:"conformanceTarget"`
	AccessibilitySupportBaseline     string      `json:"accessibilitySupportBaseline"`
	AdditionalEvaluationRequirements string      `json:"additionalEvaluationRequirements"`
	WCAGVersion                      string      `json:"wcagVersion"`
}

type earlSubject struct {
	ID          string   `json:"id"`
	Type        []string `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
}

type earlExplore struct {
	ID                     string   `json:"id"`
	EssentialFunctionality string   `json:"essentialFunctionality"`
	PageTypeVariety        string   `json:"pageTypeVariety"`
	TechnologiesReliedUpon []string `json:"technologiesReliedUpon"`
}

type earlSelect struct {
	ID               string        `json:"id"`
	StructuredSample []earlSubject `json:"structuredSample"`
	RandomSample     []earlSubject `json:"randomSample"`
}

type earlAssertion struct {
	Type       string       `json:"type"`
	Date       string       `json:"date"`
	Mode       string       `json:"mode,omitempty"`
	AssertedBy earlAssertor `json:"assertedBy"`
	Subject    earlSubject  `json:"subject"`
	Test       earlTest     `json:"test"`
	Result     earlResult   `json:"result"`
}

type earlAssertor struct {
	ID    string   `json:"id"`
	Type  []string `json:"type"`
	Title string   `json:"title"`
}

type earlTest struct {
	ID    string   `json:"id"`
	Type  []string `json:"type"`
	Title string   `json:"title"`
}

type earlResult struct {
	Type        string      `json:"type"`
	Date        string      `json:"date"`
	Description string      `json:"description"`
	Outcome     earlOutcome `json:"outcome"`
}

type earlOutcome struct {
	ID   string   `json:"id"`
	Type []string `json:"type"`
}

type earlFindings struct {
	Date                earlDate `json:"date"`
	Summary             string   `json:"summary"`
	Title               string   `json:"title"`
	Commissioner        string   `json:"commissioner"`
	Evaluator           string   `json:"evaluator"`
	DocumentSteps       []earlID `json:"documentSteps"`
	EvaluationSpecifics string   `json:"evaluationSpecifics"`
}

type earlDate struct {
	Type  string `json:"type"`
	Value string `json:"@value"`
}

type earlID struct {
	ID string `json:"id"`
}

// EARL writes d as the JSON-LD evaluation the W3C WCAG-EM Report Tool
// opens: the scope (statement.website, target AA, the WCAG version),
// the checked pages as its structured sample and one assertion per
// criterion on the website. Failures found are earl:failed, no failures
// found is earl:cantTell (never earl:passed) and not tested is
// earl:untested; the owner's manual results are the owner's, with mode
// earl:manual. The mode is earl:semiAuto for criteria the keyboard
// walkthrough tests and earl:automatic for the others.
func EARL(w io.Writer, d *Draft) error {
	website := plain(d.Owner.Website)
	site := earlSubject{ID: "_:website", Type: []string{"TestSubject", "Website"}, Title: plain(d.Owner.Owner), Description: earlScopeText(d)}
	by := earlAssertor{ID: "https://github.com/RhythmShahriar/soundcheck", Type: []string{"Assertor", "Software"}, Title: "Rhythm Soundcheck " + plain(d.Version)}
	prefix := "WCAG" + strings.ReplaceAll(d.WCAG, ".", "")
	out := earlDoc{
		Context: json.RawMessage(wcagemContext), Language: "en", Type: "Evaluation", ReportToolVersion: wcagemVersion,
		DefineScope:   earlScope{ID: "_:defineScope", Scope: site, ConformanceTarget: "AA", WCAGVersion: d.WCAG},
		ExploreTarget: earlExplore{ID: "_:exploreTarget", TechnologiesReliedUpon: []string{}},
		SelectSample:  earlSelect{ID: "_:selectSample", StructuredSample: []earlSubject{}, RandomSample: []earlSubject{}},
		AuditSample:   []earlAssertion{},
		ReportFindings: earlFindings{
			Date:    earlDate{Type: "http://www.w3.org/TR/NOTE-datetime", Value: plain(d.Date)},
			Summary: fmt.Sprintf("Soundcheck's tests found failures of %d of %d criteria and could not test %d of them. %s", d.Failed, len(d.Rows), d.NotTested, model.EvidenceNote),
			Title:   "Accessibility evidence for " + website, Commissioner: plain(d.Owner.Owner),
			DocumentSteps:       []earlID{{"_:about"}, {"_:defineScope"}, {"_:exploreTarget"}, {"_:selectSample"}},
			EvaluationSpecifics: earlMethod(d),
		},
	}
	for _, p := range d.Checked {
		u := pageURL(website, p)
		out.SelectSample.StructuredSample = append(out.SelectSample.StructuredSample, earlSubject{ID: u, Type: []string{"TestSubject", "Webpage"}, Title: plain(p), Description: u})
	}
	for _, r := range d.Rows {
		a := earlAssertion{Type: "Assertion", Date: plain(d.Date), AssertedBy: by, Subject: site,
			Test: earlTest{ID: prefix + ":" + r.Slug, Type: []string{"TestCriterion", "TestRequirement"}, Title: r.ID + " " + r.Title}}
		outcome, text := earlOutcomeOf(r)
		if r.Tests != ResultNotTested {
			a.Mode = "earl:automatic"
			for _, t := range r.TestedBy {
				if strings.HasPrefix(t, "keyboard/") {
					a.Mode = "earl:semiAuto"
				}
			}
		}
		if m := r.Manual; m != nil {
			a.Mode = "earl:manual"
			switch m.Result {
			case ManualFailures:
				outcome = outcomeFailed
			case ManualNoFailures:
				outcome = outcomeCantTell
			default:
				outcome = outcomeUntested
			}
			text = fmt.Sprintf("Manual result by %s on %s: %s. Soundcheck's tests: %s", plain(m.By), plain(m.Date), plain(m.Result), text)
		}
		a.Result = earlResult{Type: "TestResult", Date: plain(d.Date), Description: text, Outcome: earlOutcome{ID: outcome, Type: outcomeTypes[outcome]}}
		out.AuditSample = append(out.AuditSample, a)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// earlOutcomeOf is the outcome of what the tests found and its words.
func earlOutcomeOf(r Row) (outcome, text string) {
	switch r.Tests {
	case ResultFailures:
		s := fmt.Sprintf("failures found: %s on %s", elements(r), count(r.Pages, "page"))
		if len(r.Paths) > 0 {
			s += " (" + strings.Join(plainAll(r.Paths), ", ") + ")"
		}
		return outcomeFailed, s + "."
	case ResultNoFailures:
		return outcomeCantTell, "no failures found by these tests. They can't find every way this criterion fails, so a person decides."
	}
	return outcomeUntested, testsText(r) + "."
}

// earlScopeText describes what the evaluation covers.
func earlScopeText(d *Draft) string {
	if len(d.Owner.Scope) > 0 {
		return plain(joinAnd(d.Owner.Scope))
	}
	return fmt.Sprintf("The %s Soundcheck checked on %s.", count(d.Pages, "page"), plain(d.Owner.Website))
}

// earlMethod says how the results were made, for the report's
// evaluation specifics.
func earlMethod(d *Draft) string {
	s := fmt.Sprintf("Rhythm Soundcheck %s, %s: automated tests with axe-core %s", plain(d.Version), plain(d.Date), plain(d.Axe))
	switch {
	case d.AxeRules > 0 && d.OwnChecks > 0:
		s += " (" + count(d.AxeRules, "rule") + " plus " + count(d.OwnChecks, "Soundcheck check") + ")"
	case d.AxeRules > 0:
		s += " (" + count(d.AxeRules, "rule") + ")"
	}
	if k := d.Keyboard; k.PagesWalked > 0 {
		s += fmt.Sprintf(" and a keyboard walkthrough of %s", count(k.PagesWalked, "page"))
	}
	s += "."
	if d.PrivatePages > 0 {
		s += fmt.Sprintf(" %s checked logged in and aren't listed in the sample.", countWere(d.PrivatePages))
	}
	for _, n := range d.Notes {
		s += " " + plain(n) + "."
	}
	return strings.ReplaceAll(s, "..", ".")
}

// pageURL is the page's address on the public website. The path is
// already cleaned and its host masked. The runner stores it escaped
// (RequestURI), so it is parsed as a reference rather than added to
// u.Path, which would escape every '%' a second time. A path that isn't
// valid escaped text is escaped as it is, so the id stays one IRI.
func pageURL(website, path string) string {
	u, err := url.Parse(strings.TrimSuffix(website, "/"))
	if err != nil {
		return website
	}
	path = "/" + strings.TrimPrefix(plain(path), "/")
	ref, err := url.Parse(path)
	if err != nil || ref.Scheme != "" || ref.Host != "" || ref.RawQuery != "" || ref.Fragment != "" {
		ref = &url.URL{Path: path}
	}
	base := strings.TrimSuffix(u.EscapedPath(), "/")
	u.Path = strings.TrimSuffix(u.Path, "/") + ref.Path
	u.RawPath = base + ref.EscapedPath()
	return u.String()
}
