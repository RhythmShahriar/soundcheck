// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
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
// A page whose walk tested a control but couldn't judge it (Undecided)
// can't judge its key either.
//
// The widget and control checks (widgetChecks) are tested on one page
// per URL group (see widgetGroups). A page whose walk didn't test them
// (model.KeyboardSummary.WidgetsOn unset) can't judge their keys
// either, however cleanly it was walked: the control is likely still
// broken there, only not pressed this time.
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
		if p != nil && (reports(p, k) || (walkedClean(p) && widgetsJudged(p, k) && !Undecided(p, k))) {
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

// widgetChecks are the checks only a page whose widgets and mouse-only
// controls were tested reports.
var widgetChecks = []string{ids.NotOperable, ids.Menu, ids.DialogFocus, ids.Escape, ids.FocusLost}

// widgetsJudged reports whether p can judge the issue with key as far as
// widget testing goes: key isn't a widget check's, or p's widgets were
// tested. A page without a keyboard summary is judged by its audit
// alone.
func widgetsJudged(p *model.Page, key string) bool {
	check, _, _ := strings.Cut(key, "|")
	if !slices.Contains(widgetChecks, check) || p.Keyboard == nil {
		return true
	}
	return p.Keyboard.WidgetsOn
}

// Undecided reports whether p's walk tested the control of the keyboard
// issue with key yet couldn't judge it (model.KeyboardSummary.Unjudged):
// it couldn't press the control, or couldn't tell the control's answer
// to a click from the page's own activity.
func Undecided(p *model.Page, key string) bool {
	return p != nil && p.Keyboard != nil && slices.Contains(p.Keyboard.Unjudged, key)
}

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
