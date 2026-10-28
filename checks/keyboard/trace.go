// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/internal/focus"
	"github.com/RhythmShahriar/soundcheck/internal/pii"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Schema names the trace format (Trace.Schema).
const Schema = "rhythm-soundcheck/keyboard-trace/v1"

// Trace is what the keyboard walk of one page recorded, saved to
// keyboard/<pageid>.json in the run's folder. Classify reads nothing
// else, so the file is the whole evidence for the page's keyboard
// issues.
//
// The file is JSON:
//
//	{
//	  "schema":     "rhythm-soundcheck/keyboard-trace/v1",
//	  "page":       "/checkout/",       path only, no query; token-like segments masked
//	  "viewport":   "desktop",
//	  "forward":    [Step, ...],        one entry per Tab press, in order
//	  "backward":   [Step, ...],        one entry per Shift+Tab press from the last stop
//	  "trap_probe": TrapProbe | absent, Escape then Tab on a suspected trap
//	  "skip_link":  SkipLink | absent,  the skip link followed with Enter
//	  "main_index": n,                  index in forward of the first stop in the main landmark, -1 without one
//	  "widgets":    [WidgetResult, ...],
//	  "listeners":  [Candidate, ...],
//	  "rings":      [Ring, ...],        focus indicators measured by pixel difference
//	  "held":       ["POST /cart/"],    requests held while keys were pressed
//	  "events":     ["download blocked"], what else the safety envelope stopped
//	  "capped":     false,              the walk hit max_tabs, ran out of time or stopped early
//	  "stopped":    "..." | absent      why it stopped early, other than max_tabs
//	}
//
// Every forward and backward entry carries focus.Stop's fields at its
// top level (selector, backend_node_id, tag, role, name_len, name, box,
// in_viewport, in_dialog, connected, is_body, is_frame, frame_host)
// plus the Step fields below. schema, page, forward and backward and
// the Stop field names are fixed: the report reads forward[].selector
// and forward[].is_body.
type Trace struct {
	Schema    string         `json:"schema"`
	Page      string         `json:"page"`
	Viewport  string         `json:"viewport"`
	Forward   []Step         `json:"forward"`
	Backward  []Step         `json:"backward"`
	TrapProbe *TrapProbe     `json:"trap_probe,omitempty"`
	SkipLink  *SkipLink      `json:"skip_link,omitempty"`
	MainIndex int            `json:"main_index"`
	Widgets   []WidgetResult `json:"widgets"`
	Listeners []Candidate    `json:"listeners"`
	Rings     []Ring         `json:"rings"`
	Held      []string       `json:"held"`
	Events    []string       `json:"events"`
	Capped    bool           `json:"capped"`
	// Stopped says why the walk ended before it was back round, other
	// than max_tabs, such as a Tab press that took the page away.
	Stopped string `json:"stopped,omitempty"`
}

// Step is one key press of the walk and the stop it left focus on.
type Step struct {
	focus.Stop
	// Hidden says how the focused element is hidden, read after the
	// browser scrolled it into view: "" (it shows), opacity,
	// visibility, clip or offscreen.
	Hidden string `json:"hidden,omitempty"`
	// Obscured is true when every one of five points of the element
	// (its center and four inset corners) is covered by a fixed or
	// sticky element outside it, ObscuredBy that element's selector.
	Obscured   bool   `json:"obscured,omitempty"`
	ObscuredBy string `json:"obscured_by,omitempty"`
	// Effect is what the press did besides moving focus: "",
	// navigation (the page started to leave), target (a new window or
	// tab) or form (a form or other write was sent).
	Effect string `json:"effect,omitempty"`
	// ByFocus is true when the navigation Effect names started while the
	// page handled this stop's focus event: the Tab press caused it,
	// whenever it happened. Without it a navigation soon after the load
	// may be the page's own timer.
	ByFocus bool `json:"by_focus,omitempty"`
	// AtMS is the time of the press since the page loaded, in
	// milliseconds.
	AtMS int `json:"at_ms"`
	// Ignored is true for a stop matching keyboard.ignore: recorded,
	// never judged.
	Ignored bool `json:"ignored,omitempty"`
}

