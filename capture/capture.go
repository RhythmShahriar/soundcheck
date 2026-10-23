// Package capture visits one page in a browser tab and records what a
// careful tester would notice: the response and redirects, console
// errors, failed requests, every resource, lab speed metrics, the links
// on the rendered page and screenshots.
package capture

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // content fingerprint, not security
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Viewports Soundcheck screenshots at.
var Viewports = map[string]proto.EmulationSetDeviceMetricsOverride{
	"desktop": {Width: 1366, Height: 900, DeviceScaleFactor: 1},
	"mobile":  {Width: 390, Height: 844, DeviceScaleFactor: 1, Mobile: true},
}

// Pages taller than this are screenshotted down to this height.
const maxShotHeight = 12000

// Options for one visit.
type Options struct {
	Timeout   time.Duration
	Ignore    []string // CSS selectors hidden before screenshots
	Twice     bool     // take a second shot of each viewport to learn dynamic regions
	UserAgent string
	Headers   map[string]string
	Save      func(name string, data []byte) error // stores a screenshot; nil skips screenshots
}

// Recorder collects browser events for one page.
type Recorder struct {
	page    *rod.Page
	frame   proto.PageFrameID
	mu      sync.Mutex
	reqs    map[proto.NetworkRequestID]*req
	order   []proto.NetworkRequestID
	doc     proto.NetworkRequestID
	chain   []string
	console []string
	stop    func()
}

type req struct {
	url, method, typ, protocol, mime, encoding, cache string
	status                                            int
	headers                                           map[string]string
	bytes                                             int64
	failed                                            string
	done                                              bool
}

// metricsJS runs before any page script and keeps Web Vitals in
// window.__soundcheck.
const metricsJS = `(() => {
  const s = window.__soundcheck = {lcp: 0, cls: 0, tbt: 0, fcp: 0};
  const watch = (type, fn) => { try { new PerformanceObserver(l => l.getEntries().forEach(fn)).observe({type, buffered: true}); } catch (e) {} };
  watch('largest-contentful-paint', e => { s.lcp = e.renderTime || e.loadTime || e.startTime; });
  watch('layout-shift', e => { if (!e.hadRecentInput) s.cls += e.value; });
  watch('longtask', e => { s.tbt += Math.max(0, e.duration - 50); });
  watch('paint', e => { if (e.name === 'first-contentful-paint') s.fcp = e.startTime; });
})();`

// stillCSS pauses animations and hides carets so screenshots are stable.
const stillCSS = `*,*::before,*::after{animation-play-state:paused!important;animation-delay:-0.0001s!important;transition:none!important;caret-color:transparent!important}`

// Start opens recording on a fresh tab. Call it before navigating.
func Start(ctx context.Context, page *rod.Page, opts Options) (*Recorder, error) {
	r := &Recorder{page: page, reqs: map[proto.NetworkRequestID]*req{}}
	for _, call := range []func() error{
		func() error { return proto.NetworkEnable{}.Call(page) },
		// Every page loads cold, like a first visit: with the tabs sharing
		// one HTTP cache, pages after the first would weigh almost nothing
		// and their timings would depend on visit order.
		func() error { return proto.NetworkSetCacheDisabled{CacheDisabled: true}.Call(page) },
		func() error { return proto.RuntimeEnable{}.Call(page) },
		func() error { return proto.LogEnable{}.Call(page) },
		func() error { _, err := page.EvalOnNewDocument(metricsJS); return err },
	} {
		if err := call(); err != nil {
			return nil, err
		}
	}
	if opts.UserAgent != "" {
		if err := (proto.NetworkSetUserAgentOverride{UserAgent: opts.UserAgent}).Call(page); err != nil {
			return nil, err
		}
	}
	if len(opts.Headers) > 0 {
		h := proto.NetworkHeaders{}
		for k, v := range opts.Headers {
			h[k] = gsonString(v)
		}
		if err := (proto.NetworkSetExtraHTTPHeaders{Headers: h}).Call(page); err != nil {
			return nil, err
		}
	}
	ectx, cancel := context.WithCancel(ctx)
	r.stop = cancel
	wait := page.Context(ectx).EachEvent(
		func(e *proto.NetworkRequestWillBeSent) { r.onRequest(e) },
		func(e *proto.NetworkResponseReceived) { r.onResponse(e) },
		func(e *proto.NetworkLoadingFinished) { r.onFinished(e) },
		func(e *proto.NetworkLoadingFailed) { r.onFailed(e) },
		func(e *proto.RuntimeConsoleAPICalled) { r.onConsole(e) },
		func(e *proto.RuntimeExceptionThrown) { r.onException(e) },
		func(e *proto.LogEntryAdded) { r.onLog(e) },
	)
	go wait()
	return r, nil
}

