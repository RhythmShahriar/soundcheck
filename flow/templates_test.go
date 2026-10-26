// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// The four templates parse as dry key flows and start with the comment
// that explains them, not a license header; each matches its golden file
// in testdata/templates (go test -run TestKeyboardTemplates -update
// rewrites them).
func TestKeyboardTemplates(t *testing.T) {
	tpl := KeyboardTemplates()
	names := slices.Sorted(maps.Keys(tpl))
	want := []string{"keyboard-checkout.yaml", "keyboard-contact.yaml", "keyboard-dialog.yaml", "keyboard-menu.yaml"}
	if !slices.Equal(names, want) {
		t.Fatalf("templates %q, want %q", names, want)
	}
	for _, name := range names {
		b := tpl[name]
		f, err := Parse(b)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if f.Keyboard != KeyboardDry || len(f.Steps) == 0 {
			t.Errorf("%s: keyboard %q, %d steps", name, f.Keyboard, len(f.Steps))
		}
		if !bytes.HasPrefix(b, []byte("# A Soundcheck key flow:")) || bytes.Contains(b, []byte("SPDX")) ||
			!bytes.Contains(b, []byte("keyboard: send")) {
			t.Errorf("%s doesn't start with its comment, or doesn't explain send:\n%s", name, b)
		}
		golden := filepath.Join("testdata", "templates", name+".golden")
		if *update {
			if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, b, 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		g, err := os.ReadFile(golden) //nolint:gosec // the package's own testdata
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, g) {
			t.Errorf("%s differs from %s; run with -update if the change is meant", name, golden)
		}
	}
}

// withoutHeader drops only a leading copyright and SPDX pair.
func TestWithoutHeader(t *testing.T) {
	for in, want := range map[string]string{
		"# Copyright (c) 2026 X\n# SPDX-License-Identifier: MIT\n\n# A flow\nname: x\n": "# A flow\nname: x\n",
		"# Copyright (c) 2026 X\n# SPDX-License-Identifier: MIT\nname: x\n":             "name: x\n",
		"# A flow\nname: x\n":                     "# A flow\nname: x\n",
		"# Copyright (c) 2026 X\nname: x\n":       "# Copyright (c) 2026 X\nname: x\n",
		"# SPDX-License-Identifier: MIT\nname: x": "# SPDX-License-Identifier: MIT\nname: x",
		"": "",
	} {
		if got := string(withoutHeader([]byte(in))); got != want {
			t.Errorf("withoutHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

// templateSite is a small site the templates fit: a WooCommerce-like
// checkout that places its order with a script, a contact form after a
// search form, a menu toggle and a dialog.
type templateSite struct {
	url    string
	orders atomic.Int32
	sent   atomic.Int32
}

const templateHome = `<!doctype html><html lang="en"><body>
<header>
<button class="menu-toggle" aria-controls="nav" aria-expanded="false">Menu</button>
<nav id="nav" hidden><a href="/">Home</a> <a href="/contact/">Contact</a></nav>
</header>
<main>
<button id="open" aria-haspopup="dialog">Newsletter</button>
<div id="dlg" role="dialog" aria-modal="true" aria-label="Newsletter" hidden><p>Sign up</p><button id="close">Close</button></div>
</main>
<script>
const toggle = document.querySelector('.menu-toggle'), nav = document.getElementById('nav');
toggle.addEventListener('click', () => {
  const open = toggle.getAttribute('aria-expanded') !== 'true';
  toggle.setAttribute('aria-expanded', String(open));
  nav.hidden = !open;
});
const opener = document.getElementById('open'), dlg = document.getElementById('dlg');
opener.addEventListener('click', () => { dlg.hidden = false; document.getElementById('close').focus(); });
dlg.addEventListener('keydown', e => { if (e.key === 'Escape') { dlg.hidden = true; opener.focus(); } });
</script>
</body></html>`

const templateCheckout = `<!doctype html><html lang="en"><body>
<form class="checkout woocommerce-checkout" method="post" action="/checkout/">
<p><label for="billing_first_name">First name</label> <input id="billing_first_name" name="billing_first_name"></p>
<p><label for="billing_last_name">Last name</label> <input id="billing_last_name" name="billing_last_name"></p>
<p><label for="billing_address_1">Street</label> <input id="billing_address_1" name="billing_address_1"></p>
<p><label for="billing_city">Town</label> <input id="billing_city" name="billing_city"></p>
<p><label for="billing_postcode">Postcode</label> <input id="billing_postcode" name="billing_postcode"></p>
<p><label for="billing_phone">Phone</label> <input id="billing_phone" name="billing_phone" type="tel"></p>
<p><label for="billing_email">Email</label> <input id="billing_email" name="billing_email" type="email"></p>
<p><label><input type="checkbox" id="terms" name="terms"> I have read the terms</label></p>
<button type="submit" id="place_order" name="woocommerce_checkout_place_order">Place order</button>
</form>
<div id="done"></div>
<script>
document.querySelector('form.checkout').addEventListener('submit', async ev => {
  ev.preventDefault();
  const r = await fetch('/?wc-ajax=checkout', {method: 'POST', body: new FormData(ev.target)});
  if (r.ok) document.getElementById('done').textContent = 'Thank you. Your order has been received.';
});
</script>
</body></html>`

const templateContact = `<!doctype html><html lang="en"><body>
<form role="search" action="/"><input name="s" aria-label="Search"></form>
<form class="wpcf7-form" method="post" action="/contact/">
<label>Your name <input name="your-name" autocomplete="name"></label>
<label>Your email <input name="your-email" type="email"></label>
<label>Your message <textarea name="your-message"></textarea></label>
<button>Send</button>
</form>
</body></html>`

func newTemplateSite(t *testing.T) *templateSite {
	t.Helper()
	s := &templateSite{}
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Get("wc-ajax") == "checkout" {
			s.orders.Add(1)
			return
		}
		fmt.Fprint(w, templateHome)
	})
	mux.HandleFunc("/checkout/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, templateCheckout) })
	mux.HandleFunc("/contact/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if r.PostForm.Get("your-name") != "" && strings.Contains(r.PostForm.Get("your-email"), "@") && r.PostForm.Get("your-message") != "" {
				s.sent.Add(1)
			}
			fmt.Fprint(w, `<html><body><p>Thank you for your message.</p></body></html>`)
			return
		}
		fmt.Fprint(w, templateContact)
	})
	s.url = listen(t, mux).URL
	return s
}

