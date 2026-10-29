// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/internal/focus"
)

// Mouse-only controls. The scan asks Chrome which elements have click,
// mousedown, pointerdown, mouseup or pointerup listeners of their own
// (DOMDebugger.getEventListeners), which no script on the page can
// hide or fake. One that Tab can't reach is not-reachable; a focusable
// one is pressed with Enter and Space and, when neither did anything,
// clicked once with a trusted mouse event: when only the click did
// something it is not-operable.

// mouseEvents are the listener types that make an element a control.
var mouseEvents = []string{"click", "mousedown", "pointerdown", "mouseup", "pointerup"}

// maxCandidates caps the elements whose listeners are read on a page:
// two protocol calls each.
const maxCandidates = 1500

// candidatesJS returns the expression that finds the elements whose
// listeners are read, past the guards that keep false alarms out: shown,
// enabled, at least 4x4 px, taking pointer events, not inert or
// aria-hidden, not natively focusable, not inside a focusable element
// or a label, not in keyboard.ignore and with at most 5 child elements
// (a bigger container's listener is most likely delegated from its
// children). It looks in the page's own document only.
func candidatesJS(ignore []string) string {
	if ignore == nil {
		ignore = []string{}
	}
	list, _ := json.Marshal(ignore) // a JSON string list is a JavaScript literal
	return `(() => {` + prelude + widgetPrelude + `
  const ignore = ` + string(list) + `;
  const ignored = el => ignore.some(sel => { try { return !!el.closest(sel); } catch (e) { return false; } });
  const native = n => (n.localName === 'a' && n.hasAttribute('href')) || /^(button|input|select|textarea|summary|iframe|option|label|details|audio|video|area)$/.test(n.localName) || n.isContentEditable;
  const focusable = n => native(n) || (n.hasAttribute && n.hasAttribute('tabindex') && n.tabIndex >= 0);
  const out = [];
  for (const el of document.querySelectorAll('div, span, li, img, svg, td, a:not([href]), [role]')) {
    if (out.length >= ` + fmt.Sprint(maxCandidates) + `) break;
    if (native(el) || el.ownerSVGElement || el.children.length > 5 || ignored(el) || !visible(el)) continue;
    const r = el.getBoundingClientRect();
    if (r.width < 4 || r.height < 4) continue;
    if (style(el).pointerEvents === 'none' || el.closest('[inert], [aria-hidden="true"], label') || el.getAttribute('aria-disabled') === 'true') continue;
    let inside = false;
    for (let n = el.parentElement; n && !inside; n = n.parentElement) inside = focusable(n);
    if (!inside) out.push(el);
  }
  return out;
})()`
}

// candidateJS is called on an element with listeners: its tag, whether
// Tab can reach it, whether it is a link and its words for
// destructiveWidget (never stored).
const candidateJS = `function () {
  const el = this;` + prelude + `
  const words = [el.textContent, el.getAttribute('aria-label'), el.id, el.getAttribute('class'),
    el.getAttribute('title'), el.getAttribute('name')].filter(Boolean).join(' ').replace(/\s+/g, ' ').slice(0, 500);
  return JSON.stringify({tag: el.localName, focusable: el.hasAttribute('tabindex') && el.tabIndex >= 0,
    link: el.getAttribute('role') === 'link', path: path(el), words});
}`

// centerJS scrolls the element into view and returns its center, when
// a click there lands on it and not on something covering it.
const centerJS = `function () {
  const el = this;` + prelude + `
  el.scrollIntoView({block: 'center', inline: 'nearest'});
  const r = el.getBoundingClientRect();
  const x = r.left + r.width / 2, y = r.top + r.height / 2;
  const hit = document.elementFromPoint(x, y);
  return JSON.stringify({x, y, ok: !!hit && within(el, hit)});
}`

