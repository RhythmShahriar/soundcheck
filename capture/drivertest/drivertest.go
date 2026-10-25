// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package drivertest is the contract every capture.Driver keeps: one
// suite of tests that runs against Chrome, Firefox and WebKit alike, on
// local test servers.
package drivertest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
)

// Options say what the driver under test can do.
type Options struct {
	// Classic is a driver without WebDriver BiDi. It has no events,
	// preload scripts, user agent or header scoping; its screenshots stop
	// at the viewport. The suite checks those fail with
	// capture.ErrUnsupported (or report nothing) instead of working.
	Classic bool
}

// Run runs the contract against the drivers launch starts. launch is
// called once per subtest and the driver is closed after it.
func Run(t *testing.T, launch func(t *testing.T) capture.Driver, opts Options) {
	t.Helper()
	tests := []struct {
		name string
		fn   func(t *testing.T, s *servers, tab capture.Tab, opts Options)
	}{
		{"Eval", testEval},
		{"InitScript", testInitScript},
		{"Viewport", testViewport},
		{"Screenshot", testScreenshot},
		{"Events", testEvents},
		{"Network", testNetwork},
		{"ScopeHeaders", testScopeHeaders},
		{"UserAgent", testUserAgent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServers(t)
			d := launch(t)
			t.Cleanup(func() { _ = d.Close() })
			tab, err := d.NewTab(ctxFor(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tab.Close() })
			tt.fn(t, s, tab, opts)
		})
	}
}

// ctxFor is a subtest's budget: 30 s on a quiet machine, scaled for a
// slow runner.
func ctxFor(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(30*time.Second))
	t.Cleanup(cancel)
	return ctx
}

// servers are the site under test and a third party it loads from.
type servers struct {
	site, other *httptest.Server
	mu          sync.Mutex
	seen        []seen
}

// seen is a request a server got.
type seen struct {
	server, path, ua, scope, cookie string
}

