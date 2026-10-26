// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture

import (
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/rhythm/kit/httpx"
	"github.com/RhythmShahriar/soundcheck/capture/intercept"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
)

// ScopeError is the error text of a request the network scope blocked,
// as Page.Failed records it.
const ScopeError = "blocked by Soundcheck's network scope"

// Scope keeps a run's browser tabs on the networks the site is on. A
// page can make the browser request any address. Soundcheck runs on
// the user's machine or a CI runner: a public site checked from a cloud
// runner could otherwise read the cloud's metadata service
// (169.254.169.254) or a service on the runner's own network into a
// screenshot or a report.
//
// So a tab never requests a link-local address or a cloud metadata name.
// When the target is public it requests no loopback or private address
// either.
// A site on localhost or on the office network keeps its own
// neighbors. The site itself and the hosts given with --allow-host are
// always allowed. Requests are judged by the address in their URL; a
// name that resolves to a private address is caught when its answer
// comes in (see Scope.Response). That includes the site's own name: one
// rebound after the run started to an address of a class it didn't
// have is caught too.
type Scope struct {
	// Site is the site being checked. Its own host is always allowed.
	Site *url.URL
	// Target are the addresses Site's host resolved to at the start of
	// the run. A loopback or private one lets the tabs reach loopback and
	// private addresses; a link-local one lets them reach link-local ones.
	Target []netip.Addr
	// Allow are the --allow-host hosts, as config.HostKey gives them.
	Allow []string
	// Warn receives one warning per host and address whose answer came
	// from a private address while the target is public.
	Warn func(string)
	// Proxied says the browser connects through a proxy. Chrome then
	// reports the proxy's address for every answer, which says nothing
	// about where a name points, so Response judges none.
	Proxied bool

	warned sync.Map
}

// Public reports whether the target is on a public network: it resolved
// to no address, or to a public one among them. Unknown counts as
// public, which allows the least.
func Public(target []netip.Addr) bool {
	if len(target) == 0 {
		return true
	}
	return slices.ContainsFunc(target, func(ip netip.Addr) bool { return httpx.AddrClass(ip) == "" })
}

// metadataHosts are cloud metadata services by name. They answer on a
// link-local address; a page's request for the name would only be
// caught once it had been answered.
var metadataHosts = []string{"metadata.google.internal", "metadata"}

// allows reports whether the run's targets reach addresses of class,
// as httpx.AddrClass names them.
func (s *Scope) allows(class string) bool {
	switch class {
	case "":
		return true
	case "link-local":
		return slices.ContainsFunc(s.Target, func(ip netip.Addr) bool { return httpx.AddrClass(ip) == class })
	}
	return !Public(s.Target)
}

// exempt reports whether u is the site itself or a host the user
// allowed.
func (s *Scope) exempt(u *url.URL) bool {
	return check.SameSite(s.Site, u) || slices.Contains(s.Allow, config.HostKey(u))
}

// Blocks reports whether a tab may not request u.
func (s *Scope) Blocks(u *url.URL) bool {
	if s == nil || u == nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss") || s.exempt(u) {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	switch {
	case slices.Contains(metadataHosts, host):
		return !s.allows("link-local")
	case host == "localhost" || strings.HasSuffix(host, ".localhost"):
		// Chrome answers these itself, with a loopback address.
		return !s.allows("loopback")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return !s.allows(httpx.AddrClass(ip))
}

// Response reports whether an answer to u that came from remote (the
// address Chrome connected to, "" when it didn't say) is out of scope: a
// name that resolved to an address its URL didn't show. The first time
// a host and address are seen it adds a run warning.
//
// The site's own name is judged too, more strictly: its answer has to
// come from an address of a class the name resolved to at the start of
// the run (httpx.AllowAddr). A public shop.example.com that answers from
// 169.254.169.254 or 10.0.0.7 later on was rebound: the page's own
// scripts could read that answer same-origin. A site given by its
// address can't be rebound. The --allow-host hosts stay exempt: the user
// vouched for them.
func (s *Scope) Response(u *url.URL, remote string) bool {
	if s == nil || u == nil || remote == "" || s.Proxied {
		return false
	}
	ip, err := netip.ParseAddr(strings.Trim(remote, "[]"))
	if err != nil {
		return false
	}
	switch {
	case check.SameSite(s.Site, u):
		if literal(u) || httpx.AllowAddr(s.Target, ip) {
			return false
		}
	case slices.Contains(s.Allow, config.HostKey(u)) || s.allows(httpx.AddrClass(ip)):
		return false
	}
	host := u.Hostname()
	if _, seen := s.warned.LoadOrStore(host+" "+ip.String(), true); !seen && s.Warn != nil {
		s.Warn(fmt.Sprintf("blocked a response from %s at %s: %s address while the target is %s", host, ip, httpx.AddrClass(ip), s.targetClass()))
	}
	return true
}

// literal reports whether u's host is an IP address, which no DNS answer
// can change.
func literal(u *url.URL) bool {
	_, err := netip.ParseAddr(strings.Trim(u.Hostname(), "[]"))
	return err == nil
}

// targetClass names the target's network for a warning: "public", or
// the class of its addresses ("private" for a site on 10.0.0.5).
func (s *Scope) targetClass() string {
	if Public(s.Target) {
		return "public"
	}
	return httpx.AddrClass(s.Target[0])
}

// scopePatterns are the URLs the scope has Chrome pause, a cheap first
// cut: Blocks decides. The others go on without a round trip to
// Soundcheck, so a run's timings stay what they were.
func (s *Scope) scopePatterns() []string {
	ps := []string{"*://169.254.*", "*@169.254.*", "*://[*", "*@[*", "*://metadata*", "*@metadata*"}
	if Public(s.Target) {
		for _, h := range []string{"127.", "10.", "172.", "192.168.", "100.", "0.", "localhost", "*.localhost"} {
			ps = append(ps, "*://"+h+"*", "*@"+h+"*")
		}
	}
	return ps
}

// Guard blocks the requests of tab the scope doesn't allow until
// release is called. A tab a module opens itself (a login, a flow) is
// kept in scope with it; Start guards the run's own tabs. blocked, when
// not nil, is called with each blocked URL, so the page can record why
// it failed. A nil Scope guards nothing.
func (s *Scope) Guard(tab *rod.Page, blocked func(string)) (release func(), err error) {
	if s == nil {
		return func() {}, nil
	}
	return intercept.AddFor(tab, s.scopePatterns(), func(q *intercept.Request) {
		if s.Blocks(q.URL) {
			q.Block()
			if blocked != nil {
				blocked(q.URL.String())
			}
		}
	})
}
