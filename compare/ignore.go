// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// IgnoreRule names changes that are known and shouldn't count, such as
// a missing favicon nobody will fix or a team page that changes weekly.
// Every field that is set must match: Check, Title and Source are globs
// (see model.Glob), Pages a list of page globs. A rule with Pages ignores the
// change only on those pages; its other pages still count.
type IgnoreRule struct {
	Check string   `yaml:"check" json:"check,omitempty"` // e.g. "health/favicon" or "visual/*"
	Pages []string `yaml:"pages" json:"pages,omitempty"` // e.g. ["/team/*"]
	Title string   `yaml:"title" json:"title,omitempty"`
	// Source is a glob on the issue's source, such as an element's
	// selector ("footer a"), for an issue change.
	Source string `yaml:"source" json:"source,omitempty"`
	// Until is the last day the rule applies, as YYYY-MM-DD. After it the
	// change counts again, so a rule meant for a week can't hide a
	// problem for good.
	Until  string `yaml:"until" json:"until,omitempty"`
	Reason string `yaml:"reason" json:"reason,omitempty"` // why, shown with the ignored change
}

// dateLayout is how Until is written.
const dateLayout = "2006-01-02"

// ApplyIgnore moves the changes the rules match from c.Changes to
// c.Ignored, where they're counted in the summary and never fail a build
// (FailOn only reads Changes). It's the last step of a comparison: after
// Compare and Confirm, so the rules see the final classes.
//
// A rule whose Until is before now's day, or isn't a date, doesn't match
// and adds a warning instead. So does a rule with nothing to match by,
// which would ignore every change.
func ApplyIgnore(c *model.Comparison, rules []IgnoreRule, now time.Time) {
	if len(rules) == 0 {
		return
	}
	for n, r := range rules {
		label := fmt.Sprintf("ignore rule %d (%s)", n+1, r.describe())
		switch r.state(now) {
		case ruleEmpty:
			c.Warnings = append(c.Warnings, fmt.Sprintf("ignore rule %d has no check, title, source or pages and would hide every change; it is skipped", n+1))
			continue
		case ruleBadDate:
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s: until %q isn't a date like 2026-12-31; the rule is skipped", label, r.Until))
			continue
		case ruleExpired:
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s expired on %s; its changes count again", label, r.Until))
			continue
		}
		kept := c.Changes[:0:0]
		for _, ch := range c.Changes {
			hit, rest, ok := r.split(ch)
			if !ok {
				kept = append(kept, ch)
				continue
			}
			hit.Reason = r.Reason
			c.Ignored = append(c.Ignored, hit)
			if rest != nil {
				kept = append(kept, *rest)
			}
		}
		c.Changes = kept
	}
	Sort(c.Changes)
	Sort(c.Ignored)
	c.Complete()
}

// Rule states: whether a rule applies at a time (see state).
const (
	ruleLive = iota
	ruleEmpty
	ruleBadDate
	ruleExpired
)

// state says whether the rule applies at now: it has something to match
// by and its Until, if any, is a date not before now's day.
func (r IgnoreRule) state(now time.Time) int {
	if r.Check == "" && r.Title == "" && r.Source == "" && len(r.Pages) == 0 {
		return ruleEmpty
	}
	if r.Until == "" {
		return ruleLive
	}
	until, err := time.ParseInLocation(dateLayout, r.Until, now.Location())
	if err != nil {
		return ruleBadDate
	}
	// The rule covers the whole of its last day.
	if !now.Before(until.AddDate(0, 0, 1)) {
		return ruleExpired
	}
	return ruleLive
}

// matches reports whether the rule's check, title and source match.
func (r IgnoreRule) matches(check, title, source string) bool {
	return (r.Check == "" || model.Glob(r.Check, check)) && (r.Title == "" || model.Glob(r.Title, title)) &&
		(r.Source == "" || model.Glob(r.Source, source))
}

// onPage reports whether the rule covers page: it names no pages or one
// of its globs matches.
func (r IgnoreRule) onPage(page string) bool {
	return len(r.Pages) == 0 || slices.ContainsFunc(r.Pages, func(g string) bool { return model.Glob(g, page) })
}

// split matches a change against the rule. ok is false when the rule
// doesn't apply. Otherwise hit is what to ignore and rest, when the rule
// covers only some of the change's pages, is the part that still counts.
func (r IgnoreRule) split(ch model.Change) (hit model.Change, rest *model.Change, ok bool) {
	if !r.matches(ch.Check, ch.Title, ch.Source) {
		return ch, nil, false
	}
	if len(r.Pages) == 0 {
		return ch, nil, true
	}
	var in, out []string
	for _, p := range ch.Pages {
		if r.onPage(p) {
			in = append(in, p)
		} else {
			out = append(out, p)
		}
	}
	switch {
	case len(in) == 0: // a site-wide change has no page to match
		return ch, nil, false
	case len(out) == 0:
		return ch, nil, true
	}
	// Both halves name other pages now, so they get new IDs.
	hit, left := withPages(ch, in), withPages(ch, out)
	return hit, &left, true
}

