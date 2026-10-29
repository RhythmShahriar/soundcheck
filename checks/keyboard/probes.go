// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"encoding/json"
	"strconv"
)

// The walk's probes. They run in focus.Reader's isolated world, where
// the page's own scripts can't see or change them and the DOM
// prototypes are the browser's own (security X2). They read layout and
// structure only: never a field's value or a control's text, which
// stays with the page (security M1). Each is a Go constant, so nothing
// a page serves can become probe code (security X3).

// prelude defines what the probes share: up climbs from an element to
// its parent through shadow roots and same-origin frames, within says
// whether a contains b across those, path is the element's path for
// a11y.Selector and style reads its computed style in its own window.
const prelude = `
  const up = n => {
    if (n.parentElement) return n.parentElement;
    const root = n.parentNode;
    if (root && root.nodeType === 11 && root.host) return root.host;
    const view = n.ownerDocument && n.ownerDocument.defaultView;
    try { return view && view.frameElement; } catch (e) { return null; }
  };
  const within = (a, b) => { for (let n = b; n; n = up(n)) if (n === a) return true; return false; };
  const path = el => {
    const trees = [];
    let tree = [];
    for (let n = el; n && n.nodeType === 1;) {
      const p = n.parentNode;
      const same = p ? Array.from(p.children || []).filter(c => c.localName === n.localName) : [];
      tree.unshift({tag: n.localName, id: n.id || '', name: n.getAttribute('name') || '', nth: same.length > 1 ? same.indexOf(n) + 1 : 0});
      const next = up(n);
      if (next && next !== n.parentElement) { trees.unshift(tree); tree = []; }
      n = next;
    }
    trees.unshift(tree);
    return trees;
  };
  const style = n => n.ownerDocument.defaultView.getComputedStyle(n);
`

// startJS hides nothing and changes nothing a visitor would see: it
// moves focus to the document and scrolls to the top, so the first Tab
// lands on the page's first stop. Giving the body tabindex=-1 for the
// moment it is focused resets Chrome's sequential focus starting point,
// which a plain blur leaves at the element that had focus. It returns
// performance.now(), the time since the page loaded.
const startJS = `(() => {
  const b = document.body;
  if (document.activeElement && document.activeElement !== b) document.activeElement.blur();
  if (b) {
    const had = b.hasAttribute('tabindex');
    if (!had) b.setAttribute('tabindex', '-1');
    b.focus({preventScroll: true});
    b.blur();
    if (!had) b.removeAttribute('tabindex');
  }
  scrollTo(0, 0);
  return String(performance.now());
})()`

// guardJS installs the walk's navigation guard: while __scKbNav.on is
// set, a navigation the page starts (a focus handler setting location,
// a form a script submits) is canceled through the Navigation API and
// logged, so a Tab press can't take the walk off the page. Changes of
// the fragment alone (a skip link) go ahead. A navigation the browser
// won't let be canceled is logged all the same.
const guardJS = `(() => {
  if (window.__scKbNav) return 'ok';
  const pressEvents = ` + pressEvents + `;
  const g = window.__scKbNav = {on: false, log: [], els: [], ws: [], pk: [], inFocus: false, input: null, cand: null, act: null, candOff: null};
  // While a control is pressed (cand is set), Space doesn't scroll the
  // page, its default for a focused div: a scroll listener's changes
  // (a reading-progress bar, a sticky header, lazy images) would land
  // in the press's window as if the press made them. This listener on
  // window runs after the control's handlers and the document's.
  for (const t of ['keydown', 'keypress']) {
    addEventListener(t, e => { if (g.cand && e.isTrusted && e.cancelable && (e.key === ' ' || e.code === 'Space')) e.preventDefault(); });
  }
  if (!window.navigation) return 'no navigation API';
  // Set while the page handles a focus event (a window capture listener
  // runs before the element's own), so a navigation a focus handler
  // starts is told from one a timer starts.
  addEventListener('focus', () => { g.inFocus = true; setTimeout(() => { g.inFocus = false; }, 0); }, true);
  // input is the element a key press or click the walk sends is handled
  // on, set while its handlers run: a navigation one of them starts is
  // told from one a timer starts. cand is the mouse-only control being
  // pressed (see listeners.go) and act is set by candJS's listeners on
  // cand itself, only while the event is at cand or inside it: what
  // changes while it is set (a navigation, the address, the DOM) is the
  // control's own doing. moves counts its navigations and address
  // changes (armMutJS).
  g.moves = 0;
  for (const t of pressEvents) {
    addEventListener(t, e => { if (!e.isTrusted) return; g.input = e.target; setTimeout(() => { g.input = null; }, 0); }, true);
  }
  navigation.addEventListener('navigate', e => {
    // A navigation or address change (pushState, replaceState, a new
    // fragment) is the control's own only when one of its own handlers
    // started it (act, set by candJS) or it came from inside it (a
    // link's click). key marks one another handler started while a
    // press was sent (a page-wide or delegated keydown handler), which
    // listeners.go judges against the same key pressed on the body.
    const src = e.sourceElement;
    const within = n => !!(g.cand && n && n.nodeType === 1 && (g.cand === n || g.cand.contains(n)));
    const cand = !g.inFocus && !!g.cand && (g.act === g.cand || within(src));
    const key = !g.inFocus && !cand && !!g.input;
    if (cand) g.moves++;
    if (!g.on || e.hashChange || e.navigationType === 'reload' || e.navigationType === 'traverse') return;
    let p = '';
    try { p = new URL(e.destination.url).pathname; } catch (x) { p = ''; }
    // The form a script submits may start its navigation after the next
    // key press, so the press is found by the element instead: the last
    // stop inside the element the navigation came from.
    let el = -1;
    if (src) for (let i = g.els.length - 1; i >= 0; i--) { const x = g.els[i]; if (x === src || src.contains(x)) { el = i; break; } }
    g.log.push({form: !!e.formData, what: (e.formData ? 'POST ' : 'GET ') + p, stopped: e.cancelable, at: performance.now(), el, focus: g.inFocus, cand, key});
    if (e.cancelable) e.preventDefault();
  });
  return 'ok';
})()`

