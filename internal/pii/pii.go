// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package pii masks personal data and credentials in text Soundcheck
// keeps from a page, such as the accessible name of a focused element:
// a label can hold a logged-in customer's email address as easily as a
// field's purpose. What is masked is replaced with "***", so the text
// still reads ("Signed in as ***@***") without the value in it.
package pii

import (
	"regexp"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// email matches an email address: something@domain.tld.
var email = regexp.MustCompile(`[A-Za-z0-9._%+'-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)

// Redact returns s with credentials (the kit's secret rules) and email
// addresses masked.
func Redact(s string) string {
	if s == "" {
		return s
	}
	s = secrets.Redact(s)
	return email.ReplaceAllString(s, "***@***")
}
