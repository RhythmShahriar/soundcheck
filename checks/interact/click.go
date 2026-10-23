package interact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"

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

// clicks runs level 2 and returns the selectors it clicked.
func (m *Module) clicks(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page, f *facts) map[string]bool {
	clicked := map[string]bool{}
	pageURL := orDefault(p.FinalURL, p.URL)
	base, _ := url.Parse(pageURL)
	maxClicks := cc.Config.Interactions.MaxClicks
	if maxClicks <= 0 {
		maxClicks = 15
	}
	cands := slices.Clone(f.Cands)
	slices.SortStableFunc(cands, func(a, b candidate) int { return a.Prio - b.Prio })
	labels := map[string]int{}
	for _, c := range cands {
		labels[c.Label]++
	}
	if _, err := tab.Eval(guardJS); err != nil {
		return clicked
	}
	w := watch(ctx, tab)
	defer w.stop()

	record := func(c candidate, outcome, detail string) {
		if len(p.Interact) < 100 {
			p.Interact = append(p.Interact, model.Interaction{Kind: "click", Selector: c.Sel, Label: c.Label, Outcome: outcome, Detail: detail})
		}
	}
	n := 0
	for _, c := range cands {
		if ctx.Err() != nil || n >= maxClicks {
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
		name := c.Label
		if name == "" || labels[c.Label] > 1 {
			name = strings.TrimSpace(c.Label + " (" + c.Sel + ")")
		}
		e, err := m.clickOne(ctx, tab, w, c, pageURL)
		if err != nil {
			if errors.Is(err, errCovered) {
				// Something (an open menu or modal) covers it: start fresh once.
				if reload(ctx, tab, pageURL) == nil {
					e, err = m.clickOne(ctx, tab, w, c, pageURL)
				}
			}
			if err != nil {
				record(c, "skipped", err.Error())
				continue
			}
		}
		n++
		clicked[c.Sel] = true
		switch {
		case len(e.errors) > 0:
			record(c, "error", e.errors[0])
			check.Page(p, model.Issue{Check: "interact/click-error", Level: model.Error,
				Title: "Clicking " + name + " throws a JS error", Detail: e.errors[0],
				Fix: "Click it with the browser console open to see the stack trace."})
		case len(e.failed) > 0:
			d := describe(e.failed[0], base)
			record(c, "error", d)
			check.Page(p, model.Issue{Check: "interact/click-request", Level: model.Error,
				Title: "Clicking " + name + " makes a request that fails", Detail: d,
				Fix: "Check the server log for the endpoint at the time of the click."})
		case e.dead:
			record(c, "dead", "no DOM change, request or navigation within 1.5 s")
			check.Page(p, deadIssue(name, c.Sel, c.Tag == "a", "Clicking it changed nothing on the page, made no request and didn't navigate."))
		default:
			record(c, "ok", c.Kind)
		}
		if e.left {
			if err := reload(ctx, tab, pageURL); err != nil {
				break
			}
		}
	}
	// Leave the page as it was loaded for the next level.
	if n > 0 {
		_ = reload(ctx, tab, pageURL)
	}
	return clicked
}

var errCovered = errors.New("covered by another element")

type clickResult struct {
	errors []string
	failed []model.Request
	dead   bool
	left   bool // the page navigated: reload before the next click
}

// clickOne moves the mouse onto the element, waits for hover effects to
// settle, then clicks and watches for a reaction.
func (m *Module) clickOne(ctx context.Context, tab *rod.Page, w *watcher, c candidate, pageURL string) (clickResult, error) {
	var r clickResult
	sel := c.Sel
	t := tab.Context(ctx).Timeout(5 * time.Second)
	els, err := t.Elements(sel)
	if err != nil || len(els) == 0 {
		return r, errors.New("not found")
	}
	el := els[0]
	if err := el.ScrollIntoView(); err != nil {
		return r, errors.New("can't scroll to it")
	}
	pt, err := el.Interactable()
	if err != nil {
		var covered *rod.CoveredError
		if errors.As(err, &covered) {
			return r, errCovered
		}
		return r, errors.New("hidden")
	}
	if err := t.Mouse.MoveTo(*pt); err != nil {
		return r, err
	}
	settle(ctx, 200*time.Millisecond)
	if _, err := t.Eval(armJS); err != nil {
		return r, err
	}
	w.reset()
	if err := t.Mouse.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return r, err
	}

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
	_ = tab.Keyboard.Type(input.Escape)
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
	res, err := tab.Timeout(2 * time.Second).Eval(stateJS)
	if err != nil {
		return s
	}
	if err := json.Unmarshal([]byte(res.Value.JSON("", "")), &s); err != nil {
		return pageState{}
	}
	return s
}

func (c candidate) String() string { return fmt.Sprintf("%s %q", c.Sel, c.Label) }
