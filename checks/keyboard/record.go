// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/capture/intercept"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/internal/focus"
	"github.com/RhythmShahriar/soundcheck/model"
)

// maxReloads caps the page loads one walk adds.
const maxReloads = 2

// errReloads is the walk's error when a page needed more reloads than
// maxReloads.
var errReloads = errors.New("the page needed more than 2 reloads")

// walker is one page's walk in progress.
type walker struct {
	cc    *check.Context
	tab   *rod.Page
	page  *model.Page
	set   Settings
	start time.Time

	reader *focus.Reader
	guard  *guard
	// pkEvent names the event the page's refused passkey requests
	// fire, new for each walk (passkeyEvent).
	pkEvent string

	mu         sync.Mutex
	workerSeen map[proto.TargetTargetID]bool // service workers held
	workers    sync.WaitGroup                // their watches

	trace  *Trace
	aux    []stepResult // what the probe found at each forward stop, besides the Step
	probed []int        // the forward index of each stop stepJS handed the guard
	// ringSeen keeps focus indicator results across the run's pages;
	// nil measures every page afresh.
	ringSeen *ringCache
	// widgetsOn says the page's widgets are tested (widgetGroups);
	// budget is how many triggers and mouse-only controls may still be
	// pressed, keyboard.widgets at the start.
	widgetsOn bool
	budget    int
	reloads   int
	loaded    time.Time // when the page walked was loaded
	// onWindow, when set, is called in each of listeners' windows
	// after its press is sent, with the window's number from 1: the
	// tests make the page do something once at a time of their choice
	// through it, as a slow machine may.
	onWindow func(ctx context.Context, n int)
	windows  int
}

// partial is the trace recorded so far, or nil before the walk began.
func (w *walker) partial() *Trace { return w.trace }

// begin starts the page's trace.
func (w *walker) begin() *Trace {
	w.trace = &Trace{Schema: Schema, Page: tracePage(w.page), Viewport: "desktop", MainIndex: -1,
		Forward: []Step{}, Backward: []Step{}, Widgets: []WidgetResult{}, Listeners: []Candidate{}, Rings: []Ring{},
		Held: []string{}, Events: []string{}}
	w.loaded = w.start
	return w.trace
}

// save stores t as the page's trace file.
func (w *walker) save(t *Trace) error {
	data, err := t.Encode()
	if err != nil {
		return err
	}
	if w.cc.Save == nil {
		return errors.New("the run stores no files")
	}
	return w.cc.Save(tracePath(w.page), data)
}

// interacted reports whether interact clicked or submitted on the page,
// which leaves menus open and forms sent: the walk then needs the page
// as a visitor gets it.
func interacted(p *model.Page) bool {
	for _, i := range p.Interact {
		if i.Kind == "click" || i.Kind == "submit" {
			return true
		}
	}
	return false
}

// reload loads the page again, at the run's polite pace, like the
// runner's own visits.
func (w *walker) reload(ctx context.Context) error {
	if w.reloads >= maxReloads {
		return errReloads
	}
	w.reloads++
	raw := w.page.FinalURL
	if raw == "" {
		raw = w.page.URL
	}
	if w.cc.Hosts != nil {
		host := ""
		if u, err := url.Parse(raw); err == nil {
			host = u.Host
		}
		if err := w.cc.Hosts.For(host).Wait(ctx); err != nil {
			return err
		}
	}
	g := w.guard
	if g != nil {
		g.mu.Lock()
		g.reloading = true
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			g.reloading, g.left = false, false
			g.mu.Unlock()
		}()
	}
	tab := w.tab.Context(ctx)
	if err := tab.Navigate(raw); err != nil {
		return err
	}
	// As in capture.Visit, a page that never quite finishes loading is
	// still walked as it is.
	_ = tab.WaitLoad()
	tab.WaitRequestIdle(700*time.Millisecond, nil, nil, nil)()
	w.loaded = time.Now()
	if err := ctx.Err(); err != nil {
		return err
	}
	if g == nil {
		return nil
	}
	// A new document: the guard and the envelope's page parts go on it
	// again. The probes get a fresh world.
	return w.armDoc(ctx)
}