// Stop ends event recording.
func (r *Recorder) Stop() { r.stop() }

func (r *Recorder) onRequest(e *proto.NetworkRequestWillBeSent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.Type == proto.NetworkResourceTypeDocument && (r.frame == "" || e.FrameID == r.frame) && (r.doc == "" || r.doc == e.RequestID) {
		r.frame, r.doc = e.FrameID, e.RequestID
		if e.RedirectResponse != nil {
			r.chain = append(r.chain, fmt.Sprintf("%d %s", e.RedirectResponse.Status, e.RedirectResponse.URL))
		}
	}
	q, ok := r.reqs[e.RequestID]
	if !ok {
		q = &req{}
		r.reqs[e.RequestID] = q
		r.order = append(r.order, e.RequestID)
	}
	q.url, q.method, q.typ = e.Request.URL, e.Request.Method, string(e.Type)
}

func (r *Recorder) onResponse(e *proto.NetworkResponseReceived) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.reqs[e.RequestID]
	if q == nil {
		return
	}
	res := e.Response
	q.status, q.protocol, q.mime, q.typ = res.Status, res.Protocol, res.MIMEType, string(e.Type)
	q.headers = map[string]string{}
	for k, v := range res.Headers {
		q.headers[strings.ToLower(k)] = v.Str()
	}
	q.encoding, q.cache = q.headers["content-encoding"], q.headers["cache-control"]
}

func (r *Recorder) onFinished(e *proto.NetworkLoadingFinished) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q := r.reqs[e.RequestID]; q != nil {
		q.bytes, q.done = int64(e.EncodedDataLength), true
	}
}

func (r *Recorder) onFailed(e *proto.NetworkLoadingFailed) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q := r.reqs[e.RequestID]; q != nil && !e.Canceled {
		q.failed = e.ErrorText
	}
}

func (r *Recorder) onConsole(e *proto.RuntimeConsoleAPICalled) {
	if e.Type != proto.RuntimeConsoleAPICalledTypeError && e.Type != proto.RuntimeConsoleAPICalledTypeAssert {
		return
	}
	var parts []string
	for _, a := range e.Args {
		switch {
		case a.Description != "":
			parts = append(parts, a.Description)
		case a.Value.Nil():
		default:
			if s := a.Value.Str(); s != "" {
				parts = append(parts, s)
			} else {
				parts = append(parts, a.Value.JSON("", ""))
			}
		}
	}
	r.addConsole(strings.Join(parts, " "))
}

func (r *Recorder) onException(e *proto.RuntimeExceptionThrown) {
	d := e.ExceptionDetails
	msg := d.Text
	if d.Exception != nil && d.Exception.Description != "" {
		msg = firstLine(d.Exception.Description)
	}
	if d.URL != "" {
		msg += fmt.Sprintf(" (%s:%d)", d.URL, d.LineNumber+1)
	}
	r.addConsole("Uncaught " + strings.TrimPrefix(msg, "Uncaught "))
}

// onLog keeps browser-reported errors that aren't network failures (those
// are recorded per request): CSP violations, mixed content and the like.
func (r *Recorder) onLog(e *proto.LogEntryAdded) {
	if e.Entry.Level == proto.LogLogEntryLevelError && e.Entry.Source != proto.LogLogEntrySourceNetwork {
		r.addConsole(e.Entry.Text)
	}
}

func (r *Recorder) addConsole(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.console, s) {
		r.console = append(r.console, s)
	}
}

// pageFacts is what metricsJS and the DOM report after load.
type pageFacts struct {
	Title  string   `json:"title"`
	Robots string   `json:"robots"`
	Text   string   `json:"text"`
	Links  []string `json:"links"`
	TTFB   float64  `json:"ttfb"`
	Load   float64  `json:"load"`
	FCP    float64  `json:"fcp"`
	LCP    float64  `json:"lcp"`
	CLS    float64  `json:"cls"`
	TBT    float64  `json:"tbt"`
	Height int      `json:"height"`
}

