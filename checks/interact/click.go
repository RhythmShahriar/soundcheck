// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/capture/intercept"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// destructive matches controls Soundcheck never clicks unless allowlisted:
// anything that deletes, logs out, cancels, empties or buys, links
// carrying a WordPress nonce or an action. It's matched against the
// element's text, aria-label, id, class, href, title, value and name.
var destructive = regexp.MustCompile(`(?i)delete|remove|unsubscribe|log ?out|log-out|logoff|sign ?out|sign-out|cancel|deactivate|trash|destroy|erase|purge|revoke|wipe|uninstall|disconnect|unlink|empty[ _-]?cart|clear[ _-]?cart|reset[ _-]?password|lost[ _-]?password|close[ _-]?account|add[ _-]?to[ _-]?cart|place[ _-]?order|buy[ _-]?now|pay[ _-]?now|checkout|purchase|_wpnonce|/wp-admin|[?&]action=`)

// acts matches controls that act for the visitor (vote, like, follow,
// subscribe, send, request, book, apply, publish, approve). The words
// match whole only, so "Booking policy" and "likely" don't count. They're
// matched against what the control says (its text, aria-label, title,
// value, name and data-action) and not its href, class or id: links like
// /books/ or /request-a-quote/ only navigate. The write a click on such
// a control sends is held anyway (see holdWrites).
var acts = regexp.MustCompile(`(?i)\b(?:up|down)?(?:vote|like|follow|subscribe|send|request|book|apply|publish|approve)(?:s|d|ed)?\b`)

// isDestructive reports whether a control looks like it changes data.
func isDestructive(c candidate) bool {
	return destructive.MatchString(c.Words) || acts.MatchString(c.Says)
}

// Clicks wait this long for a reaction before calling the click dead.
const deadAfter = 1500 * time.Millisecond

const armJS = `() => {
  const s = window.__scClick = window.__scClick || {mut: 0, opened: 0, dialogs: 0};
  if (s.obs) s.obs.disconnect();
  s.mut = 0; s.opened = 0; s.dialogs = 0;
  s.obs = new MutationObserver(l => { s.mut += l.length; });
  s.obs.observe(document, {subtree: true, childList: true, attributes: true, characterData: true});
  s.href = location.href; s.hash = location.hash; s.x = scrollX; s.y = scrollY;
  s.vis = [...document.querySelectorAll('body *')].filter(e => e.offsetWidth || e.offsetHeight).length;
  return true;
}`

const stateJS = `() => {
  const s = window.__scClick || {};
  const vis = [...document.querySelectorAll('body *')].filter(e => e.offsetWidth || e.offsetHeight).length;
  return {armed: !!s.obs, mut: s.mut || 0, opened: s.opened || 0, dialogs: s.dialogs || 0,
    moved: location.href.split('#')[0] !== (s.href || '').split('#')[0], hash: location.hash !== s.hash,
    scrolled: scrollX !== s.x || scrollY !== s.y, vis: vis !== s.vis};
}`

type pageState struct {
	Armed    bool `json:"armed"`
	Mut      int  `json:"mut"`
	Opened   int  `json:"opened"`
	Dialogs  int  `json:"dialogs"`
	Moved    bool `json:"moved"`
	Hash     bool `json:"hash"`
	Scrolled bool `json:"scrolled"`
	Vis      bool `json:"vis"`
}

// reacted reports any visible effect. For links to "#" or
// javascript:void(0), jumping to the top of the page isn't one.
func (s pageState) reacted(voidLink bool) bool {
	return !s.Armed || s.Mut > 0 || s.Opened > 0 || s.Dialogs > 0 || s.Moved || s.Vis ||
		(!voidLink && (s.Hash || s.Scrolled))
}

// clickBudget bounds the time level 2 spends on one page, so a slow page
// can't hold a tab for minutes.
const clickBudget = 45 * time.Second