// guard watches the tab while the walk presses keys. Requests other
// than GET and HEAD are held (failed before they leave the browser)
// and listed; a navigation the page starts is canceled in the page by
// guardJS. What each forward Tab press caused is kept by the press's
// index in the trace's forward walk.
type guard struct {
	mu      sync.Mutex
	press   int // the forward press under way, -1 between them
	marks   []mark
	since   func() float64 // page time now, in milliseconds since the load
	effects map[int]string
	byFocus map[int]bool // presses whose navigation a focus handler started
	held    []string
	events  []string
	// requests counts every request the page made while the walk had
	// it (sent or held) and navs every navigation it started: a press
	// that makes either did something.
	requests, navs int
	// candNavs counts the navigations a key press or click on the
	// mouse-only control being pressed started itself (candJS): the
	// only ones that count as its response.
	candNavs int
	// keyNavs counts those another handler started while a key press
	// or click was sent (a page-wide or delegated keydown handler) and
	// keyTo lists where they went, as the guard logged them.
	keyNavs int
	keyTo   []string
	left    bool // a navigation the page couldn't be kept from committed
	// armed is true while the walk has the page; reloading while the
	// walk loads it again itself, which isn't the page leaving.
	armed, reloading bool
	stopped          []string // main-frame navigations stopped through the protocol
}

// on reports whether navigations the page starts are stopped now.
func (g *guard) on() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.armed && !g.reloading
}

// stopTop records a main-frame navigation stopped through the protocol,
// so its request is held should it still come. g.mu is held.
func (g *guard) stopTop(raw string) {
	g.navs++
	if len(g.stopped) < maxList {
		g.stopped = append(g.stopped, raw)
	}
}

// rank orders effects: a form sent says more than a window opened,
// which says more than a navigation started.
var rank = map[string]int{EffectNavigation: 1, EffectTarget: 2, EffectForm: 3}

// mark is when a key press was sent (in page time) and which forward
// press it was (-1 for any other key).
type mark struct {
	at    float64
	press int
}

// at says the forward press of that index is being sent, or with -1
// that the keys from now on aren't forward presses.
func (g *guard) at(press int) {
	g.mu.Lock()
	g.press = press
	g.marks = append(g.marks, mark{g.since(), press})
	g.mu.Unlock()
}

// effect records kind for the press under way. g.mu is held.
func (g *guard) effect(kind string) { g.effectOf(g.press, kind) }

// pressAt is the press sent last before page time ms, -1 for none: a
// form a script submits starts its navigation a moment after the key
// press that focused the field, when the walk may have read the stop
// already. g.mu is held.
func (g *guard) pressAt(ms float64) int {
	press := -1
	for _, m := range g.marks {
		if m.at <= ms+1 { // a millisecond for the clocks' rounding
			press = m.press
		}
	}
	return press
}

func (g *guard) effectOf(press int, kind string) {
	if press < 0 {
		return
	}
	if rank[kind] > rank[g.effects[press]] {
		g.effects[press] = kind
	}
}

func (g *guard) hold(what string) {
	if !slices.Contains(g.held, what) {
		g.held = append(g.held, what)
	}
}

func (g *guard) event(msg string) {
	if !slices.Contains(g.events, msg) {
		g.events = append(g.events, msg)
	}
}

