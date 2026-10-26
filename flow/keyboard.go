// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/capture/intercept"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/internal/focus"
)

// KeyFailure is a step of a key flow the keyboard couldn't do.
type KeyFailure struct {
	Kind   string // the step's action: click, fill, select, check, submit or press
	Target string // its target's text or selector, from the flow
	// Reason is what went wrong, such as `not reached after 150 Tab
	// stops; focus stayed on #menu` or `reached; Enter did nothing`.
	Reason string
	Path   []string // the selectors of the last focus stops, oldest first
}

func (k *KeyFailure) Error() string { return k.Reason }

// pathLen is how many focus stops a KeyFailure keeps.
const pathLen = 6

// effectWait is how long a key press may take to show that it did
// something.
const effectWait = time.Second

// keys is the state of a key flow while it plays. The intercept handler
// writes to it from its own goroutine, so mu guards everything below
// it.
type keys struct {
	dry      bool
	mu       sync.Mutex
	requests int   // requests the page made
	armed    bool  // this step's held writes end the flow
	held     *Held // the first write held while armed
	trace    []string
}

// keyboard starts a key flow on tab: every request is counted, so a key
// press that sends one counts as doing something. With dry every
// request but GET and HEAD is held.
func (r *runner) keyboard(tab *rod.Page, dry bool) (remove func(), err error) {
	k := &keys{dry: dry}
	r.keys = k
	return intercept.Add(tab, func(req *intercept.Request) {
		k.mu.Lock()
		defer k.mu.Unlock()
		k.requests++
		if !k.dry || req.Method == http.MethodGet || req.Method == http.MethodHead {
			return
		}
		req.Block()
		// A beacon or a write to another host, such as analytics, is held
		// without ending the flow: only the site's own writes count as the
		// step's.
		if !k.armed || k.held != nil || req.URL == nil || req.Type == proto.NetworkResourceTypePing ||
			(r.o.Base != nil && config.HostKey(req.URL) != config.HostKey(r.o.Base)) {
			return
		}
		k.held = &Held{Method: req.Method, Path: orSlash(req.URL.Path)}
	})
}

func orSlash(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// begin starts a step; armed says it's one whose held write ends the
// flow.
func (k *keys) begin(armed bool) {
	k.mu.Lock()
	k.armed, k.held = armed, nil
	k.mu.Unlock()
}

// end finishes a step and returns what it would have sent, if anything.
func (k *keys) end() *Held {
	k.mu.Lock()
	defer k.mu.Unlock()
	h := k.held
	k.armed, k.held = false, nil
	return h
}

func (k *keys) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.requests
}

func (k *keys) log(line string) {
	k.mu.Lock()
	k.trace = append(k.trace, line)
	k.mu.Unlock()
}

// activates reports whether the press can operate a control: Enter or
// Space.
func (p *Press) activates() bool {
	return p != nil && (p.Key == focus.Enter || p.Key == focus.Space)
}

// keyStep plays one step of a key flow. Steps that act on an element
// reach it with Tab (then Shift+Tab) and operate it with keys; no mouse
// event is ever sent. goto, wait, expect and screenshot work as in any
// flow.
func (r *runner) keyStep(n int, s Step) error {
	switch {
	case s.Click != nil:
		return r.keyActivate(n, "click", *s.Click, false)
	case s.Submit != nil:
		return r.keyActivate(n, "submit", *s.Submit, true)
	case s.Fill != nil:
		return r.keyFill(n, *s.Fill)
	case s.Select != nil:
		return r.keySelect(n, *s.Select)
	case s.Check != nil:
		return r.keyCheck(n, *s.Check)
	case s.Press != nil:
		if s.Press.On != nil {
			if _, _, err := r.reach(n, "press", *s.Press.On); err != nil {
				return err
			}
		}
		if s.Press.Key == focus.Enter && len(r.secrets) > 0 {
			if el, err := r.focused(); err == nil {
				if err := r.sendSecret(el, true); err != nil {
					return err
				}
			}
		}
		if err := r.press(*s.Press); err != nil {
			return err
		}
		r.keys.log(fmt.Sprintf("step %d %s", n, s.Press))
		if s.Press.activates() {
			r.settle()
			return r.stayed()
		}
		return nil
	}
	return r.step(n, s)
}

// press presses p's key p.Times times on whatever has focus.
func (r *runner) press(p Press) error {
	for range max(p.Times, 1) {
		if err := focus.Press(r.ctx, r.page, p.Key); err != nil {
			return fmt.Errorf("couldn't press %s: %w", p.Key, err)
		}
	}
	return nil
}

