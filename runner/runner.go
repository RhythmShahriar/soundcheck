// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package runner checks a whole site: it finds the pages, visits each in
// a pool of browser tabs at a polite pace, runs every module's hooks
// and returns the recorded run.
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
	"github.com/RhythmShahriar/rhythm/kit/httpx"
	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/capture/webdriver"
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
	// Engine is the browser engine pages are captured in; empty is
	// Chrome. Browser.Bin is then that engine's binary (Firefox, or
	// WebKit's driver) and the rest of Browser applies as it does to
	// Chrome. Modules that need Chrome (check.ChromeOnly) and the login
	// are skipped with a warning in another engine.
	Engine capture.Engine
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
	// WCAG is the WCAG version the accessibility checks use (2.0, 2.1 or
	// 2.2), recorded in the run's options for the a11y module and for a
	// comparison. Empty records none, which reads as 2.0.
	WCAG string
	// EAA records that the run was made with --eaa.
	EAA bool
	// Keyboard records the keyboard walk's limits, when it runs.
	Keyboard *model.KeyboardOptions

	// Done resumes an interrupted run: these pages were finished before
	// and are kept as they are, not visited again. Their links are added
	// to the pages to find. The run's pages start with them.
	Done []*model.Page
	// OnPage is called with each page as it finishes (not the Done ones,
	// nor one the cancel cut short), so it can be stored right away and a
	// later run can resume after it. Calls are serialized with Hooks.
	OnPage func(p *model.Page)

	revisit bool           // set by Revisit: no site-wide checks
	driver  capture.Driver // set by Run for an engine other than Chrome
}

// Hooks report progress. Calls are serialized.
type Hooks struct {
	Discovered func(pages int, skipped map[string]int)
	// Skipped gets the final skip counts once every page is visited, when
	// links found on the pages were left out after Discovered (robots.txt
	// unavailable, another site...), so the terminal shows them too.
	Skipped func(skipped map[string]int)
	Page    func(p *model.Page, done, total int)
	// Log gets progress notes; a leading "!" marks one to show even
	// without --verbose and a leading "~" a status line that -q hides
	// (see report.Pretty.Log).
	Log func(msg string)
	// Progress gets a module's progress through a long step, from
	// check.Context.Progress.
	Progress func(label string, done, total int)
}

