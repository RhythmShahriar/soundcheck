// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/internal/focus"
	"github.com/RhythmShahriar/soundcheck/internal/pii"
	"github.com/RhythmShahriar/soundcheck/model"
)

// frameTrapPresses is how many presses in a row focus may stay in a
// frame the walk can't look into before it counts as stuck: a payment
// form in an iframe holds focus for many presses by design.
const frameTrapPresses = 50

// timerGrace is how long after the load a navigation can be the page's
// own (a redirect on a timer) rather than the Tab press's doing.
const timerGrace = 2000

// focusPathLen is how many stops before a trap its issue lists.
const focusPathLen = 6

// skipLinkStops is how many of the first stops may be the skip link,
// and longRun how many stops before the main content make one worth
// having.
const (
	skipLinkStops = 3
	longRun       = 10
)

// Classify turns a page's trace into its keyboard issues, under the
// WCAG version (2.1 or 2.2). It needs no browser: every rule reads the
// trace alone, so the rules are tested with traces written by hand.
// Accessible names go into Detail redacted; titles stay fixed. At most
// maxPerCheck issues of each check are kept.
func Classify(t Trace, o model.KeyboardOptions, wcag string) []model.Issue {
	if o.TrapAfter < 1 {
		o.TrapAfter = defaults().TrapAfter
	}
	var set issueSet
	if i, ok := trap(t, o.TrapAfter, wcag); ok {
		set.add(i)
	}
	for n, s := range t.Forward {
		if s.IsBody || s.Ignored {
			continue
		}
		switch {
		case s.Hidden != "":
			set.add(newIssue(ids.FocusInvisible, wcag, s.Selector,
				fmt.Sprintf("Tab stop %d (%s) has focus but is hidden (%s).", n+1, describe(s), hiddenWords[s.Hidden])))
		case s.Obscured:
			set.add(newIssue(ids.FocusObscured, wcag, s.Selector,
				fmt.Sprintf("Tab stop %d (%s) is fully covered by %s.", n+1, describe(s), orUnknown(s.ObscuredBy))))
		}
		if words, ok := effectWords[s.Effect]; ok && (s.Effect != EffectNavigation || s.ByFocus || s.AtMS >= timerGrace) {
			set.add(newIssue(ids.ContextChange, wcag, s.Selector,
				fmt.Sprintf("Moving focus to %s with Tab %s without Enter.", describe(s), words)))
		}
	}
	for _, i := range skipLinks(t, o.SkipLink, wcag) {
		set.add(i)
	}
	for _, i := range noRings(t, wcag) {
		set.add(i)
	}
	for _, i := range widgetIssues(t, wcag) {
		set.add(i)
	}
	for _, i := range mouseOnly(t, wcag) {
		set.add(i)
	}
	return set.out
}

// mouseOnly reports the controls only a mouse can use: one Tab never
// reached (not-reachable) and a focusable one that did something when
// clicked but not on Enter or Space (not-operable). A control a click
// didn't move either isn't reported; neither is one the walk
// didn't press.
func mouseOnly(t Trace, wcag string) []model.Issue {
	var out []model.Issue
	for _, c := range t.Listeners {
		if c.Selector == "" || len(c.Events) == 0 {
			continue
		}
		what := describe(Step{Stop: focus.Stop{Tag: c.Tag, Name: c.Name}})
		switch {
		case !c.Focusable && !c.Seen:
			out = append(out, newIssue(ids.NotReachable, wcag, c.Selector,
				fmt.Sprintf("%s reacts to %s but Tab never reaches it.", what, c.Events[0])))
		case c.Focusable && c.Tested && c.Skipped == "" && noResponse(c.Enter) && (c.Link || noResponse(c.Space)) && !noResponse(c.Click):
			out = append(out, newIssue(ids.NotOperable, wcag, c.Selector,
				fmt.Sprintf("%s reacts to a mouse click but not to Enter or Space.", what)))
		}
	}
	return out
}

