// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

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
	"errors"
	"fmt"
	"image/png"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/capture/intercept"
	"github.com/RhythmShahriar/soundcheck/check"
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
	// Headers are the run's extra headers (-H plus --cookie as a Cookie
	// header). They go only to Site, never to third parties the page
	// loads. A Cookie header becomes real cookies for Site's host.
	Headers map[string]string
	Site    *url.URL                             // the site being checked; without it Headers go nowhere
	Save    func(name string, data []byte) error // stores a screenshot; nil skips screenshots
	// Engine is the browser engine the tab runs in, which names the
	// screenshots; empty is Chrome.
	Engine Engine
}

// Recorder collects browser events for one page.
type Recorder struct {
	page    *rod.Page // Chrome, recorded through its DevTools protocol
	tab     Tab       // any other engine (see StartTab); nil for page
	frame   proto.PageFrameID
	mu      sync.Mutex
	reqs    map[string]*req
	order   []string
	doc     string
	chain   []string
	console []string
	stop    func()
	release func() // removes the header handler
	// Requests still under way and when the network was last busy, for
	// StartTab's recorders to wait for the network to go quiet.
	inflight map[string]bool
	busy     time.Time
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
// window.__soundcheck. Long tasks are kept as [start, end] so factsJS can
// compute Total Blocking Time once FCP is known; flushTasks hands over
// long tasks the observer hasn't delivered yet and taskSeen is when the
// last one came in. Chrome only paints, and
// so only reports FCP, LCP and long tasks, for a visible page: tabs must
// be opened with browser.NewTab, which makes each one render as if it
// were in front.
const metricsJS = `(() => {
  const s = window.__soundcheck = {lcp: 0, cls: 0, fcp: 0, tasks: [], taskSeen: 0, flushTasks: () => {}};
  const watch = (type, fn) => {
    try { const o = new PerformanceObserver(l => l.getEntries().forEach(fn)); o.observe({type, buffered: true}); return o; } catch (e) { return null; }
  };
  watch('largest-contentful-paint', e => { s.lcp = e.renderTime || e.loadTime || e.startTime; });
  watch('layout-shift', e => { if (!e.hadRecentInput) s.cls += e.value; });
  const tasks = watch('longtask', e => { s.tasks.push([e.startTime, e.startTime + e.duration]); s.taskSeen = performance.now(); });
  if (tasks) s.flushTasks = () => tasks.takeRecords().forEach(e => { s.tasks.push([e.startTime, e.startTime + e.duration]); s.taskSeen = performance.now(); });
  watch('paint', e => { if (e.name === 'first-contentful-paint') s.fcp = e.startTime; });
})();`

// stillCSS pauses animations and hides carets so screenshots are stable.
const stillCSS = `*,*::before,*::after{animation-play-state:paused!important;animation-delay:-0.0001s!important;transition:none!important;caret-color:transparent!important}`

// Start opens recording on a fresh tab. Call it before navigating.
func Start(ctx context.Context, page *rod.Page, opts Options) (*Recorder, error) {
	r := &Recorder{page: page, reqs: map[string]*req{}}
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
	release, err := siteHeaders(page, opts.Site, opts.Headers)
	if err != nil {
		return nil, err
	}
	r.release = release
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

// Stop ends event recording and stops adding the run's headers.
func (r *Recorder) Stop() {
	r.stop()
	r.release()
}

// siteHeaders makes tab send headers to site only. Network.setExtraHTTPHeaders
// would be simpler. Chrome sends those to every origin though, so a
// tracker or CDN script the page loads would get the staging cookie and
// the Authorization header. Instead each request is paused (see package
// intercept) and the headers are added when it goes to site. A Cookie
// header is set as cookies for site's host: overriding the Cookie header
// would drop the cookies the site sets during the visit.
func siteHeaders(tab *rod.Page, site *url.URL, headers map[string]string) (release func(), err error) {
	release = func() {}
	if site == nil || len(headers) == 0 {
		return release, nil
	}
	extra := map[string]string{}
	for k, v := range headers {
		if !strings.EqualFold(k, "Cookie") {
			extra[k] = v
			continue
		}
		for _, c := range Cookies(v) {
			// HttpOnly keeps them from the page's own scripts, third-party
			// ones included.
			_, err := proto.NetworkSetCookie{Name: c[0], Value: c[1], URL: site.Scheme + "://" + site.Host + "/", Path: "/", HTTPOnly: true}.Call(tab)
			if err != nil {
				return release, fmt.Errorf("can't set the cookie %s: %w", c[0], err)
			}
		}
	}
	if len(extra) == 0 {
		return release, nil
	}
	return intercept.Add(tab, func(q *intercept.Request) {
		if q.URL != nil && check.SameSite(site, q.URL) {
			for k, v := range extra {
				q.SetHeader(k, v)
			}
		}
	})
}

// Cookies splits a Cookie header ("a=1; b=2") into name and value pairs.
// Like a browser, it skips parts without a name.
func Cookies(header string) [][2]string {
	var out [][2]string
	for part := range strings.SplitSeq(header, ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, [2]string{name, strings.TrimSpace(value)})
		}
	}
	return out
}