// pathOf is a request's method and path, with no query: what a held
// request is listed as. Token-like segments are masked, like the page's
// own path in the trace.
func pathOf(method string, u *url.URL) string {
	if u == nil {
		return method
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	return method + " " + echolink.RedactURL(p)
}

// arm starts watching the tab and installs the walk's in-page guard and
// caret style. The function it returns undoes all of it.
func (w *walker) arm(ctx context.Context) (func(), error) {
	g := &guard{press: -1, effects: map[int]string{}, byFocus: map[int]bool{},
		since: func() float64 { return float64(time.Since(w.loaded).Microseconds()) / 1000 }}
	w.guard = g
	remove, err := intercept.Add(w.tab, func(r *intercept.Request) {
		g.mu.Lock()
		g.requests++
		g.mu.Unlock()
		if r.Header("Upgrade") != "" || r.Type == proto.NetworkResourceTypeWebSocket {
			// A connection that would stay open past the walk (K2).
			r.Block()
			g.mu.Lock()
			g.hold(pathOf(r.Method, r.URL))
			g.event("connection upgrade held: " + pathOf(r.Method, r.URL))
			g.mu.Unlock()
			return
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if r.Type == proto.NetworkResourceTypeDocument {
				g.mu.Lock()
				defer g.mu.Unlock()
				if !g.armed || g.reloading {
					return
				}
				if r.URL != nil && slices.Contains(g.stopped, r.URL.String()) {
					// The request of a navigation the envelope stopped
					// that got here all the same.
					r.Block()
					return
				}
				// A navigation neither guard stopped, perhaps a frame's.
				// Failing it would put Chrome's error page in its place,
				// so it is only noted (see the frame watch below).
				g.effect(EffectNavigation)
			}
			return
		}
		r.Block()
		g.mu.Lock()
		g.hold(pathOf(r.Method, r.URL))
		if r.Type == proto.NetworkResourceTypeDocument {
			g.effect(EffectForm)
		}
		g.mu.Unlock()
	})
	if err != nil {
		return func() {}, fmt.Errorf("can't hold the page's requests: %w", err)
	}
	wctx, cancel := context.WithCancel(ctx)
	main := w.tab.FrameID
	waitPage := w.tab.Context(wctx).EachEvent(func(e *proto.PageFrameNavigated) {
		if e.Frame != nil && e.Frame.ID == main {
			g.mu.Lock()
			if g.armed && !g.reloading {
				g.left = true
			}
			g.mu.Unlock()
		}
	})
	go waitPage()
	b := w.tab.Browser()
	_ = proto.TargetSetDiscoverTargets{Discover: true}.Call(b)
	waitTargets := b.Context(wctx).EachEvent(func(e *proto.TargetTargetCreated) {
		if e.TargetInfo == nil || e.TargetInfo.OpenerID != w.tab.TargetID {
			return
		}
		// A window the page opened: closed at once, so the walk keeps
		// the page and the site gets no second visitor.
		_, _ = proto.TargetCloseTarget{TargetID: e.TargetInfo.TargetID}.Call(b)
		host := ""
		if u, err := url.Parse(e.TargetInfo.URL); err == nil {
			host = u.Host
		}
		g.mu.Lock()
		g.effect(EffectTarget)
		g.event("window opened and closed: " + orNone(host))
		g.mu.Unlock()
	})
	go waitTargets()
	undo := w.envelope(ctx, wctx)
	g.mu.Lock()
	g.armed = true
	g.mu.Unlock()
	if err := w.armDoc(ctx); err != nil {
		cancel()
		undo()
		remove()
		return func() {}, err
	}
	return func() {
		g.mu.Lock()
		g.armed = false
		g.mu.Unlock()
		// The walk's own context may be spent (a timeout): turning the
		// guard off gets a moment of its own.
		octx, ocancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer ocancel()
		_, _ = w.reader.Eval(octx, guardOff)
		cancel()
		undo()
		remove()
	}, nil
}

// armDoc installs what each document of the walk needs: the caret
// style, the navigation guard and the envelope's WebSocket policy and
// passkey refusal. A reload calls it again.
func (w *walker) armDoc(ctx context.Context) error {
	g := w.guard
	if err := w.caret(ctx); err != nil {
		g.mu.Lock()
		g.event("caret not hidden: " + err.Error())
		g.mu.Unlock()
	}
	if _, err := w.reader.Eval(ctx, guardJS); err != nil {
		return fmt.Errorf("can't guard the page: %w", err)
	}
	if _, err := w.reader.Eval(ctx, guardOn); err != nil {
		return err
	}
	w.docArm(ctx)
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(no host)"
	}
	return s
}

// caretRules hide the text caret and the selection highlight: Tab
// into a field selects its text. Neither a caret nor a selection
// is a focus indicator.
var caretRules = []string{
	"* { caret-color: transparent !important; }",
	"*::selection { background: transparent !important; color: inherit !important; }",
}

// caret hides the text caret and selection on the page for the rest of
// the visit, through a style sheet of the inspector's own: a blinking
// caret in a focused field changes pixels the focus indicator check
// compares.
func (w *walker) caret(ctx context.Context) error {
	p := w.tab.Context(ctx)
	if err := (proto.DOMEnable{}).Call(p); err != nil {
		return err
	}
	if err := (proto.CSSEnable{}).Call(p); err != nil {
		return err
	}
	sheet, err := proto.CSSCreateStyleSheet{FrameID: p.FrameID}.Call(p)
	if err != nil {
		return err
	}
	for _, rule := range caretRules {
		if _, err := (proto.CSSAddRule{StyleSheetID: sheet.StyleSheetID, RuleText: rule, Location: &proto.CSSSourceRange{}}).Call(p); err != nil {
			return err
		}
	}
	return nil
}