// reach moves focus to t with Tab, then Shift+Tab (see reachEl). It
// returns t's element and the focused one.
func (r *runner) reach(n int, kind string, t Target) (target, active *rod.Element, err error) {
	el, err := r.element(t)
	if err != nil {
		return nil, nil, err
	}
	active, err = r.reachEl(n, kind, t, el)
	return el, active, err
}

// reachEl moves focus to el with Tab, up to MaxTabs presses, then with
// Shift+Tab, as many again. el is reached when the focused element is el
// or inside it, through shadow roots. It returns the focused element,
// or a *KeyFailure when el isn't reached.
func (r *runner) reachEl(n int, kind string, t Target, el *rod.Element) (*rod.Element, error) {
	inside, err := r.nodes(el)
	if err != nil {
		return nil, err
	}
	var path []string
	tabs := 0
	stop, err := focus.Active(r.ctx, r.page)
	for _, k := range []focus.Key{focus.Tab, focus.ShiftTab} {
		for i := 0; ; i++ {
			if err != nil {
				return nil, err
			}
			if inside[stop.BackendNodeID] && !stop.IsBody {
				r.keys.log(fmt.Sprintf("step %d %s %q: on %s after %d key presses", n, kind, t.raw(), stopName(stop), tabs))
				return r.page.ElementFromNode(&proto.DOMNode{BackendNodeID: proto.DOMBackendNodeID(stop.BackendNodeID)})
			}
			if i == r.o.MaxTabs {
				break
			}
			if err := r.ctx.Err(); err != nil {
				return nil, err
			}
			if err := focus.Press(r.ctx, r.page, k); err != nil {
				return nil, fmt.Errorf("couldn't press %s: %w", k, err)
			}
			tabs++
			stop, err = focus.Active(r.ctx, r.page)
			if err == nil {
				path = append(path, stopName(stop))
				if len(path) > pathLen {
					path = path[1:]
				}
			}
		}
	}
	reason := fmt.Sprintf("not reached after %d Tab stops; focus stayed on %s", r.o.MaxTabs, stopName(stop))
	r.keys.log(fmt.Sprintf("step %d %s %q: %s", n, kind, t.raw(), reason))
	return nil, &KeyFailure{Kind: kind, Target: t.raw(), Reason: reason, Path: path}
}

// raw is the target as the flow names it: its text, else its selector.
func (t Target) raw() string {
	if t.Text != "" {
		return t.Text
	}
	return t.Selector
}

// stopName is the selector of a focus stop, "body" when nothing has
// focus.
func stopName(s focus.Stop) string {
	if s.Selector == "" || s.IsBody {
		return "body"
	}
	return s.Selector
}

// nodes returns the DOM nodes of el and everything inside it, through
// shadow roots, by backend node id: focus on any of them is focus on el.
func (r *runner) nodes(el *rod.Element) (map[int]bool, error) {
	res, err := proto.DOMDescribeNode{ObjectID: el.Object.ObjectID, Depth: intPtr(-1), Pierce: true}.Call(r.page)
	if err != nil {
		return nil, err
	}
	out := map[int]bool{}
	var walk func(*proto.DOMNode)
	walk = func(n *proto.DOMNode) {
		if n == nil {
			return
		}
		out[int(n.BackendNodeID)] = true
		for _, c := range n.Children {
			walk(c)
		}
		for _, c := range n.ShadowRoots {
			walk(c)
		}
	}
	walk(res.Node)
	return out, nil
}

func intPtr(i int) *int { return &i }

// focused returns the element that has focus, through shadow roots.
func (r *runner) focused() (*rod.Element, error) {
	stop, err := focus.Active(r.ctx, r.page)
	if err != nil {
		return nil, err
	}
	if stop.BackendNodeID == 0 {
		return r.page.Timeout(3 * time.Second).Element("body")
	}
	return r.page.ElementFromNode(&proto.DOMNode{BackendNodeID: proto.DOMBackendNodeID(stop.BackendNodeID)})
}

// spaceJS tells whether an element is operated with Space rather than
// Enter: a checkbox, radio, switch or a role=button without href.
const spaceJS = `function() {
  const type = (this.getAttribute('type') || '').toLowerCase();
  const role = (this.getAttribute('role') || '').toLowerCase();
  if (this.localName === 'input' && (type === 'checkbox' || type === 'radio')) return true;
  if (role === 'checkbox' || role === 'radio' || role === 'switch') return true;
  return role === 'button' && !this.hasAttribute('href');
}`

// submitTargetJS finds what a submit step reaches: a form's submit
// button, else its first field (Enter there submits the form), or the
// element itself when it isn't a form.
const submitTargetJS = `function() {
  if (this.tagName !== 'FORM') return this;
  return this.querySelector('[type=submit],button:not([type])') || this.querySelector('input,select,textarea') || this;
}`