// guardOn and guardOff turn the guard on and off; navLogJS returns and
// clears what it logged, as JSON.
const (
	guardOn  = `(() => { if (window.__scKbNav) window.__scKbNav.on = true; return ''; })()`
	guardOff = `(() => { if (window.__scKbNav) window.__scKbNav.on = false; return ''; })()`
	navLogJS = `(() => JSON.stringify(window.__scKbNav ? window.__scKbNav.log.splice(0) : []))()`
	// candJS marks the element it is called on as the control being
	// pressed (guardJS) and listens on it for the events a press sends:
	// a capture listener on the control sets act when an event reaches
	// it and a listener after the control's own clears act again, before
	// the handlers of the page around it (a document-wide keydown
	// handler) run. What changes while act is set is the control's own
	// doing. clearCandJS unmarks it and removes those listeners.
	candJS = `function () {
  const g = window.__scKbNav;
  if (!g) return '';
  if (g.candOff) g.candOff();
  const el = this, types = ` + pressEvents + `;
  const take = () => { const m = window.__scKbM; return m && m.o ? m.o.takeRecords() : []; };
  const enter = e => {
    if (!e.isTrusted) return;
    const m = window.__scKbM;
    if (m && m.sort) m.sort(take(), false);
    g.act = el;
    setTimeout(() => { if (g.act === el) g.act = null; }, 0);
  };
  const leave = e => {
    if (!e.isTrusted || g.act !== el) return;
    const m = window.__scKbM;
    if (m && m.o) m.own += take().length;
    g.act = null;
  };
  for (const t of types) { el.addEventListener(t, enter, true); el.addEventListener(t, leave); }
  g.cand = el;
  g.candOff = () => {
    for (const t of types) { el.removeEventListener(t, enter, true); el.removeEventListener(t, leave); }
    g.candOff = null;
  };
  return '';
}`
	clearCandJS = `(() => {
  const g = window.__scKbNav;
  if (g) { if (g.candOff) g.candOff(); g.cand = null; g.act = null; }
  return '';
})()`
	// pressEvents are the events a key press or a click sends, as a
	// JavaScript list.
	pressEvents = `['keydown', 'keypress', 'keyup', 'mousedown', 'mouseup', 'pointerdown', 'pointerup', 'click']`
	// envLogJS returns and clears the WebSocket hosts the envelope's
	// policy refused and the passkey requests it refused (envelope.go).
	envLogJS = `(() => { const g = window.__scKbNav; return JSON.stringify(g ? {ws: g.ws.splice(0), pk: g.pk.splice(0)} : {ws: [], pk: []}); })()`
	hasMain  = `String(!!document.querySelector('main, [role="main"]'))`
)

// stepResult is what stepJS reports about a focused element.
type stepResult struct {
	Hidden    string          `json:"hidden"`
	Obscured  bool            `json:"obscured"`
	By        json.RawMessage `json:"by"` // the covering element's path, or null
	Ignored   bool            `json:"ignored"`
	InMain    bool            `json:"in_main"`
	SkipID    string          `json:"skip_id"`    // a same-page link's fragment, "" for anything else
	SkipMatch bool            `json:"skip_match"` // its text, label or class says skip, jump, main or content
}