// record walks the page by keyboard and returns its trace: Tab up to
// max_tabs, Shift+Tab back over the same stops, a second look at a
// suspected trap and the skip link followed. A trace is returned with
// any error, holding what the walk saw until then.
func record(ctx context.Context, w *walker) (*Trace, error) {
	t := w.begin()
	if interacted(w.page) {
		if err := w.reload(ctx); err != nil {
			return t, err
		}
	}
	w.reader = focus.NewReader(w.tab)
	disarm, err := w.arm(ctx)
	if err != nil {
		return t, err
	}
	defer disarm()
	defer w.finish(t)
	now, err := w.reader.Eval(ctx, startJS)
	if err != nil {
		return t, fmt.Errorf("can't focus the page: %w", err)
	}
	if ms, err := strconv.ParseFloat(now, 64); err == nil && ms >= 0 {
		w.loaded = time.Now().Add(-time.Duration(ms * float64(time.Millisecond)))
	}
	main, err := w.reader.Eval(ctx, hasMain)
	if err != nil {
		return t, err
	}
	done, err := w.forward(ctx, t)
	if err != nil || done {
		return t, err
	}
	if err := w.backward(ctx, t); err != nil {
		return t, err
	}
	if main == "true" {
		if i := slices.IndexFunc(w.aux, func(r stepResult) bool { return r.InMain }); i >= 0 {
			t.MainIndex = i
		}
	}
	if err := w.skipLink(ctx, t); err != nil {
		return t, err
	}
	if err := w.rings(ctx, t); err != nil {
		return t, err
	}
	if err := w.widgets(ctx, t); err != nil {
		return t, err
	}
	return t, w.listeners(ctx, t)
}

// finish copies what the guard saw into the trace.
func (w *walker) finish(t *Trace) {
	g := w.guard
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, e := range g.effects {
		if i < len(t.Forward) {
			t.Forward[i].Effect = e
			t.Forward[i].ByFocus = e == EffectNavigation && g.byFocus[i]
		}
	}
	t.Held = append(t.Held, g.held...)
	t.Events = append(t.Events, g.events...)
}

// forward presses Tab until focus is back at the first stop, a trap
// holds it or max_tabs runs out. done is true when the rest of the walk
// can't go on: the page left.
func (w *walker) forward(ctx context.Context, t *Trace) (done bool, err error) {
	defer w.guard.at(-1)
	first, body, probed := 0, false, false
	for i := range w.set.MaxTabs {
		w.guard.at(len(t.Forward))
		if err := focus.Press(ctx, w.tab, focus.Tab); err != nil {
			return false, err
		}
		s, res, err := w.step(ctx, len(t.Forward))
		if err != nil {
			return false, err
		}
		t.Forward = append(t.Forward, s)
		w.aux = append(w.aux, res)
		if err := w.navigations(ctx); err != nil {
			return false, err
		}
		if w.left() {
			t.Capped = true
			t.Stopped = fmt.Sprintf("Tab stop %d (%s) left the page", i+1, s.Tag)
			return true, w.reload(ctx)
		}
		switch {
		case s.IsBody && first == 0 && i >= 1:
			return false, nil // nothing on the page takes focus
		case s.IsBody:
			body = first != 0
		case first == 0:
			first = s.BackendNodeID
		case body && s.BackendNodeID == first:
			return false, nil // wrapped round
		}
		if probed {
			continue
		}
		if set := suspect(t.Forward, w.set.TrapAfter); set != nil {
			probed = true
			w.guard.at(-1)
			left, err := w.trapProbe(ctx, t, set)
			if err != nil || !left {
				return false, err
			}
		}
	}
	t.Capped = true
	return false, nil
}

