// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/internal/focus"
	"github.com/RhythmShahriar/soundcheck/internal/pii"
	"github.com/RhythmShahriar/soundcheck/model"
)

// The widget test: menus, dialogs and disclosures opened by keyboard.
// On the first page of each URL group and on the pages keyboard.pages
// names, up to keyboard.widgets triggers are taken in document order.
// Each is focused, opened with Enter (Space for a button, ArrowDown for
// a menu, when Enter did nothing), looked into (did a dialog take
// focus, does a modal keep Tab inside), closed with Escape, its close
// control or its trigger; where focus went is written down. The
// classifier (classify.go) judges the results.
//
// A press never reaches a link to another page, a submit control or
// anything that looks destructive (destructiveWidget); the safety
// envelope holds any write a press asks for all the same.

// widgetWait is how long a widget gets to open or close after a press.
const widgetWait = 500 * time.Millisecond

// destructiveWidget matches triggers and close controls the walk never
// presses: interact's list (checks/interact) with pay and buy as words
// and the same words in German, French, Spanish, Italian and Dutch. It
// is matched against the element's text, aria-label, id, class, href,
// title, value and name.
var destructiveWidget = regexp.MustCompile(`(?i)delete|remove|unsubscribe|log ?out|log-out|logoff|sign ?out|sign-out|cancel|deactivate|trash|destroy|erase|purge|revoke|wipe|uninstall|disconnect|unlink|empty[ _-]?cart|clear[ _-]?cart|reset[ _-]?password|lost[ _-]?password|close[ _-]?account|add[ _-]?to[ _-]?cart|place[ _-]?order|buy[ _-]?now|pay[ _-]?now|checkout|purchase|_wpnonce|/wp-admin|[?&]action=|\bpay\b|\bbuy\b|` +
	`löschen|entfernen|abmelden|ausloggen|kündigen|stornieren|bestellen|kaufen|bezahlen|zur kasse|warenkorb leeren|` +
	`supprimer|retirer|d[ée]conne[xc]|désabonner|annuler|commander|acheter|payer|vider le panier|` +
	`eliminar|borrar|quitar|cerrar sesión|darse de baja|cancelar|comprar|pagar|vaciar carrito|` +
	`elimina|rimuovi|esci|disiscriviti|annulla|acquista|paga|ordina|svuota carrello|` +
	`verwijder|uitloggen|afmelden|annuleren|bestel|koop|betalen|afrekenen|winkelwagen legen`)

// widgetGroups remembers the URL groups whose first page had its
// widgets tested, across the run's pages.
type widgetGroups struct {
	mu   sync.Mutex
	seen map[string]bool
	// first is, by URL group, the planned page that comes first by
	// path (plan); nil until the run's page list is known.
	first map[string]string
}

