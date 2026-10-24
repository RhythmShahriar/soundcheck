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
		"key sk_live_4eC39HqLyjWDarjtT1zdp7dc":     "key sk_live_•••••",
		"https://shop.test/?p=12&lang=en":          "https://shop.test/?p=12&lang=en",
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
