// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyflow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/go-rod/rod"
)

// shop is a checkout page that counts the orders placed. button is the
// Place order control, so a variant can make it mouse-only.
type shop struct {
	*httptest.Server
	mu     sync.Mutex
	orders []url.Values
}

const checkoutPage = `<!doctype html><html lang="en"><body>
<a href="/">Home</a>
<form id="checkout" method="post" action="/order/?step=pay">
<label>Email <input id="email" name="email" type="email"></label>
<label>Password <input id="pass" name="pass" type="password"></label>
<label>Country <select id="country" name="country"><option value="">Pick</option><option value="DE">Germany</option><option value="NZ">New Zealand</option></select></label>
<label><input type="checkbox" id="terms" name="terms"> I agree</label>
<x-pay></x-pay>
<input type="hidden" name="later" id="later" value="">
%s
</form>
<p id="note"></p>
<script>
window.mouse = 0;
document.addEventListener('mousedown', () => window.mouse++, true);
customElements.define('x-pay', class extends HTMLElement {
  connectedCallback() {
    this.attachShadow({mode: 'open'}).innerHTML = '<button type="button">Pay later</button>';
    this.shadowRoot.querySelector('button').addEventListener('click', () => {
      document.getElementById('later').value = 'yes';
      document.getElementById('note').textContent = 'Paying later';
    });
  }
});
</script>
</body></html>`

const (
	placeButton  = `<button id="place_order">Place order</button>`
	placeMouse   = `<div id="place_order" onclick="this.closest('form').submit()">Place order</div>`
	placeNothing = `<span id="place_order" role="button" tabindex="0" onmousedown="this.closest('form').submit()">Place order</span>`
)

func newShop(t *testing.T, button string) *shop {
	t.Helper()
	s := &shop{}
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `<html><body>Home</body></html>`) })
	mux.HandleFunc("/checkout/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, checkoutPage, button) })
	mux.HandleFunc("/order/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			s.mu.Lock()
			s.orders = append(s.orders, r.PostForm)
			s.mu.Unlock()
		}
		fmt.Fprint(w, `<html><body><h1>Thank you</h1></body></html>`)
	})
	s.Server = listen(t, mux)
	return s
}

func (s *shop) placed() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.orders...)
}

const checkoutFlow = `name: Checkout by keyboard
start: /checkout/
keyboard: %s
steps:
  - fill: {selector: "#email", value: "${fake:email}"}
  - fill: {selector: "#pass", value: "${env:SOUNDCHECK_KEYS_CANARY}"}
  - select: {selector: "#country", value: New Zealand}
  - check: "#terms"
  - click: x-pay
  - expect: {text: Paying later, focus_within: "x-pay"}
  - expect: {focus: "x-pay >>> button"}
  - press: {key: Shift+Tab}
  - expect: {focus_within: "#checkout"}
  - click: "#place_order"
  - expect: {text: Thank you, url: /order/}
`

func playOn(t *testing.T, s *shop, mode string, o flow.Options) (flow.Result, *rod.Page) {
	t.Helper()
	f, err := flow.Parse([]byte(fmt.Sprintf(checkoutFlow, mode)))
	if err != nil {
		t.Fatal(err)
	}
	page := isolatedTab(t, s.URL+"/")
	return flow.Play(context.Background(), page, f, o), page
}

// A checkout a keyboard user can finish passes: every field is reached
// with Tab, the select is set by typing its option's label, the checkbox with
// Space, the button in a shadow root and Place order with Enter. No
// mouse event is sent and the password typed is in no trace.
func TestKeyFlowFinishes(t *testing.T) {
	needChrome(t)
	s := newShop(t, placeButton)
	res, page := playOn(t, s, "send", keyOptions(s.URL))
	if len(res.Failures) != 0 || res.Held != nil {
		t.Fatalf("failures:\n%s\nheld %+v\ntrace:\n%s", describe(res), res.Held, strings.Join(res.Trace, "\n"))
	}
	orders := s.placed()
	if len(orders) != 1 {
		t.Fatalf("orders: %v", orders)
	}
	o := orders[0]
	if o.Get("country") != "NZ" || o.Get("terms") != "on" || o.Get("later") != "yes" || o.Get("pass") != canary ||
		!strings.HasPrefix(o.Get("email"), "soundcheck-test+") {
		t.Errorf("order: %v", o)
	}
	if res, err := page.Eval(`() => window.mouse`); err != nil || res.Value.Int() != 0 {
		t.Errorf("mouse events: %v %v", res, err)
	}
	trace := strings.Join(res.Trace, "\n")
	if typed := fmt.Sprintf(`step 2 fill "#pass": <typed %d characters>`, len(canary)); strings.Contains(trace, canary) || !strings.Contains(trace, typed) {
		t.Errorf("trace:\n%s", trace)
	}
	if !strings.Contains(trace, `step 10 click "#place_order": Enter`) || !strings.Contains(trace, `step 4 check "#terms": Space`) ||
		!strings.Contains(trace, `step 3 select "#country": <typed 11 characters>`) {
		t.Errorf("trace:\n%s", trace)
	}
}

