// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
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

// A form whose submit button sends it to another site with formaction
// is skipped. One whose script posts its test data elsewhere is held:
// the other site gets none of it and the run names what was held.
func TestSubmitSkipsOffSiteFormAction(t *testing.T) {
	needBrowser(t)
	var mu sync.Mutex
	var elsewhere []string
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		elsewhere = append(elsewhere, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}))
	defer list.Close()
	fx := newFixture(t)
	fx.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `<!doctype html><title>Thanks</title><p>Thanks for joining.</p>`)
			return
		}
		fmt.Fprintf(w, `<!doctype html><html><head><title>News</title></head><body>
<form id="fa" method="post" action=""><h2>Newsletter</h2><input type="email" name="email" required><input name="name" required>
<button formaction="%[1]s/subscribe">Sign up</button></form>
<form id="js" method="post" action="/join/"><h2>Club</h2><input type="email" name="email" required>
<button>Join</button></form>
<script>document.getElementById('js').addEventListener('submit', async e => {
  e.preventDefault();
  // A CAPTCHA-like check elsewhere carries no test data and goes on.
  const ok = await fetch('%[1]s/verify', {method: 'POST', body: 'token'}).then(r => r.ok).catch(() => false);
  fetch('%[1]s/collect', {method: 'POST', body: new FormData(e.target)}).catch(() => {});
  document.body.insertAdjacentHTML('beforeend', ok ? '<p class="sent">Thanks</p>' : '<p>Verification failed.</p>');
});</script>
</body></html>`, list.URL)
	})
	cfg := config.Default()
	cfg.Interactions.Levels = []string{"submit"}
	run := fx.run(t, cfg, "/")
	p := run.Page("/")
	got := map[string]string{}
	for _, i := range p.Interact {
		if i.Kind == "submit" {
			got[i.Label] = i.Outcome + ": " + i.Detail
		}
	}
	host := strings.TrimPrefix(list.URL, "http://")
	if got["form #fa"] != "skipped: posts to "+host {
		t.Errorf("formaction: %q (all %v)", got["form #fa"], got)
	}
	if !strings.HasPrefix(got["form #js"], "success") {
		t.Errorf("form #js: %q", got["form #js"])
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(elsewhere, ",") != "POST /verify" {
		t.Errorf("the other site got %v, want only the verify call", elsewhere)
	}
	want := "interact /: kept a form's test data from going to another site: POST " + host + "/collect"
	if !slices.Contains(run.Warnings, want) {
		t.Errorf("warnings %q, want %q", run.Warnings, want)
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

// A form with a CAPTCHA isn't submitted: a test browser can't solve it,
// so a refusal would read as a broken form. The run says why instead.
func TestSubmitSkipsCaptchaForms(t *testing.T) {
	needBrowser(t)
	var mu sync.Mutex
	var posts []string
	fx := newFixture(t)
	fx.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mu.Lock()
			posts = append(posts, r.URL.Path)
			mu.Unlock()
			fmt.Fprint(w, `<!doctype html><title>Refused</title><p>Please prove you're not a robot.</p>`)
			return
		}
		fmt.Fprint(w, `<!doctype html><html><head><title>Contact</title></head><body>
<form id="ask" method="post" action="/ask/"><h2>Ask us</h2><input name="name" required><input type="email" name="email" required>
<div class="g-recaptcha" data-sitekey="test"></div><textarea name="g-recaptcha-response" style="display:none"></textarea><button>Send</button></form>
<form id="club" method="post" action="/join/"><h2>Club</h2><input name="name" required><button>Join</button></form>
</body></html>`)
	})
	cfg := config.Default()
	cfg.Interactions.Levels = []string{"submit"}
	run := fx.run(t, cfg, "/")
	got := map[string]string{}
	for _, i := range run.Page("/").Interact {
		if i.Kind == "submit" {
			got[i.Label] = i.Outcome + ": " + i.Detail
		}
	}
	if got["form #ask"] != "skipped: has a CAPTCHA (reCAPTCHA)" {
		t.Errorf("captcha form: %q (all %v)", got["form #ask"], got)
	}
	if !slices.ContainsFunc(run.Warnings, func(w string) bool {
		return strings.Contains(w, "interact /: form #ask has a CAPTCHA (reCAPTCHA)")
	}) {
		t.Errorf("warnings: %q", run.Warnings)
	}
	mu.Lock()
	defer mu.Unlock()
	if slices.Contains(posts, "/ask/") || !slices.Contains(posts, "/join/") {
		t.Errorf("posts: %v", posts)
	}
}
