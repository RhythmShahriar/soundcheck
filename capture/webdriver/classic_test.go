// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/soundcheck/capture"
)

// fakeDriver is a classic WebDriver server: it keeps the windows and the
// current one. It answers scripts from a table.
type fakeDriver struct {
	mu      sync.Mutex
	bidi    string // the webSocketUrl it offers; empty for none
	windows []string
	current string
	next    int
	urls    map[string]string
	log     []string
	scripts []string
	result  any
	// sessionFails is how many POST /session fail with "session not
	// created" before one succeeds.
	sessionFails int
}

func (d *fakeDriver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	path := strings.TrimPrefix(r.URL.Path, "/session/s1")
	d.log = append(d.log, r.Method+" "+path+" "+d.current)
	reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"value": v}) }
	fail := func(code int, e string) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"value": map[string]string{"error": e, "message": "nope"}})
	}
	switch r.Method + " " + path {
	case "GET /status":
		reply(map[string]bool{"ready": true})
	case "POST /session":
		if d.sessionFails > 0 {
			d.sessionFails--
			fail(500, "session not created")
			return
		}
		d.windows, d.current, d.urls = []string{"w0"}, "w0", map[string]string{}
		caps := map[string]any{}
		if d.bidi != "" {
			caps["webSocketUrl"] = d.bidi
		}
		reply(map[string]any{"sessionId": "s1", "capabilities": caps})
	case "DELETE ":
		reply(nil)
	case "GET /window":
		reply(d.current)
	case "POST /window":
		h, _ := body["handle"].(string)
		if !slices.Contains(d.windows, h) {
			fail(404, "no such window")
			return
		}
		d.current = h
		reply(nil)
	case "POST /window/new":
		d.next++
		h := "w" + string(rune('0'+d.next))
		d.windows = append(d.windows, h)
		reply(map[string]string{"handle": h, "type": "window"})
	case "DELETE /window":
		d.windows = slices.DeleteFunc(d.windows, func(h string) bool { return h == d.current })
		reply(d.windows)
	case "POST /url":
		d.urls[d.current], _ = body["url"].(string)
		reply(nil)
	case "GET /url":
		reply(d.urls[d.current])
	case "POST /execute/async":
		s, _ := body["script"].(string)
		d.scripts = append(d.scripts, s)
		reply(d.result)
	case "POST /window/rect":
		reply(body)
	case "GET /screenshot":
		var buf bytes.Buffer
		_ = png.Encode(&buf, image.NewGray(image.Rect(0, 0, 800, 600)))
		reply(base64.StdEncoding.EncodeToString(buf.Bytes()))
	default:
		fail(404, "unknown command")
	}
}

