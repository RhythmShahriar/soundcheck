// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package ids names the keyboard module's checks with their fixed
// titles, impacts and WCAG criteria. Every keyboard issue copies its
// Title, Impact and criteria from here, so the key flows (checks/keyflows)
// and the walk (checks/keyboard) report the same check the same way.
package ids

import (
	"slices"

	"github.com/RhythmShahriar/soundcheck/model"
)

// The keyboard checks, as the Check of their issues.
const (
	Trap            = "keyboard/trap"
	NotReachable    = "keyboard/not-reachable"
	NotOperable     = "keyboard/not-operable"
	Menu            = "keyboard/menu"
	FocusLost       = "keyboard/focus-lost"
	DialogFocus     = "keyboard/dialog-focus"
	NoFocusRing     = "keyboard/no-focus-ring"
	FocusInvisible  = "keyboard/focus-invisible"
	ContextChange   = "keyboard/context-change"
	SkipLinkBroken  = "keyboard/skip-link-broken"
	SkipLinkMissing = "keyboard/skip-link-missing"
	Escape          = "keyboard/escape"
	FocusObscured   = "keyboard/focus-obscured"
	Flow            = "keyboard/flow"
	ErrorNotLinked  = "keyboard/error-not-linked"
)

// Info describes one keyboard check.
type Info struct {
	ID     string      // the issue's Check
	Level  model.Level // the level it reports at
	Title  string      // the issue's Title, fixed text: names go in Detail
	Impact string      // critical, serious, moderate or minor
	// WCAG21 and WCAG22 are the success criteria it fails under each
	// WCAG version; empty for an advisory check.
	WCAG21 []string
	WCAG22 []string
	// EN lists the EN 301 549 V3.2.1 clauses of WCAG21: "9." plus each
	// criterion. Clauses gives them for any version.
	EN  []string
	Doc string // what it means and how to fix it, for --list-checks
}

