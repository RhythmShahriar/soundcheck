// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package pii masks personal data and credentials in text Soundcheck
// keeps from a page, such as the accessible name of a focused element:
// a label can hold a logged-in customer's email address, phone number,
// card or bank account as easily as a field's purpose. What is masked
// is replaced with "***", so the text still reads ("Signed in as
// ***@***") without the value in it.
package pii

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// mask replaces a phone number, card number or IBAN.
const mask = "***"

var (
	// email matches an email address: something@domain.tld.
	email = regexp.MustCompile(`[A-Za-z0-9._%+'-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)

	// iban matches a run that may hold an IBAN: a country code, two
	// check digits and alphanumerics, printed compact or in groups of
	// four. The run can swallow a following capitalised word ("DE89
	// ... BIC"), so ibanLen looks for the longest checksum-valid prefix.
	iban = regexp.MustCompile(`[A-Z]{2}[0-9]{2}(?: ?[A-Z0-9]){11,40}`)

	// card matches 13 to 19 digits, optionally grouped by single spaces
	// or dashes. Only runs passing the Luhn check are masked, so an
	// order number of the same length stays.
	card = regexp.MustCompile(`[0-9](?:[ -]?[0-9]){12,18}`)

	// phone matches an international number (+49 30 1234567) or a local
	// one with separators ((030) 123-4567, 0171 7654321). phoneLen
	// decides whether the run is one.
	phone = regexp.MustCompile(`(?:\+|\()?[0-9](?:[ ./-]?\(?[0-9]|\)[ ./-]?[0-9])+`)

	// date is a date written with separators (2026-10-26, 26.10.2026),
	// maybe followed by the hour of a time (26.10.2026 14:30), which
	// has a phone number's digit count but isn't one.
	date = regexp.MustCompile(`^[0-9]{1,4}[./-][0-9]{1,2}[./-][0-9]{1,4}(?: [0-9]{1,2})?$`)
)

// Redact returns s with credentials (the kit's secret rules), email
// addresses, IBANs, card numbers and phone numbers masked.
func Redact(s string) string {
	if s == "" {
		return s
	}
	s = secrets.Redact(s)
	s = email.ReplaceAllString(s, "***@***")
	// IBANs before cards and phones: an IBAN's digits alone can look
	// like either.
	s = replace(s, iban, ibanLen, true)
	s = replace(s, card, cardLen, true)
	return replace(s, phone, phoneLen, false)
}

// replace masks the matches of re in s. valid gets each match and
// returns how many of its leading bytes to mask, 0 for none. A masked
// span must stand alone: no letter or digit right before or after it,
// so a number inside a longer token (a SKU, a hash) stays.
//
// With retry, a rejected match is searched again from its next token: a
// phone number followed by a card number reads as one long digit run to
// the card pattern; skipping the whole run would keep the card.
// Phone numbers go without it, since the tail of a date ("10.2026 14"
// of "26.10.2026 14:30") has a phone number's shape.
func replace(s string, re *regexp.Regexp, valid func(string) int, retry bool) string {
	var b strings.Builder
	last, off := 0, 0
	for off < len(s) {
		l := re.FindStringIndex(s[off:])
		if l == nil {
			break
		}
		start, end := off+l[0], off+l[1]
		if !alnumBefore(s, start) {
			if n := valid(s[start:end]); n > 0 && !alnumAfter(s, start+n) {
				b.WriteString(s[last:start])
				b.WriteString(mask)
				last, off = start+n, start+n
				continue
			}
		}
		if off = end; retry {
			off = nextToken(s, start)
		}
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// nextToken returns the byte offset of the first letter or digit after
// the token at i, the place a rejected match is retried from.
func nextToken(s string, i int) int {
	for i < len(s) && !isSep(s[i]) {
		i++
	}
	for i < len(s) && isSep(s[i]) {
		i++
	}
	return i
}

// isSep reports whether c separates the groups of a number.
func isSep(c byte) bool { return strings.IndexByte(" ./-()+", c) >= 0 }

// alnumBefore reports whether the rune ending at byte i is a letter or
// digit.
func alnumBefore(s string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return i > 0 && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// alnumAfter reports whether the rune starting at byte i is a letter or
// digit.
func alnumAfter(s string, i int) bool {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return i < len(s) && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// digits returns the ASCII digits of s.
func digits(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if isDigit(s[i]) {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// isDigit reports whether c is an ASCII digit.
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// cardLen returns the length of the longest prefix of m that ends on a
// whole digit group and whose 13 to 19 digits pass the Luhn check, or
// 0. The match can run on into a following number ("4111 ... 1111
// 2026"), which a check of the whole run alone would let through.
func cardLen(m string) int {
	for end := len(m); end > 0; end-- {
		if !isDigit(m[end-1]) || (end < len(m) && isDigit(m[end])) {
			continue
		}
		if d := digits(m[:end]); len(d) >= 13 && luhn(d) {
			return end
		}
	}
	return 0
}

// luhn reports whether the digit string d passes the Luhn check.
func luhn(d string) bool {
	sum := 0
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if (len(d)-1-i)%2 == 1 {
			if n *= 2; n > 9 {
				n -= 9
			}
		}
		sum += n
	}
	return sum%10 == 0
}

// phoneLen returns len(m) when m reads as a phone number: 7 to 15
// digits (E.164 allows at most 15, so a 16-digit order number is never
// one) with either a leading + or separators between the digits, so a
// plain run like a product ID stays. A date is not a phone number.
func phoneLen(m string) int {
	n := len(digits(m))
	if n < 7 || n > 15 || date.MatchString(m) {
		return 0
	}
	if m[0] != '+' && !strings.ContainsAny(m, " ./-()") {
		return 0
	}
	return len(m)
}

// ibanLen returns the length of the longest prefix of m that is a valid
// IBAN, ending on a whole group when m is grouped, or 0. An IBAN is 15
// to 34 characters without spaces.
func ibanLen(m string) int {
	for end := len(m); end >= 15; end-- {
		if m[end-1] == ' ' || (end < len(m) && m[end] != ' ' && strings.Contains(m, " ")) {
			continue
		}
		compact := strings.ReplaceAll(m[:end], " ", "")
		if len(compact) >= 15 && len(compact) <= 34 && ibanValid(compact) {
			return end
		}
	}
	return 0
}

// ibanValid runs the ISO 13616 check: move the first four characters to
// the end, read letters as numbers (A=10 ... Z=35) and the whole number
// modulo 97 is 1. The remainder is kept as it goes, so a 34-character
// IBAN never needs a big integer.
func ibanValid(s string) bool {
	rem := 0
	for _, c := range []byte(s[4:] + s[:4]) {
		switch {
		case isDigit(c):
			rem = (rem*10 + int(c-'0')) % 97
		case c >= 'A' && c <= 'Z':
			rem = (rem*100 + int(c-'A') + 10) % 97
		default:
			return false
		}
	}
	return rem == 1
}
