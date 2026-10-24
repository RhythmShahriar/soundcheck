// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// destructive matches controls Soundcheck never clicks unless allowlisted:
// anything that deletes, logs out, cancels, empties or buys, and links
// carrying a WordPress nonce or an action. It's matched against the
// element's text, aria-label, id, class, href, title, value and name.
var destructive = regexp.MustCompile(`(?i)delete|remove|unsubscribe|log ?out|log-out|logoff|sign ?out|sign-out|cancel|deactivate|trash|destroy|erase|purge|revoke|wipe|uninstall|disconnect|unlink|empty[ _-]?cart|clear[ _-]?cart|reset[ _-]?password|lost[ _-]?password|close[ _-]?account|add[ _-]?to[ _-]?cart|place[ _-]?order|buy[ _-]?now|pay[ _-]?now|checkout|purchase|_wpnonce|/wp-admin|[?&]action=`)

// isDestructive reports whether a control looks like it changes data.
func isDestructive(words string) bool { return destructive.MatchString(words) }

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
		case !c.Allowed && isDestructive(c.Words):
			record(c, "skipped", "looks destructive")
			continue
		}
		e, err := m.clickOne(ctx, tab, w, c, pageURL)
		if errors.Is(err, errCovered) {
			// Something (an open menu or modal) covers it: start fresh once.
			if reload(ctx, tab, pageURL) == nil {
				e, err = m.clickOne(ctx, tab, w, c, pageURL)
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
	// Leave the page as it was loaded for the submit level. Otherwise the
	// runner's mobile pass reloads it anyway, so don't load it twice.
	if n > 0 && cc.Config.Level("submit") {
		_ = reload(ctx, tab, pageURL)
	}
	return clicked
}

var errCovered = errors.New("covered by another element")

// notThere is an element a visitor couldn't click either: hidden, clipped
// by a collapsed parent, off screen or no longer in the page.
type notThere struct{ why string }

func (e *notThere) Error() string { return e.why }

type clickResult struct {
	errors []string
	failed []model.Request
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
// runs the element's default action too (follow a link, toggle details).
const pressJS = `(sel, x, y) => {
  const el = document.querySelector(sel);
  if (!el) return false;
  const o = {bubbles: true, cancelable: true, composed: true, clientX: x, clientY: y, view: window, button: 0};
  const ptr = {pointerId: 1, pointerType: 'mouse', isPrimary: true};
  el.dispatchEvent(new PointerEvent('pointerdown', {...o, ...ptr, buttons: 1}));
  el.dispatchEvent(new MouseEvent('mousedown', {...o, buttons: 1}));
  if (el.focus) el.focus({preventScroll: true});
  el.dispatchEvent(new PointerEvent('pointerup', {...o, ...ptr}));
  el.dispatchEvent(new MouseEvent('mouseup', o));
  el.dispatchEvent(new MouseEvent('click', {...o, detail: 1}));
  return true;
}`

// escapeJS presses Escape, which closes most menus and modals.
const escapeJS = `() => {
  const t = document.activeElement || document.body || document;
  for (const type of ['keydown', 'keyup']) t.dispatchEvent(new KeyboardEvent(type, {key: 'Escape', code: 'Escape', keyCode: 27, which: 27, bubbles: true, cancelable: true}));
}`

// clickOne hovers the element, lets hover effects settle, then clicks and
// watches for a reaction.
func (m *Module) clickOne(ctx context.Context, tab *rod.Page, w *watcher, c candidate, pageURL string) (clickResult, error) {
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
	// An error here usually means the click navigated mid-call; the
	// watcher has the rest.
	_, _ = t.Eval(pressJS, c.Sel, prep.X, prep.Y)

	// Wait for a reaction: up to 1.5 s for nothing to happen, longer while
	// requests it started are still running.
	start := time.Now()
	var e effects
	var st pageState
	for {
		settle(ctx, 100*time.Millisecond)
		e = w.report()
		st = state(tab)
		elapsed := time.Since(start)
		active := st.reacted(c.Void) || e.requests > 0 || e.nav != nil || len(e.errors) > 0
		if ctx.Err() != nil || (active && e.pending == 0 && elapsed > 400*time.Millisecond) ||
			(e.pending == 0 && elapsed >= deadAfter) || elapsed > 6*time.Second {
			break
		}
	}
	r.errors, r.failed = e.errors, e.failed
	r.dead = !st.reacted(c.Void) && e.requests == 0 && e.nav == nil && len(e.errors) == 0
	r.left = e.nav != nil || st.Moved || !st.Armed
	// Close whatever opened (menus, modals) before the next click.
	_, _ = tab.Timeout(5 * time.Second).Eval(escapeJS)
	if !r.left {
		if info, err := tab.Info(); err == nil && strip(info.URL) != strip(pageURL) {
			r.left = true
		}
	}
	return r, nil
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
