// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/capture/intercept"
	"github.com/RhythmShahriar/soundcheck/check"
)

// tab is a BiDi browsing context: a top-level window and the frames in
// it.
type tab struct {
	conn        *Conn
	id          string
	userContext string

	mu sync.Mutex
	// frames are the tab's browsing contexts, its own and its frames':
	// the session sends every tab's events to every subscriber. Events
	// from a frame name the frame.
	frames  map[string]bool
	sub     string // the event subscription's ID
	stopOwn func() // stops following frames
	// ua is a user agent sent as a request header, when the browser has
	// no user agent override.
	ua string
	// headers are what ScopeHeaders adds; site is where they go.
	headers   map[string]string
	site      *url.URL
	intercept string // the network intercept's ID, while one is needed
	stopHold  func()
}

var _ capture.Tab = (*tab)(nil)

func newTab(conn *Conn, id, userContext string) *tab {
	return &tab{conn: conn, id: id, userContext: userContext, frames: map[string]bool{id: true}}
}

// tabEvents are the events a tab subscribes to.
var tabEvents = []string{"browsingContext.contextCreated", "log.entryAdded",
	"network.beforeRequestSent", "network.responseCompleted", "network.fetchError"}

// subscribe asks for the tab's events and starts following the frames
// it creates.
func (t *tab) subscribe(ctx context.Context) error {
	var res struct {
		Subscription string `json:"subscription"`
	}
	if err := t.conn.Call(ctx, "session.subscribe", map[string]any{"events": tabEvents, "contexts": []string{t.id}}, &res); err != nil {
		return err
	}
	t.sub = res.Subscription
	t.stopOwn = t.conn.On(func(e Event) {
		if e.Method != "browsingContext.contextCreated" {
			return
		}
		var c struct {
			Context string `json:"context"`
			Parent  string `json:"parent"`
		}
		if json.Unmarshal(e.Params, &c) == nil && c.Parent != "" {
			t.mu.Lock()
			if t.frames[c.Parent] {
				t.frames[c.Context] = true
			}
			t.mu.Unlock()
		}
	})
	return nil
}

// mine reports whether an event from browsing context id is this tab's.
func (t *tab) mine(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.frames[id]
}

// Rod is nil: the tab isn't Chrome.
func (t *tab) Rod() *rod.Page { return nil }

// isFunction matches a function expression, which Eval calls; anything
// else is an expression whose value Eval returns.
var isFunction = regexp.MustCompile(`^\s*(async\s+)?(function\b|\(|[A-Za-z_$][\w$]*\s*=>)`)

// Eval calls js in the page through a wrapper that passes args and the
// result as JSON: BiDi's own serialization of values is typed and
// nested. JSON is what callers decode anyway.
func (t *tab) Eval(ctx context.Context, js string, out any, args ...any) error {
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
	wrapper := "async (__args) => { const __r = await (" + js + ")(...JSON.parse(__args));" +
		" const __j = __r === undefined ? undefined : JSON.stringify(__r); return __j === undefined ? 'null' : __j; }"
	var res struct {
		Type   string `json:"type"`
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	err = t.conn.Call(ctx, "script.callFunction", map[string]any{
		"functionDeclaration": wrapper, "awaitPromise": true, "resultOwnership": "none",
		"target": map[string]string{"context": t.id}, "arguments": []any{map[string]any{"type": "string", "value": string(in)}},
	}, &res)
	if err != nil {
		return err
	}
	if res.Type == "exception" {
		return fmt.Errorf("eval: %s", res.ExceptionDetails.Text)
	}
	s, _ := res.Result.Value.(string)
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(s), out)
}

// Screenshot captures the viewport or, with full, the whole document.
func (t *tab) Screenshot(ctx context.Context, full bool) ([]byte, error) {
	origin := "viewport"
	if full {
		origin = "document"
	}
	return t.capture(ctx, map[string]any{"context": t.id, "origin": origin})
}

// Clip captures the document's top-left width by height pixels.
func (t *tab) Clip(ctx context.Context, width, height int) ([]byte, error) {
	return t.capture(ctx, map[string]any{"context": t.id, "origin": "document",
		"clip": map[string]any{"type": "box", "x": 0, "y": 0, "width": width, "height": height}})
}

func (t *tab) capture(ctx context.Context, params map[string]any) ([]byte, error) {
	params["format"] = map[string]any{"type": "image/png"}
	var res struct {
		Data string `json:"data"`
	}
	if err := t.conn.Call(ctx, "browsingContext.captureScreenshot", params, &res); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(res.Data)
}

// Navigate loads rawURL and waits for its load event.
func (t *tab) Navigate(ctx context.Context, rawURL string) error {
	return t.conn.Call(ctx, "browsingContext.navigate", map[string]any{"context": t.id, "url": rawURL, "wait": "complete"}, nil)
}

