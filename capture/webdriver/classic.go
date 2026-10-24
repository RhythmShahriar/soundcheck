// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/check"
)

// classic is a classic WebDriver session, over HTTP. A session drives
// one window at a time, so each tab's command switches to the tab's
// window first, under mu.
type classic struct {
	base    string // the driver's URL, http://127.0.0.1:port
	session string
	http    *http.Client

	mu      sync.Mutex
	current string          // the window commands go to
	tabs    map[string]bool // the windows tabs are open in
	idle    []string        // windows kept open for the next tab
}

// do sends a command and decodes its "value" into out. A WebDriver error
// is returned as an *Error.
func (c *classic) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var res struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(&res); err != nil {
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(res.Value, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &Error{Code: e.Error, Message: e.Message}
	}
	if out == nil || len(res.Value) == 0 {
		return nil
	}
	return json.Unmarshal(res.Value, out)
}

// in sends a command to window handle: it switches to it first when
// another window had the last command.
func (c *classic) in(ctx context.Context, handle, method, path string, body, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current != handle {
		if err := c.do(ctx, http.MethodPost, c.path("/window"), map[string]string{"handle": handle}, nil); err != nil {
			return err
		}
		c.current = handle
	}
	return c.do(ctx, method, c.path(path), body, out)
}

func (c *classic) path(p string) string { return "/session/" + url.PathEscape(c.session) + p }

// newTab opens a window. The session's first window is the first tab's,
// and the last window open is kept for the next tab: closing it would
// end the session.
func (c *classic) newTab(ctx context.Context) (capture.Tab, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var handle string
	switch {
	case len(c.idle) > 0:
		handle, c.idle = c.idle[len(c.idle)-1], c.idle[:len(c.idle)-1]
	case len(c.tabs) == 0 && c.current == "":
		if err := c.do(ctx, http.MethodGet, c.path("/window"), nil, &handle); err != nil {
			return nil, err
		}
	default:
		var res struct {
			Handle string `json:"handle"`
		}
		if err := c.do(ctx, http.MethodPost, c.path("/window/new"), map[string]string{"type": "window"}, &res); err != nil {
			return nil, err
		}
		handle = res.Handle
	}
	c.tabs[handle] = true
	return &classicTab{c: c, handle: handle}, nil
}

// deleteSession ends the session, closing its windows.
func (c *classic) deleteSession() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.do(ctx, http.MethodDelete, c.path(""), nil, nil)
}

// errClassic is a feature classic WebDriver doesn't have.
var errClassic = fmt.Errorf("%w (the driver offers classic WebDriver only, without BiDi)", capture.ErrUnsupported)

// classicTab is a window of a classic WebDriver session. Classic
// WebDriver has no events, network interception, preload scripts or
// full-page screenshots: Network and On report nothing, ScopeHeaders
// and SetUserAgent fail. Screenshots stop at the viewport.
type classicTab struct {
	c      *classic
	handle string
}

var _ capture.Tab = (*classicTab)(nil)

func (t *classicTab) Rod() *rod.Page { return nil }

// classicEval runs a function given as a string through
// execute/async, so a returned promise is awaited, with the arguments
// and the result passed as JSON like the BiDi tab's.
const classicEval = `const done = arguments[arguments.length - 1];
try {
  Promise.resolve((%s)(...JSON.parse(arguments[0]))).then(
    r => { const j = r === undefined ? undefined : JSON.stringify(r); done({ok: j === undefined ? 'null' : j}); },
    e => done({err: String(e && e.message || e)}));
} catch (e) { done({err: String(e && e.message || e)}); }`

func (t *classicTab) Eval(ctx context.Context, js string, out any, args ...any) error {
	if !isFunction.MatchString(js) {
		js = "() => (" + js + ")"
	}
	if args == nil {
		args = []any{}
	}
	in, err := json.Marshal(args)
	if err != nil {
		return err
	}
	var res struct {
		OK  *string `json:"ok"`
		Err string  `json:"err"`
	}
	if err := t.c.in(ctx, t.handle, http.MethodPost, "/execute/async",
		map[string]any{"script": fmt.Sprintf(classicEval, js), "args": []string{string(in)}}, &res); err != nil {
		return err
	}
	if res.OK == nil {
		return fmt.Errorf("eval: %s", res.Err)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(*res.OK), out)
}

