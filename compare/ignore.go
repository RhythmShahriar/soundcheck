// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

// IgnoreRule names changes that are known and shouldn't count, such as
// a missing favicon nobody will fix or a team page that changes weekly.
// Every field that is set must match: Check and Title are globs (see
// model.Glob), Pages a list of page globs. A rule with Pages ignores the
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
		if r.Check == "" && r.Title == "" && len(r.Pages) == 0 {
			c.Warnings = append(c.Warnings, fmt.Sprintf("ignore rule %d has no check, title or pages and would hide every change; it is skipped", n+1))
			continue
		}
		if r.Until != "" {
			until, err := time.ParseInLocation(dateLayout, r.Until, now.Location())
			if err != nil {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s: until %q isn't a date like 2026-12-31; the rule is skipped", label, r.Until))
				continue
			}
			// The rule covers the whole of its last day.
			if !now.Before(until.AddDate(0, 0, 1)) {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s expired on %s; its changes count again", label, r.Until))
				continue
			}
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

// split matches a change against the rule. ok is false when the rule
// doesn't apply. Otherwise hit is what to ignore and rest, when the rule
// covers only some of the change's pages, is the part that still counts.
func (r IgnoreRule) split(ch model.Change) (hit model.Change, rest *model.Change, ok bool) {
	if (r.Check != "" && !model.Glob(r.Check, ch.Check)) || (r.Title != "" && !model.Glob(r.Title, ch.Title)) {
		return ch, nil, false
	}
	if len(r.Pages) == 0 {
		return ch, nil, true
	}
	var in, out []string
	for _, p := range ch.Pages {
		if slices.ContainsFunc(r.Pages, func(g string) bool { return model.Glob(g, p) }) {
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
func StaleIgnores(rules []IgnoreRule, now time.Time, pages func(func(*model.Page) bool), site []model.Issue) []int {
	return nil
}
