// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package echolink

import (
	"regexp"
	"strings"

	echomodel "github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// masked replaces what Redact hides, the kit's mask for a value shown
// with no leading characters.
var masked = secrets.Mask("", 0)

var (
	// longQuery matches a long URL query value, the shape of a password
	// reset key, a magic login link or a confirmation token. "&amp;"
	// covers links inside HTML.
	longQuery = regexp.MustCompile(`([?&](?:amp;)?[A-Za-z0-9_.~%\[\]-]+=)([^&#\s"'<>]{16,})`)
	// code matches a one-time code: 6 to 8 digits standing alone. Digits
	// after "#", "&" (an HTML entity), "." or "," are colors, entities
	// and decimals, not codes.
	code = regexp.MustCompile(`(^|[^\w#&.,-])(\d{6,8})\b`)
	// tokenPath matches the path after a reset, verify, confirm, magic,
	// login or activate segment ("/password/reset/<token>",
	// "/email/verify/7/<hash>"), where frameworks put the token instead
	// of in the query; tokenSegment is a segment in it long and opaque
	// enough to be one.
	tokenPath    = regexp.MustCompile(`(?i)/(?:[\w-]*[-_])?(?:reset|verify|verification|confirm|confirmation|magic|login|activate|activation)(?:[-_][\w-]*)?((?:/[^/?#\s"'<>]*)+)`)
	tokenSegment = regexp.MustCompile(`/[A-Za-z0-9_-]{24,}`)
	// anyQuery matches a URL query value of any length with its name.
	anyQuery = regexp.MustCompile(`([?&](?:amp;)?([A-Za-z0-9_.~%\[\]-]+)=)([^&#\s"'<>]+)`)
)

// secretName lists words that make a query parameter or header a
// credential whatever its value looks like: "?token=ab12" in a webhook
// URL is too short for longQuery but still opens the endpoint.
var secretName = []string{"key", "token", "secret", "auth", "session", "password", "passwd", "signature", "sig", "code", "credential", "cookie", "otp"}

func secretNamed(name string) bool {
	name = strings.ToLower(name)
	for _, s := range secretName {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

// Redact returns msgs with the credentials, reset links (their query
// values and the tokens in their paths) and one-time codes in their
// subject, text, HTML, raw body, webhook URL and header values masked,
// for storing in a run or showing in a report. Runs end up in CI artifacts and shared reports, while a
// password reset email holds a working link. Both runs of a comparison
// are masked the same way, so a code that changes on every send no
// longer reads as a changed message.
func Redact(msgs []echomodel.Message) []echomodel.Message {
	out := make([]echomodel.Message, len(msgs))
	for i, m := range msgs {
		m.Subject, m.Text, m.HTML, m.Body = redactText(m.Subject), redactText(m.Text), redactText(m.HTML), redactText(m.Body)
		m.URL = RedactURL(m.URL)
		if m.Headers != nil {
			h := make(map[string]string, len(m.Headers))
			for name, v := range m.Headers {
				h[name] = redactHeader(name, v)
			}
			m.Headers = h
		}
		out[i] = m
	}
	return out
}

func redactText(s string) string {
	if s == "" {
		return s
	}
	return code.ReplaceAllString(redactTokens(s), "${1}"+masked)
}

// redactTokens masks what redactText masks except one-time codes: the
// secrets kit/secrets knows, the tokens in reset and login paths and
// long query values.
func redactTokens(s string) string {
	s = secrets.Redact(s)
	s = tokenPath.ReplaceAllStringFunc(s, func(m string) string {
		// The keyword's own segment stays, however long.
		at := tokenPath.FindStringSubmatchIndex(m)[2]
		return m[:at] + tokenSegment.ReplaceAllString(m[at:], "/"+masked)
	})
	return longQuery.ReplaceAllString(s, "${1}"+masked)
}

// absURL matches a URL with a scheme inside free text, such as a console
// message or an error title.
var absURL = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)

// RedactLine is free text from a page (a console message, a JS error, an
// issue title or key) as an --eaa result shows it: every URL in it masked
// the way RedactPage masks a page URL and, elsewhere, the secrets, the
// tokens in reset and login paths and the long query values Redact
// masks. One-time codes are left alone: a six digit number in an error
// line is more often a column or a size than a code.
func RedactLine(s string) string {
	if s == "" {
		return s
	}
	return redactTokens(absURL.ReplaceAllStringFunc(s, RedactPage))
}

// RedactURL masks a URL the way Redact masks text. It also masks the
// value of every query parameter named like a credential, however short.
func RedactURL(u string) string {
	if u == "" {
		return u
	}
	return anyQuery.ReplaceAllStringFunc(redactText(u), func(m string) string {
		sub := anyQuery.FindStringSubmatch(m)
		if secretNamed(sub[2]) {
			return sub[1] + masked
		}
		return m
	})
}

// RedactTarget is a start URL as an --eaa result shows it: without its
// query string or fragment, which can hold a campaign id or a session,
// and with a token in its path (/reset/<token>/) masked the way RedactURL
// masks a page path.
func RedactTarget(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	return RedactURL(u)
}

// RedactPage is a page path or URL as an --eaa result shows it: every
// query value masked, however short and whatever its name (a session id
// such as ?sid=9f3ac2e81b7d looks like neither a credential nor a long
// token), the fragment dropped and a token in the path masked the way
// RedactURL masks it. The query names stay, so two pages that differ
// only in a parameter still read as different pages.
func RedactPage(u string) string {
	if i := strings.IndexByte(u, '#'); i >= 0 {
		u = u[:i]
	}
	return anyQuery.ReplaceAllString(RedactURL(u), "${1}"+masked)
}

// redactHeader masks a header value: all of it when the name says it's a
// credential, otherwise what Redact would mask in text.
func redactHeader(name, value string) string {
	if secretNamed(name) {
		return masked
	}
	if strings.Contains(value, "://") {
		value = RedactURL(value)
	}
	// secrets.Redact sees the name too, so rules like "x-api-key: …" apply.
	return strings.TrimPrefix(redactText(name+": "+value), name+": ")
}
