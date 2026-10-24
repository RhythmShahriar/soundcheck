// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"crypto/sha1" //nolint:gosec // a short tag for test data, not security
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// Marker is in every value Soundcheck types into a form, so test entries
// are easy to find and delete in the site's inbox or database.
const Marker = "soundcheck-test"

// Fake makes believable form data. Values depend only on Seed, so one run
// always types the same thing, and two runs can be told apart by the tag
// in the email address.
type Fake struct {
	Seed string
}

// Tag is a short code derived from the seed.
func (f Fake) Tag() string {
	sum := sha1.Sum([]byte(f.Seed)) //nolint:gosec // not for security
	return hex.EncodeToString(sum[:3])
}

// Value returns fake data by kind, as used in ${fake:kind}.
func (f Fake) Value(kind string) (string, bool) {
	switch strings.ToLower(strings.ReplaceAll(kind, "-", "_")) {
	case "email":
		return Marker + "+" + f.Tag() + "@example.com", true
	case "name", "full_name":
		return "Soundcheck Test", true
	case "first_name", "firstname", "given_name":
		return "Soundcheck", true
	case "last_name", "lastname", "family_name", "surname":
		return "Test", true
	case "phone", "tel":
		return "+1 555 0100", true // 555-01xx numbers are reserved for fiction
	case "url", "website":
		return "https://example.com/" + Marker, true
	case "company", "organization":
		return "Soundcheck Test Co", true
	case "subject":
		return Marker + " " + f.Tag(), true
	case "text", "word":
		return Marker, true
	case "message", "comment", "textarea":
		return Marker + ": an automated form check by Rhythm Soundcheck (" + f.Tag() + "). Please ignore.", true
	case "number":
		return "1", true
	case "address", "street":
		return "1 Test Street", true
	case "city":
		return "Testville", true
	case "zip", "postcode", "postal_code":
		return "12345", true
	case "date":
		return "2030-01-15", true
	}
	return "", false
}

// Field describes a form field for For.
type Field struct {
	Tag          string // input, textarea, select
	Type         string // the input type
	Name         string
	ID           string
	Label        string // label text, aria-label or placeholder
	Autocomplete string
	Min, Max     string
	Step         string
	MinLength    int
	MaxLength    int
	Pattern      string
}

var (
	hintEmail   = regexp.MustCompile(`(?i)e-?mail`)
	hintPhone   = regexp.MustCompile(`(?i)phone|tel\b|mobile|cell`)
	hintURL     = regexp.MustCompile(`(?i)url|website|homepage`)
	hintFirst   = regexp.MustCompile(`(?i)first.?name|given|fname|vorname`)
	hintLast    = regexp.MustCompile(`(?i)last.?name|surname|family|lname`)
	hintName    = regexp.MustCompile(`(?i)\bname\b|your-name|full.?name|author|_name$|^name`)
	hintCompany = regexp.MustCompile(`(?i)company|organi[sz]ation|business`)
	hintSubject = regexp.MustCompile(`(?i)subject|topic|title`)
	hintZip     = regexp.MustCompile(`(?i)zip|post.?code|postal`)
	hintCity    = regexp.MustCompile(`(?i)city|town`)
	hintAddress = regexp.MustCompile(`(?i)address|street`)
	hintNumber  = regexp.MustCompile(`(?i)qty|quantity|amount|number|count|age`)
)

// For returns a value for a text-like field from its type, autocomplete
// hint, name and label. Select, checkbox, radio and file fields are filled
// by the caller.
func (f Fake) For(fl Field) string {
	v := f.guess(fl)
	if fl.MinLength > 0 && len(v) < fl.MinLength {
		v += strings.Repeat("x", fl.MinLength-len(v))
	}
	if fl.MaxLength > 0 && len(v) > fl.MaxLength {
		v = v[:fl.MaxLength]
	}
	return v
}

func (f Fake) guess(fl Field) string {
	val := func(k string) string { v, _ := f.Value(k); return v }
	switch strings.ToLower(fl.Type) {
	case "email":
		return val("email")
	case "tel":
		return val("tel")
	case "url":
		return val("url")
	case "number", "range":
		return number(fl)
	case "date":
		return clampText("2030-01-15", fl.Min, fl.Max)
	case "datetime-local":
		return clampText("2030-01-15T10:00", fl.Min, fl.Max)
	case "time":
		return clampText("10:00", fl.Min, fl.Max)
	case "month":
		return clampText("2030-01", fl.Min, fl.Max)
	case "week":
		return clampText("2030-W03", fl.Min, fl.Max)
	case "color":
		return "#336699"
	}
	if strings.EqualFold(fl.Tag, "textarea") {
		return val("message")
	}
	hint := fl.Autocomplete + " " + fl.Name + " " + fl.ID + " " + fl.Label
	switch {
	case hintEmail.MatchString(hint):
		return val("email")
	case hintPhone.MatchString(hint):
		return val("tel")
	case hintURL.MatchString(hint):
		return val("url")
	case hintFirst.MatchString(hint):
		return val("first_name")
	case hintLast.MatchString(hint):
		return val("last_name")
	case hintCompany.MatchString(hint):
		return val("company")
	case hintName.MatchString(hint):
		return val("name")
	case hintSubject.MatchString(hint):
		return val("subject")
	case hintZip.MatchString(hint):
		return val("zip")
	case hintCity.MatchString(hint):
		return val("city")
	case hintAddress.MatchString(hint):
		return val("address")
	case hintNumber.MatchString(hint):
		return number(fl)
	}
	return Marker
}

// number picks a value inside min/max: the minimum when set, else 1 (or
// the maximum when that's below 1).
func number(fl Field) string {
	lo, hasLo := parseNum(fl.Min)
	hi, hasHi := parseNum(fl.Max)
	v := 1.0
	switch {
	case hasLo:
		v = lo
	case hasHi && hi < 1:
		v = hi
	}
	if hasHi && v > hi {
		v = hi
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func parseNum(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

// clampText keeps an ISO-formatted value (which sorts as text) within
// min/max.
func clampText(v, lo, hi string) string {
	if lo != "" && v < lo {
		return lo
	}
	if hi != "" && v > hi {
		return hi
	}
	return v
}
