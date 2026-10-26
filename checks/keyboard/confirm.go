// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Unjudged is the keyboard side of the confirm pass. Given a page as
// visited again and the keys of keyboard issues looked for on it, it
// returns the keys whose absence proves nothing: the page doesn't
// report them and its keyboard walk didn't finish clean. The walk
// counts as not clean when it was capped (keyboard.max_tabs or a
// navigation it couldn't stop), timed out, failed or was skipped, or
// when the page has no keyboard audit at all.
//
// A capped walk is a finished audit (model.Audit.Finished) but it can't
// prove a trap (Classify reports none from a walk that stopped early)
// nor that a stop past the cap is fine. So a keyboard issue seen on the
// first visit and missing from such a re-walk must stay UNSTABLE and
// never be counted as FIXED.
//
// Keys that aren't keyboard ones are never returned, nor is a key the
// page reports: that issue showed again. A nil page leaves every
// keyboard key unjudged.
func Unjudged(p *model.Page, keys []string) []string {
	var out []string
	for _, k := range keys {
		if !strings.HasPrefix(k, "keyboard/") || slices.Contains(out, k) {
			continue
		}
		if p != nil && (walkedClean(p) || reports(p, k)) {
			continue
		}
		out = append(out, k)
	}
	return out
}

// Unjudged is the package's Unjudged, so the confirm pass can reach it
// through the registered module (check.Registered) with
//
//	interface{ Unjudged(*model.Page, []string) []string }
//
// without importing this package.
func (*Module) Unjudged(p *model.Page, keys []string) []string { return Unjudged(p, keys) }

// walkedClean reports whether p's keyboard walk ran to its end with
// nothing capping it.
func walkedClean(p *model.Page) bool {
	a, ok := p.Audits["keyboard"]
	return ok && a.Status == model.AuditOK
}

// reports reports whether p has the keyboard issue with key.
func reports(p *model.Page, key string) bool {
	return slices.ContainsFunc(p.Issues, func(i model.Issue) bool {
		if i.Key == "" {
			i.Identify()
		}
		return i.Key == key
	})
}