// Every template runs clean on a site it fits: dry, the checkout and the
// contact form stop at what they would send and send nothing; with
// keyboard: send they place one order and send one message. The menu
// and dialog have nothing to send and finish either way.
func TestKeyboardTemplatesRun(t *testing.T) {
	needChrome(t)
	site := newTemplateSite(t)
	for _, tc := range []struct {
		file string
		held int // the step a dry run stops at; 0 when nothing is sent
	}{
		{"keyboard-checkout.yaml", 11},
		{"keyboard-contact.yaml", 4},
		{"keyboard-menu.yaml", 0},
		{"keyboard-dialog.yaml", 0},
	} {
		for _, mode := range []string{KeyboardDry, KeyboardSend} {
			t.Run(tc.file+" "+mode, func(t *testing.T) {
				f, err := Parse(KeyboardTemplates()[tc.file])
				if err != nil {
					t.Fatal(err)
				}
				f.Keyboard = mode
				orders, sent := site.orders.Load(), site.sent.Load()
				page := isolatedTab(t, site.url+"/")
				res := Play(context.Background(), page, f, keyOptions(site.url))
				if len(res.Failures) != 0 || len(res.Unlinked) != 0 {
					t.Fatalf("failures:\n%s\nunlinked %+v\ntrace:\n%s", describe(res), res.Unlinked, strings.Join(res.Trace, "\n"))
				}
				dry := mode == KeyboardDry
				if held := res.Held != nil; held != (dry && tc.held > 0) || (held && res.Held.Step != tc.held) {
					t.Errorf("held %+v", res.Held)
				}
				placed, mailed := site.orders.Load()-orders, site.sent.Load()-sent
				wantOrders, wantSent := int32(0), int32(0)
				if !dry && tc.file == "keyboard-checkout.yaml" {
					wantOrders = 1
				}
				if !dry && tc.file == "keyboard-contact.yaml" {
					wantSent = 1
				}
				if placed != wantOrders || mailed != wantSent {
					t.Errorf("%d orders and %d messages, want %d and %d", placed, mailed, wantOrders, wantSent)
				}
			})
		}
	}
}