func (r *Recorder) onRequest(e *proto.NetworkRequestWillBeSent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := string(e.RequestID)
	if e.Type == proto.NetworkResourceTypeDocument && (r.frame == "" || e.FrameID == r.frame) && (r.doc == "" || r.doc == id) {
		r.frame, r.doc = e.FrameID, id
		if e.RedirectResponse != nil {
			r.chain = append(r.chain, fmt.Sprintf("%d %s", e.RedirectResponse.Status, e.RedirectResponse.URL))
		}
	}
	q := r.request(id)
	q.url, q.method, q.typ = e.Request.URL, e.Request.Method, string(e.Type)
}

// request is the request with id, added when it is new. The caller
// holds r.mu.
func (r *Recorder) request(id string) *req {
	q, ok := r.reqs[id]
	if !ok {
		q = &req{}
		r.reqs[id] = q
		r.order = append(r.order, id)
	}
	return q
}

func (r *Recorder) onResponse(e *proto.NetworkResponseReceived) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.reqs[string(e.RequestID)]
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
	if q := r.reqs[string(e.RequestID)]; q != nil {
		q.bytes, q.done = int64(e.EncodedDataLength), true
	}
}

func (r *Recorder) onFailed(e *proto.NetworkLoadingFailed) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q := r.reqs[string(e.RequestID)]; q != nil && !e.Canceled {
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

// factsJS reads the page after it settled. TBT follows Lighthouse: the
// part of each long task after FCP, minus 50 ms, summed; Lighthouse stops
// at Time to Interactive, Soundcheck once the page went quiet: load,
// network idle, then no long task for taskQuietMs (at most taskWaitMs),
// which is close for most pages. The wait matters: a long task the
// observer hasn't delivered yet, or one that starts just after load,
// would otherwise count as 0 ms and turn into a false FIXED or SLOWER
// between runs. A script that blocks before the first paint delays FCP
// and LCP instead.
const factsJS = `async () => {
  const s = window.__soundcheck || {};
  const begin = performance.now();
  for (;;) {
    if (s.flushTasks) s.flushTasks();
    const now = performance.now();
    if (now - Math.max(begin, s.taskSeen || 0) >= ` + taskQuietMs + ` || now - begin >= ` + taskWaitMs + `) break;
    await new Promise(r => setTimeout(r, 50));
  }
  const nav = performance.getEntriesByType('navigation')[0] || {};
  let tbt = 0;
  for (const [start, end] of s.tasks || []) {
    const d = end - Math.max(start, s.fcp || 0);
    if (end - start > 50 && d > 50) tbt += d - 50;
  }
  const robots = [...document.querySelectorAll('meta[name="robots" i],meta[name="googlebot" i]')].map(m => m.content).join(', ');
  const links = [...new Set([...document.querySelectorAll('a[href]')].map(a => a.href))].slice(0, 2000);
  return {
    title: document.title || '', robots, links,
    text: (document.body && document.body.innerText || '').slice(0, 200000),
    ttfb: nav.responseStart || 0, load: nav.loadEventEnd || nav.duration || 0,
    fcp: s.fcp || 0, lcp: s.lcp || 0, cls: s.cls || 0, tbt,
    height: Math.max(document.documentElement.scrollHeight, document.body ? document.body.scrollHeight : 0),
  };
}`

// How long factsJS waits for long tasks: until none came in for
// taskQuietMs and never more than taskWaitMs.
const (
	taskQuietMs = "300"
	taskWaitMs  = "2000"
)

// Visit navigates to rawURL at the desktop viewport, waits for the page to
// settle, and records it. Screenshots are taken when opts.Save is set.
//
// opts.Timeout covers the navigation, up to the main response, and the
// waits for load and network idle. Reading the page afterwards gets its
// own budget (see readBudget), so a page that answers just inside the
// timeout is still read instead of being recorded empty.
func Visit(ctx context.Context, r *Recorder, rawURL string, opts Options) (*model.Page, error) {
	p := newPage(rawURL)
	start := time.Now()
	// Every outcome, a timeout included, records how long it really took.
	defer func() { p.Duration = float64(time.Since(start).Milliseconds()) }()
	if r.tab != nil {
		return p, visitTab(ctx, r, p, rawURL, opts)
	}
	page := r.page.Context(ctx).Timeout(opts.Timeout)
	if err := page.SetViewport(ptr(Viewports["desktop"])); err != nil {
		return p, err
	}
	if err := page.Navigate(rawURL); err != nil {
		p.Error = navError(err)
		return p, nil
	}
	// The waits only make the reading more complete: a page that never
	// finishes loading (a hung font or tracker) is still worth checking
	// as it is, so their errors are dropped and the page is read anyway.
	_ = page.WaitLoad()
	page.WaitRequestIdle(700*time.Millisecond, nil, nil, nil)()
	settle(ctx, 300*time.Millisecond)

	// A fresh deadline: the one above may be all but spent by a slow
	// first response.
	rctx, cancel := context.WithTimeout(ctx, readBudget(opts.Timeout))
	defer cancel()
	return p, r.read(ctx, rctx, ChromeTab(r.page), p, opts)
}

// read records the loaded page into p: the facts factsJS reads, the
// recorded events, the final URL and, when opts.Save is set, the desktop
// screenshot. rctx is the reading's budget; ctx is the run's.
func (r *Recorder) read(ctx, rctx context.Context, tab Tab, p *model.Page, opts Options) error {
	var f pageFacts
	if err := tab.Eval(rctx, factsJS, &f); err != nil {
		// Empty facts would read as a blank page that lost its title.
		r.fill(p, pageFacts{})
		p.TextHash = ""
		p.Error = readError(ctx, err)
		return errors.New(p.Error)
	}
	r.fill(p, f)
	p.Text = strings.Join(strings.Fields(f.Text), " ")
	if u, err := tab.URL(rctx); err == nil {
		p.FinalURL = u
	}
	if opts.Save != nil {
		return shoot(rctx, tab, p, "desktop", f.Height, opts)
	}
	return nil
}

func newPage(rawURL string) *model.Page {
	p := &model.Page{ID: model.PageID(rawURL), URL: rawURL, Shots: map[string]model.Shot{}}
	if u, err := url.Parse(rawURL); err == nil {
		p.Path = u.RequestURI()
	}
	return p
}

// readBudget is how long reading a loaded page and screenshotting it may
// take: a quarter of the page timeout, and at least 5 seconds.
func readBudget(timeout time.Duration) time.Duration {
	return max(5*time.Second, timeout/4)
}

// readError words a failure to read the page after it loaded.
func readError(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return "timed out reading the page after load"
	}
	return "couldn't read the page after load: " + firstLine(err.Error())
}