// armMutJS starts counting the page's DOM changes from the isolated
// world, in three kinds. own are the changes the pressed control's own
// handlers make (candJS sets act while they run). foreign are those
// made while the press is sent (armMutJS until pressEndJS) by any other
// handler: a document-wide keydown listener that marks the page as used
// by keyboard, or a delegated one that answers for the control. other
// are the rest: a ticking clock, a carousel, a reply to a poll. A
// change made while the press is sent to a node that also changes
// outside it is the page's background and counts as other. With a
// baseline set (baseJS), a change the same key pressed on the page body
// also made is left out altogether: it is the page's answer to any
// press, not the control's. readMutJS stops and returns the counts,
// whether the address changed by the control's doing (moved, see
// guardJS) or another's (drift) and whether anything scrolled: a scroll
// listener's changes are the scroll's, not the press's.
const (
	armMutJS = `(() => {
  const g = window.__scKbM || (window.__scKbM = {});
  if (g.o) g.o.disconnect();
  g.own = 0; g.other = 0; g.press = true; g.dur = []; g.bg = new Set(); g.href = location.href;
  const nav = window.__scKbNav;
  g.moves = nav ? nav.moves : 0; g.sx = scrollX; g.sy = scrollY; g.scrolled = false;
  if (!g.onScroll) {
    g.onScroll = true;
    addEventListener('scroll', () => { if (g.o) g.scrolled = true; }, true);
  }
  g.sort = (l, own) => {
    for (const r of l) {
      if (own) g.own++;
      else if (!g.press) { g.other++; g.bg.add(r.target); }
      else if (g.dur.length < 1000) g.dur.push({t: r.target, k: r.type + ':' + (r.attributeName || '')});
      else g.other++;
    }
  };
  g.o = new MutationObserver(l => g.sort(l, !!(nav && nav.act)));
  g.o.observe(document, {subtree: true, childList: true, attributes: true, characterData: true});
  return '';
})()`
	pressEndJS = `(() => {
  const g = window.__scKbM, nav = window.__scKbNav;
  if (g && g.o) { g.sort(g.o.takeRecords(), !!(nav && nav.act)); g.press = false; }
  return '';
})()`
	readMutJS = `(() => {
  const g = window.__scKbM;
  if (!g || !g.o) return JSON.stringify({own: 0, other: 0, foreign: 0, moved: false, drift: false, scrolled: false});
  g.press = false;
  g.sort(g.o.takeRecords(), false);
  g.o.disconnect(); g.o = null;
  const base = g.base || [];
  let other = g.other, foreign = 0;
  for (const d of g.dur) {
    if (base.some(b => b.t === d.t && b.k === d.k)) continue;
    if (g.bg.has(d.t)) other++; else foreign++;
  }
  g.last = g.dur;
  const nav = window.__scKbNav, changed = location.href !== g.href;
  const moved = changed && !!nav && typeof nav.moves === 'number' && nav.moves !== g.moves;
  const scrolled = g.scrolled || scrollX !== g.sx || scrollY !== g.sy;
  return JSON.stringify({own: g.own, other, foreign, moved, drift: changed && !moved, scrolled});
})()`
	// settleJS waits two animation frames (at most 100 ms, as a tab in
	// the background may get none), so the scroll events and the scroll
	// listeners' changes of what came before (focusing a control scrolls
	// it into view) land before the next window opens.
	settleJS = `new Promise(r => {
  let done = false;
  const end = () => { if (!done) { done = true; r(''); } };
  requestAnimationFrame(() => requestAnimationFrame(end));
  setTimeout(end, 100);
})`
	// baseJS keeps the changes the last press made while it was sent as
	// the baseline later presses are judged against; unbaseJS drops it.
	baseJS   = `(() => { const g = window.__scKbM; if (g) g.base = g.last || []; return ''; })()`
	unbaseJS = `(() => { const g = window.__scKbM; if (g) g.base = null; return ''; })()`
	// bodyFocusJS takes focus off the page's elements, so a key press goes to
	// the page body.
	bodyFocusJS = `(() => { const a = document.activeElement; if (a && a !== document.body && a.blur) a.blur(); return ''; })()`
)

// mutations is what readMutJS returns.
type mutations struct {
	Own      int  `json:"own"`
	Other    int  `json:"other"`
	Foreign  int  `json:"foreign"`
	Moved    bool `json:"moved"`    // the control changed the address
	Drift    bool `json:"drift"`    // something else changed it
	Scrolled bool `json:"scrolled"` // the page or an element scrolled
}

// effects is what happened in one window (see window).
type effects struct {
	reqs int      // requests the page made
	navs int      // navigations it started
	own  int      // of those, the ones the pressed control started itself
	key  int      // and those another handler of the press started
	to   []string // where those went
	mut  mutations
}

// quiet says nothing happened.
func (e effects) quiet() bool {
	return e.reqs == 0 && e.navs == 0 && e.mut.Own+e.mut.Other+e.mut.Foreign == 0 && !e.mut.Moved && !e.mut.Drift
}

