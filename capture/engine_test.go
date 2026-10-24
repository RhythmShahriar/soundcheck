// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// fakeTab plays a page load back as an engine would report it.
type fakeTab struct {
	mu       sync.Mutex
	net      func(NetEvent)
	on       func(check.Event)
	viewport []Viewport
	init     []string
	ua       string
	scoped   map[string]string
	navErr   error // Navigate's error, after the events
	hang     bool  // Navigate waits for ctx, after the events
	initErr  error
	scopeErr error
	load     func(t *fakeTab, url string) // sends the page's events
	clips    [][2]int
}

var _ Tab = (*fakeTab)(nil)

func (t *fakeTab) Rod() *rod.Page { return nil }

func (t *fakeTab) Eval(_ context.Context, js string, out any, _ ...any) error {
	switch {
	case js == factsJS:
		f := out.(*pageFacts)
		*f = pageFacts{Title: " Shop ", Text: "Hello  world", Links: []string{"http://site.test/about/", "https://other.example/"},
			FCP: 120, Height: 2000}
	case strings.Contains(js, "scrollHeight"):
		*(out.(*int)) = 1500
	}
	return nil
}

func (t *fakeTab) Screenshot(context.Context, bool) ([]byte, error) { return nil, errors.New("unused") }

func (t *fakeTab) Clip(_ context.Context, w, h int) ([]byte, error) {
	t.mu.Lock()
	t.clips = append(t.clips, [2]int{w, h})
	t.mu.Unlock()
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, 10)))
	return buf.Bytes(), nil
}

func (t *fakeTab) On(_ context.Context, fn func(check.Event)) func() {
	t.on = fn
	return func() {}
}

func (t *fakeTab) Network(_ context.Context, fn func(NetEvent)) func() {
	t.net = fn
	return func() {}
}

func (t *fakeTab) Navigate(ctx context.Context, u string) error {
	if t.load != nil {
		t.load(t, u)
	}
	if t.hang {
		<-ctx.Done()
		return ctx.Err()
	}
	return t.navErr
}

func (t *fakeTab) URL(context.Context) (string, error) { return "http://site.test/", nil }

func (t *fakeTab) SetViewport(_ context.Context, v Viewport) error {
	t.viewport = append(t.viewport, v)
	return nil
}

func (t *fakeTab) SetUserAgent(_ context.Context, ua string) error { t.ua = ua; return nil }
func (t *fakeTab) DisableCache(context.Context) error              { return fmt.Errorf("cache: %w", ErrUnsupported) }

func (t *fakeTab) AddInitScript(_ context.Context, js string) error {
	t.init = append(t.init, js)
	return t.initErr
}

func (t *fakeTab) ScopeHeaders(_ context.Context, _ *url.URL, h map[string]string) (func(), error) {
	t.scoped = h
	return func() { t.scoped = nil }, t.scopeErr
}

func (t *fakeTab) Close() error { return nil }

// shopLoad is a page behind two redirects with a console error, an
// exception, a broken image and a failed script.
func shopLoad(t *fakeTab, _ string) {
	n := t.net
	n(NetEvent{Phase: NetRequest, ID: "1", Document: true, URL: "http://site.test/old", Method: "GET", Type: "Document"})
	n(NetEvent{Phase: NetResponse, ID: "1", Document: true, URL: "http://site.test/old", Status: 301, Headers: map[string]string{"location": "/mid"}})
	n(NetEvent{Phase: NetRequest, ID: "1", Document: true, URL: "http://site.test/mid", Method: "GET", Type: "Document"})
	n(NetEvent{Phase: NetResponse, ID: "1", Document: true, URL: "http://site.test/mid", Status: 302, Headers: map[string]string{"location": "/"}})
	n(NetEvent{Phase: NetRequest, ID: "1", Document: true, URL: "http://site.test/", Method: "GET", Type: "Document"})
	n(NetEvent{Phase: NetResponse, ID: "1", Document: true, URL: "http://site.test/", Status: 200, Mime: "text/html",
		Headers: map[string]string{"content-type": "text/html", "x-robots-tag": "noarchive", "content-encoding": "gzip"}})
	n(NetEvent{Phase: NetFinished, ID: "1", Bytes: 1200})
	n(NetEvent{Phase: NetRequest, ID: "2", URL: "http://site.test/missing.png", Method: "GET", Type: "Image"})
	n(NetEvent{Phase: NetResponse, ID: "2", URL: "http://site.test/missing.png", Status: 404, Type: "Image"})
	n(NetEvent{Phase: NetFinished, ID: "2", Bytes: 100})
	n(NetEvent{Phase: NetRequest, ID: "3", URL: "http://cdn.test/app.js", Method: "GET", Type: "Script"})
	n(NetEvent{Phase: NetFailed, ID: "3", Error: "NS_ERROR_UNKNOWN_HOST"})
	n(NetEvent{Phase: NetRequest, ID: "4", URL: "http://site.test/slow.js", Method: "GET", Type: "Script"})
	n(NetEvent{Phase: NetFailed, ID: "4", Error: "NS_BINDING_ABORTED", Canceled: true})
	n(NetEvent{Phase: NetRequest, ID: "5", URL: "data:image/png;base64,xx", Method: "GET", Type: "Image"})
	n(NetEvent{Phase: NetFinished, ID: "5"})
	t.on(check.Event{Kind: check.Console, Level: "error", Text: "checkout failed"})
	t.on(check.Event{Kind: check.Console, Level: "warning", Text: "just a warning"})
	t.on(check.Event{Kind: check.Exception, Text: "ReferenceError: foo is not defined\nstack", URL: "http://site.test/", Line: 3})
}