// A Place order only a mouse can use is a key failure naming the step,
// with the focus path. A control that Tab reaches but whose key does
// nothing is one too.
func TestKeyFlowStuck(t *testing.T) {
	needChrome(t)
	for _, tc := range []struct {
		name, button, reason string
		path                 bool
	}{
		{"mouse only", placeMouse, "not reached after 12 Tab stops; focus stayed on ", true},
		{"key does nothing", placeNothing, "reached; Space did nothing", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newShop(t, tc.button)
			res, _ := playOn(t, s, "send", keyOptions(s.URL))
			if len(res.Failures) != 1 {
				t.Fatalf("failures:\n%s", describe(res))
			}
			f := res.Failures[0]
			if f.Step != 10 || f.Key == nil || f.Key.Kind != "click" || f.Key.Target != "#place_order" ||
				!strings.HasPrefix(f.Key.Reason, tc.reason) || f.Err.Error() != f.Key.Reason {
				t.Fatalf("failure: %+v %+v", f, f.Key)
			}
			if tc.path && len(f.Key.Path) != pathLen {
				t.Errorf("path: %q", f.Key.Path)
			}
			if !tc.path && len(f.Key.Path) != 0 {
				t.Errorf("a reached target has a path: %q", f.Key.Path)
			}
			if len(s.placed()) != 0 {
				t.Errorf("an order was placed: %v", s.placed())
			}
			if strings.Contains(describe(res)+strings.Join(res.Trace, "\n"), canary) {
				t.Error("the canary leaked")
			}
		})
	}
}

// A select whose keys the page swallows is a key failure on its step:
// neither typing the label nor the arrows can set it.
func TestKeyFlowSelectStuck(t *testing.T) {
	needChrome(t)
	s := newShop(t, placeButton+`<script>document.getElementById('country').addEventListener('keydown', e => e.preventDefault());</script>`)
	res, _ := playOn(t, s, "send", keyOptions(s.URL))
	if len(res.Failures) != 1 {
		t.Fatalf("failures:\n%s", describe(res))
	}
	f := res.Failures[0]
	if f.Step != 3 || f.Key == nil || f.Key.Kind != "select" || f.Key.Target != "#country" ||
		f.Key.Reason != "reached; typing its label or ArrowDown didn't select the option" {
		t.Fatalf("failure: %+v %+v", f, f.Key)
	}
	if len(s.placed()) != 0 {
		t.Errorf("an order was placed: %v", s.placed())
	}
}

// Where typing the label lands on an earlier option that starts the same
// way, the arrows go on to the wanted one. Not on macOS: there the
// arrows open the option list (and keySelect counts on the label), so
// this fallback can't move the value.
func TestKeyFlowSelectArrowsOn(t *testing.T) {
	needChrome(t)
	if runtime.GOOS == "darwin" {
		t.Skip("macOS arrows open a select's list instead of moving its value")
	}
	s := newShop(t, placeButton+`<script>document.getElementById('country').add(new Option('New Zealand Islands', 'NZI'), 2);</script>`)
	res, _ := playOn(t, s, "send", keyOptions(s.URL))
	if len(res.Failures) != 0 {
		t.Fatalf("failures:\n%s\ntrace:\n%s", describe(res), strings.Join(res.Trace, "\n"))
	}
	if orders := s.placed(); len(orders) != 1 || orders[0].Get("country") != "NZ" {
		t.Errorf("orders: %v", orders)
	}
	if trace := strings.Join(res.Trace, "\n"); !strings.Contains(trace, `step 3 select "#country": ArrowDown`) {
		t.Errorf("trace:\n%s", trace)
	}
}