// clicks runs level 2 and returns the selectors it clicked.
func (m *Module) clicks(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page, f *facts, set *issueSet) map[string]bool {
	clicked := map[string]bool{}
	pageURL := orDefault(p.FinalURL, p.URL)
	base, _ := url.Parse(pageURL)
	maxClicks := cc.Config.Interactions.MaxClicks
	if maxClicks <= 0 {
		maxClicks = 15
	}
	cands := slices.Clone(f.Cands)
	slices.SortStableFunc(cands, func(a, b candidate) int { return a.Prio - b.Prio })
	if _, err := tab.Timeout(10 * time.Second).Eval(guardJS); err != nil {
		return clicked
	}
	g, err := holdWrites(tab)
	if err != nil {
		// Without the guard a click could post, vote or send for real.
		m.warnOnce(cc, "hold-writes", "interact: skipped the click level, because requests a click sends couldn't be held: "+err.Error())
		return clicked
	}
	defer g.release()
	w := watch(ctx, tab)
	defer w.stop()

	record := func(c candidate, outcome, detail string) {
		if len(p.Interact) < 100 {
			p.Interact = append(p.Interact, model.Interaction{Kind: "click", Selector: c.Sel, Label: c.Label, Outcome: outcome, Detail: detail})
		}
	}
	deadline := time.Now().Add(clickBudget)
	n, left := 0, 0
	for i, c := range cands {
		if ctx.Err() != nil || n >= maxClicks {
			break
		}
		if time.Now().After(deadline) {
			left = len(cands) - i
			break
		}
		switch {
		case c.Nav:
			record(c, "skipped", "navigates away")
			continue
		case !c.Allowed && isDestructive(c):
			record(c, "skipped", "looks destructive")
			continue
		}
		e, err := m.clickOne(ctx, tab, w, g, c, pageURL)
		if errors.Is(err, errCovered) {
			// Something (an open menu or modal) covers it: start fresh once.
			if reload(ctx, tab, pageURL) == nil {
				e, err = m.clickOne(ctx, tab, w, g, c, pageURL)
			}
		}
		var gone *notThere
		switch {
		case errors.As(err, &gone):
			continue // hidden, off screen or gone: not something a visitor can click
		case err != nil:
			record(c, "skipped", err.Error())
			continue
		}
		n++
		clicked[c.Sel] = true
		name := orDefault(c.Label, "(no label)")
		switch {
		case len(e.errors) > 0:
			record(c, "error", e.errors[0])
			set.add(model.Issue{Check: "interact/click-error", Level: model.Error,
				Title: "Clicking " + name + " throws a JS error", Detail: e.errors[0],
				Fix: "Click it with the browser console open to see the stack trace."}, c.Sel)
		case len(e.failed) > 0:
			d := describe(e.failed[0], base)
			record(c, "error", d)
			set.add(model.Issue{Check: "interact/click-request", Level: model.Error,
				Title: "Clicking " + name + " makes a request that fails", Detail: d,
				Fix: "Check the server log for the endpoint at the time of the click."}, c.Sel)
		case e.dead:
			record(c, "dead", "no DOM change, request or navigation within 1.5 s")
			set.add(deadIssue(c.Label, c.Tag == "a", "Clicking it changed nothing on the page, made no request and didn't navigate."), c.Sel)
		case len(e.held) > 0:
			record(c, "ok", wouldSend(e.held, base))
		default:
			record(c, "ok", c.Kind)
		}
		if e.left {
			if err := reload(ctx, tab, pageURL); err != nil {
				break
			}
		}
	}
	if left > 0 {
		cc.Warn(fmt.Sprintf("interact %s: stopped clicking after %s with %d controls left (the page is slow)", p.Path, clickBudget, left))
	}
	// Leave the page as it was loaded, while writes are still held: a
	// click may have set a timer that posts later, which would otherwise
	// go out once the hold ends. The submit level and the hooks that run
	// after this one get a fresh page too.
	if n > 0 {
		_ = reload(ctx, tab, pageURL)
	}
	return clicked
}

// holdWrites keeps the requests clicks make from changing anything:
// until the guard is released, every request on tab other than GET and
// HEAD (fetch, XHR, sendBeacon, a form post) fails as blocked by the
// client and never reaches the server. Clicks only explore; only the
// submit and flows levels may send data. They don't run under this
// guard.
//
// The guard also records what it held, while the request is paused and
// before Chrome is told to fail it. A form post can only take the page
// away once it has failed, so a click that made the page leave this way
// always finds its request recorded, however late the network events
// that also tell of it reach the watcher.
func holdWrites(tab *rod.Page) (*guard, error) {
	g := &guard{}
	release, err := intercept.Add(tab, func(r *intercept.Request) {
		if !isWrite(r.Method) {
			return
		}
		if d := time.Duration(latePause.Load()); d > 0 {
			time.Sleep(d)
		}
		r.Block()
		u := ""
		if r.URL != nil {
			u = r.URL.String()
		}
		if signals(r.Type, u) {
			g.mu.Lock()
			g.held = append(g.held, model.Request{URL: u, Method: r.Method, Type: string(r.Type)})
			g.mu.Unlock()
		}
	})
	g.release = release
	return g, err
}