// step reads the stop focus is on and judges it. forward is the index
// of a forward press, -1 for any other.
func (w *walker) step(ctx context.Context, forward int) (Step, stepResult, error) {
	stop, raw, err := w.reader.ActiveWith(ctx, stepJS(w.set.Ignore, forward >= 0))
	at := int(time.Since(w.loaded).Milliseconds())
	if err != nil {
		return Step{}, stepResult{}, err
	}
	s := Step{Stop: stop, AtMS: at}
	var res stepResult
	if raw == "" {
		return s, res, nil // the document's own stop: nothing to judge
	}
	if forward >= 0 {
		w.probed = append(w.probed, forward)
	}
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return s, res, fmt.Errorf("focus probe: %w", err)
	}
	s.Hidden, s.Obscured, s.Ignored = res.Hidden, res.Obscured, res.Ignored
	if res.Obscured {
		s.ObscuredBy = selectorOf(res.By)
	}
	return s, res, nil
}

// navigations moves what the in-page guard logged since the last press
// into the guard.
func (w *walker) navigations(ctx context.Context) error {
	raw, err := w.reader.Eval(ctx, navLogJS)
	if err != nil {
		return err
	}
	var log []struct {
		Form    bool    `json:"form"`
		What    string  `json:"what"`
		Stopped bool    `json:"stopped"`
		At      float64 `json:"at"`    // performance.now() when it started
		El      int     `json:"el"`    // the forward stop it came from, by probe; -1 when unknown
		Focus   bool    `json:"focus"` // it started in a focus handler
		Cand    bool    `json:"cand"`  // a press on the marked control started it
		Key     bool    `json:"key"`   // another handler of a press started it
	}
	if err := json.Unmarshal([]byte(raw), &log); err != nil {
		return fmt.Errorf("navigation log: %w", err)
	}
	var env struct {
		WS []string `json:"ws"`
		PK []string `json:"pk"`
	}
	raw, err = w.reader.Eval(ctx, envLogJS)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return fmt.Errorf("navigation log: %w", err)
	}
	g := w.guard
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, h := range env.WS {
		g.event("WebSocket refused: " + orNone(clip(h, maxText)))
	}
	for _, k := range env.PK {
		g.event("passkey request refused: " + clip(k, 10))
	}
	for _, n := range log {
		// The page's own path, read in the page: masked like any other
		// (a reset link's token), as the guard writes it down as is.
		n.What = echolink.RedactURL(n.What)
		g.navs++
		if n.Cand {
			g.candNavs++
		}
		if n.Key {
			g.keyNavs++
			if len(g.keyTo) < maxList {
				g.keyTo = append(g.keyTo, n.What)
			}
		}
		kind := EffectNavigation
		if n.Form {
			kind = EffectForm
			g.hold(n.What)
		}
		press := g.pressAt(n.At)
		if n.El >= 0 && n.El < len(w.probed) {
			press = w.probed[n.El]
		}
		g.effectOf(press, kind)
		if n.Focus && press >= 0 {
			g.byFocus[press] = true
		}
		if n.Stopped {
			g.event("navigation stopped: " + n.What)
		} else {
			g.event("navigation not stopped: " + n.What)
		}
	}
	return nil
}

func (w *walker) left() bool {
	w.guard.mu.Lock()
	defer w.guard.mu.Unlock()
	return w.guard.left
}

// selectorOf turns a probe's element path into a selector.
func selectorOf(raw json.RawMessage) string {
	var path [][]a11y.Elem
	if json.Unmarshal(raw, &path) != nil {
		return ""
	}
	return clip(a11y.Selector(path), maxSelectorRunes)
}

// suspect returns the nodes of a suspected trap at the end of steps:
// one element focused trapAfter times in a row (frameTrapPresses for a
// frame the walk can't see into), or two or three elements focused in
// turn twice over, with the document's stop nowhere among them. It
// returns nil when the end of steps looks like an ordinary walk.
func suspect(steps []Step, trapAfter int) []int {
	n := len(steps)
	if n == 0 || steps[n-1].IsBody {
		return nil
	}
	need := trapAfter
	if steps[n-1].IsFrame {
		need = frameTrapPresses
	}
	if n >= need {
		id := steps[n-1].BackendNodeID
		same := true
		for _, s := range steps[n-need:] {
			same = same && !s.IsBody && s.BackendNodeID == id
		}
		if same {
			return []int{id}
		}
	}
	for k := 2; k <= 3; k++ {
		if n < 2*k {
			continue
		}
		win := steps[n-2*k:]
		ids := map[int]bool{}
		ok := true
		for i := range k {
			a, b := win[i], win[i+k]
			ok = ok && !a.IsBody && !a.IsFrame && a.BackendNodeID == b.BackendNodeID
			ids[a.BackendNodeID] = true
		}
		if ok && len(ids) == k {
			out := make([]int, 0, k)
			for _, s := range win[:k] {
				out = append(out, s.BackendNodeID)
			}
			return out
		}
	}
	return nil
}

