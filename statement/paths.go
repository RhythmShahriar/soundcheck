// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// masked replaces what the draft hides.
var masked = secrets.Mask("", 0)

var (
	// tokenPath matches the path after a reset, verify, confirm, magic,
	// login or activate segment ("/password/reset/<token>"), where
	// frameworks put a token instead of in the query; tokenSegment is a
	// segment long and opaque enough to be one. The same rules as
	// echolink's, which masks the links in captured email.
	tokenPath    = regexp.MustCompile(`(?i)/(?:[\w-]*[-_])?(?:reset|verify|verification|confirm|confirmation|magic|login|activate|activation)(?:[-_][\w-]*)?((?:/[^/?#\s"'<>]*)+)`)
	tokenSegment = regexp.MustCompile(`/[A-Za-z0-9_-]{24,}`)
	// urlQuery matches a URL in text with its query or fragment.
	urlQuery = regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://[^\s?#"'<>]*)[?#][^\s"'<>]*`)
)

// cleanPath makes a page path fit to print in a statement: no query
// string or fragment (they carry session ids and reset keys) and tokens
// in the path masked. Every hide string (the run's host, the login
// account) is masked wherever it appears. A full URL is cut to its path.
func cleanPath(p string, hide []string) string {
	if strings.Contains(p, "://") {
		if u, err := url.Parse(p); err == nil {
			p = u.EscapedPath()
		}
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = secrets.Redact(p)
	p = tokenPath.ReplaceAllStringFunc(p, func(m string) string {
		// The keyword's own segment stays, however long.
		at := tokenPath.FindStringSubmatchIndex(m)[2]
		return m[:at] + tokenSegment.ReplaceAllString(m[at:], "/"+masked)
	})
	p = hideAll(p, hide)
	if p == "" {
		return "/"
	}
	return p
}

// cleanText makes a module's message fit to print: secrets masked,
// queries cut from the URLs in it and every hide string masked.
func cleanText(s string, hide []string) string {
	return hideAll(urlQuery.ReplaceAllString(secrets.Redact(s), "$1"), hide)
}

// hideAll masks every hide string in s, ignoring case.
func hideAll(s string, hide []string) string {
	for _, h := range hide {
		if len(h) < 3 { // too short to tell from ordinary text
			continue
		}
		s = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(h)).ReplaceAllLiteralString(s, masked)
	}
	return s
}

// hidden lists what the draft must never show: in.Hide plus the run's
// host, with and without its port.
func hidden(in Input) []string {
	out := append([]string(nil), in.Hide...)
	if in.Run != nil {
		if u, err := url.Parse(in.Run.Target); err == nil && u.Host != "" {
			out = append(out, u.Host, u.Hostname())
			if u.User != nil {
				out = append(out, u.User.Username())
			}
		}
	}
	return out
}