// keyActivate reaches a click or submit target and presses Enter on
// it, or Space on a checkbox, radio, switch or role=button.
func (r *runner) keyActivate(n int, kind string, t Target, submit bool) error {
	if submit && t.Selector == "" && t.Text == "" {
		t.Selector = "form"
	}
	el, err := r.element(t)
	if err != nil {
		return err
	}
	if submit {
		// A form holds every field, so focus on any of them would count
		// as reaching it: its submit button (or first field) is reached
		// instead.
		if btn, err := el.ElementByJS(rod.Eval(submitTargetJS)); err == nil {
			el = btn
		}
	}
	active, err := r.reachEl(n, kind, t, el)
	if err != nil {
		return err
	}
	return r.operate(n, kind, t, el, active, submit)
}

// operate presses the key that operates active, the focused element in
// the step's target el. It checks that the key did something.
func (r *runner) operate(n int, kind string, t Target, el, active *rod.Element, submit bool) error {
	if err := r.sendSecret(el, submit); err != nil {
		return err
	}
	key := focus.Enter
	if !submit {
		if res, err := active.Eval(spaceJS); err == nil && res.Value.Bool() {
			key = focus.Space
		}
	}
	did := r.watchEffect()
	if err := focus.Press(r.ctx, r.page, key); err != nil {
		return fmt.Errorf("couldn't press %s: %w", key, err)
	}
	if !did() {
		reason := fmt.Sprintf("reached; %s did nothing", key)
		r.keys.log(fmt.Sprintf("step %d %s %q: %s", n, kind, t.raw(), reason))
		return &KeyFailure{Kind: kind, Target: t.raw(), Reason: reason}
	}
	r.keys.log(fmt.Sprintf("step %d %s %q: %s", n, kind, t.raw(), key))
	r.settle()
	return r.stayed()
}

// effectJS starts watching the page for what a key press does: any DOM
// change, typing, a form sent or the URL's hash moving. It keeps what it
// saw in window[name].
const effectJS = `(name) => {
  const st = {hit: false};
  const hit = () => { st.hit = true; };
  st.o = new MutationObserver(hit);
  st.o.observe(document, {subtree: true, childList: true, attributes: true, characterData: true});
  for (const e of ['input', 'change', 'submit', 'hashchange', 'popstate', 'toggle']) document.addEventListener(e, hit, true);
  window[name] = st;
}`