// unjudged returns the keys, as Classify's issues have them, of the
// not-operable issues a trace from a page whose widgets were tested
// can't judge: a focusable control with a mouse listener the walk
// didn't press (something covered it, keyboard.widgets ran out, it was
// gone or the page went away first) or one whose keys did nothing and
// whose click had an effect it couldn't tell from the page's own
// activity. mouseOnly reports neither, so a comparison mustn't take
// their absence as fixed.
func unjudged(t Trace) []string {
	var out []string
	for _, c := range t.Listeners {
		if c.Selector == "" || len(c.Events) == 0 || !c.Focusable || c.Skipped == "looks destructive" {
			continue
		}
		pressed := c.Tested && c.Skipped == ""
		if pressed && (!noResponse(c.Enter) || !c.Link && !noResponse(c.Space) || c.Click != PressUnknown) {
			continue
		}
		k := model.IssueKey(ids.NotOperable, "", c.Selector)
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// noResponse is true for a press that did nothing the walk can tie to
// it: a navigation something else started is no response.
func noResponse(effect string) bool { return effect == "" || effect == PressUnknown }

// focusWords say where focus went after a widget closed, closeWords
// how it was closed, for an issue's Detail.
var (
	focusWords = map[string]string{FocusBody: "the page body", FocusRemoved: "a removed element", FocusHidden: "a hidden element"}
	closeWords = map[string]string{ClosedEscape: "Escape", ClosedClose: "Enter on the close control", ClosedToggle: "toggle"}
)

// widgetIssues judges the widgets the walk tested. A widget a press
// took the page away from is recorded, never judged: the walk didn't
// see the rest of it.
func widgetIssues(t Trace, wcag string) []model.Issue {
	var out []model.Issue
	for _, r := range t.Widgets {
		if r.Left || r.Trigger == "" {
			continue
		}
		name := widgetName(r)
		if !r.Opened {
			// Only a submenu the walk found can be said not to open: a
			// trigger whose popup it can't see may open something else.
			if r.Kind == WidgetMenu && r.Submenu {
				out = append(out, newIssue(ids.Menu, wcag, r.Trigger,
					fmt.Sprintf("The submenu of %s doesn't open with %s.", name, menuKeys(r))))
			}
			continue
		}
		if r.Kind == WidgetDialog {
			switch {
			case !r.FocusInside:
				out = append(out, newIssue(ids.DialogFocus, wcag, r.Trigger,
					fmt.Sprintf("The dialog opened by %s didn't get focus.", name)))
			case r.Modal && r.Leaked != "":
				out = append(out, newIssue(ids.DialogFocus, wcag, r.Trigger,
					fmt.Sprintf("The dialog opened by %s let Tab reach %s behind it.", name, r.Leaked)))
			}
		}
		if r.EscapeTried && !r.EscapeClosed && (r.Kind == WidgetDialog || r.Kind == WidgetMenu) {
			out = append(out, newIssue(ids.Escape, wcag, r.Trigger,
				fmt.Sprintf("The %s opened by %s is still visible 500 ms after Escape.", widgetWord(r.Kind), name)))
		}
		if where, ok := focusWords[r.FocusAfter]; ok && closeWords[r.Closed] != "" {
			out = append(out, newIssue(ids.FocusLost, wcag, r.Trigger,
				fmt.Sprintf("After %s on %s focus went to %s.", closeWords[r.Closed], name, where)))
		}
	}
	return out
}

// hiddenWords and effectWords say what a Step's Hidden and Effect mean,
// for an issue's Detail.
var (
	hiddenWords = map[string]string{
		HiddenOpacity: "opacity 0", HiddenVisibility: "visibility hidden",
		HiddenClip: "clipped to 1 px", HiddenOffscreen: "outside the viewport",
	}
	effectWords = map[string]string{
		EffectNavigation: "opened a new page", EffectTarget: "opened a new window", EffectForm: "sent a form",
	}
)

// describe names a stop for an issue's Detail: its tag and its
// redacted accessible name, as `a "Cart"`, or the tag alone without a
// name.
func describe(s Step) string {
	name := clip(pii.Redact(s.Name), maxName)
	if name == "" {
		return orUnknown(s.Tag)
	}
	return fmt.Sprintf("%s %q", orUnknown(s.Tag), name)
}

func orUnknown(s string) string {
	if s == "" {
		return "an element"
	}
	return s
}

// trap reports a keyboard trap: Tab kept focus on one element, or in
// turn on two or three, for trapAfter presses without passing the
// document's stop, focusable elements come after them, Shift+Tab
// couldn't leave them either and Escape then Tab didn't get out. A
// frame the walk can't see into needs frameTrapPresses presses each
// way, a dialog Escape closes holds focus on purpose and a capped walk
// proves nothing.
func trap(t Trace, trapAfter int, wcag string) (model.Issue, bool) {
	p := t.TrapProbe
	if t.Capped || p == nil || p.FocusablesAfter <= 0 || p.EscapeLeft {
		return model.Issue{}, false
	}
	var nodes []int
	end := 0
	for i := range t.Forward {
		if nodes = suspect(t.Forward[:i+1], trapAfter); nodes != nil {
			end = i + 1
			break
		}
	}
	if nodes == nil {
		return model.Issue{}, false
	}
	in := func(s Step) bool { return !s.IsBody && slices.Contains(nodes, s.BackendNodeID) }
	var members []Step
	for _, s := range t.Forward[:end] {
		if in(s) && !slices.ContainsFunc(members, func(m Step) bool { return m.BackendNodeID == s.BackendNodeID }) {
			members = append(members, s)
		}
	}
	frame, dialog, ignored := false, true, false
	for _, m := range members {
		frame = frame || m.IsFrame
		dialog = dialog && m.InDialog
		ignored = ignored || m.Ignored
	}
	if ignored || dialog && slices.ContainsFunc(t.Widgets, func(w WidgetResult) bool { return w.Kind == "dialog" && w.EscapeClosed }) {
		return model.Issue{}, false
	}
	need := trapAfter
	if frame {
		need = frameTrapPresses
	}
	if len(t.Backward) < need {
		return model.Issue{}, false
	}
	for _, s := range t.Backward[:need] {
		if !in(s) {
			return model.Issue{}, false
		}
	}
	// Presses that kept focus in the set: the forward run up to the end
	// of the walk and the backward run from its start.
	held := 0
	for i := len(t.Forward) - 1; i >= 0 && in(t.Forward[i]); i-- {
		held++
	}
	for _, s := range t.Backward {
		if !in(s) {
			break
		}
		held++
	}
	source := members[0].Selector
	if len(p.Set) > 0 && p.Set[0] != "" {
		source = p.Set[0]
	}
	after := strings.Join(p.After, ", ")
	if p.FocusablesAfter > len(p.After) {
		after += " ..."
	}
	i := newIssue(ids.Trap, wcag, source, fmt.Sprintf(
		"Tab and Shift+Tab kept focus here for %d presses; %d focusable %s after it (%s) can't be reached.",
		held, p.FocusablesAfter, plural(p.FocusablesAfter, "element", "elements"), strings.TrimSpace(after)))
	path := t.Forward[max(0, end-focusPathLen):end]
	i.FocusPath = make([]string, 0, len(path))
	for _, s := range path {
		i.FocusPath = append(i.FocusPath, s.Selector)
	}
	return i, true
}

// skipLinks reports a skip link that doesn't do its job and, as set by
// keyboard.skip_link, a long run of stops before the main content with
// no skip link.
func skipLinks(t Trace, mode, wcag string) []model.Issue {
	if sl := t.SkipLink; sl != nil {
		why := ""
		switch {
		case !sl.TargetFound:
			why = "doesn't exist"
		case !sl.NextInsideOrAfterTarget && !sl.Scrolled && !sl.NothingAfter:
			why = "isn't where the next Tab goes"
		}
		if why == "" {
			return nil
		}
		return []model.Issue{newIssue(ids.SkipLinkBroken, wcag, sl.Selector,
			fmt.Sprintf("Skip link %q points at #%s, which %s.", clip(pii.Redact(sl.Name), maxName), sl.TargetID, why))}
	}
	if mode == SkipLinkOff {
		return nil
	}
	before := 0
	for i, s := range t.Forward {
		if t.MainIndex >= 0 && i >= t.MainIndex {
			break
		}
		if s.IsBody {
			if t.MainIndex < 0 {
				break // round once: every stop of the page is counted
			}
			continue
		}
		before++
	}
	if before == 0 || mode != SkipLinkRequired && before < longRun {
		return nil
	}
	i := newIssue(ids.SkipLinkMissing, wcag, "document",
		fmt.Sprintf("%d Tab %s before the main content and none of the first %d is a skip link.",
			before, plural(before, "stop comes", "stops come"), skipLinkStops))
	if mode == SkipLinkRequired {
		// Required by the site's own policy: then it fails 2.4.1 as a
		// warning, whatever the landmarks.
		i.Level, i.WCAG, i.EN301549 = model.Warning, []string{"2.4.1"}, []string{"9.2.4.1"}
	}
	return []model.Issue{i}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// noRings reports the stops whose focus changed fewer than ringPixels
// pixels on screen. An unstable picture gives no verdict; a stop
// focus-invisible covers (it is hidden) isn't reported twice.
func noRings(t Trace, wcag string) []model.Issue {
	hidden := map[string]bool{}
	for _, s := range t.Forward {
		if s.Hidden != "" {
			hidden[s.Selector] = true
		}
	}
	var out []model.Issue
	for _, r := range t.Rings {
		if r.Unstable || r.Changed >= ringPixels || r.Selector == "" || hidden[r.Selector] {
			continue
		}
		what := orUnknown(r.Tag)
		if name := clip(pii.Redact(r.Name), maxName); name != "" {
			what = fmt.Sprintf("%q", name)
		}
		out = append(out, newIssue(ids.NoFocusRing, wcag, r.Selector, fmt.Sprintf(
			"Tab focus on %s changes %d %s around it; people using a keyboard can't see where they are.",
			what, r.Changed, plural(r.Changed, "pixel", "pixels"))))
	}
	return out
}
