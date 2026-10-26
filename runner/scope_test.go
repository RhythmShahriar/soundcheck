// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/httpx"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// loopback is what a site on 127.0.0.1 resolves to.
var loopback = []netip.Addr{netip.MustParseAddr("127.0.0.1")}

// publicAddr is a documentation address (RFC 5737) standing in for a
// public site's.
var publicAddr = netip.MustParseAddr("203.0.113.10")

// pretendPublic makes every host resolve to publicAddr until the test
// ends, so a site on 127.0.0.1 is treated as a public one.
func pretendPublic(t *testing.T) {
	t.Helper()
	old := lookup
	lookup = func(context.Context, string) []netip.Addr { return []netip.Addr{publicAddr} }
	t.Cleanup(func() { lookup = old })
}

// counter is a local server that counts the requests it gets.
type counter struct {
	n   atomic.Int32
	srv *http.Server
	url string
}

// listen starts a counter on 127.0.0.1:port (the lane's fixed ports).
func listen(t *testing.T, port int, h http.HandlerFunc) *counter {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Skip("port in use: ", err)
	}
	c := &counter{url: "http://" + ln.Addr().String()}
	c.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.n.Add(1)
		if h != nil {
			h(w, r)
		}
	})}
	go func() { _ = c.srv.Serve(ln) }()
	t.Cleanup(func() { _ = c.srv.Close() })
	return c
}

// The run's own client reaches only the target's networks: with a
// public target, another local port and the metadata address are
// refused before anything is sent; with a local target, its neighbors
// stay reachable.
func TestGuardKeepsTheClientInScope(t *testing.T) {
	site := listen(t, 37010, nil)
	other := listen(t, 37011, nil)
	u, _ := url.Parse(site.url)
	get := func(c *http.Client, raw string) error {
		resp, err := c.Get(raw)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}

	public := httpClient(Options{}, u, []netip.Addr{publicAddr})
	if err := get(public, site.url+"/robots.txt"); err != nil {
		t.Fatalf("the target itself: %v", err)
	}
	for _, raw := range []string{other.url + "/sitemap.xml", "http://169.254.169.254/latest/meta-data/"} {
		if err := get(public, raw); !errors.Is(err, httpx.ErrPrivate) {
			t.Errorf("%s from a public target: %v", raw, err)
		}
	}
	if n := other.n.Load(); n != 0 {
		t.Errorf("the other port got %d requests", n)
	}

	local := httpClient(Options{}, u, loopback)
	if err := get(local, other.url+"/"); err != nil || other.n.Load() != 1 {
		t.Errorf("a neighbor of a local target: %v, %d requests", err, other.n.Load())
	}
	if err := get(local, "http://169.254.169.254/"); !errors.Is(err, httpx.ErrPrivate) {
		t.Errorf("link-local from a local target: %v", err)
	}
}

// A proxy the user set is dialed even on a private address: it does the
// connecting.
func TestGuardLetsTheProxyThrough(t *testing.T) {
	proxy := listen(t, 37012, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "via proxy") })
	pu, _ := url.Parse(proxy.url)
	site, _ := url.Parse("http://shop.example.com/")
	c := httpClient(Options{Proxy: pu}, site, []netip.Addr{publicAddr})
	resp, err := c.Get("http://shop.example.com/robots.txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if proxy.n.Load() != 1 {
		t.Errorf("the proxy got %d requests", proxy.n.Load())
	}
}

func TestPublic(t *testing.T) {
	for _, c := range []struct {
		addrs []netip.Addr
		want  bool
	}{
		{nil, true}, // unknown: the strictest scope
		{[]netip.Addr{publicAddr}, true},
		{loopback, false},
		{[]netip.Addr{netip.MustParseAddr("10.0.0.5")}, false},
		{[]netip.Addr{netip.MustParseAddr("10.0.0.5"), publicAddr}, true},
	} {
		if got := capture.Public(c.addrs); got != c.want {
			t.Errorf("Public(%v) = %v", c.addrs, got)
		}
	}
}

// targetSeen is a module that notes the run's TargetPublic.
type targetSeen struct {
	mu     sync.Mutex
	public []bool
}

func (*targetSeen) Name() string { return "target-seen" }

func (m *targetSeen) CheckPage(cc *check.Context, _ *model.Page) {
	m.mu.Lock()
	m.public = append(m.public, cc.TargetPublic)
	m.mu.Unlock()
}

// A public site's page can't make the browser or the run's client reach
// the local network or the metadata service: the frames are blocked and
// recorded as blocked, the sitemap on another local port isn't fetched.
// Checked as a local site, the same page loads its local frame.
func TestNetworkScope(t *testing.T) {
	needBrowser(t)
	frame := listen(t, 37001, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Router admin</title>`)
	})
	site := listen(t, 37000, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", frame.url)
		case "/":
			fmt.Fprintf(w, `<!doctype html><html lang="en"><title>Shop</title><h1>Shop</h1>
<iframe src="http://169.254.169.254/latest/meta-data/"></iframe>
<iframe src="%s/admin"></iframe>
<img src="http://metadata.google.internal/computeMetadata/v1/" alt="">`, frame.url)
		default:
			http.NotFound(w, r)
		}
	})
	run := func() (*model.Run, *targetSeen) {
		t.Helper()
		seen := &targetSeen{}
		cfg := config.Default()
		cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Pages.Limit = []string{"health"}, []string{"desktop"}, 0, 1
		r, err := Run(context.Background(), Options{Config: cfg, Target: site.url, Dir: t.TempDir(), Version: "test",
			Modules: []check.Module{seen}, Browser: browser.Options{NoDownload: true}})
		if err != nil {
			t.Fatal(err)
		}
		return r, seen
	}

	pretendPublic(t)
	r, seen := run()
	if n := frame.n.Load(); n != 0 {
		t.Errorf("a public site reached another local port %d times", n)
	}
	if !slices.Equal(seen.public, []bool{true}) {
		t.Errorf("TargetPublic = %v, want true", seen.public)
	}
	blocked := map[string]bool{}
	for _, f := range r.Pages[0].Failed {
		if f.Error == capture.ScopeError {
			blocked[f.URL] = true
		}
	}
	for _, want := range []string{"http://169.254.169.254/latest/meta-data/", frame.url + "/admin", "http://metadata.google.internal/computeMetadata/v1/"} {
		if !blocked[want] {
			t.Errorf("%s isn't recorded as blocked: %+v", want, r.Pages[0].Failed)
		}
	}

	lookup = httpx.Resolve
	r, seen = run()
	if frame.n.Load() == 0 {
		t.Error("a local site's own neighbor didn't load")
	}
	blocked = map[string]bool{}
	for _, f := range r.Pages[0].Failed {
		if f.Error == capture.ScopeError {
			blocked[f.URL] = true
		}
	}
	if blocked[frame.url+"/admin"] || !blocked["http://169.254.169.254/latest/meta-data/"] {
		t.Errorf("local site: blocked %v", blocked)
	}
	if !slices.Equal(seen.public, []bool{false}) {
		t.Errorf("TargetPublic = %v, want false", seen.public)
	}
}