// latePause, set by tests, delays the guard's handling of each write,
// as a busy machine can.
var latePause atomic.Int64

// guard is the click level's hold on writes (see holdWrites).
type guard struct {
	mu      sync.Mutex
	held    []model.Request // since reset
	release func()
}

// reset starts a new window, as watcher.reset does.
func (g *guard) reset() {
	g.mu.Lock()
	g.held = nil
	g.mu.Unlock()
}

// report returns the writes held since reset.
func (g *guard) report() []model.Request {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.held)
}

func isWrite(method string) bool { return method != http.MethodGet && method != http.MethodHead }

// wouldSend describes the requests a click's guard held:
// "would have sent POST /api/vote".
func wouldSend(held []model.Request, base *url.URL) string {
	var parts []string
	for _, q := range held {
		if s := q.Method + " " + shortURL(base, q.URL); !slices.Contains(parts, s) {
			parts = append(parts, s)
		}
	}
	return "would have sent " + strings.Join(parts, ", ")
}

// heldError matches the errors browsers throw for a request that never
// got an answer (fetch's rejection, a blocked request).
var heldError = regexp.MustCompile(`(?i)failed to fetch|networkerror|network error|load failed|err_blocked_by_client`)

var errCovered = errors.New("covered by another element")

// notThere is an element a visitor couldn't click either: hidden, clipped
// by a collapsed parent, off screen or no longer in the page.
type notThere struct{ why string }

func (e *notThere) Error() string { return e.why }

type clickResult struct {
	errors []string
	failed []model.Request
	held   []model.Request // what the guard kept the click from sending, or will
	dead   bool
	left   bool // the page navigated: reload before the next click
}

// prepJS scrolls the element to the middle of the viewport, checks that a
// visitor could click it (visible, not clipped by a collapsed parent, on
// screen, not covered) and hovers it. Everything is done in the page:
// Chrome doesn't render background tabs, so CDP mouse input and
// requestAnimationFrame (which rod's scrolling waits on) stall there,
// while DOM events, layout and hit testing work in every tab.
const prepJS = `(sel) => {
  const el = document.querySelector(sel);
  if (!el) return {state: 'gone'};
  const cs = getComputedStyle(el);
  if (!el.getClientRects().length || cs.display === 'none' || cs.visibility === 'hidden' || cs.pointerEvents === 'none' || cs.opacity === '0' || el.closest('[inert]'))
    return {state: 'hidden'};
  el.scrollIntoView({block: 'center', inline: 'center', behavior: 'instant'});
  const r = el.getBoundingClientRect();
  if (r.width < 1 || r.height < 1) return {state: 'hidden'};
  const x = r.left + r.width / 2, y = r.top + r.height / 2;
  if (x < 0 || y < 0 || x > innerWidth || y > innerHeight) return {state: 'offscreen'};
  for (let n = el.parentElement; n && n !== document.body; n = n.parentElement) {
    const s = getComputedStyle(n);
    if (s.display === 'none' || s.visibility === 'hidden' || s.opacity === '0') return {state: 'hidden'};
    if (s.overflowX !== 'visible' || s.overflowY !== 'visible') {
      const b = n.getBoundingClientRect();
      if (x < b.left || x > b.right || y < b.top || y > b.bottom) return {state: 'hidden'};
    }
  }
  const hit = document.elementFromPoint(x, y);
  if (hit && hit !== el && !el.contains(hit)) return {state: 'covered', by: hit.tagName.toLowerCase() + (hit.id ? '#' + hit.id : '')};
  const o = {bubbles: true, cancelable: true, composed: true, clientX: x, clientY: y, view: window};
  const ptr = {pointerId: 1, pointerType: 'mouse', isPrimary: true};
  el.dispatchEvent(new PointerEvent('pointerover', {...o, ...ptr}));
  el.dispatchEvent(new PointerEvent('pointerenter', {...o, ...ptr, bubbles: false}));
  el.dispatchEvent(new MouseEvent('mouseover', o));
  el.dispatchEvent(new MouseEvent('mouseenter', {...o, bubbles: false}));
  el.dispatchEvent(new PointerEvent('pointermove', {...o, ...ptr}));
  el.dispatchEvent(new MouseEvent('mousemove', o));
  return {state: 'ok', x, y};
}`

