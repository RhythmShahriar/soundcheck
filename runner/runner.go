// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package runner checks a whole site: it finds the pages, visits each in
// a pool of browser tabs at a polite pace, runs every module's hooks, and
// returns the recorded run.
package runner

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/rhythm/kit/crawl"
	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// Options is what to check and how. Config holds the project's
// settings; the rest comes from the command line.
type Options struct {
	Config config.Config
	// Target is the site as the user named it: a hostname (tried on
	// https first) or a URL.
	Target string
	// Paths visits exactly these paths (a compare re-checks the
	// snapshot's pages, see Revisit) with no discovery; empty discovers
	// pages from sitemaps and links.
	Paths    []string
	Snapshot bool   // the run is a snapshot: modules learn what varies on its own
	Name     string // the snapshot's name
	Dir      string // where the run's files go
	Version  string // Soundcheck's version, recorded in the run
	// UserAgent replaces the browser's; empty keeps Chrome's own.
	UserAgent string
	// Headers go with every request to the site (-H and --cookie as a
	// Cookie header), never to other hosts.
	Headers map[string]string
	Proxy   *url.URL // for the run's requests outside the browser; Browser.Proxy is the browser's
	// Insecure accepts invalid TLS certificates (--insecure).
	Insecure bool
	Browser  browser.Options
	// Modules run in ascending phase (check.Phaser), keeping this order
	// within a phase.
	Modules []check.Module
	// Login returns how to log the test account in on the resolved site;
	// nil (or a nil result) when no credentials are set. See
	// check.Context.Login.
	Login func(base *url.URL) func(ctx context.Context, sess *rod.Browser) error
	// Echo, when set, records what each form submit and flow sends; the
	// run gets its own ID from its start time.
	Echo *echolink.Link
	// FollowHost lets the target redirect to another site and checks that
	// site instead. Without it, a redirect to another host (other than
	// apex to www) is an error, so the run and its -H headers don't
	// silently move somewhere the user didn't name.
	FollowHost bool
	Hooks      Hooks

	// Done resumes an interrupted run: these pages were finished before
	// and are kept as they are, not visited again. Their links are added
	// to the pages to find, and the run's pages start with them.
	Done []*model.Page
	// OnPage is called with each page as it finishes (not the Done ones,
	// nor one the cancel cut short), so it can be stored right away and a
	// later run can resume after it. Calls are serialized with Hooks.
	OnPage func(p *model.Page)

	revisit bool // set by Revisit: no site-wide checks
}

// Hooks report progress. Calls are serialized.
type Hooks struct {
	Discovered func(pages int, skipped map[string]int)
	Page       func(p *model.Page, done, total int)
	// Log gets progress notes; a leading "!" marks one to show even
	// without --verbose (see report.Pretty.Log).
	Log func(msg string)
	// Progress gets a module's progress through a long step, from
	// check.Context.Progress.
	Progress func(label string, done, total int)
}

