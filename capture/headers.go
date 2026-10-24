// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture

import (
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// keptHeaders are the main-document response headers the checks read,
// stored as they came. Every other header is stored masked (see
// storedHeader): runs end up in CI artifacts and shared reports where a
// misconfigured site may send an API key or a session in a header. A
// check that reads a header from model.Page.Headers must list it here;
// TestKeptHeadersCoverChecks fails otherwise.
var keptHeaders = map[string]bool{
	"cache-control": true, "age": true, "x-cache": true, "cf-cache-status": true, "x-robots-tag": true,
	"server": true, "content-type": true, "content-encoding": true, "content-length": true, "vary": true,
	"expires": true, "etag": true, "last-modified": true, "location": true, "strict-transport-security": true,
	"content-security-policy": true, "x-powered-by": true,
	// The login challenge (scheme and realm) for health/auth-required; it
	// names how to log in, not a credential.
	"www-authenticate": true,
}

// secretName matches header names that carry credentials whatever their
// value looks like.
var secretName = []string{"key", "token", "secret", "auth", "session", "password", "signature", "credential"}

// storedHeader is the value of a main-document response header as the
// run stores it. name is lowercase.
func storedHeader(name, value string) string {
	switch {
	case keptHeaders[name]:
		return value
	case name == "set-cookie" || name == "cookie":
		return maskCookies(value)
	}
	for _, s := range secretName {
		if strings.Contains(name, s) {
			return secrets.Mask(value, 0)
		}
	}
	// Redact sees the name too, so rules like "x-api-key: …" apply.
	return strings.TrimPrefix(secrets.Redact(name+": "+value), name+": ")
}

// maskCookies hides cookie values and keeps their names and attributes:
// "sid=abc; Path=/" becomes "sid=•••••; Path=/". Chrome joins several
// Set-Cookie headers with newlines.
func maskCookies(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		first, rest, more := strings.Cut(line, ";")
		if name, _, ok := strings.Cut(first, "="); ok {
			first = name + "=" + secrets.Mask("", 0)
		} else if strings.TrimSpace(first) != "" {
			first = secrets.Mask("", 0)
		}
		if more {
			first += ";" + rest
		}
		lines[i] = first
	}
	return strings.Join(lines, "\n")
}
