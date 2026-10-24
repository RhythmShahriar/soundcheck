// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"net/url"
	"strings"
)

// SameSite reports whether a and b are on one site: the same hostname
// (ignoring case and a leading "www.") on the same port. A scheme's
// default port counts as no port, so http://example.com and
// https://www.example.com are one site (sites move to https and to www
// all the time), while 127.0.0.1:8080 and 127.0.0.1:8081 are two, the
// way local setups run sibling sites side by side.
//
// It replaces the kit's crawl.SameSite, which ignores ports, until a kit
// release has the port check.
func SameSite(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return siteName(a.Hostname()) == siteName(b.Hostname()) && explicitPort(a) == explicitPort(b)
}

// Downgrade reports whether a move from a to b drops https: a is https
// and b is plain http.
func Downgrade(a, b *url.URL) bool {
	return a != nil && b != nil && strings.EqualFold(a.Scheme, "https") && strings.EqualFold(b.Scheme, "http")
}

// Trusted reports whether a request to u may carry the run's headers and
// cookies for site: u is on the same site and keeps its https. A staging
// cookie or token on an http link of an https site would cross the
// network in cleartext.
func Trusted(site, u *url.URL) bool { return SameSite(site, u) && !Downgrade(site, u) }

func siteName(host string) string { return strings.TrimPrefix(strings.ToLower(host), "www.") }

// explicitPort is u's port, or "" when it is the scheme's default.
func explicitPort(u *url.URL) string {
	p := u.Port()
	switch {
	case p == "80" && !strings.EqualFold(u.Scheme, "https"), p == "443" && !strings.EqualFold(u.Scheme, "http"):
		return ""
	}
	return p
}