// Run checks the site. When ctx is canceled it starts no more pages and
// returns the run so far with ctx.Err(): pages it finished are complete,
// and a page whose visit the cancel cut short has the Error
// "context canceled" and nothing else (see canceled). With opts.Done it
// resumes such a run, visiting only the pages it didn't finish.
func Run(ctx context.Context, opts Options) (*model.Run, error) {
	start := time.Now()
	cfg := opts.Config
	opts.Modules = check.SortByPhase(opts.Modules)
	target, _, err := ratelimit.ParseTarget(opts.Target)
	if err != nil {
		return nil, err
	}
	client := httpClient(opts, target)
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
	startURL, err := resolve(ctx, client, opts.Target, opts.UserAgent, opts.FollowHost, log)
	if err != nil {
		return nil, err
	}
	// With FollowHost the start page may be on another site, which is
	// now the one the headers belong to.
	client = httpClient(opts, startURL)
	// Modules get the start page without its query: production URLs
	// (config) and flows match and resolve against scheme, host and path.
	base := &url.URL{Scheme: startURL.Scheme, Host: startURL.Host, Path: startURL.Path, RawPath: startURL.RawPath}
	run := &model.Run{
		Schema: model.Schema, Tool: "rhythm-soundcheck", Version: opts.Version, Target: startURL.String(),
		Name: opts.Name, Started: start.UTC(),
		Options: model.Options{Viewports: cfg.Viewports, Levels: cfg.Interactions.Levels, Modules: cfg.Modules,
			Limit: cfg.Pages.Limit},
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
		frontier, err = crawl.New(copts).Crawl(ctx, startURL)
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for _, p := range cfg.Pages.Extra {
			addListed(frontier, startURL, p, "config", cfg.Pages.Exclude, log)
		}
	} else {
		copts.PerGroup, copts.Limit, copts.IgnoreRobots = -1, len(opts.Paths)+1, true
		frontier = crawl.NewFrontier(base, copts)
		for _, p := range opts.Paths {
			addListed(frontier, base, p, "snapshot", cfg.Pages.Exclude, log)
		}
	}
	// The kit's frontier tells sites apart by hostname alone, so pages of
	// a sibling site on another port (a sitemap listing both, a local
	// setup) get in; they are dropped here and counted like the
	// frontier's own "other site" skips.
	var (
		smu      sync.Mutex
		offSite  int
		sameSite = func(found []crawl.Page) []crawl.Page {
			kept := slices.DeleteFunc(found, func(p crawl.Page) bool {
				u, err := url.Parse(p.URL)
				return err != nil || !check.SameSite(base, u)
			})
			smu.Lock()
			offSite += len(found) - len(kept)
			smu.Unlock()
			return kept
		}
		skipped = func() map[string]int {
			s := frontier.Skipped()
			smu.Lock()
			defer smu.Unlock()
			if offSite > 0 {
				s["other site"] += offSite
			}
			return s
		}
	)
	// Pages a resumed run already finished are kept as they are. Their
	// links go back into the frontier, so the pages only they led to are
	// still found.
	finished := map[string]bool{}
	for _, p := range opts.Done {
		u := p.URL
		if norm, ok := crawl.Normalize(startURL, p.URL); ok {
			u = norm
		}
		finished[u] = true
		if discover {
			frontier.Add(p.URL, "rendered", 1)
			addLinks(frontier, startURL, p.Links, 1)
		}
	}
	pages := slices.DeleteFunc(sameSite(frontier.Pages()), func(p crawl.Page) bool { return finished[p.URL] })
	if len(pages) == 0 && len(opts.Done) == 0 {
		return nil, fmt.Errorf("found no pages to check at %s", startURL)
	}
	if opts.Hooks.Discovered != nil {
		emit(func() { opts.Hooks.Discovered(len(pages)+len(opts.Done), skipped()) })
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
	if opts.Hooks.Progress != nil {
		cc.OnProgress = func(label string, done, total int) {
			emit(func() { opts.Hooks.Progress(label, done, total) })
		}
	}
	if opts.Login != nil {
		cc.Login = opts.Login(base)
	}
	if opts.Echo != nil && cfg.Has("echo") {
		cc.Echo = opts.Echo.ForRun(run.Started)
		run.Options.Echo, run.Options.EchoMode, run.Options.EchoURL = true, cc.Echo.Mode, cc.Echo.URL
	} else {
		// Without a link nothing is recorded: don't let a comparison think
		// the module ran.
		run.Options.Modules = slices.DeleteFunc(slices.Clone(run.Options.Modules), func(m string) bool { return m == "echo" })
	}
	// Try the login once up front: a wrong password or a changed login
	// form is one clear warning, and the checks that need it skip.
	if cc.Login != nil {
		if err := tryLogin(ctx, b, cc.Login); err != nil {
			cc.Warn("login failed, so the checks that need it were skipped: " + err.Error())
			cc.Login = nil
		}
	}
	run.Options.LoggedIn = cc.Login != nil

	// Visit pages in parallel tabs; links found while rendering are added
	// to the queue when discovering.
	var (
		qmu     sync.Mutex
		queue   []crawl.Page
		total   = len(pages) + len(opts.Done)
		done    = len(opts.Done)
		wg      sync.WaitGroup
		sem     = make(chan struct{}, cfg.Tabs)
		results = map[string]*model.Page{}
		order   []string
		ordered = maps.Clone(finished) // a guard: each page is visited once
	)
	queue = append(queue, pages...)
	for ctx.Err() == nil {
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
		if ordered[pg.URL] {
			total-- // queued twice: visit it once
			qmu.Unlock()
			continue
		}
		ordered[pg.URL] = true
		order = append(order, pg.URL)
		qmu.Unlock()
		// Once the run is canceled no page starts: it would only be
		// recorded as canceled.
		acquired := false
		select {
		case sem <- struct{}{}:
			acquired = true
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			if acquired {
				<-sem
			}
			break
		}
		wg.Go(func() {
			defer func() { <-sem }()
			p := visit(ctx, cc, opts, hosts, pg)
			if discover {
				if added := sameSite(addLinks(frontier, startURL, p.Links, pg.Depth+1)); len(added) > 0 {
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
			// A page the cancel cut short isn't finished: a resumed run
			// visits it again.
			if opts.OnPage != nil && p.Error != canceled {
				emit(func() { opts.OnPage(p) })
			}
		})
	}
	wg.Wait()
	for _, p := range opts.Done {
		run.Pages = append(run.Pages, *p)
	}
	for _, u := range order {
		if p := results[u]; p != nil {
			run.Pages = append(run.Pages, *p)
		}
	}
	run.Skipped = skipped()

	if ctx.Err() == nil && !opts.revisit {
		// Probing every link can take minutes after the last page.
		if slices.ContainsFunc(opts.Modules, func(m check.Module) bool { _, ok := m.(check.SiteChecker); return ok }) {
			log("!Checking site-wide issues (links, sitemap)...")
		}
		for _, m := range opts.Modules {
			if s, ok := m.(check.SiteChecker); ok {
				check.Safe(m.Name(), "CheckSite", cc.Warn, func() { s.CheckSite(ctx, cc) })
			}
		}
	}
	run.Duration = float64(time.Since(start).Milliseconds())
	return run, ctx.Err()
}

// Revisit visits exactly paths again with opts and returns them as a
// run: no discovery and no links followed, each page in a fresh tab. A
// compare uses it to confirm changes before reporting them, since one
// that doesn't come back on a second visit is unstable rather than
// broken. Site-wide checks (CheckSite) don't run: the run holds only
// these pages. opts.Paths, opts.Done and opts.OnPage are ignored, as a
// revisit is not part of a stored run.
func Revisit(ctx context.Context, opts Options, paths []string) (*model.Run, error) {
	if len(paths) == 0 {
		return nil, errors.New("revisit: no pages to visit")
	}
	opts.Paths, opts.Done, opts.OnPage, opts.revisit = slices.Clone(paths), nil, nil, true
	return Run(ctx, opts)
}

// addListed adds a page the user named to f: one from --page (source
// "config") or a path to re-check ("snapshot"). When f leaves it out, a
// note says why, shown even without --verbose: a page asked for by name
// mustn't vanish into the skip counts.
func addListed(f *crawl.Frontier, start *url.URL, raw, source string, exclude []string, log func(string)) {
	name := "page"
	if source == "config" {
		name = "--page"
	}
	before := f.Skipped()
	if f.Add(raw, source, 0) {
		// The frontier tells sites apart by hostname alone; the run drops
		// pages of a sibling site on another port later.
		if norm, ok := crawl.Normalize(start, raw); ok {
			if u, err := url.Parse(norm); err == nil && !check.SameSite(start, u) {
				log(fmt.Sprintf("!%s %s is on another site; not checked", name, raw))
			}
		}
		return
	}
	for reason, n := range f.Skipped() {
		if n > before[reason] {
			log(fmt.Sprintf("!%s %s %s; not checked", name, raw, dropReason(start, raw, reason, exclude)))
		}
	}
}

// dropReason words the frontier's reason for leaving raw out, naming the
// exclude pattern it matched.
func dropReason(start *url.URL, raw, reason string, exclude []string) string {
	switch reason {
	case "excluded":
		norm, _ := crawl.Normalize(start, raw)
		if u, err := url.Parse(norm); err == nil {
			for _, p := range crawl.DefaultExcludes {
				if crawl.Excluded(u, []string{p}) {
					return "matches the default exclude " + p
				}
			}
			for _, p := range exclude {
				if crawl.Excluded(u, []string{p}) {
					return "matches the exclude " + p
				}
			}
		}
		return "is excluded"
	case "other site":
		return "is on another site"
	case "not a page":
		return "isn't a page"
	case "not included":
		return "doesn't match pages.include"
	case "robots.txt":
		return "is disallowed by robots.txt"
	case "page limit":
		return "is past the page limit"
	}
	return "was left out (" + reason + ")"
}

// addLinks offers a page's rendered links to f and returns the pages it
// accepted, so each new page is queued by the one visit that found it
// first. Taking the frontier's pages added since some earlier count
// instead would let two visits that add links at the same time both
// queue the other's pages and visit them twice.
func addLinks(f *crawl.Frontier, start *url.URL, links []string, depth int) []crawl.Page {
	if addHook != nil {
		addHook(false)
	}
	accepted := map[string]bool{}
	for _, l := range links {
		if f.Add(l, "rendered", depth) {
			if norm, ok := crawl.Normalize(start, l); ok {
				accepted[norm] = true
			}
		}
	}
	if addHook != nil {
		addHook(true)
	}
	if len(accepted) == 0 {
		return nil
	}
	// The frontier knows each page's group. They were just added, so
	// they are among its last pages.
	all := f.Pages()
	var out []crawl.Page
	for i := len(all) - 1; i >= 0 && len(out) < len(accepted); i-- {
		if accepted[all[i].URL] {
			out = append(out, all[i])
		}
	}
	slices.Reverse(out)
	return out
}

// addHook, when set by a test, runs before a visit adds its links and
// again before it looks them up, to make visits interleave there.
var addHook func(added bool)

// canceled is the Error of a page whose visit the run's cancel cut
// short (Ctrl+C). Such a page has no facts, issues or shots. What it
// recorded before the cancel is partial; compared with a snapshot it
// would read as a page that lost its title or went blank.
var canceled = context.Canceled.Error()

// visit checks one page in its own tab.
func visit(ctx context.Context, cc *check.Context, opts Options, hosts *ratelimit.Hosts, pg crawl.Page) *model.Page {
	cfg := opts.Config
	start := time.Now()
	fail := func(err error) *model.Page {
		msg := err.Error()
		if ctx.Err() != nil {
			msg = canceled
		}
		return &model.Page{ID: model.PageID(pg.URL), URL: pg.URL, Path: pathOf(pg.URL), Group: pg.Group, Error: msg,
			Duration: float64(time.Since(start).Milliseconds())}
	}
	tab, err := browser.NewTab(cc.Browser)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = tab.Close() }()
	shots, twice := captureNeeds(cfg, opts.Modules, opts.Snapshot)
	copts := capture.Options{
		Timeout: time.Duration(cfg.Timeout) * time.Second, Ignore: cfg.Visual.Ignore, Twice: twice,
		UserAgent: opts.UserAgent, Headers: opts.Headers, Site: cc.Base,
	}
	if shots {
		copts.Save = cc.Save
	}
	rec, err := capture.Start(ctx, tab, copts)
	if err != nil {
		return fail(err)
	}
	defer rec.Stop()
	ctab := check.RodTab(tab)
	prepared := func(m check.Module, err error) {
		if err != nil {
			cc.Warn(fmt.Sprintf("%s: %v", m.Name(), err))
		}
	}
	for _, m := range opts.Modules {
		if pr, ok := m.(check.Preparer); ok {
			check.Safe(m.Name(), "Prepare", cc.Warn, func() { prepared(m, pr.Prepare(ctx, cc, tab)) })
		}
		if pr, ok := m.(check.TabPreparer); ok {
			check.Safe(m.Name(), "PrepareTab", cc.Warn, func() { prepared(m, pr.PrepareTab(ctx, cc, ctab)) })
		}
	}
	host := hostOf(pg.URL)
	if err := hosts.For(host).Wait(ctx); err != nil {
		return fail(err)
	}
	p, err := capture.Visit(ctx, rec, pg.URL, copts)
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	if err != nil {
		cc.Warn(fmt.Sprintf("%s: %v", pg.URL, err))
	}
	p.Group = pg.Group
	if p.Error == "" {
		for _, m := range opts.Modules {
			if l, ok := m.(check.Live); ok {
				check.Safe(m.Name(), "Live", cc.Warn, func() { l.Live(ctx, cc, tab, p) })
			}
			if l, ok := m.(check.TabLive); ok {
				check.Safe(m.Name(), "LiveTab", cc.Warn, func() { l.LiveTab(ctx, cc, ctab, p) })
			}
		}
		if slices.Contains(cfg.Viewports, "mobile") {
			if err := hosts.For(host).Wait(ctx); err == nil {
				if err := capture.Mobile(ctx, rec, p, pg.URL, copts); err != nil && ctx.Err() == nil {
					cc.Warn(fmt.Sprintf("%s (mobile): %v", pg.URL, err))
				}
			}
		}
	}
	// A cancel during the modules' interactions or the mobile pass leaves
	// them half done.
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	for _, m := range opts.Modules {
		if c, ok := m.(check.PageChecker); ok {
			check.Safe(m.Name(), "CheckPage", cc.Warn, func() { c.CheckPage(cc, p) })
		}
	}
	// The text can run to 200,000 characters a page. Nothing past the
	// page's own checks reads it unless a module says so.
	if !slices.ContainsFunc(opts.Modules, func(m check.Module) bool { _, ok := m.(check.NeedsText); return ok }) {
		p.Text = ""
	}
	return p
}

// captureNeeds reports whether the run stores screenshots and whether
// each gets a second capture, from the modules' CaptureNeeds.
//
// A visual module that doesn't implement CaptureNeeds yet gets the shots
// it always had: whenever cfg enables "visual" (which the testbed uses
// to give health the rendered mobile width without the module), and a
// second one in snapshots. The fallback goes once visual implements
// CaptureNeeds.
func captureNeeds(cfg config.Config, modules []check.Module, snapshot bool) (shots, twice bool) {
	legacy := cfg.Has("visual")
	for _, m := range modules {
		n, ok := m.(check.CaptureNeeds)
		if !ok {
			continue
		}
		if m.Name() == "visual" {
			legacy = false
		}
		if n.NeedsShots() {
			shots = true
			twice = twice || n.NeedsSecondShot(snapshot)
		}
	}
	if legacy {
		shots, twice = true, twice || snapshot
	}
	return shots, twice
}

// resolve fetches the target once, following redirects, and returns the
// start page: the URL the site really answers on, with the path and query
// it ended up at, so a run started at /q?lang=fr checks that page and one
// redirected to /b checks /b.
//
// Redirects may move to https and between apex and www (logged as "moved
// to ..."); one to another site is an error unless followHost is set. A
// bare hostname falls back to http only when https is refused outright:
// a bad certificate or a timeout is a reason to stop, not to downgrade.
func resolve(ctx context.Context, client *http.Client, target, ua string, followHost bool, log func(string)) (*url.URL, error) {
	u, explicit, err := ratelimit.ParseTarget(target)
	if err != nil {
		return nil, err
	}
	c := *client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if !followHost && !check.SameSite(via[0].URL, req.URL) {
			return &hostChangeError{from: via[0].URL, to: req.URL}
		}
		return nil
	}
	try := func(u *url.URL) (*url.URL, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		_ = resp.Body.Close()
		final := resp.Request.URL
		return &url.URL{Scheme: final.Scheme, Host: final.Host, Path: final.Path, RawPath: final.RawPath, RawQuery: final.RawQuery}, nil
	}
	base, err := try(u)
	if err != nil && !explicit && ctx.Err() == nil && refused(err) {
		log("!https refused; using http")
		u.Scheme = "http"
		base, err = try(u)
	}
	if err != nil {
		var cv *tls.CertificateVerificationError
		var hc *hostChangeError
		switch {
		case errors.As(err, &hc):
			return nil, hc
		case errors.As(err, &cv):
			return nil, fmt.Errorf("can't reach %s: invalid TLS certificate (rerun with --insecure to check it anyway)", u.Host)
		}
		return nil, fmt.Errorf("can't reach %s: %w", u.Host, err)
	}
	if base.Path == "" {
		base.Path = "/"
	}
	if base.Scheme != u.Scheme || !strings.EqualFold(base.Host, u.Host) {
		log("!moved to " + base.String())
	}
	return base, nil
}

// hostChangeError is a target that redirects to another site.
type hostChangeError struct{ from, to *url.URL }

func (e *hostChangeError) Error() string {
	return fmt.Sprintf("%s redirects to %s, which is another site; check that address instead if it's the one you meant", e.from, e.to)
}

// refused reports whether err is a refused connection: nothing listens on
// the port. Windows words it differently and has its own error number,
// so the message is matched too.
func refused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connection refused") || strings.Contains(s, "actively refused")
}

// headerTransport adds the run's extra headers to requests to the site
// being checked. Others (a CDN, a third-party script, a link to another
// site, a redirect away) get none: the headers often carry a staging
// cookie or an Authorization token.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
	site    *url.URL
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if len(t.headers) == 0 || !check.SameSite(t.site, r.URL) {
		return t.base.RoundTrip(r)
	}
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

// httpClient is the client for requests outside the browser; it adds
// the run's headers to requests to site only.
func httpClient(opts Options, site *url.URL) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if opts.Proxy != nil {
		tr.Proxy = http.ProxyURL(opts.Proxy)
	}
	if opts.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the user asked for --insecure
	}
	return &http.Client{Transport: headerTransport{tr, opts.Headers, site}, Timeout: time.Duration(opts.Config.Timeout) * time.Second}
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

// tryLogin logs in within a throwaway private session.
func tryLogin(ctx context.Context, b *browser.Browser, login func(context.Context, *rod.Browser) error) error {
	sess, err := b.Session()
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	return login(ctx, sess)
}