// stepJS returns the probe run on each focused element, which judges
// it once any focus transition has run (at most 300 ms): is it hidden
// (opacity, visibility, clip or offscreen), is it covered whole by a
// fixed or sticky element, does it match keyboard.ignore, is it in the
// main landmark and is it a skip link.
//
// With forward set it also hands the element to the guard (guardJS),
// which then finds the press behind a navigation by its element.
func stepJS(ignore []string, forward bool) string {
	if ignore == nil {
		ignore = []string{}
	}
	list, _ := json.Marshal(ignore) // a JSON string list is a JavaScript literal
	return `async function () {
  const el = this;` + prelude + `
  if (` + strconv.FormatBool(forward) + ` && window.__scKbNav) window.__scKbNav.els.push(el);
  try {
    const anims = el.getAnimations ? el.getAnimations() : [];
    if (anims.length) await Promise.race([Promise.all(anims.map(a => a.finished.catch(() => {}))), new Promise(r => setTimeout(r, 300))]);
  } catch (e) {}
  const out = {hidden: '', obscured: false, by: null, ignored: false, in_main: false, skip_id: '', skip_match: false};
  for (const sel of ` + string(list) + `) {
    for (let n = el; n && !out.ignored; n = up(n)) { try { if (n.matches && n.matches(sel)) out.ignored = true; } catch (e) { break; } }
  }
  for (let n = el; n; n = up(n)) {
    if (n.localName === 'main' || (n.getAttribute && n.getAttribute('role') === 'main')) { out.in_main = true; break; }
  }
  if (el.localName === 'a' && el.href) {
    try {
      const u = new URL(el.href), here = new URL(el.ownerDocument.URL);
      if (u.hash.length > 1 && u.origin + u.pathname + u.search === here.origin + here.pathname + here.search) {
        out.skip_id = decodeURIComponent(u.hash.slice(1));
        out.skip_match = /skip|jump|main|content/i.test((el.textContent || '') + ' ' + (el.getAttribute('aria-label') || '') + ' ' + (el.getAttribute('class') || ''));
      }
    } catch (e) {}
  }
  const win = el.ownerDocument.defaultView, s = style(el);
  let opacity = 1;
  for (let n = el; n; n = up(n)) opacity *= parseFloat(style(n).opacity);
  const r = el.getBoundingClientRect();
  let vis = {l: r.left, t: r.top, r: r.right, b: r.bottom};
  let clipped = r.width <= 1 || r.height <= 1;
  for (let n = el; n && !clipped; n = up(n)) {
    if (n.ownerDocument !== el.ownerDocument) break;
    const ns = style(n);
    const m = /rect\(\s*(-?[\d.]+)px,?\s*(-?[\d.]+)px,?\s*(-?[\d.]+)px,?\s*(-?[\d.]+)px/.exec(ns.clip || '');
    if (m && ns.position === 'absolute' || m && ns.position === 'fixed') {
      if (parseFloat(m[2]) - parseFloat(m[4]) <= 1 || parseFloat(m[3]) - parseFloat(m[1]) <= 1) clipped = true;
    }
    if (/inset\(\s*50%|circle\(\s*0(px)?\s*(at|\))/.test(ns.clipPath || '')) clipped = true;
    if (n !== el && (ns.overflowX !== 'visible' || ns.overflowY !== 'visible')) {
      const nr = n.getBoundingClientRect();
      vis = {l: Math.max(vis.l, nr.left), t: Math.max(vis.t, nr.top), r: Math.min(vis.r, nr.right), b: Math.min(vis.b, nr.bottom)};
      if (vis.r - vis.l <= 1 || vis.b - vis.t <= 1) clipped = true;
    }
  }
  if (opacity < 0.02) out.hidden = 'opacity';
  else if (s.visibility !== 'visible') out.hidden = 'visibility';
  else if (clipped) out.hidden = 'clip';
  else if (r.right <= 0 || r.bottom <= 0 || r.left >= win.innerWidth || r.top >= win.innerHeight) out.hidden = 'offscreen';
  if (out.hidden) return JSON.stringify(out);
  const inset = Math.min(2, r.width / 4, r.height / 4);
  const points = [[(r.left + r.right) / 2, (r.top + r.bottom) / 2], [r.left + inset, r.top + inset], [r.right - inset, r.top + inset],
    [r.left + inset, r.bottom - inset], [r.right - inset, r.bottom - inset]];
  let cover = null;
  for (const [x, y] of points) {
    if (x < 0 || y < 0 || x >= win.innerWidth || y >= win.innerHeight) { cover = null; break; }
    let hit = el.ownerDocument.elementFromPoint(x, y);
    while (hit && hit.shadowRoot) { const d = hit.shadowRoot.elementFromPoint(x, y); if (!d || d === hit) break; hit = d; }
    if (!hit || within(el, hit) || within(hit, el)) { cover = null; break; }
    let fixed = null;
    for (let n = hit; n; n = up(n)) {
      if (within(n, el)) break;
      const p = style(n).position;
      if (p === 'fixed' || p === 'sticky') { fixed = n; break; }
    }
    if (!fixed) { cover = null; break; }
    cover = cover || fixed;
  }
  if (cover) { out.obscured = true; out.by = path(cover); }
  return JSON.stringify(out);
}`
}

