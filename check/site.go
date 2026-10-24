// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"net/url"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/crawl"
)

// SameSite reports whether a and b are on one site: the same hostname
// (ignoring case and a leading "www.") on the same port. A scheme's
// default port counts as no port, so http://example.com and
// https://www.example.com are one site (sites move to https and to www
// all the time), while 127.0.0.1:8080 and 127.0.0.1:8081 are two, the
// way local setups run sibling sites side by side.
//
// It is the kit's crawl.SameSite, without its scheme rule: the kit
// allows only the upgrade from http to https, while a page here may also
// link or post back to http while a local server may answer both on one
// port. Downgrade and Trusted tell those moves apart.
func SameSite(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	if crawl.SameSite(a, b) || crawl.SameSite(b, a) {
		return true
	}
	// The same explicit port under the other scheme: compare it as a's.
	if p := b.Port(); p != "" && p != "80" && p != "443" && !strings.EqualFold(a.Scheme, b.Scheme) {
		c := *b
		c.Scheme = a.Scheme
		return crawl.SameSite(a, &c)
	}
	return false
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