type candidateInfo struct {
	Tag       string          `json:"tag"`
	Focusable bool            `json:"focusable"`
	Link      bool            `json:"link"`
	Path      json.RawMessage `json:"path"`
	Words     string          `json:"words"`
}

// listeners scans the page for mouse-only controls and presses the
// focusable ones, while the budget lasts, on pages whose widgets are
// tested.
func (w *walker) listeners(ctx context.Context, t *Trace) error {
	ids, err := w.reader.Nodes(ctx, candidatesJS(w.set.Ignore))
	if err != nil {
		return fmt.Errorf("listeners: %w", err)
	}
	seen := map[string]bool{}
	for _, list := range [][]Step{t.Forward, t.Backward} {
		for _, s := range list {
			seen[s.Selector] = true
		}
	}
	type found struct {
		id   int
		info candidateInfo
		c    Candidate
	}
	var all []found
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		events, err := w.mouseListeners(ctx, id)
		if err != nil || len(events) == 0 {
			continue
		}
		raw, err := w.reader.CallOn(ctx, candidateJS, id)
		if err != nil {
			continue
		}
		var info candidateInfo
		if json.Unmarshal([]byte(raw), &info) != nil {
			continue
		}
		c := Candidate{Selector: selectorOf(info.Path), Events: events, Tag: info.Tag, Focusable: info.Focusable, Link: info.Link}
		if c.Selector == "" {
			continue
		}
		c.Seen = seen[c.Selector]
		_, c.Name, _ = w.reader.Name(ctx, id)
		all = append(all, found{id, info, c})
		if len(all) >= maxList {
			break
		}
	}
	gone := false
	for i := range all {
		f := &all[i]
		if f.c.Focusable && !gone {
			switch {
			case !w.widgetsOn:
			case destructiveWidget.MatchString(f.info.Words):
				f.c.Skipped = "looks destructive"
			case w.budget <= 0:
				f.c.Skipped = "keyboard.widgets reached"
			default:
				w.budget--
				if err := w.operable(ctx, f.id, &f.c); err != nil {
					return err
				}
				if w.left() {
					// The page went away: the rest can't be pressed
					// on it. It is loaded again for the trace's sake.
					gone = true
					if w.reloads < maxReloads {
						if err := w.reload(ctx); err != nil {
							return err
						}
					}
				}
			}
		}
		t.Listeners = append(t.Listeners, f.c)
	}
	return nil
}

// mouseListeners returns the mouse event types the element listens to
// itself, in mouseEvents order.
func (w *walker) mouseListeners(ctx context.Context, id int) ([]string, error) {
	p := w.tab.Context(ctx)
	obj, err := proto.DOMResolveNode{BackendNodeID: proto.DOMBackendNodeID(id)}.Call(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = proto.RuntimeReleaseObject{ObjectID: obj.Object.ObjectID}.Call(p) }()
	res, err := proto.DOMDebuggerGetEventListeners{ObjectID: obj.Object.ObjectID}.Call(p)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ev := range mouseEvents {
		if slices.ContainsFunc(res.Listeners, func(l *proto.DOMDebuggerEventListener) bool { return l.Type == ev }) {
			out = append(out, ev)
		}
	}
	return out, nil
}

// operable presses a focusable candidate: Enter, then Space (not for a
// link), then one mouse click when neither key did anything. Each gets
// 500 ms to show an effect. A press that took the page away ends it.
func (w *walker) operable(ctx context.Context, id int, c *Candidate) error {
	c.Tested = true
	// The guard credits a navigation to the press only when a handler
	// of this control's own events started it.
	if _, err := w.reader.CallOn(ctx, candJS, id); err != nil {
		c.Skipped = "gone"
		return nil
	}
	defer func() { _, _ = w.reader.Eval(ctx, clearCandJS) }()
	keys := []focus.Key{focus.Enter, focus.Space}
	if c.Link {
		keys = keys[:1]
	}
	for _, k := range keys {
		if err := w.focusNode(ctx, id); err != nil {
			c.Skipped = "gone"
			return nil
		}
		press := func() error { return focus.Press(ctx, w.tab, k) }
		eff, err := w.effectOf(ctx, press, &baseline{
			press:   press,
			refocus: func() error { return w.focusNode(ctx, id) },
		})
		if err != nil {
			if errors.Is(err, errGone) {
				c.Skipped = "gone"
				return nil
			}
			return err
		}
		if k == focus.Enter {
			c.Enter = eff
		} else {
			c.Space = eff
		}
		if !noResponse(eff) || w.left() {
			return nil
		}
	}
	raw, err := w.reader.CallOn(ctx, centerJS, id)
	if err != nil {
		c.Skipped = "gone"
		return nil
	}
	var at struct {
		X, Y float64
		OK   bool
	}
	if json.Unmarshal([]byte(raw), &at) != nil || !at.OK {
		c.Skipped = "covered"
		return nil
	}
	c.Click, err = w.effectOf(ctx, func() error { return w.click(ctx, at.X, at.Y) }, nil)
	return err
}

