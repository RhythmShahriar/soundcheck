// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// eaaModules are the modules an --eaa result gets a JUnit case for on
// every page, in report order.
var eaaModules = []string{"a11y", "keyboard"}

// eaaSuites is the JUnit view of an --eaa result by page: a suite per
// page with a case per page and accessibility module ("/checkout/ ·
// keyboard"), so a CI test tab shows which page and module failed. A
// case has one failure per error at the threshold: for a run the
// --fail-on level (error when there is none), for a comparison the
// errors a failing change covers, so a trap the snapshot already had
// doesn't fail a compare that otherwise passes; such known errors are
// listed as output. A module whose audit of the page didn't finish is
// skipped, neither a pass nor a failure. A partial result writes its
// errors as output, like every other case. It is nil for a result
// without --eaa, so other runs' JUnit is unchanged.
func eaaSuites(r Result, failOn, stamp string, partial bool) []junitSuite {
	if r.Run == nil || (eaaOf(r) == nil && !r.Run.Options.EAA) {
		return nil
	}
	modules := eaaRunModules(r.Run.Options)
	if len(modules) == 0 {
		return nil
	}
	fails := func(model.Issue, string) bool { return false }
	if !partial {
		fails = eaaFails(r, failOn)
	}
	var suites []junitSuite
	for _, p := range r.Run.Pages {
		if p.Canceled() {
			continue
		}
		s := junitSuite{Name: "soundcheck.page " + xmlText(p.Path), Timestamp: stamp}
		for _, m := range modules {
			tc := junitCase{Name: xmlText(p.Path) + " · " + m, Classname: "soundcheck." + m}
			var out []string
			switch audit, ok := p.Audits[m]; {
			case p.Error != "":
				tc.Skipped = &struct{}{}
				out = append(out, "not checked: the page couldn't be visited: "+xmlText(p.Error))
			case ok && !audit.Finished():
				tc.Skipped = &struct{}{}
				why := audit.Status
				if audit.Reason != "" {
					why = audit.Reason
				}
				out = append(out, "not checked: "+xmlText(m+": "+why))
			}
			for _, i := range p.Issues {
				if !strings.HasPrefix(i.Check, m+"/") {
					continue
				}
				text := eaaCaseText(i)
				if tc.Skipped == nil && fails(i, p.Path) {
					tc.Failure = append(tc.Failure, junitFailure{Message: xmlText(i.Title), Type: string(i.Level), Text: text})
					continue
				}
				if i.Level == model.Error || partial {
					out = append(out, string(i.Level)+": "+xmlText(i.Title)+"\n"+text)
				}
			}
			if tc.Skipped != nil {
				s.Skipped++
			}
			if len(tc.Failure) > 0 {
				s.Failures++
			}
			tc.Out = strings.Join(out, "\n\n")
			s.Cases = append(s.Cases, tc)
		}
		s.Tests = len(s.Cases)
		suites = append(suites, s)
	}
	return suites
}

// eaaRunModules are the accessibility modules the run used: both under
// --eaa, else the ones --modules named.
func eaaRunModules(o model.Options) []string {
	if o.EAA {
		return eaaModules
	}
	var out []string
	for _, m := range eaaModules {
		if slices.Contains(o.Modules, m) {
			out = append(out, m)
		}
	}
	return out
}

// eaaFails says whether a page's issue fails its JUnit case at failOn;
// see eaaSuites.
func eaaFails(r Result, failOn string) func(model.Issue, string) bool {
	if !r.Compared() {
		limit := model.Level(failOn).Rank()
		if failOn == "" {
			limit = model.Error.Rank()
		}
		return func(i model.Issue, _ string) bool {
			return i.Level == model.Error && i.Level.Rank() <= limit
		}
	}
	limit := slices.Index(model.Classes, failOn)
	if failOn == "" {
		limit = slices.Index(model.Classes, model.New)
	}
	var failing []model.Change
	for _, c := range r.Comparison.Changes {
		if i := slices.Index(model.Classes, c.Class); i >= 0 && i <= limit && c.Class != model.Fixed && c.Class != model.Unstable {
			failing = append(failing, c)
		}
	}
	return func(i model.Issue, page string) bool {
		if i.Level != model.Error {
			return false
		}
		for _, c := range failing {
			if c.Check == i.Check && c.Title == i.Title && c.Source == i.Source && slices.Contains(c.Pages, page) {
				return true
			}
		}
		return false
	}
}

// eaaCaseText is an issue's lines in a page case: its check, criteria
// and focus path besides the usual source, detail and fix.
func eaaCaseText(i model.Issue) string {
	lines := []string{"check: " + xmlText(i.Check)}
	if w := wcagLine(i.WCAG, i.EN301549); w != "" {
		lines = append(lines, xmlText(w))
	}
	if p := pathLine(i.FocusPath, true); p != "" {
		lines = append(lines, xmlText(p))
	}
	for _, kv := range [][2]string{{"source", i.Source}, {"detail", i.Detail}, {"fix", i.Fix}} {
		if kv[1] != "" {
			lines = append(lines, kv[0]+": "+xmlText(kv[1]))
		}
	}
	return strings.Join(lines, "\n")
}