// Checks lists every keyboard check, in the order --list-checks shows
// them.
var Checks = []Info{
	{ID: Trap, Level: model.Error, Impact: "critical", Title: "Keyboard focus is trapped",
		WCAG21: []string{"2.1.2"}, WCAG22: []string{"2.1.2"}, EN: []string{"9.2.1.2"},
		Doc: "Tab and Shift+Tab can't move focus out of an element or region, so a keyboard user is stuck there."},
	{ID: NotReachable, Level: model.Warning, Impact: "serious", Title: "Clickable element can't be reached by keyboard",
		WCAG21: []string{"2.1.1"}, WCAG22: []string{"2.1.1"}, EN: []string{"9.2.1.1"},
		Doc: "An element reacts to the mouse but Tab never reaches it. Use a button or link, or give it tabindex=\"0\" and key handlers."},
	{ID: NotOperable, Level: model.Warning, Impact: "serious", Title: "Control doesn't respond to Enter or Space",
		WCAG21: []string{"2.1.1"}, WCAG22: []string{"2.1.1"}, EN: []string{"9.2.1.1"},
		Doc: "A custom control reacts to a click but not to Enter or Space. Use a native button, or handle both keys."},
	{ID: Menu, Level: model.Warning, Impact: "serious", Title: "Submenu doesn't open by keyboard",
		WCAG21: []string{"2.1.1"}, WCAG22: []string{"2.1.1"}, EN: []string{"9.2.1.1"},
		Doc: "A navigation submenu opens on hover only. Open it on focus or with a button that toggles aria-expanded."},
	{ID: FocusLost, Level: model.Warning, Impact: "serious", Title: "Focus is lost after closing",
		WCAG21: []string{"2.4.3"}, WCAG22: []string{"2.4.3"}, EN: []string{"9.2.4.3"},
		Doc: "Closing a dialog, menu or disclosure drops focus on the page body. Move focus back to the control that opened it."},
	{ID: DialogFocus, Level: model.Warning, Impact: "serious", Title: "Dialog doesn't take or keep focus",
		WCAG21: []string{"2.4.3"}, WCAG22: []string{"2.4.3"}, EN: []string{"9.2.4.3"},
		Doc: "A dialog opens without moving focus into it, or a modal lets Tab reach the page behind it."},
	{ID: NoFocusRing, Level: model.Warning, Impact: "serious", Title: "No visible focus indicator",
		WCAG21: []string{"2.4.7"}, WCAG22: []string{"2.4.7"}, EN: []string{"9.2.4.7"},
		Doc: "Nothing on screen changes when the element gets keyboard focus. Don't remove the outline without a visible replacement."},
	{ID: FocusInvisible, Level: model.Warning, Impact: "serious", Title: "Focused element is hidden",
		WCAG21: []string{"2.4.7"}, WCAG22: []string{"2.4.7"}, EN: []string{"9.2.4.7"},
		Doc: "An element gets focus while it is transparent, hidden, clipped or off screen. Show it on focus or take it out of the Tab order."},
	{ID: ContextChange, Level: model.Warning, Impact: "serious", Title: "Moving focus changes the page",
		WCAG21: []string{"3.2.1"}, WCAG22: []string{"3.2.1"}, EN: []string{"9.3.2.1"},
		Doc: "Tabbing onto an element alone opens a page, a window or sends a form. Wait for Enter or a click."},
	{ID: SkipLinkBroken, Level: model.Warning, Impact: "moderate", Title: "Skip link doesn't move focus to the content",
		WCAG21: []string{"2.4.1"}, WCAG22: []string{"2.4.1"}, EN: []string{"9.2.4.1"},
		Doc: "The skip link's target is missing, or after following it the next Tab doesn't land in the main content."},
	{ID: SkipLinkMissing, Level: model.Notice, Impact: "minor", Title: "No skip link before a long run of stops",
		Doc: "Many Tab stops come before the main content and there is no skip link. Landmarks and headings can also do the job."},
	{ID: Escape, Level: model.Notice, Impact: "minor", Title: "Escape doesn't close it",
		Doc: "A dialog or menu opened by keyboard stays open after Escape."},
	{ID: FocusObscured, Level: model.Notice, Impact: "moderate", Title: "Focused element is covered by fixed content",
		WCAG22: []string{"2.4.11"},
		Doc:    "A sticky header, footer or banner covers the focused element entirely. Leave room for it with scroll-padding."},
	{ID: Flow, Level: model.Error, Impact: "critical", Title: "Key flow can't be finished by keyboard",
		WCAG21: []string{"2.1.1"}, WCAG22: []string{"2.1.1"}, EN: []string{"9.2.1.1"},
		Doc: "A step of a key flow (keyboard: true) can't be reached with Tab or operated with the keyboard."},
	{ID: ErrorNotLinked, Level: model.Warning, Impact: "serious", Title: "Form errors aren't tied to their fields",
		WCAG21: []string{"3.3.1"}, WCAG22: []string{"3.3.1"}, EN: []string{"9.3.3.1"},
		Doc: "After a failed submit, focus doesn't move to the error and no field points at its message with aria-describedby."},
}

// Lookup returns the check with id and whether there is one.
func Lookup(id string) (Info, bool) {
	i := slices.IndexFunc(Checks, func(c Info) bool { return c.ID == id })
	if i < 0 {
		return Info{}, false
	}
	return Checks[i], true
}

// WCAG returns the criteria the check fails under the WCAG version
// (2.2, else 2.1).
func (i Info) WCAG(version string) []string {
	if version == "2.2" {
		return slices.Clone(i.WCAG22)
	}
	return slices.Clone(i.WCAG21)
}

// Clauses returns the EN 301 549 clauses the check fails under the WCAG
// version: "9." plus each criterion of WCAG(version).
func (i Info) Clauses(version string) []string {
	var out []string
	for _, c := range i.WCAG(version) {
		out = append(out, "9."+c)
	}
	return out
}