// URL is the tab's address.
func (t *tab) URL(ctx context.Context) (string, error) {
	var res struct {
		Contexts []struct {
			URL string `json:"url"`
		} `json:"contexts"`
	}
	if err := t.conn.Call(ctx, "browsingContext.getTree", map[string]any{"root": t.id, "maxDepth": 0}, &res); err != nil {
		return "", err
	}
	if len(res.Contexts) == 0 {
		return "", errors.New("the tab is gone")
	}
	return res.Contexts[0].URL, nil
}

// SetViewport resizes the page. BiDi has no phone emulation, so Mobile
// only sets the size.
func (t *tab) SetViewport(ctx context.Context, v capture.Viewport) error {
	return t.conn.Call(ctx, "browsingContext.setViewport", map[string]any{"context": t.id,
		"viewport": map[string]int{"width": v.Width, "height": v.Height}, "devicePixelRatio": 1}, nil)
}

// SetUserAgent overrides the tab's user agent. A browser without the
// override gets it as a request header instead, which the page's
// navigator.userAgent doesn't show.
func (t *tab) SetUserAgent(ctx context.Context, ua string) error {
	err := t.conn.Call(ctx, "emulation.setUserAgentOverride", map[string]any{"userAgent": ua, "contexts": []string{t.id}}, nil)
	if !Unsupported(err) {
		return err
	}
	t.mu.Lock()
	t.ua = ua
	t.mu.Unlock()
	return t.hold(ctx)
}

// DisableCache bypasses the cache, where the browser can.
func (t *tab) DisableCache(ctx context.Context) error {
	err := t.conn.Call(ctx, "network.setCacheBehavior", map[string]any{"cacheBehavior": "bypass", "contexts": []string{t.id}}, nil)
	if Unsupported(err) {
		return nil
	}
	return err
}

// AddInitScript runs js before the page's scripts in every document.
func (t *tab) AddInitScript(ctx context.Context, js string) error {
	return t.conn.Call(ctx, "script.addPreloadScript", map[string]any{"functionDeclaration": "() => {\n" + js + "\n}",
		"contexts": []string{t.id}}, nil)
}

// ScopeHeaders sets a Cookie header as cookies for site and adds the
// other headers to the requests to site, which a network intercept
// holds until they are added. See capture.siteHeaders for why the
// headers aren't simply sent with every request.
func (t *tab) ScopeHeaders(ctx context.Context, site *url.URL, headers map[string]string) (func(), error) {
	release := func() {}
	if site == nil || len(headers) == 0 {
		return release, nil
	}
	extra := map[string]string{}
	for k, v := range headers {
		if intercept.Unsafe(k) {
			// As in Chrome: one refused header would cost the request the
			// others (see capture.HeaderNotes).
			continue
		}
		if !strings.EqualFold(k, "Cookie") {
			extra[k] = v
			continue
		}
		for _, c := range capture.Cookies(v) {
			if err := t.setCookie(ctx, site, c[0], c[1]); err != nil {
				return release, fmt.Errorf("can't set the cookie %s: %w", c[0], err)
			}
		}
	}
	if len(extra) == 0 {
		return release, nil
	}
	t.mu.Lock()
	t.headers, t.site = extra, site
	t.mu.Unlock()
	if err := t.hold(ctx); err != nil {
		return release, err
	}
	return func() {
		t.mu.Lock()
		t.headers, t.site = nil, nil
		t.mu.Unlock()
	}, nil
}

func (t *tab) setCookie(ctx context.Context, site *url.URL, name, value string) error {
	// The user context's partition: a context partition would key the
	// cookie to the blank page's origin, so the site never got it. A
	// domain without a leading dot makes a host-only cookie, as Chrome's
	// URL cookie is: subdomains don't get it (TestFirefoxCookieIsHostOnly).
	partition := map[string]any{"type": "storageKey"}
	if t.userContext != "" {
		partition["userContext"] = t.userContext
	}
	return t.conn.Call(ctx, "storage.setCookie", map[string]any{
		"cookie": map[string]any{"name": name, "value": map[string]string{"type": "string", "value": value},
			"domain": site.Hostname(), "path": "/", "httpOnly": true, "secure": site.Scheme == "https"},
		"partition": partition,
	}, nil)
}

// hold starts holding every request of the tab before it is sent, once,
// so headers can be added to it.
func (t *tab) hold(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.intercept != "" {
		return nil
	}
	var res struct {
		Intercept string `json:"intercept"`
	}
	if err := t.conn.Call(ctx, "network.addIntercept", map[string]any{"phases": []string{"beforeRequestSent"},
		"contexts": []string{t.id}}, &res); err != nil {
		return err
	}
	t.intercept = res.Intercept
	t.stopHold = t.conn.On(t.continueRequest)
	return nil
}

