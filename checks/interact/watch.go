// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/capture/intercept"
	"github.com/RhythmShahriar/soundcheck/model"
)

// watcher records what happens in a tab between reset and report: the
// requests started, their outcome, uncaught JS errors and navigations,
// so each click or submit can be judged by its own effects.
type watcher struct {
	tab   *rod.Page
	mu    sync.Mutex
	reqs  map[proto.NetworkRequestID]*wreq
	order []proto.NetworkRequestID
	errs  []string
	nav   *wreq // the main-frame document load the action caused, if any
	stop  func()
}

type wreq struct {
	url, method string
	typ         proto.NetworkResourceType
	status      int
	failed      string
	done        bool
}

// Analytics and ad hosts fire on almost any click; their beacons don't
// show that the click did something.
var trackers = []string{
	"google-analytics.com", "googletagmanager.com", "doubleclick.net", "googleadservices.com", "facebook.com", "facebook.net",
	"hotjar.com", "clarity.ms", "segment.io", "segment.com", "mixpanel.com", "plausible.io", "matomo", "stats.wp.com",
	"pixel.wp.com", "bing.com", "linkedin.com", "tiktok.com", "twitter.com", "sentry.io", "newrelic.com", "nr-data.net",
}

func isTracker(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	for _, t := range trackers {
		if strings.Contains(h, t) {
			return true
		}
	}
	return false
}

func watch(ctx context.Context, tab *rod.Page) *watcher {
	w := &watcher{tab: tab, reqs: map[proto.NetworkRequestID]*wreq{}}
	_ = proto.NetworkEnable{}.Call(tab)
	_ = proto.RuntimeEnable{}.Call(tab)
	_ = proto.PageEnable{}.Call(tab)
	wctx, cancel := context.WithCancel(ctx)
	w.stop = cancel
	wait := tab.Context(wctx).EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			if strings.HasPrefix(e.Request.URL, "data:") || strings.HasPrefix(e.Request.URL, "blob:") {
				return
			}
			w.mu.Lock()
			defer w.mu.Unlock()
			q, ok := w.reqs[e.RequestID]
			if !ok {
				q = &wreq{}
				w.reqs[e.RequestID] = q
				w.order = append(w.order, e.RequestID)
			}
			q.url, q.method, q.typ = e.Request.URL, e.Request.Method, e.Type
			if e.Type == proto.NetworkResourceTypeDocument && e.FrameID == tab.FrameID {
				w.nav = q
			}
		},
		func(e *proto.NetworkResponseReceived) {
			w.mu.Lock()
			defer w.mu.Unlock()
			if q := w.reqs[e.RequestID]; q != nil {
				q.status = e.Response.Status
			}
		},
		func(e *proto.NetworkLoadingFinished) {
			w.mu.Lock()
			defer w.mu.Unlock()
			if q := w.reqs[e.RequestID]; q != nil {
				q.done = true
			}
		},
		func(e *proto.NetworkLoadingFailed) {
			w.mu.Lock()
			defer w.mu.Unlock()
			if q := w.reqs[e.RequestID]; q != nil {
				q.done = true
				if !e.Canceled {
					q.failed = e.ErrorText
				}
			}
		},
		func(e *proto.RuntimeExceptionThrown) {
			d := e.ExceptionDetails
			msg := d.Text
			if d.Exception != nil && d.Exception.Description != "" {
				msg, _, _ = strings.Cut(d.Exception.Description, "\n")
			}
			w.mu.Lock()
			w.errs = append(w.errs, strings.TrimSpace("Uncaught "+strings.TrimPrefix(msg, "Uncaught ")))
			w.mu.Unlock()
		},
		func(e *proto.PageJavascriptDialogOpening) {
			// guardJS replaces alert/confirm/prompt. A beforeunload
			// dialog can still open: always dismiss, never confirm.
			go func() { _ = proto.PageHandleJavaScriptDialog{Accept: false}.Call(tab) }()
		},
	)
	go wait()
	return w
}

// reset starts a new observation window.
func (w *watcher) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reqs = map[proto.NetworkRequestID]*wreq{}
	w.order, w.errs, w.nav = nil, nil, nil
}

// effects is what a watcher saw in one window.
type effects struct {
	requests int // that signal activity (not images, fonts or trackers)
	pending  int
	failed   []model.Request
	errors   []string
	nav      *wreq
	held     []model.Request // what the click guard kept from being sent (see holdWrites)
}

func (w *watcher) report() effects {
	w.mu.Lock()
	defer w.mu.Unlock()
	var e effects
	e.errors = append([]string(nil), w.errs...)
	if w.nav != nil {
		n := *w.nav
		e.nav = &n
	}
	for _, id := range w.order {
		q := w.reqs[id]
		switch q.typ {
		case proto.NetworkResourceTypeImage, proto.NetworkResourceTypeFont, proto.NetworkResourceTypeMedia, proto.NetworkResourceTypeStylesheet:
			continue
		}
		if isTracker(q.url) || strings.HasSuffix(q.url, "/favicon.ico") {
			continue
		}
		e.requests++ // a held request still shows the click did something
		if !q.done {
			e.pending++
		}
		if intercept.IsBlocked(q.failed) {
			e.held = append(e.held, model.Request{URL: q.url, Method: q.method, Type: string(q.typ)})
			continue
		}
		if q.failed != "" || q.status >= 400 {
			e.failed = append(e.failed, model.Request{URL: q.url, Method: q.method, Type: string(q.typ), Status: q.status, Error: q.failed})
		}
	}
	return e
}

func describe(r model.Request, base *url.URL) string {
	what := fmt.Sprintf("returned %d", r.Status)
	if r.Error != "" {
		what = "failed (" + strings.TrimPrefix(r.Error, "net::") + ")"
	}
	m := r.Method
	if m == "" {
		m = "GET"
	}
	return m + " " + shortURL(base, r.URL) + " " + what
}

// shortURL shows same-site URLs as paths.
func shortURL(base *url.URL, raw string) string {
	u, err := url.Parse(raw)
	if err != nil || base == nil || !strings.EqualFold(u.Host, base.Host) {
		return raw
	}
	return u.RequestURI()
}