func TestStartTabVisit(t *testing.T) {
	tab := &fakeTab{load: shopLoad}
	site, _ := url.Parse("http://site.test/")
	saved := map[string]int{}
	opts := Options{Timeout: 5 * time.Second, UserAgent: "SC/1", Headers: map[string]string{"X-Gate": "1"}, Site: site,
		Engine: Firefox, Save: func(name string, data []byte) error { saved[name] = len(data); return nil }}
	ctx := context.Background()
	r, err := StartTab(ctx, tab, opts)
	if err != nil {
		t.Fatal(err)
	}
	if tab.ua != "SC/1" || tab.scoped["X-Gate"] != "1" || !slices.Equal(tab.init, []string{metricsJS}) {
		t.Errorf("tab set up with ua %q, headers %v, init %d scripts", tab.ua, tab.scoped, len(tab.init))
	}
	p, err := Visit(ctx, r, "http://site.test/old", opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != 200 || p.Title != "Shop" || p.FinalURL != "http://site.test/" || p.Text != "Hello world" || p.Error != "" {
		t.Errorf("page: status %d, title %q, final %q, text %q, error %q", p.Status, p.Title, p.FinalURL, p.Text, p.Error)
	}
	if want := []string{"301 http://site.test/old", "302 http://site.test/mid"}; !slices.Equal(p.Redirects, want) {
		t.Errorf("redirects = %q, want %q", p.Redirects, want)
	}
	if want := []string{"checkout failed", "Uncaught ReferenceError: foo is not defined (http://site.test/:3)"}; !slices.Equal(p.Console, want) {
		t.Errorf("console = %q, want %q", p.Console, want)
	}
	if want := []model.Request{
		{URL: "http://site.test/missing.png", Method: "GET", Type: "Image", Status: 404},
		{URL: "http://cdn.test/app.js", Method: "GET", Type: "Script", Error: "NS_ERROR_UNKNOWN_HOST"},
	}; !slices.Equal(p.Failed, want) {
		t.Errorf("failed = %+v", p.Failed)
	}
	if p.Robots != "noarchive" || p.Headers["content-encoding"] != "gzip" || p.Metrics.FCP != 120 {
		t.Errorf("robots %q, headers %v, metrics %+v", p.Robots, p.Headers, p.Metrics)
	}
	// The document, the image, the failed and the canceled scripts; data:
	// URLs don't count.
	if p.Metrics.Requests != 4 || p.Metrics.Bytes != 1300 {
		t.Errorf("weight: %d requests, %d bytes", p.Metrics.Requests, p.Metrics.Bytes)
	}
	if !slices.Equal(p.Links, []string{"http://site.test/about/"}) {
		t.Errorf("links = %v", p.Links)
	}
	if s := p.Shots["desktop"]; s.File != "shots/"+p.ID+"-desktop-firefox.png" || s.Width != 1366 || saved[s.File] == 0 {
		t.Errorf("desktop shot %+v, saved %v", s, saved)
	}
	if !slices.Equal(tab.clips, [][2]int{{1366, 2000}}) || tab.viewport[0] != (Viewport{1366, 900, false}) {
		t.Errorf("clips %v, viewports %v", tab.clips, tab.viewport)
	}

	// Mobile: only the mobile load's errors are added.
	tab.load = func(t *fakeTab, _ string) {
		t.on(check.Event{Kind: check.Console, Level: "error", Text: "checkout failed"})
		t.on(check.Event{Kind: check.Console, Level: "assert", Text: "Assertion failed: mobile menu"})
	}
	if err := Mobile(ctx, r, p, "http://site.test/old", opts); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Console, "Assertion failed: mobile menu") || len(p.Console) != 3 {
		t.Errorf("console after mobile = %q", p.Console)
	}
	if s := p.Shots["mobile"]; s.File != "shots/"+p.ID+"-mobile-firefox.png" || tab.viewport[1] != (Viewport{390, 844, true}) ||
		tab.clips[1] != [2]int{390, 1500} {
		t.Errorf("mobile shot %+v, viewports %v, clips %v", s, tab.viewport, tab.clips)
	}
	r.Stop()
	if tab.scoped != nil {
		t.Error("Stop didn't release the headers")
	}
}

