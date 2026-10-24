// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"slices"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Confirm checks the BROKE and NEW changes of c against again, a second
// visit of their pages (the engine's re-visit) and moves the ones that
// didn't show again to UNSTABLE. A flaky 500, a timeout under load or a
// script that failed once then doesn't fail a build.
//
// Only page issues can be confirmed this way (see
// model.Change.FromPageIssue); other changes stay as they are. A change
// is judged page by page, against the snapshot's page (before) and the
// re-visited one: where it reproduced it keeps its class; the pages
// where it didn't become a separate UNSTABLE change. Pages that weren't
// re-visited, or whose second visit was canceled or failed to load, say
// nothing and keep the change.
func Confirm(c *model.Comparison, before, again *model.Run) {
	if again == nil || len(again.Pages) == 0 {
		return
	}
	var out []model.Change
	moved := false
	for _, ch := range c.Changes {
		if (ch.Class != model.Broke && ch.Class != model.New) || !ch.FromPageIssue {
			out = append(out, ch)
			continue
		}
		var still, gone []string
		for _, p := range ch.Pages {
			if reproduced(issueKey(ch), p, before, again) {
				still = append(still, p)
			} else {
				gone = append(gone, p)
			}
		}
		switch {
		case len(gone) == 0:
			out = append(out, ch)
		case len(still) == 0:
			// Same check, title, source and pages: the ID stays, so a
			// script can follow the change across the reclassification.
			ch.Class = model.Unstable
			out = append(out, ch)
		default:
			unstable := withPages(ch, gone)
			unstable.Class = model.Unstable
			out = append(out, withPages(ch, still), unstable)
		}
		moved = moved || len(gone) > 0
	}
	if !moved {
		return
	}
	c.Changes = out
	Sort(c.Changes)
	c.Complete()
}

// reproduced reports whether the issue with key showed again on page,
// or whether the re-visit can't tell.
func reproduced(key, page string, before, again *model.Run) bool {
	ap := again.Page(page)
	if ap == nil || ap.Canceled() || (before != nil && before.Page(page) == nil) {
		return true
	}
	if slices.ContainsFunc(ap.Issues, func(i model.Issue) bool { return issueKeyOf(i) == key }) {
		return true
	}
	// A second visit that failed to load proves nothing about an issue
	// found on a page that did load. (When the change is the failure to
	// load, the same failure matched its key above.)
	return ap.Error != ""
}

// issueKey is the key of the issues a change comes from; see
// model.Issue.Identify.
func issueKey(ch model.Change) string { return ch.Check + "|" + ch.Title + "|" + ch.Source }

func issueKeyOf(i model.Issue) string {
	if i.Key == "" {
		i.Identify()
	}
	return i.Key
}