// Mobile reloads rawURL at the mobile viewport and screenshots it. Errors
// that only happen on mobile are added to the page.
func Mobile(ctx context.Context, r *Recorder, p *model.Page, rawURL string, opts Options) error {
	if r.tab != nil {
		return mobileTab(ctx, r, p, rawURL, opts)
	}
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
	// Best effort, as in Visit.
	_ = page.WaitLoad()
	page.WaitRequestIdle(700*time.Millisecond, nil, nil, nil)()
	settle(ctx, 300*time.Millisecond)
	rctx, cancel := context.WithTimeout(ctx, readBudget(opts.Timeout))
	defer cancel()
	return r.readMobile(rctx, ChromeTab(r.page), p, opts)
}

// readMobile adds the errors of the mobile load to p and takes the
// mobile screenshot.
func (r *Recorder) readMobile(rctx context.Context, tab Tab, p *model.Page, opts Options) error {
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
	_ = tab.Eval(rctx, `() => document.documentElement.scrollHeight`, &h)
	return shoot(rctx, tab, p, "mobile", h, opts)
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
		if u, err := url.Parse(l); err == nil && base != nil && check.SameSite(base, u) {
			p.Links = append(p.Links, l)
		}
	}
	p.Metrics = model.Metrics{TTFB: f.TTFB, FCP: f.FCP, LCP: f.LCP, CLS: round(f.CLS, 3), TBT: f.TBT, Load: f.Load}
	for _, id := range r.order {
		q := r.reqs[id]
		if id == r.doc {
			p.Status = q.status
			if q.headers != nil {
				p.Headers = map[string]string{}
				for k, v := range q.headers {
					p.Headers[k] = storedHeader(k, v)
				}
			}
			if xr := q.headers["x-robots-tag"]; xr != "" {
				p.Robots = strings.Trim(p.Robots+", "+xr, ", ")
			}
			p.Metrics.Requests++ // the page's weight includes its HTML
			p.Metrics.Bytes += q.bytes
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
func shoot(ctx context.Context, tab Tab, p *model.Page, viewport string, height int, opts Options) error {
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
	ignore := opts.Ignore
	if ignore == nil {
		ignore = []string{}
	}
	_ = tab.Eval(ctx, js, &masked, css, ignore)
	vp := Viewports[viewport]
	take := func() ([]byte, error) {
		return tab.Clip(ctx, vp.Width, min(max(height, vp.Height), maxShotHeight))
	}
	img, err := take()
	if err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}
	shot := model.Shot{File: shotFile(p.ID, viewport, opts.Engine, ""), Masked: masked}
	if cfg, err := png.DecodeConfig(bytes.NewReader(img)); err == nil {
		shot.Width, shot.Height = cfg.Width, cfg.Height
	}
	if err := opts.Save(shot.File, img); err != nil {
		return err
	}
	if opts.Twice {
		time.Sleep(1200 * time.Millisecond)
		if img2, err := take(); err == nil {
			shot.Second = shotFile(p.ID, viewport, opts.Engine, ".2")
			if err := opts.Save(shot.Second, img2); err != nil {
				return err
			}
		}
	}
	_ = tab.Eval(ctx, `() => { const s = document.getElementById('__soundcheck_still'); if (s) s.remove(); }`, nil)
	p.Shots[viewport] = shot
	return nil
}

// shotFile is where a page's screenshot is stored: shots/<page>-<viewport>.png
// in Chrome, with the engine added for the others
// (shots/<page>-<viewport>-firefox.png), so shots of one engine are only
// ever compared with shots of the same engine.
func shotFile(page, viewport string, engine Engine, second string) string {
	name := page + "-" + viewport
	if engine != "" && engine != Chrome {
		name += "-" + string(engine)
	}
	return "shots/" + name + second + ".png"
}

func navError(err error) string {
	s := err.Error()
	// Chrome's net::ERR_… and Firefox's NS_ERROR_… name the failure.
	if i := strings.Index(s, "net::"); i >= 0 {
		s = s[i:]
	} else if i := strings.Index(s, "NS_ERROR_"); i >= 0 {
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