// plan records the run's planned pages, so each URL group's widgets
// are tested on the group's first page by path, whichever page the
// parallel walks reach first.
func (g *widgetGroups) plan(paths []string) {
	first := map[string]string{}
	for _, p := range paths {
		group := urlGroup(p)
		if f, ok := first[group]; !ok || p < f {
			first[group] = p
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.first = first
}

// claim reports whether the page's widgets are tested: the page matches
// keyboard.pages or is the first page of its URL group. With a plan
// (Module.Plan) that is the group's first planned page by path, so the
// same page is tested from run to run. A group the plan doesn't have,
// or a run with no plan, takes the first page walked: pages are walked
// as the run visits them, several at once, so that is the first to get
// here.
func (g *widgetGroups) claim(path string, pages []string) bool {
	if slices.ContainsFunc(pages, func(p string) bool { return model.Glob(p, path) }) {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	group := urlGroup(path)
	if f, ok := g.first[group]; ok {
		return path == f
	}
	if g.seen[group] {
		return false
	}
	if g.seen == nil {
		g.seen = map[string]bool{}
	}
	g.seen[group] = true
	return true
}

// widgetPrelude defines what the widget probes share, after prelude:
// visible, what a trigger opens (widgetOf) and the tabbable elements
// of a region.
const widgetPrelude = `
  const visible = n => !!n && n.isConnected && (n.checkVisibility ? n.checkVisibility({visibilityProperty: true, opacityProperty: true}) : n.getClientRects().length > 0);
  const isDialog = n => !!n && (n.localName === 'dialog' || /^(alert)?dialog$/.test(n.getAttribute('role') || ''));
  const byID = (d, ref) => { const id = (ref || '').trim().split(/\s+/)[0]; return id ? d.getElementById(id) : null; };
  const dataTarget = el => {
    const v = el.getAttribute('data-target') || el.getAttribute('data-bs-target') || '';
    return /^#[A-Za-z][\w-]*$/.test(v) ? el.ownerDocument.getElementById(v.slice(1)) : null;
  };
  const leaves = el => {
    if (el.localName !== 'a' || !el.hasAttribute('href')) return false;
    const h = el.getAttribute('href').trim();
    if (h === '' || h.startsWith('#') || /^javascript:/i.test(h)) return false;
    try {
      const u = new URL(el.href), here = new URL(el.ownerDocument.URL);
      return !(u.hash && u.origin + u.pathname + u.search === here.origin + here.pathname + here.search);
    } catch (e) { return true; }
  };
  const sublist = li => {
    for (const c of li.children) if (/^(ul|ol)$/.test(c.localName) || c.getAttribute('role') === 'menu') return c;
    for (const c of li.children) if (c.localName === 'div' && c.querySelector('ul, ol, [role="menu"]')) return c;
    return null;
  };
  const tabbable = root => Array.from(root.querySelectorAll('a[href], area[href], button, input, select, textarea, iframe, summary, [tabindex], [contenteditable="true"], [contenteditable=""]'))
    .filter(n => n.tabIndex >= 0 && !n.disabled && !(n.localName === 'input' && n.type === 'hidden') && visible(n) && !n.closest('[inert]'));
  // dialogFor finds the dialog a plain button opens with a script of
  // its own (showModal() in a click handler), which no attribute ties
  // to it: a closed dialog nothing else claims whose id the button's id
  // or onclick names (times-open opens #times), whose label the
  // button's name holds (Show delivery times opens "Delivery times") or
  // that comes right after the button or its parent. A guess that is
  // wrong only costs a press: a dialog that doesn't open is never an
  // issue.
  let closedDialogs = null;
  const dialogFor = el => {
    if (el.localName !== 'button' && el.getAttribute('role') !== 'button') return null;
    if (el.closest('dialog, [role="dialog"], [role="alertdialog"]')) return null;
    const d = el.ownerDocument;
    if (!closedDialogs) {
      const claimed = new Set();
      for (const t of d.querySelectorAll('[aria-controls], [data-target], [data-bs-target]')) {
        const x = byID(d, t.getAttribute('aria-controls')) || dataTarget(t);
        if (x) claimed.add(x);
      }
      closedDialogs = Array.from(d.querySelectorAll('dialog, [role="dialog"], [role="alertdialog"]')).filter(x => !claimed.has(x) && !visible(x));
    }
    if (!closedDialogs.length) return null;
    const norm = s => (s || '').toLowerCase().replace(/[^\p{L}\p{N}]+/gu, ' ').trim();
    const label = x => norm(x.getAttribute('aria-label') || (x.getAttribute('aria-labelledby') || '').split(/\s+/).map(id => { const t = id && d.getElementById(id); return t ? t.textContent : ''; }).join(' '));
    const name = ' ' + norm([el.textContent, el.getAttribute('aria-label')].filter(Boolean).join(' ')) + ' ';
    const click = el.getAttribute('onclick') || '';
    const next = x => x === el.nextElementSibling || (!!el.parentElement && x === el.parentElement.nextElementSibling);
    return closedDialogs.find(x => {
      if (x.id) {
        const id = x.id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        if (el.id !== x.id && new RegExp('(^|[-_])' + id + '([-_]|$)').test(el.id)) return true;
        if (click.includes(x.id)) return true;
      }
      const l = label(x);
      return (l !== '' && name.includes(' ' + l + ' ')) || next(x);
    }) || null;
  };
  const widgetOf = el => {
    const tag = el.localName, role = el.getAttribute('role') || '';
    if (/^(input|textarea|select|option)$/.test(tag) || /^(combobox|tab|option|switch|checkbox|radio)$/.test(role) || el.isContentEditable) return null;
    if (tag === 'button' && el.form && el.type === 'submit') return null;
    if (el.disabled || el.closest('[inert], [aria-hidden="true"]') || el.getAttribute('aria-disabled') === 'true') return null;
    if (tag === 'summary') {
      const d = el.parentElement;
      return d && d.localName === 'details' && !d.open && d.querySelector('summary') === el ? {kind: 'details', target: d, sub: null, li: null} : null;
    }
    if (el.getAttribute('aria-expanded') === 'true') return null;
    const target = byID(el.ownerDocument, el.getAttribute('aria-controls')) || dataTarget(el);
    const pop = (el.getAttribute('aria-haspopup') || '').toLowerCase();
    const nav = !!el.closest('nav, [role="navigation"], [role="menubar"]');
    const li = el.closest('li');
    const first = li && li.firstElementChild && (li.firstElementChild === el || li.firstElementChild.contains(el));
    const sub = nav && first ? sublist(li) : null;
    if (isDialog(target) || pop === 'dialog') return {kind: 'dialog', target, sub: null, li: null};
    if (pop && pop !== 'false' && !/^(listbox|tree|grid)$/.test(pop)) {
      const s = target || sub;
      return {kind: 'menu', target: s, sub: s, li: s === sub ? li : null};
    }
    if (sub && !visible(sub)) return {kind: 'menu', target: sub, sub, li, hover: !el.hasAttribute('aria-expanded')};
    if (el.hasAttribute('aria-expanded')) {
      if (nav && target) return {kind: 'menu', target, sub: target, li: null};
      return {kind: 'disclosure', target, sub: null, li: null};
    }
    if (target && el.hasAttribute('aria-controls') && !visible(target)) return {kind: 'disclosure', target, sub: null, li: null};
    if (!target && !pop && !el.hasAttribute('aria-expanded')) {
      const g = dialogFor(el);
      if (g) return {kind: 'dialog', target: g, sub: null, li: null};
    }
    return null;
  };
`

// widgetsJS returns the expression that finds the page's widget
// triggers in document order: shown, enabled, not in keyboard.ignore
// and opening something widgetOf knows. It looks in the page's own
// document only, never into frames.
func widgetsJS(ignore []string) string {
	if ignore == nil {
		ignore = []string{}
	}
	list, _ := json.Marshal(ignore) // a JSON string list is a JavaScript literal
	return `(() => {` + prelude + widgetPrelude + `
  const ignore = ` + string(list) + `;
  const ignored = el => ignore.some(sel => { try { return !!el.closest(sel); } catch (e) { return false; } });
  return Array.from(document.querySelectorAll('a, button, summary, [role="button"], [role="menuitem"], [aria-expanded], [aria-haspopup], [aria-controls], [data-target], [data-bs-target]'))
    .filter(el => visible(el) && !ignored(el) && widgetOf(el));
})()`
}

// beginWidgetJS is called on a trigger before it is pressed. It keeps
// what the trigger opens in the isolated world (window.__scKbT) for the
// probes that follow and returns what the walk needs to know about it.
// words is matched against destructiveWidget and never stored.
const beginWidgetJS = `function () {
  const el = this;` + prelude + widgetPrelude + `
  const w = widgetOf(el);
  if (!w) return JSON.stringify({kind: ''});
  const dialogs = Array.from(document.querySelectorAll('dialog, [role="dialog"], [role="alertdialog"]')).filter(visible);
  window.__scKbT = {el, kind: w.kind, target: w.target, sub: w.sub, li: w.li, before: dialogs, dialog: null, last: null};
  const role = el.getAttribute('role') || '';
  const words = [el.textContent, el.getAttribute('aria-label'), el.id, el.getAttribute('class'), el.getAttribute('href'),
    el.getAttribute('title'), el.getAttribute('value'), el.getAttribute('name')].filter(Boolean).join(' ').replace(/\s+/g, ' ').slice(0, 500);
  return JSON.stringify({kind: w.kind, tag: el.localName, link: leaves(el),
    button: el.localName === 'button' || el.localName === 'summary' || role === 'button',
    hover: !!w.hover, target: w.target ? path(w.target) : null, submenu: !!w.sub, words});
}`

// openJS reports whether what the trigger opens is open: a dialog that
// shows (the one it controls, or one that wasn't there before), the
// details element open, or the trigger's aria-expanded true and its
// submenu or panel shown. For a dialog it also says whether it is modal
// and how many tabbable elements it has.
const openJS = `(() => {` + prelude + widgetPrelude + `
  const T = window.__scKbT;
  if (!T) return JSON.stringify({open: false});
  let open = false, modal = false, n = 0;
  if (T.kind === 'dialog') {
    let d = isDialog(T.target) && visible(T.target) ? T.target : null;
    if (!d) d = Array.from(document.querySelectorAll('dialog, [role="dialog"], [role="alertdialog"]')).find(x => visible(x) && !T.before.includes(x)) || null;
    if (d) {
      T.dialog = d;
      open = true;
      modal = (d.localName === 'dialog' && d.matches(':modal')) || d.getAttribute('aria-modal') === 'true';
      n = tabbable(d).length;
    }
  } else if (T.kind === 'details') {
    open = !!T.target.open;
  } else if (T.sub) {
    open = visible(T.sub);
  } else if (T.target) {
    open = visible(T.target) || T.el.getAttribute('aria-expanded') === 'true';
  } else {
    open = T.el.getAttribute('aria-expanded') === 'true';
  }
  return JSON.stringify({open, modal, n});
})()`

// insideJS is called on the focused element: is it inside the dialog
// the trigger opened, or with sub set inside the submenu.
func insideJS(sub bool) string {
	which := "T.dialog"
	if sub {
		which = "T.sub"
	}
	return `function () {` + prelude + `
  const T = window.__scKbT, d = T && ` + which + `;
  if (T) T.last = this;
  return String(!!d && within(d, this));
}`
}

// lastGoneJS reports whether the element focused last inside the
// widget (insideJS keeps it) has left the document.
const lastGoneJS = `(() => { const T = window.__scKbT; return String(!!(T && T.last && !T.last.isConnected)); })()`

// hiddenJS is called on the focused element: can a visitor see it.
const hiddenJS = `function () {` + prelude + widgetPrelude + `
  return String(!visible(this));
}`

// closeControlJS finds the dialog's close control: a shown button or
// in-page link inside it whose label, text, title or class says close
// or dismiss (or is a cross).
const closeControlJS = `(() => {` + prelude + widgetPrelude + `
  const T = window.__scKbT, d = T && T.dialog;
  if (!d || !visible(d)) return [];
  return Array.from(d.querySelectorAll('button, [role="button"], a[href^="#"], a:not([href])')).filter(b => visible(b) &&
    /close|dismiss|schlie(ß|ss)en|fermer|cerrar|chiudi|sluit|^\s*[×✕✖x]\s*$/i.test([b.getAttribute('aria-label'), b.textContent, b.getAttribute('title'), b.getAttribute('class')].filter(Boolean).join(' '))).slice(0, 1);
})()`

// liJS returns the list item of a nav submenu trigger, which hover is
// forced on.
const liJS = `(() => { const T = window.__scKbT; return T && T.li ? [T.li] : []; })()`

// subShownJS reports whether the submenu shows.
const subShownJS = `(() => {` + widgetPrelude + `
  const T = window.__scKbT;
  return String(!!(T && T.sub && visible(T.sub)));
})()`

// trigger is what beginWidgetJS says of a trigger.
type trigger struct {
	Kind    string          `json:"kind"`
	Tag     string          `json:"tag"`
	Link    bool            `json:"link"`
	Button  bool            `json:"button"`
	Hover   bool            `json:"hover"`
	Target  json.RawMessage `json:"target"`
	Submenu bool            `json:"submenu"`
	Words   string          `json:"words"`
}

type openState struct {
	Open  bool `json:"open"`
	Modal bool `json:"modal"`
	N     int  `json:"n"`
}

// widgets tests the page's widgets, up to the walk's budget. A press
// that takes the page away or a widget left open costs a reload; with
// none left the test stops there and says so in the trace's events.
func (w *walker) widgets(ctx context.Context, t *Trace) error {
	if !w.widgetsOn || w.budget <= 0 {
		return nil
	}
	tested := map[string]bool{}
	for w.budget > 0 {
		ids, err := w.reader.Nodes(ctx, widgetsJS(w.set.Ignore))
		if err != nil {
			return fmt.Errorf("widgets: %w", err)
		}
		reset := false
		for _, id := range ids {
			if w.budget <= 0 || ctx.Err() != nil {
				break
			}
			r, ok, err := w.widget(ctx, id, tested)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			w.budget--
			t.Widgets = append(t.Widgets, r)
			if r.Left || !r.Opened || r.Closed != "" {
				if r.Left {
					reset = true
					break
				}
				continue
			}
			// Left open: the next widget would be tested under it.
			reset = true
			break
		}
		if !reset {
			return ctx.Err()
		}
		if w.reloads >= maxReloads {
			w.note("widgets stopped: no reload left to reset the page")
			return nil
		}
		if err := w.reload(ctx); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// note adds an event to the trace's list.
func (w *walker) note(msg string) {
	w.guard.mu.Lock()
	w.guard.event(msg)
	w.guard.mu.Unlock()
}

// widget tests one trigger. ok is false for a trigger that isn't
// tested: tested already (by selector), destructive or a link to
// another page that opens nothing on focus.
func (w *walker) widget(ctx context.Context, id int, tested map[string]bool) (r WidgetResult, ok bool, err error) {
	raw, err := w.reader.CallOn(ctx, beginWidgetJS, id)
	if err != nil {
		return r, false, nil // gone since the page was searched
	}
	var tr trigger
	if err := json.Unmarshal([]byte(raw), &tr); err != nil {
		return r, false, fmt.Errorf("widgets: %w", err)
	}
	stop, _, err := w.stopOf(ctx, id)
	if err != nil {
		return r, false, nil
	}
	if tr.Kind == "" || tested[stop.Selector] || destructiveWidget.MatchString(tr.Words) {
		return r, false, nil
	}
	if tr.Link && tr.Kind != WidgetMenu {
		return r, false, nil // a link to another page is never pressed
	}
	if tr.Hover {
		// A nested list revealed by hover alone is a submenu; one that
		// hover doesn't show either is something else (a mobile menu).
		shows, err := w.hoverShows(ctx)
		if err != nil || !shows {
			return r, false, nil
		}
	}
	tested[stop.Selector] = true
	r = WidgetResult{Kind: tr.Kind, Trigger: stop.Selector, Tag: tr.Tag, Name: stop.Name, Link: tr.Link,
		Submenu: tr.Submenu, Target: selectorOf(tr.Target)}
	if err := w.focusNode(ctx, id); err != nil {
		return r, true, nil
	}
	keys := []focus.Key{focus.Enter}
	switch {
	case tr.Link:
		keys = []focus.Key{focus.Tab}
	case tr.Button && tr.Kind == WidgetMenu:
		keys = []focus.Key{focus.Enter, focus.Space, focus.ArrowDown}
	case tr.Button:
		keys = []focus.Key{focus.Enter, focus.Space}
	case tr.Kind == WidgetMenu:
		keys = []focus.Key{focus.Enter, focus.ArrowDown}
	}
	var st openState
	for _, k := range keys {
		if k != keys[0] {
			// Focus back on the trigger: a press that did nothing may
			// still have moved it.
			if err := w.focusNode(ctx, id); err != nil {
				return r, true, nil
			}
		}
		r.Keys = append(r.Keys, string(k))
		if k == focus.Tab {
			st, err = w.tabInto(ctx)
		} else {
			st, err = w.pressAndWait(ctx, k, true)
		}
		if err != nil {
			return r, true, err
		}
		if w.left() {
			return w.leftPage(ctx, r, k)
		}
		if st.Open {
			r.Opened, r.OpenedBy = true, string(k)
			break
		}
	}
	if !r.Opened {
		return r, true, nil
	}
	r.Modal = st.Modal
	return w.inspectOpen(ctx, r, id, st)
}

// inspectOpen looks into an open widget and closes it: where focus
// is, whether a modal keeps Tab inside, Escape, the close control and
// the trigger again. It records where focus went once it closed.
func (w *walker) inspectOpen(ctx context.Context, r WidgetResult, id int, st openState) (WidgetResult, bool, error) {
	if r.Kind == WidgetDialog {
		s, inside, err := w.reader.ActiveWith(ctx, insideJS(false))
		if err != nil {
			return r, true, err
		}
		r.FocusInside = !s.IsBody && inside == "true"
		if r.Modal && r.FocusInside {
			for range st.N + 2 {
				if err := focus.Press(ctx, w.tab, focus.Tab); err != nil {
					return r, true, err
				}
				t, in, err := w.reader.ActiveWith(ctx, insideJS(false))
				if err != nil {
					return r, true, err
				}
				if !t.IsBody && in != "true" {
					r.Leaked = t.Selector
					break
				}
			}
			if r.Leaked != "" {
				// Escape is pressed from inside the dialog, where focus
				// was when it opened, not from the page behind it.
				_ = w.focusNode(ctx, s.BackendNodeID)
			}
		}
	}
	if r.OpenedBy == string(focus.Tab) {
		// A submenu that shows on focus closes when focus moves on: no
		// Escape test, nothing to put back.
		return r, true, nil
	}
	// Focus somewhere inside the widget, as a visitor's would be when
	// they close it: the probe keeps the element to tell later whether
	// closing removed it.
	if _, _, err := w.reader.ActiveWith(ctx, insideJS(r.Kind != WidgetDialog)); err != nil {
		return r, true, err
	}
	if r.Kind == WidgetDialog || r.Kind == WidgetMenu {
		r.EscapeTried = true
		st, err := w.pressAndWait(ctx, focus.Escape, false)
		if err != nil {
			return r, true, err
		}
		if w.left() {
			return w.leftPage(ctx, r, focus.Escape)
		}
		if !st.Open {
			r.EscapeClosed, r.Closed = true, ClosedEscape
			return w.focusAfter(ctx, r)
		}
	}
	if r.Kind == WidgetDialog {
		ids, err := w.reader.Nodes(ctx, closeControlJS)
		if err != nil {
			return r, true, err
		}
		if len(ids) > 0 {
			if words, err := w.reader.CallOn(ctx, wordsJS, ids[0]); err == nil && !destructiveWidget.MatchString(words) {
				if err := w.focusNode(ctx, ids[0]); err == nil {
					if _, _, err := w.reader.ActiveWith(ctx, insideJS(false)); err != nil {
						return r, true, err
					}
					st, err := w.pressAndWait(ctx, focus.Enter, false)
					if err != nil {
						return r, true, err
					}
					if w.left() {
						return w.leftPage(ctx, r, focus.Enter)
					}
					if !st.Open {
						r.Closed = ClosedClose
						return w.focusAfter(ctx, r)
					}
				}
			}
		}
	}
	if r.OpenedBy == string(focus.ArrowDown) {
		return r, true, nil // ArrowDown opens a menu; it doesn't close one
	}
	if err := w.focusNode(ctx, id); err != nil {
		return r, true, nil
	}
	st, err := w.pressAndWait(ctx, focus.Key(r.OpenedBy), false)
	if err != nil {
		return r, true, err
	}
	if w.left() {
		return w.leftPage(ctx, r, focus.Key(r.OpenedBy))
	}
	if !st.Open {
		r.Closed = ClosedToggle
		return w.focusAfter(ctx, r)
	}
	return r, true, nil
}

// wordsJS gives an element's words for destructiveWidget, like
// beginWidgetJS does for a trigger.
const wordsJS = `function () {
  const el = this;
  return [el.textContent, el.getAttribute('aria-label'), el.id, el.getAttribute('class'), el.getAttribute('href'),
    el.getAttribute('title'), el.getAttribute('value'), el.getAttribute('name')].filter(Boolean).join(' ').replace(/\s+/g, ' ').slice(0, 500);
}`

// focusAfter records where focus is once the widget closed: on the
// document (body), the document because the focused element went with
// the widget (removed) or on an element a visitor can't see (hidden).
func (w *walker) focusAfter(ctx context.Context, r WidgetResult) (WidgetResult, bool, error) {
	s, hidden, err := w.reader.ActiveWith(ctx, hiddenJS)
	if err != nil {
		return r, true, err
	}
	switch {
	case s.IsBody:
		r.FocusAfter = FocusBody
		if gone, err := w.reader.Eval(ctx, lastGoneJS); err == nil && gone == "true" {
			r.FocusAfter = FocusRemoved
		}
	case !s.Connected:
		r.FocusAfter = FocusRemoved
	case hidden == "true":
		r.FocusAfter = FocusHidden
	}
	return r, true, nil
}

// leftPage records a press that took the page away: the widget is not
// tested further and the run is told.
func (w *walker) leftPage(_ context.Context, r WidgetResult, k focus.Key) (WidgetResult, bool, error) {
	r.Left = true
	name := r.Name
	if name == "" {
		name = r.Tag
	}
	w.cc.Warn(fmt.Sprintf("keyboard: %s: %s on %q left the page; skipped the rest of that widget", w.page.Path, k, name))
	return r, true, nil
}

// pressAndWait presses k and waits up to widgetWait for the widget to
// be open (open set) or closed (open unset), then returns its state.
func (w *walker) pressAndWait(ctx context.Context, k focus.Key, open bool) (openState, error) {
	if err := focus.Press(ctx, w.tab, k); err != nil {
		return openState{}, err
	}
	return w.waitOpen(ctx, open)
}

func (w *walker) waitOpen(ctx context.Context, open bool) (openState, error) {
	deadline := time.Now().Add(widgetWait)
	for {
		if err := w.navigations(ctx); err != nil {
			return openState{}, err
		}
		var st openState
		if w.left() {
			return st, nil
		}
		raw, err := w.reader.Eval(ctx, openJS)
		if err != nil {
			return st, err
		}
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			return st, fmt.Errorf("widgets: %w", err)
		}
		if st.Open == open || time.Now().After(deadline) {
			return st, nil
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// tabInto tries a link trigger's submenu by Tab alone: focused, does
// the submenu show; does the next Tab land in it.
func (w *walker) tabInto(ctx context.Context) (openState, error) {
	if err := focus.Press(ctx, w.tab, focus.Tab); err != nil {
		return openState{}, err
	}
	st, err := w.waitOpen(ctx, true)
	if err != nil || !st.Open {
		return st, err
	}
	s, inside, err := w.reader.ActiveWith(ctx, insideJS(true))
	if err != nil {
		return st, err
	}
	st.Open = !s.IsBody && inside == "true"
	return st, nil
}

// hoverShows reports whether forcing :hover on the trigger's list item
// shows its submenu. The hover is taken off again.
func (w *walker) hoverShows(ctx context.Context) (bool, error) {
	ids, err := w.reader.Nodes(ctx, liJS)
	if err != nil || len(ids) == 0 {
		return false, err
	}
	p := w.tab.Context(ctx)
	if _, err := (proto.DOMGetDocument{Depth: new(int)}).Call(p); err != nil {
		return false, err
	}
	pushed, err := proto.DOMPushNodesByBackendIDsToFrontend{BackendNodeIDs: []proto.DOMBackendNodeID{proto.DOMBackendNodeID(ids[0])}}.Call(p)
	if err != nil || len(pushed.NodeIDs) == 0 {
		return false, err
	}
	node := pushed.NodeIDs[0]
	if err := (proto.CSSForcePseudoState{NodeID: node, ForcedPseudoClasses: []string{"hover"}}).Call(p); err != nil {
		return false, err
	}
	shown, err := w.reader.Eval(ctx, subShownJS)
	_ = proto.CSSForcePseudoState{NodeID: node, ForcedPseudoClasses: []string{}}.Call(p)
	return shown == "true", err
}

// focusNode gives the element focus through the protocol.
func (w *walker) focusNode(ctx context.Context, id int) error {
	return proto.DOMFocus{BackendNodeID: proto.DOMBackendNodeID(id)}.Call(w.tab.Context(ctx))
}

// stopOf describes an element the way a focus stop is described: its
// selector and redacted name.
func (w *walker) stopOf(ctx context.Context, id int) (focus.Stop, string, error) {
	raw, err := w.reader.CallOn(ctx, `function () {`+prelude+` return JSON.stringify(path(this)); }`, id)
	if err != nil {
		return focus.Stop{}, "", err
	}
	s := focus.Stop{Selector: selectorOf(json.RawMessage(raw)), BackendNodeID: id}
	if s.Selector == "" {
		return s, "", fmt.Errorf("no selector")
	}
	s.Role, s.Name, s.NameLen = w.reader.Name(ctx, id)
	return s, raw, nil
}

// widgetName names a widget's trigger for an issue's Detail: its
// redacted name in quotes, or its tag.
func widgetName(r WidgetResult) string {
	if name := clip(pii.Redact(r.Name), maxName); name != "" {
		return fmt.Sprintf("%q", name)
	}
	return orUnknown(r.Tag)
}

// widgetWord is what a widget is called in an issue's Detail.
func widgetWord(kind string) string {
	if kind == WidgetDialog {
		return "dialog"
	}
	return "menu"
}

// menuKeys names the keys a submenu was tried with, for an issue's
// Detail.
func menuKeys(r WidgetResult) string {
	if r.Link {
		return "Tab"
	}
	return "Enter, Space or ArrowDown"
}