// Screenshot captures the viewport, with or without full: classic
// WebDriver has no full-page screenshot.
func (t *classicTab) Screenshot(ctx context.Context, _ bool) ([]byte, error) {
	var data string
	if err := t.c.in(ctx, t.handle, http.MethodGet, "/screenshot", nil, &data); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(data)
}

// Clip captures the viewport, cut to width by height when it is larger.
func (t *classicTab) Clip(ctx context.Context, width, height int) ([]byte, error) {
	img, err := t.Screenshot(ctx, false)
	if err != nil {
		return nil, err
	}
	return crop(img, width, height)
}

// crop cuts a PNG to at most width by height pixels from its top-left.
func crop(data []byte, width, height int) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	if b.Dx() <= width && b.Dy() <= height {
		return data, nil
	}
	r := image.Rect(0, 0, min(width, b.Dx()), min(height, b.Dy()))
	dst := image.NewRGBA(r)
	draw.Draw(dst, r, src, b.Min, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// On reports nothing: classic WebDriver sends no events.
func (t *classicTab) On(context.Context, func(check.Event)) func() { return func() {} }

// Network reports nothing: classic WebDriver sends no events.
func (t *classicTab) Network(context.Context, func(capture.NetEvent)) func() { return func() {} }

// Navigate loads rawURL; the driver returns once the page loaded.
func (t *classicTab) Navigate(ctx context.Context, rawURL string) error {
	return t.c.in(ctx, t.handle, http.MethodPost, "/url", map[string]string{"url": rawURL}, nil)
}

func (t *classicTab) URL(ctx context.Context) (string, error) {
	var u string
	err := t.c.in(ctx, t.handle, http.MethodGet, "/url", nil, &u)
	return u, err
}

// SetViewport sizes the window so its viewport is v: it sets the
// window, measures the browser's own frame and sets it again.
func (t *classicTab) SetViewport(ctx context.Context, v capture.Viewport) error {
	set := func(w, h int) error {
		return t.c.in(ctx, t.handle, http.MethodPost, "/window/rect", map[string]int{"width": w, "height": h}, nil)
	}
	if err := set(v.Width, v.Height); err != nil {
		return err
	}
	var frame [2]int
	if err := t.Eval(ctx, `() => [outerWidth - innerWidth, outerHeight - innerHeight]`, &frame); err != nil || frame == [2]int{} {
		return nil //nolint:nilerr // the first size is close enough
	}
	return set(v.Width+frame[0], v.Height+frame[1])
}

// SetUserAgent fails: classic WebDriver can't change it.
func (t *classicTab) SetUserAgent(context.Context, string) error {
	return fmt.Errorf("setting the user agent: %w", errClassic)
}

// DisableCache does nothing: classic WebDriver has no cache setting.
func (t *classicTab) DisableCache(context.Context) error { return nil }

// AddInitScript fails: classic WebDriver has no preload scripts.
func (t *classicTab) AddInitScript(context.Context, string) error {
	return fmt.Errorf("preload scripts: %w", errClassic)
}

// ScopeHeaders fails when there are headers: classic WebDriver can't
// add them to requests. Soundcheck never sends them to every host.
func (t *classicTab) ScopeHeaders(_ context.Context, site *url.URL, headers map[string]string) (func(), error) {
	if site == nil || len(headers) == 0 {
		return func() {}, nil
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	return func() {}, fmt.Errorf("sending %s to the site alone: %w", strings.Join(names, ", "), errClassic)
}

// Close closes the window, or keeps the session's last one for the next
// tab.
func (t *classicTab) Close() error {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	if !t.c.tabs[t.handle] {
		return nil
	}
	if len(t.c.tabs) == 1 && len(t.c.idle) == 0 {
		delete(t.c.tabs, t.handle)
		t.c.idle = append(t.c.idle, t.handle)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if t.c.current != t.handle {
		if err := t.c.do(ctx, http.MethodPost, t.c.path("/window"), map[string]string{"handle": t.handle}, nil); err != nil {
			return err
		}
	}
	delete(t.c.tabs, t.handle)
	t.c.current = ""
	return t.c.do(ctx, http.MethodDelete, t.c.path("/window"), nil, nil)
}
