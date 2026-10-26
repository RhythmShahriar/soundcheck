// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
)

// listen serves h on a free port of 37040-37059, the ports this
// package's key flow tests use, so they don't take another suite's.
func listen(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	for port := 37040; port <= 37059; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	t.Fatal("ports 37040-37059 are all taken")
	return nil
}

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

func needChrome(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// canaryEnv is a variable the flow may read (SOUNDCHECK_*) holding a
// password assembled at run time, so no literal secret is in the source.
const canaryEnv = "SOUNDCHECK_KEYS_CANARY"

var canary = "Cn" + "ry-" + strings.Repeat("7", 6) + "-pw"

func keyOptions(base string) Options {
	u, _ := url.Parse(base)
	return Options{Base: u, Timeout: testenv.Timeout(3 * time.Second), Fake: Fake{Seed: "k"}, MaxTabs: 12,
		Env: func(k string) string { return map[string]string{canaryEnv: canary}[k] }}
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

func playOn(t *testing.T, s *shop, mode string, o Options) (Result, *rod.Page) {
	t.Helper()
	f, err := Parse([]byte(fmt.Sprintf(checkoutFlow, mode)))
	if err != nil {
		t.Fatal(err)
	}
	page := isolatedTab(t, s.URL+"/")
	return Play(context.Background(), page, f, o), page
}

func describe(res Result) string {
	var out []string
	for _, f := range res.Failures {
		out = append(out, fmt.Sprintf("%d %s: %v", f.Step, f.Desc, f.Err))
	}
	return strings.Join(out, "\n")
}

// A checkout a keyboard user can finish passes: every field is reached
// with Tab, the select is set with the arrow keys, the checkbox with
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
	if !strings.Contains(trace, `step 10 click "#place_order": Enter`) || !strings.Contains(trace, `step 4 check "#terms": Space`) {
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
			if res.Held == nil || *res.Held != (Held{Step: 10, Method: "POST", Path: "/order/"}) {
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
	f, err := Parse([]byte(`name: Keys by mouse
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
	got := describe(Play(context.Background(), page, f, keyOptions(s.URL)))
	want := "7 expect focus #email: focus is on #terms, not #email\n" +
		"8 expect focus within nav: focus isn't inside nav; it is on #terms"
	if got != want {
		t.Errorf("failures:\n%s\nwant:\n%s", got, want)
	}
	if res, err := page.Eval(`() => document.getElementById('terms').checked`); err != nil || !res.Value.Bool() {
		t.Errorf("Space didn't check the box: %v %v", res, err)
	}
}