const factsJS = `() => {
  const nav = performance.getEntriesByType('navigation')[0] || {};
  const s = window.__soundcheck || {};
  const robots = [...document.querySelectorAll('meta[name="robots" i],meta[name="googlebot" i]')].map(m => m.content).join(', ');
  const links = [...new Set([...document.querySelectorAll('a[href]')].map(a => a.href))].slice(0, 2000);
  return {
    title: document.title || '', robots, links,
    text: (document.body && document.body.innerText || '').slice(0, 200000),
    ttfb: nav.responseStart || 0, load: nav.loadEventEnd || nav.duration || 0,
    fcp: s.fcp || 0, lcp: s.lcp || 0, cls: s.cls || 0, tbt: s.tbt || 0,
    height: Math.max(document.documentElement.scrollHeight, document.body ? document.body.scrollHeight : 0),
  };
}`

// Visit navigates to rawURL at the desktop viewport, waits for the page to
// settle, and records it. Screenshots are taken when opts.Save is set.
func Visit(ctx context.Context, r *Recorder, rawURL string, opts Options) (*model.Page, error) {
	p := &model.Page{ID: model.PageID(rawURL), URL: rawURL, Shots: map[string]model.Shot{}}
	if u, err := url.Parse(rawURL); err == nil {
		p.Path = u.RequestURI()
	}
	start := time.Now()
	page := r.page.Context(ctx).Timeout(opts.Timeout)
	if err := page.SetViewport(ptr(Viewports["desktop"])); err != nil {
		return p, err
	}
	if err := page.Navigate(rawURL); err != nil {
		p.Error = navError(err)
		return p, nil
	}
	_ = page.WaitLoad()
	page.WaitRequestIdle(700*time.Millisecond, nil, nil, nil)()
	settle(ctx, 300*time.Millisecond)

	var f pageFacts
	if obj, err := page.Eval(factsJS); err == nil {
		_ = json.Unmarshal([]byte(obj.Value.JSON("", "")), &f)
	}
	r.fill(p, f)
	p.Text = strings.Join(strings.Fields(f.Text), " ")
	if info, err := page.Info(); err == nil {
		p.FinalURL = info.URL
	}
	if opts.Save != nil {
		if err := shoot(page, p, "desktop", f.Height, opts); err != nil {
			return p, err
		}
	}
	p.Duration = float64(time.Since(start).Milliseconds())
	return p, nil
}

// Mobile reloads rawURL at the mobile viewport and screenshots it. Errors
// that only happen on mobile are added to the page.
func Mobile(ctx context.Context, r *Recorder, p *model.Page, rawURL string, opts Options) error {
	page := r.page.Context(ctx).Timeout(opts.Timeout)
	if err := page.SetViewport(ptr(Viewports["mobile"])); err != nil {
		return err
	}
	// Only errors from the mobile load count: modules' clicks at desktop
	// size (Live) report their own errors, and mustn't show up twice.
	r.mu.Lock()
	r.console = nil
	r.mu.Unlock()
	if err := page.Navigate(rawURL); err != nil {
		return nil // the desktop visit already recorded what it could
	}
	_ = page.WaitLoad()
	page.WaitRequestIdle(700*time.Millisecond, nil, nil, nil)()
	settle(ctx, 300*time.Millisecond)
	r.mu.Lock()
	for _, c := range r.console {
		if !slices.Contains(p.Console, c) {
			p.Console = append(p.Console, c)
		}
	}
	r.mu.Unlock()
	if opts.Save == nil {
		return nil
	}
	h := 0
	if obj, err := page.Eval(`() => document.documentElement.scrollHeight`); err == nil {
		h = obj.Value.Int()
	}
	return shoot(page, p, "mobile", h, opts)
}