// pressJS clicks like a mouse: down, focus, up, click. The click event
// runs the element's default action too (follow a link, toggle details,
// submit a form). It returns the writes the click started while its
// events ran: form posts (a submit event nobody canceled, form.submit()),
// fetch, XHR and sendBeacon calls other than GET and HEAD.
//
// It also keeps the page's form posts in this tab, where the guard holds
// them, from now until the page is reloaded: a form that would post to a
// new window (target="_blank" or a window name that isn't a frame here)
// posts in this one instead, however late the page submits it.
const pressJS = `(sel, x, y) => {
  const el = document.querySelector(sel);
  if (!el) return {ok: false, asked: []};
  const F = HTMLFormElement.prototype;
  if (!window.__scHold) {
    const h = window.__scHold = {submit: F.submit, rec: null, subs: []};
    const here = (f, sub) => {
      for (const [n, a] of [[f, 'target'], [sub, 'formtarget']]) {
        if (!n || !n.hasAttribute(a)) continue;
        const v = n.getAttribute(a), t = v.trim().toLowerCase();
        if (t && !['_self', '_parent', '_top'].includes(t) && !document.querySelector('iframe[name="' + CSS.escape(v) + '"],frame[name="' + CSS.escape(v) + '"]'))
          n.setAttribute(a, '_self');
      }
    };
    F.submit = function() { here(this, null); if (h.rec) h.rec(this, null); return h.submit.apply(this, arguments); };
    // Capturing, so the page can't stop it. Whether the page cancels the
    // submit is known once the click's events have run.
    window.addEventListener('submit', e => {
      if (!(e.target instanceof HTMLFormElement)) return;
      here(e.target, e.submitter);
      if (h.rec) h.subs.push(e);
    }, true);
  }
  const asked = [];
  const ask = (m, u, nav) => {
    m = String(m || 'GET').toUpperCase();
    try { u = new URL(String(u), location.href).href; } catch (e) { return; }
    if (m !== 'GET' && m !== 'HEAD' && /^https?:/.test(u)) asked.push({method: m, url: u, nav});
  };
  const h = window.__scHold;
  h.subs = [];
  h.rec = (f, sub) => {
    const method = sub && sub.hasAttribute('formmethod') ? sub.formMethod : f.method;
    if (method !== 'dialog') ask(method, sub && sub.hasAttribute('formaction') ? sub.formAction : f.action, true);
  };
  const X = XMLHttpRequest.prototype, N = Navigator.prototype;
  const open0 = X.open, send0 = X.send, beacon0 = N.sendBeacon, fetch0 = window.fetch;
  X.open = function(m, u) { this.__scReq = [m, u]; return open0.apply(this, arguments); };
  X.send = function() { if (this.__scReq) ask(this.__scReq[0], this.__scReq[1], false); return send0.apply(this, arguments); };
  N.sendBeacon = function(u) { ask('POST', u, false); return beacon0.apply(this, arguments); };
  window.fetch = function(input, init) {
    const req = input instanceof Request ? input : null;
    ask((init && init.method) || (req ? req.method : 'GET'), req ? req.url : input, false);
    return fetch0.apply(this, arguments);
  };
  try {
    const o = {bubbles: true, cancelable: true, composed: true, clientX: x, clientY: y, view: window, button: 0};
    const ptr = {pointerId: 1, pointerType: 'mouse', isPrimary: true};
    el.dispatchEvent(new PointerEvent('pointerdown', {...o, ...ptr, buttons: 1}));
    el.dispatchEvent(new MouseEvent('mousedown', {...o, buttons: 1}));
    if (el.focus) el.focus({preventScroll: true});
    el.dispatchEvent(new PointerEvent('pointerup', {...o, ...ptr}));
    el.dispatchEvent(new MouseEvent('mouseup', o));
    el.dispatchEvent(new MouseEvent('click', {...o, detail: 1}));
  } finally {
    X.open = open0; X.send = send0; N.sendBeacon = beacon0; window.fetch = fetch0;
    for (const e of h.subs) if (!e.defaultPrevented) h.rec(e.target, e.submitter);
    h.rec = null;
  }
  return {ok: true, asked};
}`

// escapeJS presses Escape, which closes most menus and modals.
const escapeJS = `() => {
  const t = document.activeElement || document.body || document;
  for (const type of ['keydown', 'keyup']) t.dispatchEvent(new KeyboardEvent(type, {key: 'Escape', code: 'Escape', keyCode: 27, which: 27, bubbles: true, cancelable: true}));
}`

