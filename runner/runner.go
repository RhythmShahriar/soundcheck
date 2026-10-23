// Package runner checks a whole site: it finds the pages, visits each in
// a pool of browser tabs at a polite pace, runs every module's hooks, and
// returns the recorded run.
package runner

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/rhythm/kit/crawl"
	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

type Options struct {
	Config    config.Config
	Target    string
	Paths     []string // visit exactly these paths (a compare re-checks the snapshot's pages); empty discovers
	Snapshot  bool
	Name      string
	Dir       string // where the run's files go
	Version   string
	UserAgent string
	Headers   map[string]string
	Proxy     *url.URL
	Insecure  bool
	Browser   browser.Options
	Modules   []check.Module
	// Login returns how to log the test account in on the resolved site;
	// nil (or a nil result) when no credentials are set. See
	// check.Context.Login.
	Login func(base *url.URL) func(ctx context.Context, sess *rod.Browser) error
	Hooks Hooks
}

// Hooks report progress. Calls are serialized.
type Hooks struct {
	Discovered func(pages int, skipped map[string]int)
	Page       func(p *model.Page, done, total int)
	Log        func(msg string)
}

// Run checks the site.
func Run(ctx context.Context, opts Options) (*model.Run, error) {
	start := time.Now()
	cfg := opts.Config
	client := httpClient(opts)
	base, err := resolve(ctx, client, opts.Target, opts.UserAgent)
	if err != nil {
		return nil, err
	}
	run := &model.Run{
		Schema: model.Schema, Tool: "rhythm-soundcheck", Version: opts.Version, Target: base.String(),
		Name: opts.Name, Started: start.UTC(),
		Options: model.Options{Viewports: cfg.Viewports, Levels: cfg.Interactions.Levels, Modules: cfg.Modules,
			Limit: cfg.Pages.Limit},
	}
	var mu sync.Mutex // serializes hooks
	emit := func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		fn()
	}
	log := func(s string) {
		if opts.Hooks.Log != nil {
			emit(func() { opts.Hooks.Log(s) })
		}
	}

	// Pages: the given paths, or discovered from sitemaps and links.
	copts := crawl.Options{Limit: cfg.Pages.Limit, PerGroup: cfg.Pages.PerGroup, Include: cfg.Pages.Include,
		Exclude: cfg.Pages.Exclude, IgnoreRobots: cfg.Pages.IgnoreRobots, UserAgent: opts.UserAgent,
		Rate: cfg.Rate, HTTP: client,
		// The browser reports each page's rendered links, which the visit
		// loop below adds; crawling the HTML too would load every page twice.
		NoFollow: true}
	discover := len(opts.Paths) == 0
	var frontier *crawl.Frontier
	if discover {
		frontier, err = crawl.New(copts).Crawl(ctx, base)
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for _, p := range cfg.Pages.Extra {
			frontier.Add(p, "config", 0)
		}
	} else {
		copts.PerGroup, copts.Limit, copts.IgnoreRobots = -1, len(opts.Paths)+1, true
		frontier = crawl.NewFrontier(base, copts)
		for _, p := range opts.Paths {
			frontier.Add(p, "snapshot", 0)
		}
	}
	pages := frontier.Pages()
	if len(pages) == 0 {
		return nil, fmt.Errorf("found no pages to check at %s", base)
	}
	if opts.Hooks.Discovered != nil {
		emit(func() { opts.Hooks.Discovered(len(pages), frontier.Skipped()) })
	}

	b, err := browser.Launch(ctx, withLog(opts.Browser, log))
	if err != nil {
		return nil, err
	}
	defer b.Close()
	sess, err := b.Session()
	if err != nil {
		return nil, err
	}
	hosts := ratelimit.NewHosts(cfg.Rate)
	cc := &check.Context{
		Config: cfg, Base: base, Run: run, Dir: opts.Dir, Snapshot: opts.Snapshot, HTTP: client,
		UserAgent: opts.UserAgent, Hosts: hosts, Browser: sess, NewSession: b.Session, Save: store.Saver(opts.Dir),
	}
	if opts.Login != nil {
		cc.Login = opts.Login(base)
	}
	run.Options.LoggedIn = cc.Login != nil

	// Visit pages in parallel tabs; links found while rendering are added
	// to the queue when discovering.
	var (
		qmu     sync.Mutex
		queue   []crawl.Page
		total   = len(pages)
		done    int
		wg      sync.WaitGroup
		sem     = make(chan struct{}, cfg.Tabs)
		results = map[string]*model.Page{}
		order   []string
	)
	queue = append(queue, pages...)
	for {
		qmu.Lock()
		if len(queue) == 0 {
			qmu.Unlock()
			wg.Wait()
			qmu.Lock()
			if len(queue) == 0 {
				qmu.Unlock()
				break
			}
		}
		pg := queue[0]
		queue = queue[1:]
		order = append(order, pg.URL)
		qmu.Unlock()
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			p := visit(ctx, cc, opts, hosts, pg)
			if discover {
				before := len(frontier.Pages())
				for _, l := range p.Links {
					frontier.Add(l, "rendered", pg.Depth+1)
				}
				if added := frontier.Pages()[before:]; len(added) > 0 {
					qmu.Lock()
					queue = append(queue, added...)
					total += len(added)
					qmu.Unlock()
				}
			}
			qmu.Lock()
			results[pg.URL] = p
			done++
			d, t := done, total
			qmu.Unlock()
			if opts.Hooks.Page != nil {
				emit(func() { opts.Hooks.Page(p, d, t) })
			}
		})
	}
	wg.Wait()
	for _, u := range order {
		if p := results[u]; p != nil {
			run.Pages = append(run.Pages, *p)
		}
	}
	run.Skipped = frontier.Skipped()

	if ctx.Err() == nil {
		for _, m := range opts.Modules {
			if s, ok := m.(check.SiteChecker); ok {
				s.CheckSite(ctx, cc)
			}
		}
	}
	run.Duration = float64(time.Since(start).Milliseconds())
	return run, ctx.Err()
}

