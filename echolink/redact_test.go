// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package echolink

import (
	"strings"
	"testing"

	echomodel "github.com/RhythmShahriar/echo/model"
)

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"Reset it: https://shop.test/wp-login.php?action=rp&key=Xk3pQ9vLm2Ab7Cd1Ef4G&login=sam": "Reset it: https://shop.test/wp-login.php?action=rp&key=•••••&login=sam",
		`<a href="https://shop.test/verify?token=abcdefghijklmnop1234&amp;u=7">Verify</a>`:      `<a href="https://shop.test/verify?token=ab•••••&amp;u=7">Verify</a>`,
		"Your code is 482913.":                     "Your code is •••••.",
		"Code: 12345678":                           "Code: •••••",
		"Order 1234 of 2 items, total 1,234567.50": "Order 1234 of 2 items, total 1,234567.50",
		`<td style="color:#123456">&#128512;</td>`: `<td style="color:#123456">&#128512;</td>`,
		"Call 0123456789 any time":                 "Call 0123456789 any time",
		"key sk_live_4eC39HqLyjWDarjtT1zdp7dc":     "key sk_l•••••",
		"https://shop.test/?p=12&lang=en":          "https://shop.test/?p=12&lang=en",
		// Tokens in the path after a reset or verify segment.
		"https://app.test/password/reset/9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08?email=sam%40app.test": "https://app.test/password/reset/•••••?email=sam%40app.test",
		`<a href="https://app.test/email/verify/7/b2c1f0e4a9d8c7b6a5f4e3d2c1b0a9f8e7d6c5b4?expires=1">`:                         `<a href="https://app.test/email/verify/7/•••••?expires=1">`,
		"https://app.test/magic-login/Ab3_xY9-kLm2Pq7rSt4uVw8z":                                                                 "https://app.test/magic-login/•••••",
		// Long segments elsewhere stay, as do short ones after the keyword.
		"https://shop.test/product/a-very-long-product-name-with-words": "https://shop.test/product/a-very-long-product-name-with-words",
		"https://shop.test/login/help":                                  "https://shop.test/login/help",
		"https://shop.test/reset-your-password-in-three-steps/guide":    "https://shop.test/reset-your-password-in-three-steps/guide",
	} {
		if got := redactText(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
	msgs := []echomodel.Message{{Subject: "Your login code 551234", Text: "Use 551234", HTML: "<b>551234</b>", Body: "Use 551234", To: []string{"sam@shop.test"}}}
	got := Redact(msgs)
	for _, s := range []string{got[0].Subject, got[0].Text, got[0].HTML, got[0].Body} {
		if strings.Contains(s, "551234") {
			t.Errorf("not masked: %q", s)
		}
	}
	if msgs[0].Text != "Use 551234" || got[0].To[0] != "sam@shop.test" {
		t.Errorf("Redact changed its input or the recipients: %+v %+v", msgs[0], got[0])
	}
}

func TestRedactURLAndHeaders(t *testing.T) {
	for in, want := range map[string]string{
		"https://hooks.test/in?token=ab12&channel=sales":  "https://hooks.test/in?token=•••••&channel=sales",
		"https://hooks.test/in?sig=x9&ts=1700000000":      "https://hooks.test/in?sig=•••••&ts=1700000000",
		"https://hooks.test/in?api_key=k&page=2":          "https://hooks.test/in?api_key=•••••&page=2",
		"https://hooks.test/T0/B0/abcdefghijklmnopqrstuv": "https://hooks.test/T0/B0/abcdefghijklmnopqrstuv",
		"https://hooks.test/in?id=4":                      "https://hooks.test/in?id=4",
	} {
		if got := RedactURL(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
	msgs := []echomodel.Message{{URL: "https://hooks.test/in?token=ab12", Headers: map[string]string{
		"Authorization": "Bearer abc", "X-Hook-Signature": "deadbeef", "Content-Type": "application/json",
		"Referer": "https://shop.test/wp-login.php?action=rp&key=Xk3pQ9vLm2Ab7Cd1Ef4G",
	}}}
	got := Redact(msgs)[0]
	if got.URL != "https://hooks.test/in?token=•••••" {
		t.Errorf("URL = %q", got.URL)
	}
	for name, want := range map[string]string{
		"Authorization": "•••••", "X-Hook-Signature": "•••••", "Content-Type": "application/json",
		"Referer": "https://shop.test/wp-login.php?action=rp&key=•••••",
	} {
		if got.Headers[name] != want {
			t.Errorf("header %s = %q, want %q", name, got.Headers[name], want)
		}
	}
	if msgs[0].Headers["Authorization"] != "Bearer abc" {
		t.Error("Redact changed its input's headers")
	}
}

// A start URL loses its query string and fragment and has a token in
// its path masked.
func TestRedactTarget(t *testing.T) {
	token := strings.Repeat("ab12cd34ef", 4)
	for in, want := range map[string]string{
		"https://shop.test/":                              "https://shop.test/",
		"https://shop.test/reset/" + token + "/":          "https://shop.test/reset/" + masked + "/",
		"https://shop.test/embed.php?utm=abc#top":         "https://shop.test/embed.php",
		"https://shop.test/reset/" + token + "/?k=1#frag": "https://shop.test/reset/" + masked + "/",
		"": "",
	} {
		if got := RedactTarget(in); got != want {
			t.Errorf("RedactTarget(%q) = %q, want %q", in, got, want)
		}
	}
}