// click sends one trusted left click at x, y in the viewport.
func (w *walker) click(ctx context.Context, x, y float64) error {
	p := w.tab.Context(ctx)
	if err := (proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseMoved, X: x, Y: y}).Call(p); err != nil {
		return err
	}
	down, up := 1, 0 // the buttons held after each event: the left one, then none
	if err := (proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMousePressed, X: x, Y: y,
		Button: proto.InputMouseButtonLeft, Buttons: &down, ClickCount: 1}).Call(p); err != nil {
		return err
	}
	if err := (proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseReleased, X: x, Y: y,
		Button: proto.InputMouseButtonLeft, Buttons: &up, ClickCount: 1}).Call(p); err != nil {
		return err
	}
	return nil
}

// baseline is how effectOf tells a key press's answer from the page's
// answer to any press of that key: press sends the key, refocus puts
// focus back on the control.
type baseline struct {
	press, refocus func() error
}

// errGone is effectOf's error when the control can't be focused again.
var errGone = errors.New("the control is gone")

// effectOf runs act and says what it did within widgetWait: started a
// navigation or changed the address (navigation), made a request, sent
// or held (request), or changed the DOM (dom); "" for nothing. Only
// what the pressed control's own handlers caused counts as such: a
// navigation or address change they started (guardJS, candJS) or a DOM
// change they made (armMutJS). A request, a DOM change or an address
// change made later (after a reply or a timer) may be the page's own
// background activity: they count only when the page is quiet for as
// long again without a press and, for a key, when the control's second
// press does the same and that key on the page body doesn't, as a page
// may do something once at any time. When not, the effect is unknown,
// as it is for a navigation something else started and for anything
// that came with a scroll: a scroll listener's changes are no answer.
// Navigations logged before act are set aside first.
//
// A DOM change, navigation or address change another handler made
// while the press was sent (a document-wide keydown listener) is no
// answer by itself: many pages mark themselves as used by keyboard on
// any key; some navigate or rewrite the address on it. With base, the
// same key is pressed on the page body first and the control is
// pressed again: what the body press did too is left out and what is
// left is the control's answer, made for it by a delegated handler.
// Without base (a click), the effect is unknown.
func (w *walker) effectOf(ctx context.Context, act func() error, base *baseline) (string, error) {
	e, err := w.window(ctx, act)
	if err != nil {
		return "", err
	}
	if eff, ok := ownEffect(e); ok {
		return eff, nil
	}
	if w.left() {
		// Something other than the control took the page away.
		return PressUnknown, nil
	}
	if e.mut.Foreign > 0 || e.key > 0 || e.mut.Drift && base != nil {
		if base == nil {
			return PressUnknown, nil
		}
		first := e
		var body effects
		if body, e, err = w.again(ctx, act, base); err != nil {
			return "", err
		}
		if w.left() {
			return PressUnknown, nil
		}
		if eff, ok := ownEffect(e); ok {
			return eff, nil
		}
		switch {
		case e.mut.Foreign > 0:
			return PressDOM, nil
		case slices.ContainsFunc(e.to, func(to string) bool { return !slices.Contains(body.to, to) && slices.Contains(first.to, to) }):
			// A delegated handler sent the control somewhere the body
			// press doesn't go, the same place both times: a handler
			// that goes somewhere new on every press answers none.
			return PressNavigation, nil
		}
		// What the body press did too is the page's answer to any press.
		e.navs -= e.key
		if body.mut.Drift {
			e.mut.Drift = false
		}
	}
	if e.mut.Scrolled && !e.quiet() {
		return PressUnknown, nil
	}
	switch {
	case e.reqs == 0 && e.mut.Other == 0 && e.navs == 0 && !e.mut.Drift:
		return "", nil
	case e.navs > 0 && e.reqs == 0 && e.mut.Other == 0 && !e.mut.Drift:
		return PressUnknown, nil
	}
	// Something happened the walk can't tie to the press: the same
	// time with no press says whether the page does it by itself.
	eff := laterEffect(e)
	q, err := w.window(ctx, func() error { return nil })
	if err != nil {
		return "", err
	}
	if w.left() || !q.quiet() || q.mut.Scrolled || eff == "" {
		return PressUnknown, nil
	}
	if base == nil {
		return eff, nil
	}
	// A quiet page may still do something once: the tail of its load
	// (a late favicon or a request the browser makes for it) on a slow
	// machine lands in whatever window is open then. So the key goes
	// to the page body for as long again, then to the control once
	// more: the effect counts only when the control's second press
	// does it too and the body press doesn't.
	body, again, err := w.again(ctx, act, base)
	if err != nil {
		return "", err
	}
	if w.left() {
		return PressUnknown, nil
	}
	if own, ok := ownEffect(again); ok {
		return own, nil
	}
	if shows(body, eff) || !shows(again, eff) || again.mut.Scrolled {
		return PressUnknown, nil
	}
	return eff, nil
}