// trapResult is what trapJS reports about a suspected trap.
type trapResult struct {
	Set   []json.RawMessage `json:"set"`   // the paths of the set's elements, in document order
	After []json.RawMessage `json:"after"` // the first two tabbable elements after it
	N     int               `json:"n"`     // how many tabbable elements come after it
}

// trapJS is called on the elements of a suspected trap (the arguments).
// It sorts them into document order and counts the tabbable elements
// after the last of them, which the trap keeps out of reach. Elements
// in shadow roots and frames are placed by the shadow host or frame of
// the page's own document that holds them.
const trapJS = `function (...set) {` + prelude + `
  const top = n => { for (let x = n; x; x = up(x)) if (x.getRootNode() === document) return x; return n; };
  const before = (a, b) => a !== b && !!(top(a).compareDocumentPosition(top(b)) & Node.DOCUMENT_POSITION_FOLLOWING);
  const sorted = set.slice().sort((a, b) => before(a, b) ? -1 : before(b, a) ? 1 : 0);
  const last = top(sorted[sorted.length - 1]);
  const tabbable = Array.from(document.querySelectorAll('a[href], area[href], button, input, select, textarea, iframe, summary, [tabindex], [contenteditable="true"], [contenteditable=""]'))
    .filter(n => n.tabIndex >= 0 && !n.disabled && !(n.localName === 'input' && n.type === 'hidden') &&
      (n.checkVisibility ? n.checkVisibility({visibilityProperty: true}) : n.getClientRects().length > 0) &&
      !n.closest('[inert]') && !set.some(s => within(top(s), n) || within(n, s)) &&
      !!(last.compareDocumentPosition(n) & Node.DOCUMENT_POSITION_FOLLOWING) && !last.contains(n));
  return JSON.stringify({set: sorted.map(path), after: tabbable.slice(0, 2).map(path), n: tabbable.length});
}`

// focusJS focuses the element it is called on for the skip link probe
// and returns the page's scroll position.
const focusJS = `function () { this.focus(); return JSON.stringify([scrollX, scrollY]); }`

// scrollJS returns the page's scroll position.
const scrollJS = `JSON.stringify([scrollX, scrollY])`

// targetJS reports whether the document has the skip link's target,
// by id or (as browsers also accept) an anchor's name.
func targetJS(id string) string {
	lit, _ := json.Marshal(id)
	return `String(!!(document.getElementById(` + string(lit) + `) || document.getElementsByName(` + string(lit) + `)[0]))`
}

// afterTargetJS counts the tabbable elements inside the skip link's
// target or after it in document order. With none, the Tab after the
// link can only go back to the top of the page, which says nothing
// about the link.
func afterTargetJS(id string) string {
	lit, _ := json.Marshal(id)
	return `String((() => {
  const t = document.getElementById(` + string(lit) + `) || document.getElementsByName(` + string(lit) + `)[0];
  if (!t) return 0;
  return Array.from(document.querySelectorAll('a[href], area[href], button, input, select, textarea, iframe, summary, [tabindex], [contenteditable="true"], [contenteditable=""]'))
    .filter(n => n.tabIndex >= 0 && !n.disabled && !(n.localName === 'input' && n.type === 'hidden') &&
      (n.checkVisibility ? n.checkVisibility({visibilityProperty: true}) : n.getClientRects().length > 0) &&
      !n.closest('[inert]') && (t.contains(n) || !!(t.compareDocumentPosition(n) & Node.DOCUMENT_POSITION_FOLLOWING))).length;
})())`
}

// nextJS returns the probe run on the stop one Tab after the skip link
// was followed: is it inside the target or after it in document order.
func nextJS(id string) string {
	lit, _ := json.Marshal(id)
	return `function () {
  const t = document.getElementById(` + string(lit) + `) || document.getElementsByName(` + string(lit) + `)[0];
  if (!t) return 'false';
  return String(t.contains(this) || !!(t.compareDocumentPosition(this) & Node.DOCUMENT_POSITION_FOLLOWING));
}`
}
