// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture

import (
	"context"
	"net/url"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
)

// ChromeDriver is the Chrome engine as a Driver, opening tabs in sess
// (see browser.NewTab). Close calls closeFn, which may be nil.
//
// A run in Chrome records pages with Start and Visit, which read more
// of Chrome than a Tab offers (the browser's own log, frame IDs); the
// driver gives Chrome the same Tab the other engines have, so a module
// or a test can treat every engine alike.
func ChromeDriver(sess *rod.Browser, closeFn func()) Driver {
	return &chromeDriver{sess: sess, closeFn: closeFn}
}

type chromeDriver struct {
	sess    *rod.Browser
	closeFn func()
}

func (d *chromeDriver) Engine() Engine { return Chrome }

func (d *chromeDriver) NewTab(context.Context) (Tab, error) {
	p, err := browser.NewTab(d.sess)
	if err != nil {
		return nil, err
	}
	return ChromeTab(p), nil
}

func (d *chromeDriver) Close() error {
	if d.closeFn != nil {
		d.closeFn()
	}
	return nil
}

// ChromeTab is p as a Tab.
func ChromeTab(p *rod.Page) Tab { return chromeTab{Tab: check.RodTab(p), p: p} }

type chromeTab struct {
	check.Tab
	p *rod.Page
}

func (t chromeTab) Navigate(ctx context.Context, rawURL string) error {
	page := t.p.Context(ctx)
	if err := page.Navigate(rawURL); err != nil {
		return err
	}
	return page.WaitLoad()
}

func (t chromeTab) URL(ctx context.Context) (string, error) {
	info, err := t.p.Context(ctx).Info()
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

func (t chromeTab) SetViewport(ctx context.Context, v Viewport) error {
	return t.p.Context(ctx).SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: v.Width, Height: v.Height, DeviceScaleFactor: 1, Mobile: v.Mobile})
}

func (t chromeTab) SetUserAgent(ctx context.Context, ua string) error {
	return proto.NetworkSetUserAgentOverride{UserAgent: ua}.Call(t.p.Context(ctx))
}

func (t chromeTab) DisableCache(ctx context.Context) error {
	page := t.p.Context(ctx)
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return err
	}
	return proto.NetworkSetCacheDisabled{CacheDisabled: true}.Call(page)
}

func (t chromeTab) AddInitScript(ctx context.Context, js string) error {
	_, err := t.p.Context(ctx).EvalOnNewDocument(js)
	return err
}

func (t chromeTab) ScopeHeaders(ctx context.Context, site *url.URL, headers map[string]string) (func(), error) {
	return siteHeaders(t.p.Context(ctx), site, headers)
}

// Network turns Chrome's Network events into NetEvents. Chrome reports a
// redirect on the next hop's request, so that becomes a NetResponse for
// the redirect and then the NetRequest.
func (t chromeTab) Network(ctx context.Context, fn func(NetEvent)) func() {
	ctx, stop := context.WithCancel(ctx)
	page := t.p.Context(ctx)
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		stop()
		return stop
	}
	main := t.p.FrameID
	types := map[proto.NetworkRequestID]string{}
	wait := page.EachEvent(
		func(e *proto.NetworkRequestWillBeSent) {
			id, doc := string(e.RequestID), e.Type == proto.NetworkResourceTypeDocument && e.FrameID == main
			if r := e.RedirectResponse; r != nil {
				fn(NetEvent{Phase: NetResponse, ID: id, Document: doc, URL: r.URL, Type: string(e.Type), Status: r.Status,
					Protocol: r.Protocol, Mime: r.MIMEType, Headers: lowerHeaders(r.Headers)})
			}
			types[e.RequestID] = string(e.Type)
			fn(NetEvent{Phase: NetRequest, ID: id, Document: doc, URL: e.Request.URL, Method: e.Request.Method, Type: string(e.Type)})
		},
		func(e *proto.NetworkResponseReceived) {
			r := e.Response
			fn(NetEvent{Phase: NetResponse, ID: string(e.RequestID), Document: e.Type == proto.NetworkResourceTypeDocument && e.FrameID == main,
				URL: r.URL, Type: string(e.Type), Status: r.Status, Protocol: r.Protocol, Mime: r.MIMEType, Headers: lowerHeaders(r.Headers)})
		},
		func(e *proto.NetworkLoadingFinished) {
			fn(NetEvent{Phase: NetFinished, ID: string(e.RequestID), Type: types[e.RequestID], Bytes: int64(e.EncodedDataLength)})
		},
		func(e *proto.NetworkLoadingFailed) {
			fn(NetEvent{Phase: NetFailed, ID: string(e.RequestID), Type: string(e.Type), Error: e.ErrorText, Canceled: e.Canceled})
		},
	)
	go wait()
	return stop
}

func lowerHeaders(h proto.NetworkHeaders) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[strings.ToLower(k)] = v.Str()
	}
	return out
}

func (t chromeTab) Clip(ctx context.Context, width, height int) ([]byte, error) {
	// Clip the page instead of resizing the viewport to it, which would
	// stretch 100vh layouts and push footers out.
	return t.p.Context(ctx).Screenshot(false, &proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng,
		CaptureBeyondViewport: true, Clip: &proto.PageViewport{Width: float64(width), Height: float64(height), Scale: 1}})
}

func (t chromeTab) Close() error { return t.p.Close() }