// bidiHeader is a header as BiDi sends it.
type bidiHeader struct {
	Name  string `json:"name"`
	Value struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"value"`
}

func header(name, value string) bidiHeader {
	h := bidiHeader{Name: name}
	h.Value.Type, h.Value.Value = "string", value
	return h
}

// beforeRequest is network.beforeRequestSent's params, as far as they
// are read.
type beforeRequest struct {
	Context    string      `json:"context"`
	Navigation *string     `json:"navigation"`
	IsBlocked  bool        `json:"isBlocked"`
	Intercepts []string    `json:"intercepts"`
	Request    requestData `json:"request"`
}

// requestData is the request in a network event.
type requestData struct {
	Request       string       `json:"request"` // its ID
	URL           string       `json:"url"`
	Method        string       `json:"method"`
	Headers       []bidiHeader `json:"headers"`
	Destination   string       `json:"destination"`
	InitiatorType string       `json:"initiatorType"`
}

// continueRequest lets a held request go on, with the user agent and,
// when it goes to the site, the scoped headers. Every held request must
// be continued, or the page waits for it forever.
func (t *tab) continueRequest(e Event) {
	if e.Method != "network.beforeRequestSent" {
		return
	}
	var r beforeRequest
	if json.Unmarshal(e.Params, &r) != nil || !r.IsBlocked {
		return
	}
	t.mu.Lock()
	ours := t.intercept != "" && slices.Contains(r.Intercepts, t.intercept)
	ua, extra, site := t.ua, t.headers, t.site
	t.mu.Unlock()
	if !ours {
		return
	}
	params := map[string]any{"request": r.Request.Request}
	set := map[string]string{}
	if ua != "" {
		set["User-Agent"] = ua
	}
	if u, err := url.Parse(r.Request.URL); err == nil && site != nil && check.Trusted(site, u) {
		for k, v := range extra {
			set[k] = v
		}
	}
	if len(set) > 0 {
		hs := slices.DeleteFunc(slices.Clone(r.Request.Headers), func(h bidiHeader) bool {
			for k := range set {
				if strings.EqualFold(h.Name, k) {
					return true
				}
			}
			return false
		})
		for k, v := range set {
			hs = append(hs, header(k, v))
		}
		params["headers"] = hs
	}
	ctx := context.Background()
	if err := t.conn.Call(ctx, "network.continueRequest", params, nil); err != nil && len(set) > 0 {
		// A header the browser refuses mustn't leave the request held.
		_ = t.conn.Call(ctx, "network.continueRequest", map[string]any{"request": r.Request.Request}, nil)
	}
}

// responseCompleted is network.responseCompleted's params (and
// network.fetchError's).
type responseCompleted struct {
	Context  string      `json:"context"`
	Request  requestData `json:"request"`
	Response struct {
		URL           string       `json:"url"`
		Protocol      string       `json:"protocol"`
		Status        int          `json:"status"`
		Headers       []bidiHeader `json:"headers"`
		MimeType      string       `json:"mimeType"`
		BytesReceived int64        `json:"bytesReceived"`
	} `json:"response"`
	Navigation *string `json:"navigation"`
	ErrorText  string  `json:"errorText"` // network.fetchError
}

// Network reports the tab's requests.
func (t *tab) Network(ctx context.Context, fn func(capture.NetEvent)) func() {
	return t.follow(ctx, func(e Event) {
		switch e.Method {
		case "network.beforeRequestSent":
			var r beforeRequest
			if json.Unmarshal(e.Params, &r) != nil || !t.mine(r.Context) {
				return
			}
			q := r.Request
			fn(capture.NetEvent{Phase: capture.NetRequest, ID: q.Request, Document: t.document(r.Context, r.Navigation),
				URL: q.URL, Method: q.Method, Type: capture.ResourceType(q.Destination, q.InitiatorType)})
		case "network.responseCompleted":
			var r responseCompleted
			if json.Unmarshal(e.Params, &r) != nil || !t.mine(r.Context) {
				return
			}
			res := r.Response
			hs := map[string]string{}
			for _, h := range res.Headers {
				name := strings.ToLower(h.Name)
				if old, ok := hs[name]; ok {
					hs[name] = old + ", " + h.Value.Value
				} else {
					hs[name] = h.Value.Value
				}
			}
			mt, _, _ := strings.Cut(res.MimeType, ";")
			mt = strings.ToLower(strings.TrimSpace(mt))
			base := capture.NetEvent{ID: r.Request.Request, Document: t.document(r.Context, r.Navigation), URL: res.URL,
				Type: capture.ResourceType(r.Request.Destination, r.Request.InitiatorType)}
			resp := base
			resp.Phase, resp.Status, resp.Protocol, resp.Mime, resp.Headers = capture.NetResponse, res.Status, res.Protocol, mt, hs
			fn(resp)
			if res.Status < 300 || res.Status >= 400 || hs["location"] == "" {
				fin := base
				fin.Phase, fin.Bytes = capture.NetFinished, res.BytesReceived
				fn(fin)
			}
		case "network.fetchError":
			var r responseCompleted
			if json.Unmarshal(e.Params, &r) != nil || !t.mine(r.Context) {
				return
			}
			fn(capture.NetEvent{Phase: capture.NetFailed, ID: r.Request.Request, Document: t.document(r.Context, r.Navigation),
				URL: r.Request.URL, Type: capture.ResourceType(r.Request.Destination, r.Request.InitiatorType),
				Error: r.ErrorText, Canceled: canceled(r.ErrorText)})
		}
	})
}

// document reports whether a request is a navigation of the tab's top
// frame.
func (t *tab) document(context string, navigation *string) bool {
	return context == t.id && navigation != nil
}

// canceled reports whether a fetch error is a canceled request: Firefox
// aborts the requests of a page it leaves.
func canceled(errorText string) bool {
	return strings.Contains(errorText, "NS_BINDING_ABORTED") || strings.Contains(errorText, "NS_ERROR_ABORT")
}

// logEntry is log.entryAdded's params.
type logEntry struct {
	Type   string `json:"type"` // console or javascript
	Level  string `json:"level"`
	Method string `json:"method"` // console: log, warn, error, assert…
	Text   string `json:"text"`
	Source struct {
		Context string `json:"context"`
	} `json:"source"`
	StackTrace *struct {
		CallFrames []struct {
			URL        string `json:"url"`
			LineNumber int    `json:"lineNumber"`
		} `json:"callFrames"`
	} `json:"stackTrace"`
}

// consoleLevels maps console methods to the levels Chrome reports.
var consoleLevels = map[string]string{"warn": "warning", "trace": "trace", "dir": "dir", "table": "table"}

// On reports console calls, uncaught exceptions and responses.
func (t *tab) On(ctx context.Context, fn func(check.Event)) func() {
	return t.follow(ctx, func(e Event) {
		switch e.Method {
		case "log.entryAdded":
			var l logEntry
			if json.Unmarshal(e.Params, &l) != nil || !t.mine(l.Source.Context) {
				return
			}
			switch l.Type {
			case "console":
				level := l.Method
				if v, ok := consoleLevels[level]; ok {
					level = v
				}
				if level == "" {
					level = l.Level
				}
				fn(check.Event{Kind: check.Console, Level: level, Text: l.Text})
			case "javascript":
				ev := check.Event{Kind: check.Exception, Text: l.Text}
				if l.StackTrace != nil && len(l.StackTrace.CallFrames) > 0 {
					f := l.StackTrace.CallFrames[0]
					ev.URL, ev.Line = f.URL, f.LineNumber+1
				}
				fn(ev)
			}
		case "network.responseCompleted":
			var r responseCompleted
			if json.Unmarshal(e.Params, &r) != nil || !t.mine(r.Context) {
				return
			}
			fn(check.Event{Kind: check.Response, URL: r.Response.URL, Status: r.Response.Status,
				Type: capture.ResourceType(r.Request.Destination, r.Request.InitiatorType)})
		}
	})
}

// follow calls fn with the connection's events until ctx ends or stop
// is called.
func (t *tab) follow(ctx context.Context, fn func(Event)) func() {
	ctx, cancel := context.WithCancel(ctx)
	stopOn := t.conn.On(func(e Event) {
		if ctx.Err() == nil {
			fn(e)
		}
	})
	go func() { <-ctx.Done(); stopOn() }()
	return cancel
}

// Close closes the tab and drops its subscription and intercept.
func (t *tab) Close() error {
	ctx := context.Background()
	t.mu.Lock()
	stopHold, stopOwn, intercept, sub := t.stopHold, t.stopOwn, t.intercept, t.sub
	t.intercept = ""
	t.mu.Unlock()
	if intercept != "" {
		_ = t.conn.Call(ctx, "network.removeIntercept", map[string]any{"intercept": intercept}, nil)
	}
	if stopHold != nil {
		stopHold()
	}
	if stopOwn != nil {
		stopOwn()
	}
	if sub != "" {
		_ = t.conn.Call(ctx, "session.unsubscribe", map[string]any{"subscriptions": []string{sub}}, nil)
	}
	return t.conn.Call(ctx, "browsingContext.close", map[string]any{"context": t.id}, nil)
}