// Hidden values (Step.Hidden).
const (
	HiddenOpacity    = "opacity"
	HiddenVisibility = "visibility"
	HiddenClip       = "clip"
	HiddenOffscreen  = "offscreen"
)

// Effect values (Step.Effect).
const (
	EffectNavigation = "navigation"
	EffectTarget     = "target"
	EffectForm       = "form"
)

// TrapProbe is the walk's second look at a suspected trap: Escape, then
// Tab, from inside the set of stops focus kept returning to.
type TrapProbe struct {
	Set []string `json:"set"` // selectors of the stops, in DOM order
	// EscapeLeft is true when focus was outside the set after Escape
	// and Tab.
	EscapeLeft bool `json:"escape_left"`
	// FocusablesAfter counts the tabbable elements after the set in
	// document order, the ones a trap keeps out of reach; After names
	// the first two.
	FocusablesAfter int      `json:"focusables_after"`
	After           []string `json:"after,omitempty"`
}

// SkipLink is the skip link among the first three stops, followed with
// Enter after the walk.
type SkipLink struct {
	Selector string `json:"selector"`
	Name     string `json:"name"`      // the link's accessible name, redacted
	TargetID string `json:"target_id"` // the id its href points at
	// TargetFound is true when the document has an element with the id.
	TargetFound bool `json:"target_found"`
	// NextStop is the stop one Tab after following the link.
	NextStop string `json:"next_stop"`
	// NextInsideOrAfterTarget is true when that stop is inside the
	// target or after it in document order: the link did its job, even
	// for a target without tabindex (WCAG's sequential focus start
	// point).
	NextInsideOrAfterTarget bool `json:"next_inside_or_after_target"`
	// Scrolled is true when following the link scrolled the page.
	Scrolled bool `json:"scrolled"`
	// NothingAfter is true when no tabbable element is inside the
	// target or after it, so the next Tab can't show where the link
	// went: the link isn't judged by it.
	NothingAfter bool `json:"nothing_after,omitempty"`
}

// WidgetResult is one menu, dialog or disclosure the walk tested by
// keyboard: opened from its trigger, Tab inside, Escape, closed.
type WidgetResult struct {
	Kind    string `json:"kind"`    // menu, dialog, details or disclosure
	Trigger string `json:"trigger"` // selector of the control that opens it
	Target  string `json:"target,omitempty"`
	Tag     string `json:"tag,omitempty"`  // the trigger's tag
	Name    string `json:"name,omitempty"` // the trigger's accessible name, redacted
	// Link is true for a trigger that is a link to another page: it is
	// never pressed, only Tab is tried into its submenu.
	Link bool `json:"link,omitempty"`
	// Submenu is true for a menu whose submenu the walk found: a list
	// nested in the item or the element the trigger controls.
	Submenu bool `json:"submenu,omitempty"`
	// Keys lists the keys tried to open it, in order: Enter, Space,
	// ArrowDown or Tab.
	Keys []string `json:"keys,omitempty"`
	// Opened is true when one of them opened it, OpenedBy that key.
	Opened   bool   `json:"opened"`
	OpenedBy string `json:"opened_by,omitempty"`
	// Modal is true for a dialog opened with showModal() or marked
	// aria-modal="true".
	Modal bool `json:"modal,omitempty"`
	// FocusInside is true when focus was inside the dialog once it
	// opened.
	FocusInside bool `json:"focus_inside,omitempty"`
	// Leaked is the element behind a modal dialog Tab reached from
	// inside it, within its focusable elements plus two presses.
	Leaked string `json:"leaked,omitempty"`
	// EscapeTried is true when Escape was pressed on it, EscapeClosed
	// when that closed it again within 500 ms.
	EscapeTried  bool `json:"escape_tried,omitempty"`
	EscapeClosed bool `json:"escape_closed"`
	// Closed says how it was closed: escape, close (Enter on its close
	// control) or toggle (its trigger again); "" when it stayed open.
	Closed string `json:"closed,omitempty"`
	// FocusAfter says where focus went once it closed: "" (somewhere a
	// visitor can see), body, removed or hidden.
	FocusAfter string `json:"focus_after,omitempty"`
	// Left is true when a press took the page away: the rest of the
	// widget was skipped and the page loaded again.
	Left bool `json:"left,omitempty"`
}