// trapProbe takes a second look at a suspected trap: it counts what the
// trap keeps out of reach, then presses Escape and Tab, the way out a
// widget that holds focus on purpose offers. left is true when focus
// got out of the set.
func (w *walker) trapProbe(ctx context.Context, t *Trace, set []int) (left bool, err error) {
	raw, err := w.reader.CallOn(ctx, trapJS, set...)
	if err != nil {
		return false, fmt.Errorf("trap probe: %w", err)
	}
	var res trapResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return false, fmt.Errorf("trap probe: %w", err)
	}
	p := &TrapProbe{FocusablesAfter: res.N}
	for _, s := range res.Set {
		p.Set = append(p.Set, selectorOf(s))
	}
	for _, s := range res.After {
		p.After = append(p.After, selectorOf(s))
	}
	t.TrapProbe = p
	for _, k := range []focus.Key{focus.Escape, focus.Tab} {
		if err := focus.Press(ctx, w.tab, k); err != nil {
			return false, err
		}
	}
	s, err := w.reader.Active(ctx)
	if err != nil {
		return false, err
	}
	p.EscapeLeft = !slices.Contains(set, s.BackendNodeID)
	return p.EscapeLeft, nil
}

// backward presses Shift+Tab from where the forward walk ended, at most
// as many times as it pressed Tab, stopping once it is back round.
func (w *walker) backward(ctx context.Context, t *Trace) error {
	first, body := 0, false
	for range len(t.Forward) {
		if err := focus.Press(ctx, w.tab, focus.ShiftTab); err != nil {
			return err
		}
		s, _, err := w.step(ctx, -1)
		if err != nil {
			return err
		}
		t.Backward = append(t.Backward, s)
		if err := w.navigations(ctx); err != nil {
			return err
		}
		switch {
		case s.IsBody:
			body = first != 0
		case first == 0:
			first = s.BackendNodeID
		case body && s.BackendNodeID == first:
			return nil
		}
	}
	return nil
}

// skipLink follows the skip link, when one of the first three stops is
// one: focus it, press Enter (a same-page link, the one link the walk
// presses) and Tab once, to see whether focus went on from the target.
func (w *walker) skipLink(ctx context.Context, t *Trace) error {
	at := -1
	seen := 0
	for i, s := range t.Forward {
		if s.IsBody || s.Ignored {
			continue
		}
		if seen++; seen > 3 {
			break
		}
		if r := w.aux[i]; r.SkipID != "" && r.SkipMatch {
			at = i
			break
		}
	}
	if at < 0 {
		return nil
	}
	link, id := t.Forward[at], w.aux[at].SkipID
	sl := &SkipLink{Selector: link.Selector, Name: link.Name, TargetID: clip(id, maxText)}
	t.SkipLink = sl
	before, err := w.reader.CallOn(ctx, focusJS, link.BackendNodeID)
	if err != nil {
		return fmt.Errorf("skip link: %w", err)
	}
	if err := focus.Press(ctx, w.tab, focus.Enter); err != nil {
		return err
	}
	// Room for a smooth scroll to start.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(150 * time.Millisecond):
	}
	if err := w.navigations(ctx); err != nil {
		return err
	}
	found, err := w.reader.Eval(ctx, targetJS(id))
	if err != nil {
		return err
	}
	after, err := w.reader.Eval(ctx, scrollJS)
	if err != nil {
		return err
	}
	sl.TargetFound, sl.Scrolled = found == "true", after != before
	if sl.TargetFound {
		n, err := w.reader.Eval(ctx, afterTargetJS(id))
		if err != nil {
			return err
		}
		sl.NothingAfter = n == "0"
	}
	if err := focus.Press(ctx, w.tab, focus.Tab); err != nil {
		return err
	}
	next, inside, err := w.reader.ActiveWith(ctx, nextJS(id))
	if err != nil {
		return err
	}
	sl.NextStop, sl.NextInsideOrAfterTarget = next.Selector, inside == "true"
	return nil
}
