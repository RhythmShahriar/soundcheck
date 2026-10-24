// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cache

import (
	"slices"
	"testing"
)

func TestNonces(t *testing.T) {
	cases := []struct {
		name, body string
		kinds      []string
	}{
		{"wpnonce field", `<form><input type="hidden" id="_wpnonce" name="_wpnonce" value="a1b2c3d4e5" /></form>`, []string{"_wpnonce"}},
		{"wpnonce value first", `<input value="a1b2c3d4e5" name="_wpnonce" type="hidden">`, []string{"_wpnonce"}},
		{"wpnonce in a link", `<a href="/?action=x&amp;_wpnonce=0f0f0f0f0f">x</a>`, []string{"_wpnonce"}},
		{"rest api", `<script>var wpApiSettings = {"root":"https:\/\/shop.test\/wp-json\/","nonce":"9a8b7c6d5e","versionString":"wp\/v2\/"};</script>`, []string{"wpApiSettings nonce"}},
		{"woocommerce params", `<script>var wc_cart_fragments_params = {"ajax_url":"\/wp-admin\/admin-ajax.php","wc_ajax_url":"\/?wc-ajax=%%endpoint%%"}; var wc_checkout_params = {"update_order_review_nonce":"1234abcdef"};</script>`, []string{"nonce"}},
		{"woocommerce store api", `<script>wp.apiFetch.use( wp.apiFetch.createNonceMiddleware( "abcdef1234" ) ); var wcBlocksMiddlewareConfig = {"storeApiNonce":"bbbbbbbbbb"};</script>`, []string{"WooCommerce nonce"}},
		{"woocommerce form", `<input type="hidden" id="woocommerce-login-nonce" name="woocommerce-login-nonce" value="cafebabe00" />`, []string{"WooCommerce nonce"}},
		{"contact form 7", `<input type="hidden" name="_wpcf7_nonce" value="0123456789" />`, []string{"Contact Form 7 nonce"}},
		{"none", `<p>Order a1b2c3d4e5 shipped. Our nonce policy is strict.</p><input name="_wpnonce" value="">`, nil},
		{"not hex", `<input name="_wpnonce" value="zzzzzzzzzz">`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nonceKinds(Nonces(c.body)); !slices.Equal(got, c.kinds) {
				t.Errorf("got %v, want %v", got, c.kinds)
			}
		})
	}
}

func TestMainText(t *testing.T) {
	page := `<html><body class="home logged-in"><header>Menu Shop</header><main><h1>Beans</h1><p>Fresh coffee.</p>
<script>var x = 1;</script><style>p{}</style></main><footer>© 2026</footer></body></html>`
	if got := MainText(page); got != "Beans Fresh coffee." {
		t.Errorf("main = %q", got)
	}
	if got := MainText(`<body><div id="content"><p>Hello</p></div><aside>side</aside></body>`); got != "Hello" {
		t.Errorf("#content = %q", got)
	}
	if got := MainText(`<body><p>Only body</p></body>`); got != "Only body" {
		t.Errorf("body = %q", got)
	}
	if got := BodyClasses(page); !slices.Equal(got, []string{"home", "logged-in"}) {
		t.Errorf("classes = %v", got)
	}
}

func TestWordsIgnoreVolatileText(t *testing.T) {
	same := [][2]string{
		{"Posted 3 hours ago by Ana. Ref a8f3c9d2e1b4. Updated 10:42 am on 2026-10-24.", "Posted 5 hours ago by Ana. Ref 77e3c9d2aa01. Updated 11:02 am on 2026-10-25."},
		{"Visitors: 1234567 today", "Visitors: 1234890 today"},
		{"Tea Coffee Cocoa", "Cocoa Tea Coffee"}, // a rotating widget reorders
	}
	for _, c := range same {
		if a, b := wordDiff(words(c[0]), words(c[1])); len(a)+len(b) > 0 {
			t.Errorf("%q vs %q differ: %v %v", c[0], c[1], a, b)
		}
	}
	gone, added := wordDiff(words("Espresso grinder $129 In stock"), words("Espresso grinder $149 Sold out"))
	if !slices.Equal(gone, []string{"$129", "in", "stock"}) || !slices.Equal(added, []string{"$149", "out", "sold"}) {
		t.Errorf("gone %v added %v", gone, added)
	}
}