// Kinds of widget (WidgetResult.Kind).
const (
	WidgetMenu       = "menu"
	WidgetDialog     = "dialog"
	WidgetDetails    = "details"
	WidgetDisclosure = "disclosure"
)

// How a widget was closed (WidgetResult.Closed).
const (
	ClosedEscape = "escape"
	ClosedClose  = "close"
	ClosedToggle = "toggle"
)

// Where focus went after a widget closed (WidgetResult.FocusAfter).
const (
	FocusBody    = "body"
	FocusRemoved = "removed"
	FocusHidden  = "hidden"
)

// Candidate is an element with mouse listeners of its own, checked for
// keyboard access. Only elements past the false-alarm guards are
// listed (visible, enabled, not natively focusable, at least 4x4 px,
// not inside a focusable element or a label, not a container of more
// than 5 elements a listener may be delegated from).
type Candidate struct {
	Selector string   `json:"selector"`
	Events   []string `json:"events,omitempty"` // click, mousedown, pointerdown, mouseup or pointerup
	Tag      string   `json:"tag,omitempty"`
	Name     string   `json:"name,omitempty"` // the accessible name, redacted
	// Focusable is true when its tabindex is 0 or more: Tab can reach
	// it. Seen is true when the walk's Tab or Shift+Tab focused it.
	Focusable bool `json:"focusable,omitempty"`
	Seen      bool `json:"seen,omitempty"`
	// Link is true for an element with role link: it gets Enter only.
	Link bool `json:"link,omitempty"`
	// Tested is true when the walk pressed it: Enter, then Space, then
	// a mouse click when neither key did anything. Enter, Space and
	// Click say what each did: "" nothing, or dom, navigation or
	// request (sent or held).
	Tested bool   `json:"tested,omitempty"`
	Enter  string `json:"enter,omitempty"`
	Space  string `json:"space,omitempty"`
	Click  string `json:"click,omitempty"`
	// Skipped says why a focusable control wasn't pressed: it looks
	// destructive, keyboard.widgets ran out or something covers it.
	Skipped string `json:"skipped,omitempty"`
}

// Effects of a press on a mouse-only candidate (Candidate.Enter,
// Space and Click).
const (
	PressDOM        = "dom"
	PressNavigation = "navigation"
	PressRequest    = "request"
)

// Caps a trace keeps to, on disk and when it is read back.
const (
	// MaxTraceBytes caps a trace file.
	MaxTraceBytes = 2 << 20
	// maxSteps caps forward and backward: max_tabs at its highest, plus
	// the first stop seen again.
	maxSteps = 2001
	// maxName and maxSelectorRunes cap a stop's name and selector.
	maxName          = 120
	maxSelectorRunes = 300
	// maxList caps every other list in a trace.
	maxList = 500
	// maxText caps every other string.
	maxText = 300
)

// ErrTraceTooBig is DecodeTrace's error for a file over MaxTraceBytes.
var ErrTraceTooBig = errors.New("keyboard trace is over 2 MB")

// DecodeTrace reads a trace file. The file comes from a page the user
// doesn't control, so it is held to the caps the recorder writes with:
// longer lists and strings are cut, whatever the file says.
func DecodeTrace(data []byte) (Trace, error) {
	var t Trace
	if len(data) > MaxTraceBytes {
		return t, ErrTraceTooBig
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return Trace{}, fmt.Errorf("keyboard trace: %w", err)
	}
	if t.Schema != Schema {
		return Trace{}, fmt.Errorf("keyboard trace: schema %q, want %q", clip(t.Schema, maxText), Schema)
	}
	t.clamp()
	return t, nil
}