// clickOne hovers the element, lets hover effects settle, then clicks and
// watches for a reaction.
func (m *Module) clickOne(ctx context.Context, tab *rod.Page, w *watcher, g *guard, c candidate, pageURL string) (clickResult, error) {
	var r clickResult
	t := tab.Context(ctx).Timeout(10 * time.Second)
	res, err := t.Eval(prepJS, c.Sel)
	if err != nil {
		return r, fmt.Errorf("couldn't reach it: %w", err)
	}
	var prep struct {
		State string  `json:"state"`
		By    string  `json:"by"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
	}
	if err := json.Unmarshal([]byte(res.Value.JSON("", "")), &prep); err != nil {
		return r, err
	}
	switch prep.State {
	case "ok":
	case "covered":
		return r, fmt.Errorf("%w (%s)", errCovered, prep.By)
	default:
		return r, &notThere{prep.State}
	}
	settle(ctx, 200*time.Millisecond)
	if _, err := t.Eval(armJS); err != nil {
		return r, err
	}
	w.reset()
	g.reset()
	// An error here usually means the click navigated mid-call; the
	// watcher and the guard have the rest.
	var press struct {
		Asked []struct {
			model.Request
			Nav bool `json:"nav"`
		} `json:"asked"`
	}
	if res, err := t.Eval(pressJS, c.Sel, prep.X, prep.Y); err == nil {
		_ = json.Unmarshal([]byte(res.Value.JSON("", "")), &press)
	}
	var asked []model.Request
	for _, q := range press.Asked {
		if signals(proto.NetworkResourceType(q.Type), q.URL) {
			asked = append(asked, q.Request)
		}
		r.left = r.left || q.Nav // a form post takes the page away, or will
	}

	// Wait for a reaction: up to 1.5 s for nothing to happen, longer while
	// requests it started are still running or the guard has yet to see
	// the writes it started.
	start := time.Now()
	var e effects
	var st pageState
	var held []model.Request
	for {
		settle(ctx, 100*time.Millisecond)
		e = w.report()
		held = g.report()
		st = state(tab)
		elapsed := time.Since(start)
		seen := e.requests > 0 || len(held) > 0 || e.nav != nil || len(e.errors) > 0
		active := st.reacted(c.Void) || seen
		// The page tells at once which writes the click started. The
		// browser can take long to pass them to the guard on a busy
		// machine. The click's effect is what the guard held, so it isn't
		// known before then.
		waiting := len(held) < len(asked)
		// A page that can no longer be read is leaving. A write taking it
		// away is in held by then (see holdWrites); a navigation or a
		// request can still reach the watcher later. Until something
		// comes, or nothing does for deadAfter, the click's effect isn't
		// known.
		leaving := !st.Armed && !seen
		if ctx.Err() != nil || elapsed > clickWait {
			break
		}
		if !waiting && e.pending == 0 && ((active && !leaving && elapsed > 400*time.Millisecond) || elapsed >= deadAfter) {
			break
		}
	}
	// The guard holds a write whenever it comes, so one the page asked
	// for that it hasn't seen yet is reported as held too.
	for _, q := range asked {
		if !slices.ContainsFunc(held, func(h model.Request) bool { return h.Method == q.Method && sameURL(h.URL, q.URL) }) {
			held = append(held, q)
		}
	}
	r.errors, r.failed, r.held = e.errors, e.failed, held
	if len(r.held) > 0 {
		// The page's own code sees a held request fail; the error it then
		// throws is the guard's doing, not the site's.
		r.errors = slices.DeleteFunc(r.errors, heldError.MatchString)
	}
	r.dead = !st.reacted(c.Void) && e.requests == 0 && e.nav == nil && len(e.errors) == 0 && len(r.held) == 0
	r.left = r.left || e.nav != nil || st.Moved || !st.Armed
	// Close whatever opened (menus, modals) before the next click.
	_, _ = tab.Timeout(5 * time.Second).Eval(escapeJS)
	if !r.left {
		if info, err := tab.Info(); err == nil && strip(info.URL) != strip(pageURL) {
			r.left = true
		}
	}
	return r, nil
}

// clickWait bounds how long one click is watched.
const clickWait = 6 * time.Second

// sameURL reports whether a and b are the same URL, as the guard (Go)
// and the page (JavaScript) may spell it differently.
func sameURL(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return ua.String() == ub.String()
}

// state reads the page's reaction since armJS. If the page navigated, the
// observer is gone and Armed is false.
func state(tab *rod.Page) pageState {
	var s pageState
	res, err := tab.Timeout(5 * time.Second).Eval(stateJS)
	if err != nil {
		return s
	}
	if err := json.Unmarshal([]byte(res.Value.JSON("", "")), &s); err != nil {
		return pageState{}
	}
	return s
}

func (c candidate) String() string { return fmt.Sprintf("%s %q", c.Sel, c.Label) }
