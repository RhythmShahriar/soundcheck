// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
)

// A form posting to another site (a newsletter service) is skipped and
// that site gets nothing; a form posting to this site is still sent.
func TestSubmitSkipsOffSiteForms(t *testing.T) {
	needBrowser(t)
	var mu sync.Mutex
	var elsewhere, here []string
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		elsewhere = append(elsewhere, r.Method+" "+r.URL.Path)
		mu.Unlock()
	}))
	defer list.Close()
	fx := newFixture(t)
	fx.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mu.Lock()
			here = append(here, r.URL.Path)
			mu.Unlock()
			fmt.Fprint(w, `<!doctype html><title>Thanks</title><p>Thanks for joining.</p>`)
			return
		}
		fmt.Fprintf(w, `<!doctype html><html><head><title>News</title></head><body>
<form id="mc" method="post" action="%s/subscribe/post"><h2>Newsletter</h2><input type="email" name="EMAIL" required><button>Sign up</button></form>
<form id="club" method="post" action="/join/"><h2>Club</h2><input name="name" required><input type="email" name="email" required><button>Join</button></form>
</body></html>`, list.URL)
	})
	cfg := config.Default()
	cfg.Interactions.Levels = []string{"submit"}
	p := fx.run(t, cfg, "/").Page("/")
	got := map[string]string{}
	for _, i := range p.Interact {
		if i.Kind == "submit" {
			got[i.Label] = i.Outcome + ": " + i.Detail
		}
	}
	host := strings.TrimPrefix(list.URL, "http://")
	if got["form #mc"] != "skipped: posts to "+host {
		t.Errorf("newsletter: %q (all %v)", got["form #mc"], got)
	}
	if !strings.HasPrefix(got["form #club"], "success") {
		t.Errorf("club: %q", got["form #club"])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(elsewhere) != 0 {
		t.Errorf("the other site got %v", elsewhere)
	}
	if len(here) == 0 || here[len(here)-1] != "/join/" {
		t.Errorf("this site got %v", here)
	}
}

// checkoutPage is a WooCommerce checkout with the given payment methods,
// the first one preselected, as Woo renders them.
func checkoutPage(methods ...[2]string) string {
	var b strings.Builder
	for i, m := range methods {
		checked := ""
		if i == 0 {
			checked = " checked"
		}
		fmt.Fprintf(&b, `<li class="wc_payment_method payment_method_%[1]s"><input id="payment_method_%[1]s" type="radio" class="input-radio" name="payment_method" value="%[1]s"%[3]s>
<label for="payment_method_%[1]s">%[1]s</label><div class="payment_box" style="display:none">%[2]s</div></li>`, m[0], m[1], checked)
	}
	return `<!doctype html><html><head><title>Checkout</title></head><body>
<form name="checkout" method="post" class="checkout woocommerce-checkout" action="/checkout/">
<p><label for="billing_first_name">First name</label><input id="billing_first_name" name="billing_first_name" required></p>
<p><label for="billing_email">Email</label><input type="email" id="billing_email" name="billing_email" required></p>
<div id="payment"><ul class="wc_payment_methods payment_methods methods">` + b.String() + `</ul>
<button type="submit" id="place_order">Place order</button></div>
</form></body></html>`
}

// Woo checkout is submitted only with a payment method that takes no
// money selected, never with the site's default live gateway.
func TestWooCheckoutGateway(t *testing.T) {
	needBrowser(t)
	var mu sync.Mutex
	var paid []string
	page := ""
	fx := newFixture(t)
	fx.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			mu.Lock()
			paid = append(paid, r.PostForm.Get("payment_method"))
			mu.Unlock()
			if r.PostForm.Get("billing_email") == "" {
				fmt.Fprint(w, `<!doctype html><title>Checkout</title><ul class="woocommerce-error"><li>Billing email is required.</li></ul>`)
				return
			}
			fmt.Fprint(w, `<!doctype html><title>Order received</title><p class="woocommerce-order-received">Thank you. Your order has been received.</p>`)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(w, page)
	})
	cfg := config.Default()
	cfg.Interactions.Levels = []string{"submit"}
	for _, c := range []struct {
		name    string
		methods [][2]string
		want    string // the outcome, or the gateway posted
	}{
		{"sandbox listed", [][2]string{{"stripe", "Pay with your card."}, {"ppcp", "PayPal Sandbox mode is on."}}, "ppcp"},
		{"offline listed", [][2]string{{"stripe", "Pay with your card."}, {"cod", "Pay with cash upon delivery."}}, "cod"},
		{"live only", [][2]string{{"stripe", "Pay with your card."}, {"ppcp", "Pay with PayPal."}}, "skipped"},
	} {
		mu.Lock()
		page, paid = checkoutPage(c.methods...), nil
		mu.Unlock()
		p := fx.run(t, cfg, "/checkout/").Page("/checkout/")
		mu.Lock()
		posted := append([]string(nil), paid...)
		mu.Unlock()
		outcome := outcomes(p, "submit")["WooCommerce checkout"]
		if c.want == "skipped" {
			if outcome != "skipped" || len(posted) != 0 {
				t.Errorf("%s: outcome %q, posted %v (%+v)", c.name, outcome, posted, p.Interact)
			}
			continue
		}
		if outcome != "success" || len(posted) == 0 {
			t.Errorf("%s: outcome %q, posted %v (%+v)", c.name, outcome, posted, p.Interact)
		}
		for _, g := range posted {
			if g != c.want {
				t.Errorf("%s: posted with %q, want %q", c.name, g, c.want)
			}
		}
	}
}