func TestStartTabFailures(t *testing.T) {
	site, _ := url.Parse("http://site.test/")
	ctx := context.Background()
	// Headers that can't be scoped stop the visit: they would go
	// everywhere.
	tab := &fakeTab{scopeErr: fmt.Errorf("sending X-Gate: %w", ErrUnsupported)}
	if _, err := StartTab(ctx, tab, Options{Headers: map[string]string{"X-Gate": "1"}, Site: site}); err == nil {
		t.Error("StartTab went on without scoping the headers")
	}
	// No preload scripts only costs the speed metrics.
	tab = &fakeTab{initErr: fmt.Errorf("preload: %w", ErrUnsupported)}
	if _, err := StartTab(ctx, tab, Options{}); err != nil {
		t.Errorf("StartTab without preload scripts: %v", err)
	}
	tab = &fakeTab{initErr: errors.New("tab crashed")}
	if _, err := StartTab(ctx, tab, Options{}); err == nil {
		t.Error("StartTab ignored a real error")
	}
}

func TestVisitTabNavigation(t *testing.T) {
	ctx := context.Background()
	opts := Options{Timeout: 300 * time.Millisecond}

	// A navigation that got no answer is the page's error.
	tab := &fakeTab{navErr: errors.New("browsingContext.navigate: unknown error: NS_ERROR_CONNECTION_REFUSED")}
	r, _ := StartTab(ctx, tab, opts)
	p, err := Visit(ctx, r, "http://site.test/", opts)
	if err != nil || p.Error != "NS_ERROR_CONNECTION_REFUSED" {
		t.Errorf("refused: %q, %v", p.Error, err)
	}

	// One that answered but never finished loading is read as it is.
	tab = &fakeTab{hang: true, load: func(t *fakeTab, _ string) {
		t.net(NetEvent{Phase: NetRequest, ID: "1", Document: true, URL: "http://site.test/", Type: "Document"})
		t.net(NetEvent{Phase: NetResponse, ID: "1", Document: true, URL: "http://site.test/", Status: 200})
		t.net(NetEvent{Phase: NetRequest, ID: "2", URL: "http://site.test/font.woff2", Type: "Font"})
	}}
	r, _ = StartTab(ctx, tab, opts)
	p, err = Visit(ctx, r, "http://site.test/", opts)
	if err != nil || p.Error != "" || p.Status != 200 || p.Title != "Shop" {
		t.Errorf("hung load: error %q, status %d, title %q, %v", p.Error, p.Status, p.Title, err)
	}

	// One that timed out with no answer did time out.
	tab = &fakeTab{hang: true}
	r, _ = StartTab(ctx, tab, opts)
	if p, _ = Visit(ctx, r, "http://site.test/", opts); p.Error != "timed out" {
		t.Errorf("no answer: %q", p.Error)
	}
}

func TestParseEngine(t *testing.T) {
	for in, want := range map[string]Engine{"": Chrome, "Chrome": Chrome, "chromium": Chrome, " firefox ": Firefox,
		"WebKit": WebKit, "safari": WebKit} {
		if got, err := ParseEngine(in); err != nil || got != want {
			t.Errorf("ParseEngine(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseEngine("lynx"); err == nil || !strings.Contains(err.Error(), "chrome, firefox or webkit") {
		t.Errorf("ParseEngine(lynx) = %v", err)
	}
}

func TestResourceType(t *testing.T) {
	for _, tc := range []struct{ dest, init, want string }{
		{"document", "", "Document"}, {"iframe", "", "Document"}, {"style", "link", "Stylesheet"},
		{"script", "script", "Script"}, {"image", "img", "Image"}, {"font", "css", "Font"},
		{"video", "", "Media"}, {"", "xmlhttprequest", "XHR"}, {"", "fetch", "Fetch"}, {"", "beacon", "Fetch"},
		{"", "img", "Image"}, {"", "", "Other"}, {"manifest", "", "Manifest"},
	} {
		if got := ResourceType(tc.dest, tc.init); got != tc.want {
			t.Errorf("ResourceType(%q, %q) = %q, want %q", tc.dest, tc.init, got, tc.want)
		}
	}
}

func TestShotFile(t *testing.T) {
	for _, tc := range []struct {
		engine       Engine
		second, want string
	}{
		{"", "", "shots/ab-desktop.png"},
		{Chrome, ".2", "shots/ab-desktop.2.png"},
		{Firefox, "", "shots/ab-desktop-firefox.png"},
		{WebKit, ".2", "shots/ab-desktop-webkit.2.png"},
	} {
		if got := shotFile("ab", "desktop", tc.engine, tc.second); got != tc.want {
			t.Errorf("shotFile(%q, %q) = %q, want %q", tc.engine, tc.second, got, tc.want)
		}
	}
}