// watchEffect starts watching what the next key press does. The
// function it returns waits up to effectWait and reports whether the
// page changed, navigated, moved focus or sent a request.
func (r *runner) watchEffect() func() bool {
	name := "__soundcheckKey" + strconv.FormatInt(time.Now().UnixNano(), 36)
	reqs := r.keys.count()
	before, _ := r.page.Timeout(3 * time.Second).Info()
	stop, _ := focus.Active(r.ctx, r.page)
	_, _ = r.page.Timeout(3*time.Second).Eval(effectJS, name)
	return func() bool {
		deadline := time.Now().Add(effectWait)
		for {
			if r.keys.count() != reqs {
				return true
			}
			// A new document has no window[name]: the page navigated, as
			// does an Eval that fails because the old one went away.
			res, err := r.page.Timeout(500*time.Millisecond).Eval(`n => window[n] ? window[n].hit : 'gone'`, name)
			if err != nil || res.Value.Bool() || res.Value.Str() == "gone" {
				return true
			}
			if info, err := r.page.Timeout(time.Second).Info(); err == nil && before != nil && info.URL != before.URL {
				return true
			}
			if now, err := focus.Active(r.ctx, r.page); err == nil && now.BackendNodeID != stop.BackendNodeID {
				return true
			}
			if time.Now().After(deadline) || r.ctx.Err() != nil {
				return false
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// keyFill reaches a field with Tab and types into it with
// Input.insertText, as an input method commits text: no key events
// carry the value, so nothing that logs keys sees it.
func (r *runner) keyFill(n int, in Input) error {
	v, err := r.expandToType(in.Value)
	if err != nil {
		return err
	}
	_, active, err := r.reach(n, "fill", Target{Selector: in.Selector})
	if err != nil {
		return err
	}
	// Selecting the old text lets the typing replace it.
	_, _ = active.Timeout(r.o.Timeout).Eval(`function() { if (this.select) this.select(); }`)
	if err := (proto.InputInsertText{Text: v}).Call(r.page); err != nil {
		return fmt.Errorf("couldn't type into it: %w", err)
	}
	r.keys.log(fmt.Sprintf("step %d fill %q: <typed %d characters>", n, in.Selector, utf8.RuneCountInString(v)))
	return nil
}

// optionsJS finds the option a select step wants, by value then label,
// and where the select is now.
const optionsJS = `function(v) {
  const os = [...this.options];
  let want = os.findIndex(o => o.value === v);
  if (want < 0) want = os.findIndex(o => o.text.trim() === v);
  return {want, cur: this.selectedIndex, n: os.length};
}`

// keySelect reaches a select and moves through its options with
// ArrowDown (or ArrowUp, for an option above the current one) until the
// wanted one is selected.
func (r *runner) keySelect(n int, in Input) error {
	v, err := r.expandToType(in.Value)
	if err != nil {
		return err
	}
	el, _, err := r.reach(n, "select", Target{Selector: in.Selector})
	if err != nil {
		return err
	}
	var st struct{ Want, Cur, N int }
	read := func() error {
		res, err := el.Timeout(r.o.Timeout).Eval(optionsJS, v)
		if err != nil {
			return err
		}
		return res.Value.Unmarshal(&st)
	}
	if err := read(); err != nil {
		return err
	}
	if st.Want < 0 {
		return errors.New("no option with that value or label")
	}
	key := focus.ArrowDown
	if st.Want < st.Cur {
		key = focus.ArrowUp
	}
	for range st.N + 1 {
		if st.Cur == st.Want {
			r.keys.log(fmt.Sprintf("step %d select %q: %s", n, in.Selector, key))
			return nil
		}
		if err := focus.Press(r.ctx, r.page, key); err != nil {
			return fmt.Errorf("couldn't press %s: %w", key, err)
		}
		if err := read(); err != nil {
			return err
		}
	}
	if st.Cur == st.Want {
		return nil
	}
	reason := fmt.Sprintf("reached; %s didn't select the option", key)
	return &KeyFailure{Kind: "select", Target: in.Selector, Reason: reason}
}

// checkedJS reads whether a checkbox, radio or role=checkbox is checked.
const checkedJS = `function() {
  return this.checked !== undefined ? !!this.checked : this.getAttribute('aria-checked') === 'true';
}`

// keyCheck reaches a checkbox or radio and presses Space when it isn't
// as the step wants it.
func (r *runner) keyCheck(n int, c Toggle) error {
	el, _, err := r.reach(n, "check", Target{Selector: c.Selector})
	if err != nil {
		return err
	}
	want := c.Checked == nil || *c.Checked
	read := func() (bool, error) {
		res, err := el.Timeout(r.o.Timeout).Eval(checkedJS)
		if err != nil {
			return false, err
		}
		return res.Value.Bool(), nil
	}
	cur, err := read()
	if err != nil || cur == want {
		return err
	}
	if err := focus.Press(r.ctx, r.page, focus.Space); err != nil {
		return fmt.Errorf("couldn't press Space: %w", err)
	}
	if cur, err = read(); err != nil {
		return err
	}
	if cur != want {
		reason := "reached; Space did nothing"
		r.keys.log(fmt.Sprintf("step %d check %q: %s", n, c.Selector, reason))
		return &KeyFailure{Kind: "check", Target: c.Selector, Reason: reason}
	}
	r.keys.log(fmt.Sprintf("step %d check %q: Space", n, c.Selector))
	return nil
}

// matchJS reports whether the element, or with within any element it is
// inside through shadow roots, matches a selector. A selector with
// " >>> " names one element through shadow roots, as pierceJS finds it.
const matchJS = `function(sel, within) {
  let test = n => n.nodeType === 1 && n.matches(sel);
  if (sel.includes('>>>')) {
    let root = document, el = null;
    for (const part of sel.split('>>>')) {
      el = root && root.querySelector(part.trim());
      root = el && (el.shadowRoot || el);
    }
    test = n => !!el && n === el;
  }
  for (let n = this; n; n = within ? (n.parentNode || n.host) : null) {
    if (test(n)) return true;
  }
  return false;
}`

// expectFocus checks that the focused element matches sel, or with
// within that it is inside an element that does; with absent, that it
// doesn't.
func (r *runner) expectFocus(sel string, within, absent bool) error {
	stop, err := focus.Active(r.ctx, r.page)
	if err != nil {
		return err
	}
	el, err := r.focused()
	if err != nil {
		return err
	}
	res, err := el.Timeout(3*time.Second).Eval(matchJS, sel, within)
	if err != nil {
		return err
	}
	on := stopName(stop)
	switch got := res.Value.Bool(); {
	case got && absent && within:
		return fmt.Errorf("focus is still inside %s (on %s)", sel, on)
	case got && absent:
		return fmt.Errorf("focus is still on %s", sel)
	case !got && !absent && within:
		return fmt.Errorf("focus isn't inside %s; it is on %s", sel, on)
	case !got && !absent:
		return fmt.Errorf("focus is on %s, not %s", on, sel)
	}
	return nil
}