// clamp holds t to the caps. It masks every selector first (see mask),
// so a cut can't leave part of a token the masking no longer knows.
func (t *Trace) clamp() {
	t.mask()
	t.Page, t.Viewport, t.Stopped = clip(t.Page, maxText), clip(t.Viewport, maxText), clip(t.Stopped, maxText)
	t.Forward, t.Backward = clampSteps(t.Forward), clampSteps(t.Backward)
	if t.MainIndex < -1 || t.MainIndex >= len(t.Forward) {
		t.MainIndex = -1
	}
	if p := t.TrapProbe; p != nil {
		p.Set = clipAll(p.Set[:min(len(p.Set), maxList)], maxSelectorRunes)
		p.After = clipAll(p.After[:min(len(p.After), 2)], maxSelectorRunes)
		p.FocusablesAfter = max(p.FocusablesAfter, 0)
	}
	if s := t.SkipLink; s != nil {
		s.Selector, s.NextStop = clip(s.Selector, maxSelectorRunes), clip(s.NextStop, maxSelectorRunes)
		s.Name, s.TargetID = clip(s.Name, maxName), clip(s.TargetID, maxText)
	}
	t.Widgets = t.Widgets[:min(len(t.Widgets), maxList)]
	for i := range t.Widgets {
		w := &t.Widgets[i]
		w.Kind, w.Trigger, w.Target = clip(w.Kind, maxText), clip(w.Trigger, maxSelectorRunes), clip(w.Target, maxSelectorRunes)
		w.Tag, w.Name, w.Leaked = clip(w.Tag, maxText), clip(w.Name, maxName), clip(w.Leaked, maxSelectorRunes)
		w.Keys = clipAll(w.Keys[:min(len(w.Keys), 5)], maxText)
		w.OpenedBy, w.Closed, w.FocusAfter = clip(w.OpenedBy, maxText), clip(w.Closed, maxText), clip(w.FocusAfter, maxText)
	}
	t.Listeners = t.Listeners[:min(len(t.Listeners), maxList)]
	for i := range t.Listeners {
		c := &t.Listeners[i]
		c.Selector = clip(c.Selector, maxSelectorRunes)
		c.Events = clipAll(c.Events[:min(len(c.Events), 5)], maxText)
		c.Tag, c.Name, c.Skipped = clip(c.Tag, maxText), clip(c.Name, maxName), clip(c.Skipped, maxText)
		c.Enter, c.Space, c.Click = clip(c.Enter, maxText), clip(c.Space, maxText), clip(c.Click, maxText)
	}
	t.Rings = t.Rings[:min(len(t.Rings), maxRings)]
	for i := range t.Rings {
		r := &t.Rings[i]
		r.Selector, r.Name, r.Tag, r.Crop = clip(r.Selector, maxSelectorRunes), clip(r.Name, maxName), clip(r.Tag, maxText), clip(r.Crop, maxText)
		r.Changed, r.Stop = max(r.Changed, 0), max(r.Stop, 0)
	}
	t.Held = clipAll(t.Held[:min(len(t.Held), maxList)], maxText)
	t.Events = clipAll(t.Events[:min(len(t.Events), maxList)], maxText)
}

// mask masks credentials, email addresses, phone, card and IBAN numbers
// in every selector the trace holds (internal/pii): a site can put a
// token or a customer's email address in an id, a name or a class. The
// trace is a file in the run's folder. The walk itself compares the
// selectors as they are; the file and the issues get them masked.
func (t *Trace) mask() {
	for _, steps := range [][]Step{t.Forward, t.Backward} {
		for i := range steps {
			steps[i].Selector, steps[i].ObscuredBy = pii.Redact(steps[i].Selector), pii.Redact(steps[i].ObscuredBy)
		}
	}
	if p := t.TrapProbe; p != nil {
		maskAll(p.Set)
		maskAll(p.After)
	}
	if s := t.SkipLink; s != nil {
		s.Selector, s.NextStop, s.TargetID = pii.Redact(s.Selector), pii.Redact(s.NextStop), pii.Redact(s.TargetID)
	}
	for i := range t.Widgets {
		w := &t.Widgets[i]
		w.Trigger, w.Target, w.Leaked = pii.Redact(w.Trigger), pii.Redact(w.Target), pii.Redact(w.Leaked)
	}
	for i := range t.Listeners {
		t.Listeners[i].Selector = pii.Redact(t.Listeners[i].Selector)
	}
	for i := range t.Rings {
		t.Rings[i].Selector = pii.Redact(t.Rings[i].Selector)
	}
}

func maskAll(s []string) {
	for i := range s {
		s[i] = pii.Redact(s[i])
	}
}