func newServers(t *testing.T) *servers {
	s := &servers{}
	s.other = httptest.NewServer(s.record("other", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`)
	}))
	t.Cleanup(s.other.Close)
	// Cookies don't tell ports apart: the third party is on another
	// host name, as a real one would be.
	s.other.URL = strings.Replace(s.other.URL, "127.0.0.1", "localhost", 1)
	s.site = httptest.NewServer(s.record("site", s.serveSite))
	t.Cleanup(s.site.Close)
	return s
}

func (s *servers) record(name string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, seen{name, r.URL.Path, r.UserAgent(), r.Header.Get("X-Scope"), r.Header.Get("Cookie")})
		s.mu.Unlock()
		h(w, r)
	}
}

func (s *servers) requests(server string) []seen {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(s.seen), func(x seen) bool { return x.server != server })
}

func (s *servers) serveSite(w http.ResponseWriter, r *http.Request) {
	html := func(body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>Contract</title></head><body style="margin:0">`+body+`</body></html>`)
	}
	switch r.URL.Path {
	case "/":
		html(`<p>hello</p>`)
	case "/order":
		html(`<script>window.__order = window.__init === 1 ? 'after' : 'before';</script>`)
	case "/tall":
		html(`<div style="height:3000px;background:linear-gradient(red,blue)"></div>`)
	case "/errors":
		html(`<img src="/missing.png"><script>console.error("contract error"); setTimeout(() => { throw new Error("contract boom"); }, 0);</script>`)
	case "/redirect":
		http.Redirect(w, r, "/final", http.StatusMovedPermanently)
	case "/final":
		html(`<img src="/missing.png"><script>fetch("/api");</script>`)
	case "/third":
		html(`<img src="` + s.other.URL + `/pixel.svg"><img src="/own.svg">`)
	case "/own.svg":
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`)
	case "/api":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

func navigate(t *testing.T, tab capture.Tab, u string) {
	t.Helper()
	if err := tab.Navigate(ctxFor(t), u); err != nil {
		t.Fatalf("Navigate %s: %v", u, err)
	}
}

func testEval(t *testing.T, s *servers, tab capture.Tab, _ Options) {
	ctx := ctxFor(t)
	navigate(t, tab, s.site.URL+"/")
	var title string
	if err := tab.Eval(ctx, `() => document.title`, &title); err != nil || title != "Contract" {
		t.Errorf("title = %q, %v", title, err)
	}
	var sum int
	if err := tab.Eval(ctx, `(a, b) => a + b`, &sum, 2, 3); err != nil || sum != 5 {
		t.Errorf("args: %d, %v", sum, err)
	}
	var obj struct {
		Items []string `json:"items"`
		N     float64  `json:"n"`
	}
	if err := tab.Eval(ctx, `async () => { await new Promise(r => setTimeout(r, 10)); return {items: ['a', 'b'], n: 1.5}; }`, &obj); err != nil ||
		!slices.Equal(obj.Items, []string{"a", "b"}) || obj.N != 1.5 {
		t.Errorf("promise: %+v, %v", obj, err)
	}
	if err := tab.Eval(ctx, `() => { throw new Error("nope") }`, nil); err == nil {
		t.Error("a throwing script returned no error")
	}
	if err := tab.Eval(ctx, `() => undefined`, nil); err != nil {
		t.Errorf("undefined: %v", err)
	}
	u, err := tab.URL(ctx)
	if err != nil || u != s.site.URL+"/" {
		t.Errorf("URL = %q, %v", u, err)
	}
}

func testInitScript(t *testing.T, s *servers, tab capture.Tab, opts Options) {
	err := tab.AddInitScript(ctxFor(t), `window.__init = 1;`)
	if opts.Classic {
		if !errors.Is(err, capture.ErrUnsupported) {
			t.Errorf("classic AddInitScript = %v, want ErrUnsupported", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	navigate(t, tab, s.site.URL+"/order")
	var order string
	if err := tab.Eval(ctxFor(t), `() => window.__order`, &order); err != nil || order != "after" {
		t.Errorf("the page's script ran %q the init script (%v), want after", order, err)
	}
}

func testViewport(t *testing.T, s *servers, tab capture.Tab, _ Options) {
	ctx := ctxFor(t)
	if err := tab.SetViewport(ctx, capture.Viewport{Width: 640, Height: 480}); err != nil {
		t.Fatal(err)
	}
	navigate(t, tab, s.site.URL+"/")
	var size [2]int
	if err := tab.Eval(ctx, `() => [innerWidth, innerHeight]`, &size); err != nil || size != [2]int{640, 480} {
		t.Errorf("viewport = %v, %v; want [640 480]", size, err)
	}
}

func pngSize(t *testing.T, shot func() ([]byte, error)) (int, int) {
	t.Helper()
	data, err := shot()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	return cfg.Width, cfg.Height
}

func testScreenshot(t *testing.T, s *servers, tab capture.Tab, opts Options) {
	ctx := ctxFor(t)
	if err := tab.SetViewport(ctx, capture.Viewport{Width: 640, Height: 480}); err != nil {
		t.Fatal(err)
	}
	navigate(t, tab, s.site.URL+"/tall")
	w, h := pngSize(t, func() ([]byte, error) { return tab.Screenshot(ctx, false) })
	if w != 640 || h != 480 {
		t.Errorf("viewport shot = %dx%d, want 640x480", w, h)
	}
	w, h = pngSize(t, func() ([]byte, error) { return tab.Screenshot(ctx, true) })
	if opts.Classic {
		if h != 480 {
			t.Errorf("classic full shot = %dx%d, want the viewport", w, h)
		}
	} else if w != 640 || h < 3000 {
		t.Errorf("full shot = %dx%d, want 640 by at least 3000", w, h)
	}
	clipH := 1500
	if opts.Classic {
		clipH = 300
	}
	w, h = pngSize(t, func() ([]byte, error) { return tab.Clip(ctx, 600, clipH) })
	if w != 600 || h != clipH {
		t.Errorf("clip = %dx%d, want 600x%d", w, h, clipH)
	}
}

// collect gathers events until stop.
type collect[T any] struct {
	mu  sync.Mutex
	got []T
}

func (c *collect[T]) add(e T) {
	c.mu.Lock()
	c.got = append(c.got, e)
	c.mu.Unlock()
}

// wait returns the events once ok holds for them, or after a few
// seconds.
func (c *collect[T]) wait(ok func([]T) bool) []T {
	deadline := time.Now().Add(testenv.Timeout(5 * time.Second))
	for {
		c.mu.Lock()
		got := slices.Clone(c.got)
		c.mu.Unlock()
		if ok(got) || time.Now().After(deadline) {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func testEvents(t *testing.T, s *servers, tab capture.Tab, opts Options) {
	var c collect[check.Event]
	stop := tab.On(ctxFor(t), c.add)
	defer stop()
	navigate(t, tab, s.site.URL+"/errors")
	has := func(got []check.Event, kind check.EventKind, match func(check.Event) bool) bool {
		return slices.ContainsFunc(got, func(e check.Event) bool { return e.Kind == kind && match(e) })
	}
	console := func(e check.Event) bool { return e.Level == "error" && e.Text == "contract error" }
	exception := func(e check.Event) bool {
		return strings.Contains(e.Text, "contract boom") && e.URL == s.site.URL+"/errors" && e.Line == 1
	}
	missing := func(e check.Event) bool {
		return strings.HasSuffix(e.URL, "/missing.png") && e.Status == 404 && e.Type == "Image"
	}
	got := c.wait(func(got []check.Event) bool {
		return has(got, check.Console, console) && has(got, check.Exception, exception) && has(got, check.Response, missing)
	})
	if opts.Classic {
		if len(got) > 0 {
			t.Errorf("classic sent events: %+v", got)
		}
		return
	}
	if !has(got, check.Console, console) {
		t.Errorf("no console error in %+v", got)
	}
	if !has(got, check.Exception, exception) {
		t.Errorf("no exception at %s/errors:1 in %+v", s.site.URL, got)
	}
	if !has(got, check.Response, missing) {
		t.Errorf("no 404 image response in %+v", got)
	}
}

func testNetwork(t *testing.T, s *servers, tab capture.Tab, opts Options) {
	var c collect[capture.NetEvent]
	stop := tab.Network(ctxFor(t), c.add)
	defer stop()
	navigate(t, tab, s.site.URL+"/redirect")
	find := func(got []capture.NetEvent, phase capture.NetPhase, match func(capture.NetEvent) bool) *capture.NetEvent {
		for i := range got {
			if got[i].Phase == phase && match(got[i]) {
				return &got[i]
			}
		}
		return nil
	}
	redirect := func(e capture.NetEvent) bool {
		return e.Document && e.Status == 301 && strings.HasSuffix(e.URL, "/redirect")
	}
	final := func(e capture.NetEvent) bool {
		return e.Document && e.Status == 200 && strings.HasSuffix(e.URL, "/final")
	}
	image := func(e capture.NetEvent) bool { return strings.HasSuffix(e.URL, "/missing.png") }
	api := func(e capture.NetEvent) bool { return strings.HasSuffix(e.URL, "/api") }
	got := c.wait(func(got []capture.NetEvent) bool {
		return find(got, capture.NetResponse, redirect) != nil && find(got, capture.NetResponse, final) != nil &&
			find(got, capture.NetResponse, func(e capture.NetEvent) bool { return image(e) && e.Status == 404 }) != nil &&
			find(got, capture.NetFinished, func(capture.NetEvent) bool { return true }) != nil &&
			find(got, capture.NetResponse, api) != nil
	})
	if opts.Classic {
		if len(got) > 0 {
			t.Errorf("classic sent network events: %+v", got)
		}
		return
	}
	req := find(got, capture.NetRequest, func(e capture.NetEvent) bool { return e.Document && strings.HasSuffix(e.URL, "/redirect") })
	if req == nil || req.Method != "GET" || req.Type != "Document" {
		t.Fatalf("no document request for /redirect in %+v", got)
	}
	if r := find(got, capture.NetResponse, redirect); r == nil || r.ID != req.ID || r.Headers["location"] != "/final" {
		t.Errorf("redirect response: %+v", r)
	}
	if r := find(got, capture.NetResponse, final); r == nil || r.ID != req.ID || r.Mime != "text/html" || r.Headers["content-type"] == "" {
		t.Errorf("final response: %+v (request %s)", r, req.ID)
	}
	if f := find(got, capture.NetFinished, func(e capture.NetEvent) bool { return e.ID == req.ID }); f == nil || f.Bytes <= 0 {
		t.Errorf("document finished: %+v", f)
	}
	if r := find(got, capture.NetResponse, image); r == nil || r.Status != 404 || r.Type != "Image" || r.Document {
		t.Errorf("image response: %+v", r)
	}
	if r := find(got, capture.NetResponse, api); r == nil || r.Type != "Fetch" {
		t.Errorf("fetch response: %+v", r)
	}
}

func testScopeHeaders(t *testing.T, s *servers, tab capture.Tab, opts Options) {
	site, _ := url.Parse(s.site.URL)
	release, err := tab.ScopeHeaders(ctxFor(t), site, map[string]string{"X-Scope": "secret", "Cookie": "sc=1; other=2"})
	if opts.Classic {
		if !errors.Is(err, capture.ErrUnsupported) {
			t.Errorf("classic ScopeHeaders = %v, want ErrUnsupported", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	navigate(t, tab, s.site.URL+"/third")
	deadline := time.Now().Add(testenv.Timeout(5 * time.Second))
	for len(s.requests("other")) == 0 || !slices.ContainsFunc(s.requests("site"), func(x seen) bool { return x.path == "/own.svg" }) {
		if time.Now().After(deadline) {
			t.Fatalf("the page's images never loaded: site %+v, other %+v", s.requests("site"), s.requests("other"))
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, r := range s.requests("site") {
		if r.path == "/favicon.ico" {
			continue
		}
		if r.scope != "secret" || !strings.Contains(r.cookie, "sc=1") || !strings.Contains(r.cookie, "other=2") {
			t.Errorf("site request %s got X-Scope %q, Cookie %q", r.path, r.scope, r.cookie)
		}
	}
	for _, r := range s.requests("other") {
		if r.scope != "" || r.cookie != "" {
			t.Errorf("third party %s got X-Scope %q, Cookie %q", r.path, r.scope, r.cookie)
		}
	}
	var visible string
	if err := tab.Eval(ctxFor(t), `() => document.cookie`, &visible); err != nil || visible != "" {
		t.Errorf("the page's scripts can read the cookies: %q, %v", visible, err)
	}
	release()
}

func testUserAgent(t *testing.T, s *servers, tab capture.Tab, opts Options) {
	err := tab.SetUserAgent(ctxFor(t), "SoundcheckContract/1")
	if opts.Classic {
		if !errors.Is(err, capture.ErrUnsupported) {
			t.Errorf("classic SetUserAgent = %v, want ErrUnsupported", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	navigate(t, tab, s.site.URL+"/")
	for _, r := range s.requests("site") {
		if r.path == "/" && r.ua != "SoundcheckContract/1" {
			t.Errorf("the site got User-Agent %q", r.ua)
		}
	}
}
