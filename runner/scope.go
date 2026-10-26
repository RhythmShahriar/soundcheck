// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/httpx"
)

// lookup resolves a host to its addresses. Tests replace it to make a
// site on 127.0.0.1 look public.
var lookup = httpx.Resolve

// lookupTimeout bounds the start of the run's lookup of the target.
const lookupTimeout = 10 * time.Second

// targetAddrs resolves u's host once for the run. The network scope of
// the browser (capture.Scope) and of the run's own requests (guard)
// both follow what it finds.
func targetAddrs(ctx context.Context, u *url.URL) []netip.Addr {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	return lookup(ctx, u.Hostname())
}

// guard keeps the run's own requests (the target's first fetch,
// robots.txt, sitemaps, the modules' checks) on the target's networks,
// as capture.Scope keeps the browser's: kit httpx's rule, a loopback,
// link-local or private address only for a target of the same kind,
// applied to the addresses the target resolved to at the start of the
// run. So a public site's sitemap can't send the run to the cloud's
// metadata service or the runner's own network. The target itself and a
// proxy are dialed as they are.
type guard struct {
	addrs   []netip.Addr
	exempt  map[string]bool // host:port of the target
	proxies sync.Map        // host:port of a proxy in use
}

func newGuard(site *url.URL, addrs []netip.Addr) *guard {
	g := &guard{addrs: addrs, exempt: map[string]bool{}}
	host := site.Hostname()
	if p := site.Port(); p != "" {
		g.exempt[net.JoinHostPort(host, p)] = true
	} else {
		// A bare hostname is tried on https and then on http.
		g.exempt[net.JoinHostPort(host, "443")] = true
		g.exempt[net.JoinHostPort(host, "80")] = true
	}
	return g
}

// proxy wraps a transport's Proxy so the proxy's address is dialed
// without the check: the user set it up and it does the connecting.
func (g *guard) proxy(next func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(r *http.Request) (*url.URL, error) {
		p, err := next(r)
		if p != nil {
			port := p.Port()
			if port == "" {
				port = map[string]string{"http": "80", "https": "443", "socks5": "1080", "socks5h": "1080"}[p.Scheme]
			}
			g.proxies.Store(net.JoinHostPort(p.Hostname(), port), true)
		}
		return p, err
	}
}

// dial connects like the default transport but refuses an address out
// of scope before any packet is sent. The check runs on each resolved
// address, so a name that points at a private address is caught too.
func (g *guard) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if _, proxied := g.proxies.Load(addr); !proxied && !g.exempt[addr] {
		d.ControlContext = func(_ context.Context, _, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
			if err != nil {
				return err
			}
			if !httpx.AllowAddr(g.addrs, ip) {
				return fmt.Errorf("refusing to connect to %s: %w outside the target's network", ip, httpx.ErrPrivate)
			}
			return nil
		}
	}
	return d.DialContext(ctx, network, addr)
}