func clampSteps(s []Step) []Step {
	s = s[:min(len(s), maxSteps)]
	for i := range s {
		st := &s[i]
		st.Selector, st.Name = clip(st.Selector, maxSelectorRunes), clip(st.Name, maxName)
		st.Tag, st.Role, st.FrameHost = clip(st.Tag, maxText), clip(st.Role, maxText), clip(st.FrameHost, maxText)
		st.Hidden, st.Effect, st.ObscuredBy = clip(st.Hidden, maxText), clip(st.Effect, maxText), clip(st.ObscuredBy, maxSelectorRunes)
		st.NameLen = max(st.NameLen, 0)
	}
	return s
}

// clip cuts s to n runes at a rune boundary and drops invalid UTF-8.
func clip(s string, n int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func clipAll(s []string, n int) []string {
	for i := range s {
		s[i] = clip(s[i], n)
	}
	return s
}

// Encode writes t as its trace file. A trace over MaxTraceBytes loses
// steps from the end of its backward and then its forward walk and is
// marked capped, so the file always fits and never pretends to be
// whole.
func (t *Trace) Encode() ([]byte, error) {
	t.clamp()
	for {
		data, err := json.MarshalIndent(t, "", "  ")
		if err != nil || len(data) <= MaxTraceBytes {
			return data, err
		}
		t.Capped = true
		switch {
		case len(t.Backward) > 0:
			t.Backward = t.Backward[:len(t.Backward)/2]
		case len(t.Forward) > 1:
			t.Forward = t.Forward[:len(t.Forward)/2]
			if t.MainIndex >= len(t.Forward) {
				t.MainIndex = -1
			}
		default:
			t.Events, t.Held, t.Listeners, t.Widgets, t.Rings = nil, nil, nil, nil, nil
			if data, err = json.MarshalIndent(t, "", "  "); err != nil || len(data) <= MaxTraceBytes {
				return data, err
			}
			return nil, ErrTraceTooBig
		}
	}
}

// Stops counts the distinct stops of the forward walk, the document's
// own stop left out.
func (t *Trace) Stops() int {
	seen := map[int]bool{}
	n := 0
	for _, s := range t.Forward {
		if s.IsBody {
			continue
		}
		if s.BackendNodeID == 0 || !seen[s.BackendNodeID] {
			n++
		}
		seen[s.BackendNodeID] = true
	}
	return n
}

// Wrapped reports whether the forward walk came back round to its first
// stop through the document's own stop, as every page without a trap
// does.
func (t *Trace) Wrapped() bool {
	first := -1
	body := false
	for _, s := range t.Forward {
		switch {
		case s.IsBody:
			body = first >= 0
		case first < 0:
			first = s.BackendNodeID
		case body && s.BackendNodeID == first:
			return true
		}
	}
	return false
}

// FrameSummary describes each cross-origin frame the walk passed, by
// host only: "<host>: <n> stops, trap yes|no". Nothing from inside such
// a frame is recorded, only that focus was in it.
func (t *Trace) FrameSummary() []string {
	type frame struct {
		stops, run, longest int
		last                int
	}
	frames := map[string]*frame{}
	var order []string
	for i, s := range t.Forward {
		if !s.IsFrame || s.FrameHost == "" {
			continue
		}
		f := frames[s.FrameHost]
		if f == nil {
			f = &frame{last: -2}
			frames[s.FrameHost] = f
			order = append(order, s.FrameHost)
		}
		f.stops++
		if f.last == i-1 {
			f.run++
		} else {
			f.run = 1
		}
		f.last = i
		f.longest = max(f.longest, f.run)
	}
	var out []string
	for _, h := range order {
		f := frames[h]
		trap := "no"
		if f.longest >= frameTrapPresses {
			trap = "yes"
		}
		out = append(out, fmt.Sprintf("%s: %d stops, trap %s", h, f.stops, trap))
	}
	return out
}

// tracePath is where a page's trace is saved, relative to the run's
// folder.
func tracePath(p *model.Page) string { return "keyboard/" + p.ID + ".json" }

// tracePage is the page's path as a trace records it: no query or
// fragment. Token-like segments (a password reset link's key)
// are masked the way Echo's messages are.
func tracePage(p *model.Page) string {
	path := p.Path
	if u, err := url.Parse(p.URL); err == nil && u.Path != "" {
		path = u.EscapedPath()
	}
	path, _, _ = strings.Cut(path, "?")
	path, _, _ = strings.Cut(path, "#")
	if path == "" {
		path = "/"
	}
	return echolink.RedactURL(path)
}
