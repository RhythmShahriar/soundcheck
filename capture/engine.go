// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// StartTab opens recording on a fresh tab of any engine, through the
// Tab interface alone. Call it before navigating; Visit and Mobile then
// work as they do with Start.
//
// What an engine can't do is left out rather than failing the visit,
// except for the run's headers: an engine that can't send them to the
// site alone fails here, as sending them everywhere would leak them.
// Without preload scripts (classic WebDriver) the speed metrics stay 0;
// without events the page has no requests, console errors or status.
func StartTab(ctx context.Context, tab Tab, opts Options) (*Recorder, error) {
	r := &Recorder{tab: tab, reqs: map[string]*req{}, inflight: map[string]bool{}, release: func() {}}
	ectx, cancel := context.WithCancel(ctx)
	stopNet := tab.Network(ectx, r.onNet)
	stopOn := tab.On(ectx, r.onTabEvent)
	r.stop = func() { stopNet(); stopOn(); cancel() }
	fail := func(err error) (*Recorder, error) {
		r.Stop()
		return nil, err
	}
	// Every page loads cold; see Start.
	if err := tab.DisableCache(ctx); err != nil && !errors.Is(err, ErrUnsupported) {
		return fail(err)
	}
	if err := tab.AddInitScript(ctx, metricsJS); err != nil && !errors.Is(err, ErrUnsupported) {
		return fail(err)
	}
	if opts.UserAgent != "" {
		if err := tab.SetUserAgent(ctx, opts.UserAgent); err != nil {
			return fail(err)
		}
	}
	release, err := tab.ScopeHeaders(ctx, opts.Site, opts.Headers)
	if err != nil {
		return fail(err)
	}
	r.release = release
	return r, nil
}

// onNet records one step of a request, the way the Chrome handlers do.
func (r *Recorder) onNet(e NetEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.busy = time.Now()
	switch e.Phase {
	case NetRequest:
		if e.Document && (r.doc == "" || r.doc == e.ID) {
			r.doc = e.ID
		}
		q := r.request(e.ID)
		// A redirect's next hop starts over.
		q.url, q.method, q.typ, q.status, q.done, q.failed = e.URL, e.Method, e.Type, 0, false, ""
		r.inflight[e.ID] = true
	case NetResponse:
		q := r.reqs[e.ID]
		if q == nil {
			return
		}
		q.status, q.protocol, q.mime = e.Status, e.Protocol, e.Mime
		if e.Type != "" {
			q.typ = e.Type
		}
		q.headers = e.Headers
		if q.headers == nil {
			q.headers = map[string]string{}
		}
		q.encoding, q.cache = q.headers["content-encoding"], q.headers["cache-control"]
		if e.ID == r.doc && e.Status >= 300 && e.Status < 400 && q.headers["location"] != "" {
			r.chain = append(r.chain, fmt.Sprintf("%d %s", e.Status, e.URL))
		}
	case NetFinished:
		if q := r.reqs[e.ID]; q != nil {
			q.bytes, q.done = e.Bytes, true
		}
		delete(r.inflight, e.ID)
	case NetFailed:
		if q := r.reqs[e.ID]; q != nil && !e.Canceled {
			q.failed = e.Error
		}
		delete(r.inflight, e.ID)
	}
}

// onTabEvent keeps console errors and uncaught exceptions, worded like
// Chrome's (see onConsole and onException).
func (r *Recorder) onTabEvent(e check.Event) {
	switch e.Kind {
	case check.Console:
		if e.Level == "error" || e.Level == "assert" {
			r.addConsole(e.Text)
		}
	case check.Exception:
		msg := firstLine(e.Text)
		if e.URL != "" {
			msg += fmt.Sprintf(" (%s:%d)", e.URL, max(e.Line, 1))
		}
		r.addConsole("Uncaught " + strings.TrimPrefix(msg, "Uncaught "))
	}
}

// waitIdle waits until no request has been under way for quiet, or ctx
// ends.
func (r *Recorder) waitIdle(ctx context.Context, quiet time.Duration) {
	for {
		r.mu.Lock()
		idle := len(r.inflight) == 0 && time.Since(r.busy) >= quiet
		r.mu.Unlock()
		if idle {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// answered reports whether the page's document got a response, so a
// navigation that timed out waiting for load still has a page to read.
func (r *Recorder) answered() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.reqs[r.doc]
	return q != nil && q.status > 0
}

// load navigates the tab within the page timeout and waits for the
// network to go quiet. As in Chrome, a page that never finishes loading
// (a hung font or tracker) is still read as it is: only a navigation
// that got no answer at all is an error.
func (r *Recorder) load(ctx context.Context, rawURL string, opts Options, viewport string) error {
	nctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	if err := r.tab.SetViewport(nctx, viewportOf(viewport)); err != nil {
		return err
	}
	if err := r.tab.Navigate(nctx, rawURL); err != nil {
		slow := errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil && r.answered()
		if !slow {
			return err
		}
	}
	r.waitIdle(nctx, 700*time.Millisecond)
	settle(ctx, 300*time.Millisecond)
	waitLate(nctx, r.tab, func() { r.waitIdle(nctx, 700*time.Millisecond) })
	return nil
}

// visitTab is Visit for a StartTab recorder.
func visitTab(ctx context.Context, r *Recorder, p *model.Page, rawURL string, opts Options) error {
	if err := r.load(ctx, rawURL, opts, "desktop"); err != nil {
		p.Error = navError(err)
		r.answer(p)
		return nil
	}
	rctx, cancel := context.WithTimeout(ctx, readBudget(opts.Timeout))
	defer cancel()
	return r.read(ctx, rctx, r.tab, p, opts)
}

// mobileTab is Mobile for a StartTab recorder.
func mobileTab(ctx context.Context, r *Recorder, p *model.Page, rawURL string, opts Options) error {
	r.mu.Lock()
	r.console = nil
	r.mu.Unlock()
	if err := r.load(ctx, rawURL, opts, "mobile"); err != nil {
		return nil //nolint:nilerr // the desktop visit already recorded what it could
	}
	rctx, cancel := context.WithTimeout(ctx, readBudget(opts.Timeout))
	defer cancel()
	return r.readMobile(rctx, r.tab, p, opts)
}

// viewportOf is the named viewport as a Viewport.
func viewportOf(name string) Viewport {
	v := Viewports[name]
	return Viewport{Width: v.Width, Height: v.Height, Mobile: v.Mobile}
}