// fill copies recorded events and DOM facts into p.
func (r *Recorder) fill(p *model.Page, f pageFacts) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.Title, p.Robots = strings.TrimSpace(f.Title), f.Robots
	text := strings.Join(strings.Fields(f.Text), " ")
	p.TextLength = len(text)
	sum := sha1.Sum([]byte(text)) //nolint:gosec // fingerprint
	p.TextHash = hex.EncodeToString(sum[:8])
	p.Redirects = append([]string(nil), r.chain...)
	p.Console = append([]string(nil), r.console...)
	base, _ := url.Parse(p.URL)
	for _, l := range f.Links {
		if u, err := url.Parse(l); err == nil && base != nil && sameSite(base, u) {
			p.Links = append(p.Links, l)
		}
	}
	p.Metrics = model.Metrics{TTFB: f.TTFB, FCP: f.FCP, LCP: f.LCP, CLS: round(f.CLS, 3), TBT: f.TBT, Load: f.Load}
	for _, id := range r.order {
		q := r.reqs[id]
		if id == r.doc {
			p.Status, p.Headers = q.status, q.headers
			if xr := q.headers["x-robots-tag"]; xr != "" {
				p.Robots = strings.Trim(p.Robots+", "+xr, ", ")
			}
			continue
		}
		if strings.HasPrefix(q.url, "data:") || strings.HasPrefix(q.url, "blob:") {
			continue
		}
		p.Metrics.Requests++
		p.Metrics.Bytes += q.bytes
		if q.failed != "" || q.status >= 400 {
			p.Failed = append(p.Failed, model.Request{URL: q.url, Method: q.method, Type: q.typ, Status: q.status, Error: q.failed})
		}
		if q.done || q.status > 0 {
			p.Resources = append(p.Resources, model.Resource{URL: q.url, Type: q.typ, Status: q.status, Bytes: q.bytes,
				Protocol: q.protocol, Encoding: q.encoding, Cache: q.cache, Mime: q.mime})
		}
	}
}

// shoot hides ignored elements, pauses animations and takes a full-page
// screenshot (capped at maxShotHeight), plus a second one when learning
// dynamic regions.
func shoot(page *rod.Page, p *model.Page, viewport string, height int, opts Options) error {
	css := stillCSS
	if len(opts.Ignore) > 0 {
		css += strings.Join(opts.Ignore, ",") + "{visibility:hidden!important}"
	}
	js := `(css, sels) => {
	  const s = document.createElement('style'); s.id = '__soundcheck_still'; s.textContent = css; document.documentElement.appendChild(s);
	  const rects = [];
	  for (const sel of sels) { try { document.querySelectorAll(sel).forEach(e => { const r = e.getBoundingClientRect(); if (r.width && r.height) rects.push({X: Math.round(r.left + scrollX), Y: Math.round(r.top + scrollY), W: Math.round(r.width), H: Math.round(r.height)}); }); } catch (e) {} }
	  return rects;
	}`
	var masked []model.Rect
	if obj, err := page.Eval(js, css, opts.Ignore); err == nil {
		_ = json.Unmarshal([]byte(obj.Value.JSON("", "")), &masked)
	}
	take := func() ([]byte, error) {
		// Clip the page at its height instead of resizing the viewport to
		// it, which would stretch 100vh layouts and push footers out.
		vp := Viewports[viewport]
		req := &proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng, CaptureBeyondViewport: true,
			Clip: &proto.PageViewport{Width: float64(vp.Width), Height: float64(min(max(height, vp.Height), maxShotHeight)), Scale: 1}}
		return page.Screenshot(false, req)
	}
	img, err := take()
	if err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}
	shot := model.Shot{File: fmt.Sprintf("shots/%s-%s.png", p.ID, viewport), Masked: masked}
	if cfg, err := png.DecodeConfig(bytes.NewReader(img)); err == nil {
		shot.Width, shot.Height = cfg.Width, cfg.Height
	}
	if err := opts.Save(shot.File, img); err != nil {
		return err
	}
	if opts.Twice {
		time.Sleep(1200 * time.Millisecond)
		if img2, err := take(); err == nil {
			shot.Second = fmt.Sprintf("shots/%s-%s.2.png", p.ID, viewport)
			if err := opts.Save(shot.Second, img2); err != nil {
				return err
			}
		}
	}
	_, _ = page.Eval(`() => { const s = document.getElementById('__soundcheck_still'); if (s) s.remove(); }`)
	p.Shots[viewport] = shot
	return nil
}

func navError(err error) string {
	s := err.Error()
	if i := strings.Index(s, "net::"); i >= 0 {
		s = s[i:]
	}
	if strings.Contains(s, "context deadline exceeded") {
		return "timed out"
	}
	return firstLine(s)
}

func settle(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

func sameSite(a, b *url.URL) bool {
	return strings.TrimPrefix(strings.ToLower(a.Hostname()), "www.") == strings.TrimPrefix(strings.ToLower(b.Hostname()), "www.")
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func round(f float64, n int) float64 {
	p := 1.0
	for range n {
		p *= 10
	}
	return float64(int(f*p+0.5)) / p
}

func ptr[T any](v T) *T { return &v }