// keyboard: true holds the order: Place order passes, records what it
// would have sent and ends the flow. Options.Dry does the same for a
// keyboard: send flow. A mouse flow ignores Dry.
func TestKeyFlowDry(t *testing.T) {
	needChrome(t)
	for _, tc := range []struct {
		name, mode string
		dry        bool
	}{{"keyboard true", "true", false}, {"send made dry", "send", true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newShop(t, placeButton)
			o := keyOptions(s.URL)
			o.Dry = tc.dry
			res, _ := playOn(t, s, tc.mode, o)
			if len(res.Failures) != 0 {
				t.Errorf("failures:\n%s", describe(res))
			}
			if res.Held == nil || *res.Held != (flow.Held{Step: 10, Method: "POST", Path: "/order/"}) {
				t.Errorf("held: %+v", res.Held)
			}
			if n := len(s.placed()); n != 0 {
				t.Errorf("%d orders placed", n)
			}
		})
	}
}

// Press and the focus expects work in a mouse flow too.
func TestPressInMouseFlow(t *testing.T) {
	needChrome(t)
	s := newShop(t, placeButton)
	f, err := flow.Parse([]byte(`name: Keys by mouse
start: /checkout/
steps:
  - press: {key: Tab, on: "#email"}
  - expect: {focus: "#pass", focus_within: form}
  - press: {key: Tab, times: 2}
  - expect: {focus: "#terms"}
  - press: Space
  - expect: {focus: "#email", absent: true}
  - expect: {focus: "#email"}
  - expect: {focus_within: nav}
`))
	if err != nil {
		t.Fatal(err)
	}
	page := isolatedTab(t, s.URL+"/")
	got := describe(flow.Play(context.Background(), page, f, keyOptions(s.URL)))
	want := "7 expect focus #email: focus is on #terms, not #email\n" +
		"8 expect focus within nav: focus isn't inside nav; it is on #terms"
	if got != want {
		t.Errorf("failures:\n%s\nwant:\n%s", got, want)
	}
	if res, err := page.Eval(`() => document.getElementById('terms').checked`); err != nil || !res.Value.Bool() {
		t.Errorf("Space didn't check the box: %v %v", res, err)
	}
}

// targetsPage has click targets a mouse flow names by what it clicks: a
// label tied to its checkbox, a span inside a button and a label whose
// checkbox Tab can't reach.
const targetsPage = `<!doctype html><html lang="en"><body>
<form onsubmit="return false">
<input type="checkbox" id="terms"><label for="terms">I accept the terms</label>
<button type="button" id="b" onclick="document.getElementById('note').textContent = 'Pressed'"><span class="t">Go</span></button>
<input type="checkbox" id="hid" tabindex="-1"><label for="hid">Mouse only box</label>
</form>
<p id="note"></p>
</body></html>`

// A key flow reaches the control a click target stands for: the
// checkbox of a label and the button a span sits in. A control Tab
// can't reach still fails.
func TestKeyFlowClickTargets(t *testing.T) {
	needChrome(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, targetsPage) })
	srv := listen(t, mux)
	play := func(steps string) (flow.Result, *rod.Page) {
		t.Helper()
		f, err := flow.Parse([]byte("name: Targets\nstart: /\nkeyboard: true\nsteps:\n" + steps))
		if err != nil {
			t.Fatal(err)
		}
		page := isolatedTab(t, srv.URL+"/")
		return flow.Play(context.Background(), page, f, keyOptions(srv.URL)), page
	}
	checked := func(page *rod.Page, id string) bool {
		t.Helper()
		res, err := page.Eval(`id => document.getElementById(id).checked`, id)
		if err != nil {
			t.Fatal(err)
		}
		return res.Value.Bool()
	}

	res, page := play("  - click: {text: I accept the terms}\n")
	if len(res.Failures) != 0 || !checked(page, "terms") {
		t.Errorf("label: checked %v, failures:\n%s", checked(page, "terms"), describe(res))
	}
	res, _ = play("  - click: \"#b .t\"\n  - expect: {text: Pressed}\n")
	if len(res.Failures) != 0 {
		t.Errorf("span in a button:\n%s", describe(res))
	}
	res, page = play("  - click: {text: Mouse only box}\n")
	if len(res.Failures) != 1 || res.Failures[0].Key == nil || !strings.HasPrefix(res.Failures[0].Key.Reason, "not reached after") || checked(page, "hid") {
		t.Errorf("unreachable checkbox:\n%s", describe(res))
	}
}