// laterEffect names what a window shows that came after its press (a
// request, a DOM change or an address change after a reply or a timer),
// "" for none of them.
func laterEffect(e effects) string {
	switch {
	case e.reqs > 0:
		return PressRequest
	case e.mut.Other > 0:
		return PressDOM
	case e.mut.Drift:
		// The address changed later (after a reply or a timer).
		return PressNavigation
	}
	return ""
}

// shows says whether a window shows the later effect named eff.
func shows(e effects, eff string) bool {
	switch eff {
	case PressRequest:
		return e.reqs > 0
	case PressDOM:
		return e.mut.Other > 0
	case PressNavigation:
		return e.mut.Drift
	}
	return false
}

// ownEffect says what a press did when its control's own handlers
// answered it: a navigation or address change, or a DOM change.
func ownEffect(e effects) (string, bool) {
	switch {
	case e.own > 0 || e.mut.Moved:
		return PressNavigation, true
	case e.mut.Own > 0:
		return PressDOM, true
	}
	return "", false
}

// again presses base's key on the page body and keeps what that changed
// as the baseline, then runs act on the control once more, judged
// against it. It returns what the body press and act did.
func (w *walker) again(ctx context.Context, act func() error, base *baseline) (body, e effects, err error) {
	if _, err = w.reader.Eval(ctx, bodyFocusJS); err != nil {
		return
	}
	if body, err = w.window(ctx, base.press); err != nil {
		return
	}
	if w.left() {
		return
	}
	if _, err = w.reader.Eval(ctx, baseJS); err != nil {
		return
	}
	defer func() { _, _ = w.reader.Eval(ctx, unbaseJS) }()
	if base.refocus() != nil {
		err = errGone
		return
	}
	e, err = w.window(ctx, act)
	return
}

// window lets what came before settle, runs act and waits widgetWait,
// then returns the requests the page made, the navigations it started,
// those the pressed control started itself, those another handler of
// the press started and its DOM changes in that time.
func (w *walker) window(ctx context.Context, act func() error) (e effects, err error) {
	if err = w.navigations(ctx); err != nil {
		return
	}
	if _, err = w.reader.Eval(ctx, settleJS); err != nil {
		return
	}
	if _, err = w.reader.Eval(ctx, armMutJS); err != nil {
		return
	}
	g := w.guard
	g.mu.Lock()
	reqs, navs, own, key, to := g.requests, g.navs, g.candNavs, g.keyNavs, len(g.keyTo)
	g.mu.Unlock()
	if err = act(); err != nil {
		return
	}
	if w.onWindow != nil {
		w.windows++
		w.onWindow(ctx, w.windows)
	}
	if _, err = w.reader.Eval(ctx, pressEndJS); err != nil {
		return
	}
	select {
	case <-ctx.Done():
		err = ctx.Err()
		return
	case <-time.After(widgetWait):
	}
	if err = w.navigations(ctx); err != nil {
		return
	}
	g.mu.Lock()
	e.reqs, e.navs, e.own, e.key = g.requests-reqs, g.navs-navs, g.candNavs-own, g.keyNavs-key
	e.to = slices.Clone(g.keyTo[min(to, len(g.keyTo)):])
	g.mu.Unlock()
	raw, err := w.reader.Eval(ctx, readMutJS)
	if err != nil {
		return
	}
	if err = json.Unmarshal([]byte(raw), &e.mut); err != nil {
		err = fmt.Errorf("listeners: %w", err)
	}
	return
}