// visit checks one page in its own tab.
func visit(ctx context.Context, cc *check.Context, opts Options, hosts *ratelimit.Hosts, pg crawl.Page) *model.Page {
	cfg := opts.Config
	fail := func(err error) *model.Page {
		return &model.Page{ID: model.PageID(pg.URL), URL: pg.URL, Path: pathOf(pg.URL), Group: pg.Group, Error: err.Error()}
	}
	tab, err := cc.Browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return fail(err)
	}
	defer func() { _ = tab.Close() }()
	copts := capture.Options{
		Timeout: time.Duration(cfg.Timeout) * time.Second, Ignore: cfg.Visual.Ignore, Twice: opts.Snapshot && cfg.Has("visual"),
		UserAgent: opts.UserAgent, Headers: opts.Headers,
	}
	if cfg.Has("visual") {
		copts.Save = cc.Save
	}
	rec, err := capture.Start(ctx, tab, copts)
	if err != nil {
		return fail(err)
	}
	defer rec.Stop()
	for _, m := range opts.Modules {
		if pr, ok := m.(check.Preparer); ok {
			if err := pr.Prepare(ctx, cc, tab); err != nil {
				cc.Warn(fmt.Sprintf("%s: %v", m.Name(), err))
			}
		}
	}
	host := hostOf(pg.URL)
	if err := hosts.For(host).Wait(ctx); err != nil {
		return fail(err)
	}
	p, err := capture.Visit(ctx, rec, pg.URL, copts)
	if err != nil {
		cc.Warn(fmt.Sprintf("%s: %v", pg.URL, err))
	}
	p.Group = pg.Group
	if p.Error == "" {
		for _, m := range opts.Modules {
			if l, ok := m.(check.Live); ok {
				l.Live(ctx, cc, tab, p)
			}
		}
		if slices.Contains(cfg.Viewports, "mobile") {
			if err := hosts.For(host).Wait(ctx); err == nil {
				if err := capture.Mobile(ctx, rec, p, pg.URL, copts); err != nil {
					cc.Warn(fmt.Sprintf("%s (mobile): %v", pg.URL, err))
				}
			}
		}
	}
	for _, m := range opts.Modules {
		if c, ok := m.(check.PageChecker); ok {
			c.CheckPage(cc, p)
		}
	}
	return p
}

// resolve fetches the target once, following redirects, so the run uses
// the scheme and host the site really answers on (http → https, apex →
// www). A bare hostname falls back to http when https fails.
func resolve(ctx context.Context, client *http.Client, target, ua string) (*url.URL, error) {
	u, explicit, err := ratelimit.ParseTarget(target)
	if err != nil {
		return nil, err
	}
	try := func(u *url.URL) (*url.URL, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		_ = resp.Body.Close()
		final := resp.Request.URL
		return &url.URL{Scheme: final.Scheme, Host: final.Host, Path: u.Path}, nil
	}
	base, err := try(u)
	if err != nil && !explicit && ctx.Err() == nil {
		u.Scheme = "http"
		base, err = try(u)
	}
	if err != nil {
		var cv *tls.CertificateVerificationError
		if errors.As(err, &cv) {
			return nil, fmt.Errorf("can't reach %s: invalid TLS certificate (rerun with --insecure to check it anyway)", u.Host)
		}
		return nil, fmt.Errorf("can't reach %s: %w", u.Host, err)
	}
	if base.Path == "" {
		base.Path = "/"
	}
	return base, nil
}

// headerTransport adds the run's extra headers to every request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.headers {
		if strings.EqualFold(k, "Host") {
			r.Host = v
			continue
		}
		r.Header.Set(k, v)
	}
	return t.base.RoundTrip(r)
}

func httpClient(opts Options) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if opts.Proxy != nil {
		tr.Proxy = http.ProxyURL(opts.Proxy)
	}
	if opts.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the user asked for --insecure
	}
	return &http.Client{Transport: headerTransport{tr, opts.Headers}, Timeout: time.Duration(opts.Config.Timeout) * time.Second}
}

func withLog(o browser.Options, log func(string)) browser.Options {
	if o.Log == nil {
		o.Log = log
	}
	return o
}

func pathOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.RequestURI()
	}
	return raw
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}
