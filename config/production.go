// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/idna"
)

// prodSite is one production entry, parsed for matching. Users write
// the natural thing ("example.com", "https://www.example.com/",
// "*.example.com") and expect it to cover the site however a URL spells
// it, so matching is by normalised host, not by string prefix.
type prodSite struct {
	host     string // lowercase ASCII, no trailing dot, no www.
	wildcard bool   // *.host: every subdomain too
	port     string // "" matches any port; set only for a non-default port
	path     string // "" for the whole host, or a prefix without a trailing slash
}

// String shows the entry the way it is matched.
func (p prodSite) String() string {
	s := p.host
	if p.wildcard {
		s = "*." + s
	}
	if p.port != "" {
		s += ":" + p.port
	}
	return s + p.path
}

// matches reports whether u is on this site, under its path.
func (p prodSite) matches(u *url.URL) bool {
	host := normHost(u.Hostname())
	if host != p.host && (!p.wildcard || !strings.HasSuffix(host, "."+p.host)) {
		return false
	}
	if p.port != "" && p.port != normPort(u.Port()) {
		return false
	}
	path := strings.ToLower(u.Path)
	return p.path == "" || path == p.path || strings.HasPrefix(path, p.path+"/")
}

// parseProduction parses one production entry. A bare host stands for
// both schemes, since the scheme is ignored when matching.
func parseProduction(entry string) (prodSite, error) {
	bad := settingErr("production", "%q isn't a site like example.com, *.example.com or https://example.com/shop/", entry)
	raw := strings.TrimSpace(entry)
	if !strings.Contains(raw, "://") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
		return prodSite{}, bad
	}
	host := u.Hostname()
	p := prodSite{port: normPort(u.Port()), path: strings.TrimRight(strings.ToLower(u.Path), "/")}
	if rest, ok := strings.CutPrefix(host, "*."); ok {
		p.wildcard, host = true, rest
	}
	if p.host = normHost(host); p.host == "" || strings.Contains(p.host, "*") {
		return prodSite{}, bad
	}
	return p, nil
}

// normHost lowercases a host, drops a trailing dot and a leading www.
// (the same site to a visitor) and turns an IDN into punycode, so
// bücher.de and xn--bcher-kva.de are one host.
func normHost(h string) string {
	h = strings.TrimSuffix(h, ".")
	if a, err := idna.Lookup.ToASCII(h); err == nil {
		h = a
	} else {
		h = strings.ToLower(h) // IPv6, underscores: not IDNs, compared as typed
	}
	return strings.TrimPrefix(h, "www.")
}

// normPort drops the default http and https ports whatever the scheme,
// so example.com:443 is example.com.
func normPort(p string) string {
	if p == "80" || p == "443" {
		return ""
	}
	return p
}

// parseProductions parses every production entry, naming the first bad one.
func parseProductions(entries []string) ([]prodSite, error) {
	out := make([]prodSite, 0, len(entries))
	for _, e := range entries {
		p, err := parseProduction(e)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// productionSites returns the parsed entries: the ones Validate stored, or
// for a config built in code (or changed since), parsed now, skipping any
// that don't parse.
func (c Config) productionSites() []prodSite {
	if slices.Equal(c.prodFrom, c.Production) {
		return c.prod
	}
	var out []prodSite
	for _, e := range c.Production {
		if p, err := parseProduction(e); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// IsProduction reports whether rawURL is on one of the production sites:
// the same host (case, a trailing dot, www., punycode and default ports
// don't matter, nor does the scheme), or a subdomain of a *. entry. It
// must also be under the entry's path on a segment boundary, so /shop
// covers /shop/cart but not /shopping.
func (c Config) IsProduction(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" {
		return false
	}
	return slices.ContainsFunc(c.productionSites(), func(p prodSite) bool { return p.matches(u) })
}

// ProductionSites lists the production entries as they are matched, for
// the run header: "example.com", "*.example.com", "example.com/shop".
func (c Config) ProductionSites() []string {
	var out []string
	for _, p := range c.productionSites() {
		out = append(out, p.String())
	}
	return out
}

// HostKey is how Soundcheck tells one site from another: the normalised
// host (case, a trailing dot, www. and punycode don't matter), plus the
// port when it isn't 80 or 443. The scheme is ignored, so an http site
// that moves to https is the same site.
func HostKey(u *url.URL) string {
	key := normHost(u.Hostname())
	if strings.Contains(key, ":") {
		key = "[" + key + "]" // IPv6
	}
	if p := normPort(u.Port()); p != "" {
		key += ":" + p
	}
	return key
}

// ParseHost reads a host as a user types it ("example.com",
// "localhost:8080", "https://cdn.example.com/") and returns its HostKey.
func ParseHost(s string) (string, error) {
	raw := strings.TrimSpace(s)
	if !strings.Contains(raw, "://") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("%q isn't a host like example.com or localhost:8080", s)
	}
	return HostKey(u), nil
}

// IsLoopback reports whether host (no port) is this machine: localhost,
// 127.0.0.0/8 or ::1. Nothing sent there leaves the computer.
func IsLoopback(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "localhost" { // not *.localhost: a resolver may send those anywhere
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