func classicBrowser(t *testing.T, d *fakeDriver) *Browser {
	t.Helper()
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	b := &Browser{engine: capture.WebKit}
	if err := b.openSession(context.Background(), srv.URL, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if b.BiDi() {
		t.Fatal("BiDi without a webSocketUrl")
	}
	return b
}

func TestClassicTabs(t *testing.T) {
	d := &fakeDriver{}
	b := classicBrowser(t, d)
	ctx := context.Background()
	a, err := b.NewTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := b.NewTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Each tab's commands go to its own window.
	if err := a.Navigate(ctx, "http://site.test/a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Navigate(ctx, "http://site.test/c"); err != nil {
		t.Fatal(err)
	}
	if u, err := a.URL(ctx); err != nil || u != "http://site.test/a" {
		t.Errorf("a's URL = %q, %v", u, err)
	}
	if u, err := c.URL(ctx); err != nil || u != "http://site.test/c" {
		t.Errorf("c's URL = %q, %v", u, err)
	}

	d.result = map[string]any{"ok": `{"title":"Shop","n":2}`}
	var out struct {
		Title string
		N     int
	}
	if err := a.Eval(ctx, "(x) => ({title: document.title, n: x})", &out, 2); err != nil || out.Title != "Shop" || out.N != 2 {
		t.Errorf("eval = %+v, %v", out, err)
	}
	d.result = map[string]any{"err": "ReferenceError: x is not defined"}
	if err := a.Eval(ctx, "document.title", nil); err == nil || !strings.Contains(err.Error(), "ReferenceError") {
		t.Errorf("failing eval = %v", err)
	}
	if last := d.scripts[len(d.scripts)-1]; !strings.Contains(last, "(() => (document.title))") {
		t.Errorf("an expression wasn't wrapped as a function: %s", last)
	}

	img, err := a.Clip(ctx, 640, 300)
	if err != nil {
		t.Fatal(err)
	}
	if cfg, _ := png.DecodeConfig(bytes.NewReader(img)); cfg.Width != 640 || cfg.Height != 300 {
		t.Errorf("clip = %dx%d", cfg.Width, cfg.Height)
	}
	d.result = map[string]any{"ok": "[0,0]"}
	if err := a.SetViewport(ctx, capture.Viewport{Width: 640, Height: 480}); err != nil {
		t.Error(err)
	}

	// What classic WebDriver can't do fails or reports nothing.
	site, _ := url.Parse("http://site.test/")
	if _, err := a.ScopeHeaders(ctx, site, map[string]string{"X-Gate": "1", "Authorization": "x"}); !errors.Is(err, capture.ErrUnsupported) ||
		!strings.Contains(err.Error(), "sending Authorization, X-Gate to the site alone") {
		t.Errorf("ScopeHeaders = %v", err)
	}
	if _, err := a.ScopeHeaders(ctx, site, nil); err != nil {
		t.Errorf("ScopeHeaders without headers = %v", err)
	}
	if err := a.SetUserAgent(ctx, "x"); !errors.Is(err, capture.ErrUnsupported) {
		t.Errorf("SetUserAgent = %v", err)
	}
	if err := a.AddInitScript(ctx, "x"); !errors.Is(err, capture.ErrUnsupported) {
		t.Errorf("AddInitScript = %v", err)
	}
	if err := a.DisableCache(ctx); err != nil {
		t.Errorf("DisableCache = %v", err)
	}

	// Closing the tabs closes one window and keeps the last for the next
	// tab, which would otherwise end the session.
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if len(d.windows) != 1 {
		t.Errorf("windows left: %v", d.windows)
	}
	again, err := b.NewTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again.(*classicTab).handle != d.windows[0] || len(d.windows) != 1 {
		t.Errorf("the next tab got %s, windows %v", again.(*classicTab).handle, d.windows)
	}
	_ = b.Close()
	if last := d.log[len(d.log)-1]; !strings.HasPrefix(last, "DELETE  ") {
		t.Errorf("Close didn't end the session: %s", last)
	}
}

// A driver that offers a webSocketUrl it can't be reached on falls back
// to classic WebDriver, with a note.
func TestWebKitBiDiFallback(t *testing.T) {
	d := &fakeDriver{bidi: "ws://127.0.0.1:1/session"}
	srv := httptest.NewServer(d)
	defer srv.Close()
	var notes []string
	b := &Browser{engine: capture.WebKit, logf: func(s string) { notes = append(notes, s) }}
	if err := b.openSession(context.Background(), srv.URL, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if b.BiDi() || len(notes) != 1 || !strings.Contains(notes[0], "using classic WebDriver") {
		t.Errorf("BiDi %v, notes %q", b.BiDi(), notes)
	}
}

// A session the driver couldn't create is asked for once more; a second
// failure is the error.
func TestWebKitSessionRetry(t *testing.T) {
	for _, tc := range []struct {
		fails int
		ok    bool
	}{{1, true}, {2, false}} {
		d := &fakeDriver{sessionFails: tc.fails}
		srv := httptest.NewServer(d)
		var notes []string
		b := &Browser{engine: capture.WebKit, logf: func(s string) { notes = append(notes, s) }}
		err := b.startSession(context.Background(), srv.URL, map[string]any{})
		srv.Close()
		if (err == nil) != tc.ok || len(notes) != 1 || !strings.Contains(notes[0], "trying once more") {
			t.Errorf("%d failures: %v, notes %q", tc.fails, err, notes)
		}
		if !tc.ok && !strings.Contains(fmt.Sprint(err), "session not created") {
			t.Errorf("%d failures: %v", tc.fails, err)
		}
	}
}

func TestCrop(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewGray(image.Rect(0, 0, 100, 50)))
	small, err := crop(buf.Bytes(), 200, 200)
	if err != nil || !bytes.Equal(small, buf.Bytes()) {
		t.Errorf("a smaller image was changed: %v", err)
	}
	cut, err := crop(buf.Bytes(), 40, 200)
	if cfg, _ := png.DecodeConfig(bytes.NewReader(cut)); err != nil || cfg.Width != 40 || cfg.Height != 50 {
		t.Errorf("crop = %dx%d, %v", cfg.Width, cfg.Height, err)
	}
	if _, err := crop([]byte("nope"), 1, 1); err == nil {
		t.Error("cropped a non-PNG")
	}
}