// withPages is ch on only the given pages. An issue change's score is its
// page count, so it follows; other scores (percent changed) stay.
func withPages(ch model.Change, pages []string) model.Change {
	if ch.Score == float64(len(ch.Pages)) {
		ch.Score = float64(len(pages))
	}
	ch.Pages, ch.ID = pages, ""
	return ch
}

// describe names a rule in warnings: "check health/favicon, pages /team/*".
func (r IgnoreRule) describe() string {
	var parts []string
	if r.Check != "" {
		parts = append(parts, "check "+r.Check)
	}
	if r.Title != "" {
		parts = append(parts, fmt.Sprintf("title %q", r.Title))
	}
	if r.Source != "" {
		parts = append(parts, fmt.Sprintf("source %q", r.Source))
	}
	if len(r.Pages) > 0 {
		parts = append(parts, "pages "+strings.Join(r.Pages, ", "))
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, ", ")
}

// StaleIgnores returns the indexes in rules of the rules that match
// nothing in the run: not its pages (pages yields them) nor its
// site-level issues (site). The problem they hide looks fixed, so the
// rule can go. now decides which rules have expired.
//
// It reads the run's issues, not a comparison's changes: a problem a
// rule ignores that is still there makes no change at all, which must
// not make the rule look stale. Rules that don't apply (expired, a bad
// date or nothing to match by) are never listed: ApplyIgnore already
// warns about them. Only issues are matched, so a rule that may hide
// another kind of change is never listed either (see issueRule).
func StaleIgnores(rules []IgnoreRule, now time.Time, pages func(func(*model.Page) bool), site []model.Issue) []int {
	var open []int
	var mods []string
	if len(rules) > 0 {
		mods = comparers()
	}
	for n, r := range rules {
		if r.state(now) == ruleLive && issueRule(r, mods) {
			open = append(open, n)
		}
	}
	if len(open) == 0 {
		return nil
	}
	hit := make([]bool, len(rules))
	// checked says the run checked a page the rule covers, so a rule
	// whose pages weren't visited (or whose audit didn't finish there)
	// isn't called stale: nothing says the problem is gone.
	checked := make([]bool, len(rules))
	match := func(i model.Issue, on func(IgnoreRule) bool) {
		for _, n := range open {
			if !hit[n] && rules[n].matches(i.Check, i.Title, i.Source) && on(rules[n]) {
				hit[n] = true
			}
		}
	}
	if pages != nil {
		pages(func(p *model.Page) bool {
			on := func(r IgnoreRule) bool { return r.onPage(p.Path) }
			for _, n := range open {
				if !checked[n] && on(rules[n]) && checkedFor(p, rules[n]) {
					checked[n] = true
				}
			}
			for _, i := range p.Issues {
				match(i, on)
			}
			return true
		})
	}
	for _, i := range site {
		match(i, func(r IgnoreRule) bool {
			// A site issue lists its pages; a rule with pages needs one of
			// them, as split does.
			return len(r.Pages) == 0 || slices.ContainsFunc(i.Pages, r.onPage)
		})
	}
	var out []int
	for _, n := range open {
		if !hit[n] && checked[n] {
			out = append(out, n)
		}
	}
	return out
}

// checkedFor reports whether the run checked page p for what rule r
// hides: the page loaded and, when it records an audit of the module the
// rule's check belongs to (a11y for "a11y/label"), that audit finished.
func checkedFor(p *model.Page, r IgnoreRule) bool {
	if p.Error != "" || p.Canceled() {
		return false
	}
	module, _, ok := strings.Cut(r.Check, "/")
	if !ok || strings.ContainsAny(module, "*?[") {
		return true
	}
	a, ok := p.Audits[module]
	return !ok || a.Finished()
}

// builtinComparers are the shipped modules that report changes of their
// own besides issue changes (screenshots, timings, page content,
// redirects, caching, messages). They count even when the registry
// doesn't hold them, e.g. when compare runs without the checks loaded.
var builtinComparers = []string{"visual", "speed", "seo", "echo", "health", "cache"}

// comparers returns the modules a rule for which may hide a change no
// issue carries: the built-in ones plus every registered module that
// implements check.Comparer (a check.PageComparer is one too). Tests
// replace it.
var comparers = func() []string {
	out := slices.Clone(builtinComparers)
	for _, m := range check.Registered() {
		if _, ok := m.(check.Comparer); ok && !slices.Contains(out, m.Name()) {
			out = append(out, m.Name())
		}
	}
	return out
}

// issueRule reports whether a rule can only hide issue changes, so that
// matching no issue makes it stale: its check names one module, which
// isn't in mods (see comparers). A rule for any check ("*" or none) may
// hide a visual change and is never called stale.
func issueRule(r IgnoreRule, mods []string) bool {
	mod, _, _ := strings.Cut(r.Check, "/")
	return mod != "" && !strings.Contains(mod, "*") && !slices.Contains(mods, mod)
}
