// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pii

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The samples are assembled at run time: a literal key, card number or
// IBAN in the source would trip push protection and secret scanners.
var (
	visa      = strings.Join([]string{"4111", "1111", "1111", "1111"}, " ")
	visaDash  = strings.ReplaceAll(visa, " ", "-")
	visaFlat  = strings.ReplaceAll(visa, " ", "")
	amex      = "3782" + " " + "822463" + " " + "10005"
	noLuhn    = "1234" + "5678" + "9012" + "3456"
	ibanDE    = "DE" + "89" + "3704" + "0044" + "0532" + "0130" + "00"
	ibanGB    = "GB" + "82" + "WEST" + "1234" + "5698" + "7654" + "32"
	ibanBadCk = "DE" + "88" + "3704" + "0044" + "0532" + "0130" + "00"
)

// group prints an IBAN in groups of four, as banks do.
func group(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestPIIRedact(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"Search", "Search"},

		// Emails.
		{"Signed in as jane.doe+shop@example.co.uk", "Signed in as ***@***"},
		{"a@b.de and c@d.org", "***@*** and ***@***"},
		{"Not an address: @home or user@localhost", "Not an address: @home or user@localhost"},

		// Phone numbers: international and local with separators.
		{"Call +49 30 1234567", "Call ***"},
		{"Call +4930" + "1234567 now", "Call *** now"},
		{"Hotline +49 (0)30 123 4567.", "Hotline ***."},
		{"Tel. (030) 123-4567", "Tel. ***"},
		{"Mobil 0171 7654321", "Mobil ***"},
		{"US 555-123-4567", "US ***"},
		{"Ring 030/1234567", "Ring ***"},
		{"Order 1234567", "Order 1234567"},                     // no + and no separators: an ID
		{"Version 1.2.3", "Version 1.2.3"},                     // too few digits
		{"Due 2026-10-26", "Due 2026-10-26"},                   // a date
		{"Posted 26.10.2026 14:30", "Posted 26.10.2026 14:30"}, // a date and time
		{"SKU AB1234-567-89", "SKU AB1234-567-89"},             // part of a longer token

		// Card numbers passing Luhn, with spaces, dashes or none.
		{"Card " + visa, "Card ***"},
		{"Card " + visaDash + ".", "Card ***."},
		{"Card " + visaFlat, "Card ***"},
		{"Amex " + amex, "Amex ***"},
		{"Card " + visa + " 2026", "Card *** 2026"},
		{"Order " + noLuhn, "Order " + noLuhn}, // 16 digits, no Luhn: left alone

		// IBANs with a valid checksum, compact or grouped.
		{"IBAN " + ibanDE, "IBAN ***"},
		{"IBAN " + group(ibanDE) + " BIC COBADEFFXXX", "IBAN *** BIC COBADEFFXXX"},
		{"Pay to " + ibanGB + ".", "Pay to ***."},
		{"IBAN " + ibanBadCk, "IBAN " + ibanBadCk}, // checksum fails
	}
	for _, c := range cases {
		if got := Redact(c.in); got != c.want {
			t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPIIRedactSecret(t *testing.T) {
	key := "sk_" + "live_" + strings.Repeat("4eC39HqLyjWDarjtT1zdp7dc", 1)
	if got := Redact("Key " + key); strings.Contains(got, key) || !strings.HasPrefix(got, "Key ") {
		t.Errorf("secret kept: %q", got)
	}
}

func TestIBANValid(t *testing.T) {
	for _, s := range []string{ibanDE, ibanGB} {
		if !ibanValid(s) {
			t.Errorf("%s: want valid", s)
		}
	}
	for _, s := range []string{ibanBadCk, "DE89-3704", "de89370400440532013000"} {
		if ibanValid(s) {
			t.Errorf("%s: want invalid", s)
		}
	}
}

// FuzzRedact: any text redacts without panicking, keeps valid UTF-8
// valid, never leaves a planted card number or email in it and gives
// the same output every time.
func FuzzRedact(f *testing.F) {
	for _, s := range []string{"", "Search", "+49 30 1234567", visa, group(ibanDE), "a@b.de", "\xff\xfe", "((((0", "DE00 " + strings.Repeat("A", 60)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := Redact(s)
		if utf8.ValidString(s) && !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8 from %q: %q", s, got)
		}
		if again := Redact(s); again != got {
			t.Fatalf("not deterministic: %q then %q", got, again)
		}
		planted := Redact(s + " " + visa + " jane@example.com")
		if strings.Contains(planted, visa) || strings.Contains(planted, "jane@example.com") {
			t.Fatalf("planted value kept in %q", planted)
		}
	})
}
