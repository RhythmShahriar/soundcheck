// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package model

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"strings"
)

// Kinds tell a run's JSON from a comparison's: both use Schema.
const (
	KindRun        = "run"
	KindComparison = "comparison"
)

// Unstable is the class of a change that didn't show again when its
// page was checked a second time. Summary counts it already so the JSON
// contract doesn't change when Soundcheck starts confirming changes.
const Unstable = "unstable"

// Verdicts of a comparison, in Summary.
const (
	VerdictClean       = "clean"       // nothing broke, appeared, changed or slowed down
	VerdictChanged     = "changed"     // new problems, changes or slowdowns, nothing broke
	VerdictBroke       = "broke"       // at least one BROKE change
	VerdictInterrupted = "interrupted" // stopped before it finished; the counts are partial
)

// Verdicts of a single run, in RunSummary.
const (
	VerdictWarnings = "warnings" // warnings, no errors
	VerdictErrors   = "errors"   // at least one error
)

// Summary counts a comparison's changes by class, for scripts.
type Summary struct {
	Broke    int    `json:"broke"`
	New      int    `json:"new"`
	Changed  int    `json:"changed"`
	Slower   int    `json:"slower"`
	Fixed    int    `json:"fixed"`
	Unstable int    `json:"unstable"`
	Ignored  int    `json:"ignored"`
	Verdict  string `json:"verdict"` // clean, changed, broke or interrupted
	// Failed says whether the result fails the build at the --fail-on
	// threshold. It's only set when there is one.
	Failed *bool `json:"failed,omitempty"`
}

// RunSummary counts a single run's grouped issues by level.
type RunSummary struct {
	Errors   int    `json:"errors"`
	Warnings int    `json:"warnings"`
	Notices  int    `json:"notices"`
	Verdict  string `json:"verdict"` // clean, warnings, errors or interrupted
	Failed   *bool  `json:"failed,omitempty"`
}

// Group is one problem of a run with every page that has it, as the
// terminal lists it.
type Group struct {
	Key   string   `json:"key"`
	Check string   `json:"check"`
	Level Level    `json:"level"`
	Title string   `json:"title"`
	Pages []string `json:"pages"`
}

// Summarize counts the changes and gives the verdict. Failed is left
// for the caller, which knows the threshold.
func (c *Comparison) Summarize() Summary {
	s := Summary{Broke: c.Count(Broke), New: c.Count(New), Changed: c.Count(Changed), Slower: c.Count(Slower),
		Fixed: c.Count(Fixed), Unstable: c.Count(Unstable)}
	switch {
	case c.Interrupted:
		s.Verdict = VerdictInterrupted
	case s.Broke > 0:
		s.Verdict = VerdictBroke
	case s.New+s.Changed+s.Slower > 0:
		s.Verdict = VerdictChanged
	default:
		s.Verdict = VerdictClean
	}
	return s
}

// Complete makes c ready to write as JSON: the kind, lists that are
// empty rather than missing, an ID on every change and a fresh summary.
// Scripts can then rely on every field being there.
func (c *Comparison) Complete() {
	if c.Schema == "" {
		c.Schema = Schema
	}
	c.Kind = KindComparison
	if c.Changes == nil {
		c.Changes = []Change{}
	}
	if c.Warnings == nil {
		c.Warnings = []string{}
	}
	for i := range c.Changes {
		if c.Changes[i].Pages == nil {
			c.Changes[i].Pages = []string{}
		}
	}
	identify(c.Changes)
	failed := c.Summary.Failed
	c.Summary = c.Summarize()
	c.Summary.Failed = failed
}

// Complete is Comparison.Complete for a run.
func (r *Run) Complete() {
	if r.Schema == "" {
		r.Schema = Schema
	}
	r.Kind = KindRun
	if r.Pages == nil {
		r.Pages = []Page{}
	}
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
}

// ChangeID is the first 8 hex digits of the SHA-256 of the change's
// check, title, source and pages. The same change in two comparisons
// gets the same ID; a change that spread to another page gets a new one.
// The class is left out, so a change keeps its ID when it's only
// reclassified (a NEW change found UNSTABLE).
func ChangeID(c Change) string {
	return shortHash(c.Check, c.Title, c.Source, strings.Join(c.Pages, ","))
}

func shortHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:4])
}

// identify gives every change without an ID one that is unique in cs.
// Two changes that ChangeID can't tell apart (the same title on the same
// page with other values) are told apart by class, before and after,
// then by their order, which Sort makes deterministic.
func identify(cs []Change) {
	seen := map[string]bool{}
	for _, c := range cs {
		if c.ID != "" {
			seen[c.ID] = true
		}
	}
	for i := range cs {
		c := &cs[i]
		if c.ID != "" {
			continue
		}
		id := ChangeID(*c)
		if seen[id] {
			id = shortHash(id, c.Class, c.Before, c.After)
		}
		for n := 2; seen[id]; n++ {
			id = shortHash(ChangeID(*c), fmt.Sprint(n))
		}
		c.ID = id
		seen[id] = true
	}
}

// Schemas holds the JSON Schemas of the rhythm-soundcheck/v1 output:
// schema/run.v1.json and schema/comparison.v1.json. Fields are only
// ever added within v1, so a reader written for it keeps working.
//
//go:embed schema/*.json
var Schemas embed.FS