// Run checks the site. When ctx is canceled it starts no more pages and
// returns the run so far with ctx.Err(). Pages it finished are complete;
// a page whose visit the cancel cut short has the Error
// "context canceled" and nothing else (see canceled). With opts.Done it
// resumes such a run, visiting only the pages it didn't finish.
func Run(ctx context.Context, opts Options) (*model.Run, error) {
	start := time.Now()
	cfg := opts.Config
	opts.Modules = check.SortByPhase(opts.Modules)
	target, _, err := httpx.ParseTarget(opts.Target)
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
	if err := capture.HostError(opts.Headers, startURL); err != nil {
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
			Limit: cfg.Pages.Limit, WCAG: opts.WCAG, EAA: opts.EAA, Keyboard: opts.Keyboard},
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
			addLinks(frontier, p.Links, 1)
		}
	}
	pages := slices.DeleteFunc(frontier.Pages(), func(p crawl.Page) bool { return finished[p.URL] })
	if len(pages) == 0 && len(opts.Done) == 0 {
		return nil, fmt.Errorf("found no pages to check at %s", startURL)
	}
	discovered := frontier.Skipped()
	if opts.Hooks.Discovered != nil {
		emit(func() { opts.Hooks.Discovered(len(pages)+len(opts.Done), discovered) })
	}

	engine := cmpEngine(opts.Engine)
	if engine != capture.Chrome {
		run.Options.Engine = string(engine)
	}
	hosts := ratelimit.NewHosts(cfg.Rate)
	cc := &check.Context{
		Ctx:    ctx,
		Config: cfg, Base: base, Run: run, Dir: opts.Dir, Snapshot: opts.Snapshot, HTTP: client,
		UserAgent: opts.UserAgent, Hosts: hosts, Save: store.Saver(opts.Dir),
	}
	for _, note := range capture.HeaderNotes(opts.Headers) {
		cc.Warn(note)
	}
	// The kit's crawler then admits only the start page and listed pages,
	// as RFC 9309 asks; a snapshot of one page must not pass for the site.
	if why := frontier.RobotsUnavailable(); why != "" {
		cc.Warn(fmt.Sprintf("robots.txt couldn't be read (%s), so only the start page and the pages you listed were checked; on your own site, pass --ignore-robots", why))
	}
	var b *browser.Browser
	if engine == capture.Chrome {
		b, err = browser.Launch(ctx, withLog(opts.Browser, log))
		if err != nil {
			return nil, err
		}
		defer b.Close()
		sess, err := b.Session()
		if err != nil {
			return nil, err
		}
		cc.Browser, cc.NewSession = sess, b.Session
	} else {
		d, err := launchDriver(ctx, engine, withLog(opts.Browser, log))
		if err != nil {
			return nil, err
		}
		defer func() { _ = d.Close() }()
		opts.driver = d
		var skippedModules []string
		opts.Modules, skippedModules = chromeFree(opts.Modules, engine, cc.Warn)
		// As with echo below: a comparison mustn't think they ran.
		run.Options.Modules = slices.DeleteFunc(slices.Clone(run.Options.Modules), func(name string) bool {
			return slices.Contains(skippedModules, name)
		})
		if opts.Login != nil {
			cc.Warn(fmt.Sprintf("the login was skipped in %s: logging in needs Chrome", engine))
			opts.Login = nil
		}
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
	// form is one clear warning. The checks that need it skip.
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
				if added := addLinks(frontier, p.Links, pg.Depth+1); len(added) > 0 {
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
			// A page the cancel or the run's deadline cut short isn't
			// finished: a resumed run visits it again.
			if opts.OnPage != nil && !cutShort(p) {
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
	run.Skipped = frontier.Skipped()
	if opts.Hooks.Skipped != nil && !maps.Equal(run.Skipped, discovered) {
		emit(func() { opts.Hooks.Skipped(maps.Clone(run.Skipped)) })
	}

	if ctx.Err() == nil && !opts.revisit {
		// Probing every link can take minutes after the last page.
		if slices.ContainsFunc(opts.Modules, func(m check.Module) bool { _, ok := m.(check.SiteChecker); return ok }) {
			log("~Checking site-wide issues (links, sitemap)...")
		}
		had := make([]int, len(run.Pages))
		for i := range run.Pages {
			had[i] = len(run.Pages[i].Issues)
		}
		for _, m := range opts.Modules {
			if s, ok := m.(check.SiteChecker); ok {
				check.Safe(m.Name(), "CheckSite", cc.Warn, func() { s.CheckSite(ctx, cc) })
			}
		}
		markSiteIssues(run, had)
	}
	run.Duration = float64(time.Since(start).Milliseconds())
	// A cancel after the last page cut the site-wide checks short (or
	// skipped them): the run isn't a full result even though every page
	// finished.
	if ctx.Err() != nil {
		run.Interrupted = true
	}
	return run, ctx.Err()
}

// markSiteIssues marks the page issues the site-wide checks added, those
// past had[i] on page i, as FromSite: a re-visit doesn't run those checks,
// so it can't confirm them.
func markSiteIssues(run *model.Run, had []int) {
	for i := range run.Pages {
		if i >= len(had) {
			break
		}
		for j := had[i]; j < len(run.Pages[i].Issues); j++ {
			run.Pages[i].Issues[j].FromSite = true
		}
	}
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
func addLinks(f *crawl.Frontier, links []string, depth int) []crawl.Page {
	if addHook != nil {
		addHook(false)
	}
	var out []crawl.Page
	for _, l := range links {
		if p, ok := f.AddPage(l, "rendered", depth); ok {
			out = append(out, p)
		}
	}
	if addHook != nil {
		addHook(true)
	}
	return out
}

// addHook, when set by a test, runs before a visit adds its links and
// again after, to make visits interleave there.
var addHook func(added bool)

// canceled is the Error of a page whose visit the run's cancel cut
// short (Ctrl+C). Such a page has no facts, issues or shots. What it
// recorded before the cancel is partial; compared with a snapshot it
// would read as a page that lost its title or went blank.
var canceled = context.Canceled.Error()

// cutShort reports whether the run ending, by a cancel or its deadline,
// cut p's visit short. visit records both as the context's error.
func cutShort(p *model.Page) bool {
	return p.Error == canceled || p.Error == context.DeadlineExceeded.Error()
}

// visit checks one page in its own tab.
func visit(ctx context.Context, cc *check.Context, opts Options, hosts *ratelimit.Hosts, pg crawl.Page) *model.Page {
	cfg := opts.Config
	start := time.Now()
	fail := func(err error) *model.Page {
		// Once the run's context ends the visit's own error ("navigation
		// failed") hides why: the run was canceled or ran out of time.
		msg := err.Error()
		if cerr := ctx.Err(); cerr != nil {
			msg = cerr.Error()
		}
		return &model.Page{ID: model.PageID(pg.URL), URL: pg.URL, Path: pathOf(pg.URL), Group: pg.Group, Error: msg,
			Duration: float64(time.Since(start).Milliseconds())}
	}
	shots, twice := captureNeeds(opts.Modules, opts.Snapshot)
	copts := capture.Options{
		Timeout: time.Duration(cfg.Timeout) * time.Second, Ignore: cfg.Visual.Ignore, Twice: twice,
		UserAgent: opts.UserAgent, Headers: opts.Headers, Site: cc.Base, Engine: cmpEngine(opts.Engine),
	}
	if shots {
		copts.Save = cc.Save
	}
	// tab is Chrome's own tab, for the Preparer and Live hooks; nil in
	// another engine, which runs no such module (see chromeFree).
	var (
		tab  *rod.Page
		ctab check.Tab
		rec  *capture.Recorder
	)
	if opts.driver != nil {
		t, err := opts.driver.NewTab(ctx)
		if err != nil {
			return fail(err)
		}
		defer func() { _ = t.Close() }()
		if rec, err = capture.StartTab(ctx, t, copts); err != nil {
			return fail(err)
		}
		ctab = t
	} else {
		var err error
		if tab, err = browser.NewTab(cc.Browser); err != nil {
			return fail(err)
		}
		defer func() { _ = tab.Close() }()
		if rec, err = capture.Start(ctx, tab, copts); err != nil {
			return fail(err)
		}
		ctab = check.RodTab(tab)
	}
	defer rec.Stop()
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
func captureNeeds(modules []check.Module, snapshot bool) (shots, twice bool) {
	for _, m := range modules {
		if n, ok := m.(check.CaptureNeeds); ok && n.NeedsShots() {
			shots = true
			twice = twice || n.NeedsSecondShot(snapshot)
		}
	}
	return shots, twice
}

// resolve fetches the target once (following redirects) and returns the
// start page: the URL the site really answers on, with the path and query
// it ended up at, so a run started at /q?lang=fr checks that page and one
// redirected to /b checks /b.
//
// Redirects may move to https and between apex and www (logged as "moved
// to ..."); one to another site is an error unless followHost is set. So
// is one from an https target to http: the run's headers would then go
// out in cleartext. A bare hostname may end up on http, by a redirect or
// when https is refused outright. A bad certificate or a timeout is a
// reason to stop, not to downgrade.
func resolve(ctx context.Context, client *http.Client, target, ua string, followHost bool, log func(string)) (*url.URL, error) {
	u, explicit, err := httpx.ParseTarget(target)
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
		if explicit && check.Downgrade(via[0].URL, req.URL) {
			return &downgradeError{from: via[0].URL, to: req.URL}
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
	if err != nil && !explicit && ctx.Err() == nil && (refused(err) || noTLS(err)) {
		log("!https refused; using http")
		u.Scheme = "http"
		base, err = try(u)
	}
	if err != nil {
		var cv *tls.CertificateVerificationError
		var hc *hostChangeError
		var dg *downgradeError
		switch {
		case errors.As(err, &hc):
			return nil, hc
		case errors.As(err, &dg):
			return nil, dg
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

// downgradeError is an https target that redirects to http.
type downgradeError struct{ from, to *url.URL }

func (e *downgradeError) Error() string {
	return fmt.Sprintf("%s redirects to %s, which drops https; check %s instead if you mean to check it over http", e.from, e.to, e.to)
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

// noTLS reports whether err says the port speaks plain http: the
// server answered the TLS handshake with something that isn't TLS. A bad
// certificate is a TLS answer, so it never counts.
func noTLS(err error) bool {
	var rh tls.RecordHeaderError
	if errors.As(err, &rh) || errors.Is(err, http.ErrSchemeMismatch) {
		return true
	}
	return strings.Contains(err.Error(), "first record does not look like a TLS handshake")
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
	if len(t.headers) == 0 || !check.Trusted(t.site, r.URL) {
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

// cmpEngine is e, or Chrome when e is empty.
func cmpEngine(e capture.Engine) capture.Engine {
	if e == "" {
		return capture.Chrome
	}
	return e
}

// launchDriver starts the browser of an engine other than Chrome.
func launchDriver(ctx context.Context, engine capture.Engine, o browser.Options) (capture.Driver, error) {
	wo := webdriver.Options{Bin: o.Bin, Proxy: o.Proxy, Insecure: o.Insecure, Visible: o.Visible, Log: o.Log}
	switch engine {
	case capture.Firefox:
		return webdriver.LaunchFirefox(ctx, wo)
	case capture.WebKit:
		return webdriver.LaunchWebKit(ctx, wo)
	}
	return nil, fmt.Errorf("unknown browser engine %q", engine)
}

// chromeFree drops the modules that run only in Chrome
// (check.ChromeOnly) from a run in another engine, with a warning
// naming each. It returns the rest and the names it dropped.
func chromeFree(modules []check.Module, engine capture.Engine, warn func(string)) (kept []check.Module, skipped []string) {
	for _, m := range modules {
		if check.ChromeOnly(m) {
			warn(fmt.Sprintf("%s was skipped in %s: it needs Chrome", m.Name(), engine))
			skipped = append(skipped, m.Name())
			continue
		}
		kept = append(kept, m)
	}
	return kept, skipped
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
