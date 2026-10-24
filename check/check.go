// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package check defines how Soundcheck's modules plug into a run. A
// module implements any of the hook interfaces below; the runner calls
// each hook at its point in a page visit or at the end of the run. A hook
// that panics doesn't end the run: the runner calls each one through
// Safe, which turns the panic into a warning.
package check

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Module is a named group of checks.
type Module interface {
	Name() string
}

// Preparer runs on a fresh tab before it navigates, e.g. to start JS
// coverage or install observers.
type Preparer interface {
	Prepare(ctx context.Context, cc *Context, tab *rod.Page) error
}

// Live runs while the page is loaded at the desktop viewport, after
// capture and before the mobile pass. It may interact with the page (the
// mobile pass reloads it). It records results on p.
type Live interface {
	Live(ctx context.Context, cc *Context, tab *rod.Page, p *model.Page)
}

// PageChecker inspects a recorded page without the browser.
type PageChecker interface {
	CheckPage(cc *Context, p *model.Page)
}

// SiteChecker runs once after every page is visited, e.g. to test links
// across the site or compare logged-in and anonymous views.
type SiteChecker interface {
	CheckSite(ctx context.Context, cc *Context)
}

// NeedsText is a module that reads Page.Text after the page's
// PageCheckers ran, e.g. in CheckSite. The text can be 200,000
// characters a page, so without such a module the runner drops it from
// each page as soon as its PageCheckers are done.
type NeedsText interface {
	NeedsText()
}

// Comparer reports differences between two runs beyond new and fixed
// issues: visual changes, slowdowns, lost content.
type Comparer interface {
	Compare(ctx context.Context, cmp *CompareContext) []model.Change
}

// IssueOwner is a Comparer that reports every change in its own issues,
// e.g. with noise floors for lab measurements that vary between runs. The
// generic new/fixed issue diff skips checks named "<module>/…" for it.
type IssueOwner interface {
	Comparer
	OwnsIssues()
}

// ChangeFilter tidies the full list of changes after every comparer ran,
// e.g. dropping a generic NEW issue that one of its own changes already
// explains.
type ChangeFilter interface {
	FilterChanges(changes []model.Change) []model.Change
}

// Context is what a module can use during a run.
type Context struct {
	Config     config.Config
	Base       *url.URL     // the site being checked
	Run        *model.Run   // pages are appended as they're visited
	Dir        string       // where this run's files are stored
	Snapshot   bool         // this run is a snapshot (learn dynamic regions…)
	HTTP       *http.Client // for requests outside the browser; carries the run's headers, proxy and TLS settings
	UserAgent  string
	Hosts      *ratelimit.Hosts
	Browser    *rod.Browser                         // the anonymous session
	NewSession func() (*rod.Browser, error)         // a separate private session (e.g. to log in)
	Save       func(name string, data []byte) error // stores a file in the run directory
	// Login logs the test account in within sess (a session from
	// NewSession), using SOUNDCHECK_USER/SOUNDCHECK_PASSWORD. Nil when no
	// credentials are set; modules that need a logged-in user then skip.
	Login func(ctx context.Context, sess *rod.Browser) error
	// Echo records the messages form submits and flows make the site
	// send. Nil unless Echo is configured and running.
	Echo *echolink.Link
	// OnProgress, set by the runner, shows how far a long step has got.
	// Modules call Progress rather than this.
	OnProgress func(label string, done, total int)
	// Ctx is the run's context, canceled when the run stops (Ctrl-C).
	// Hooks without a context of their own, such as CheckPage, read it
	// through RunContext so the work they start stops with the run.
	Ctx context.Context

	mu sync.Mutex
}

// RunContext is the run's context, or context.Background() when the
// Context was made without one (in tests).
func (c *Context) RunContext() context.Context {
	if c.Ctx == nil {
		return context.Background()
	}
	return c.Ctx
}

// Progress reports that a long step, such as a SiteChecker probing every
// link, has done of total items. label names the step ("Checking
// links"). It does nothing when the run shows no progress.
func (c *Context) Progress(label string, done, total int) {
	if c.OnProgress != nil {
		c.OnProgress(label, done, total)
	}
}

// Lock serializes access to Run from hooks running in parallel. Hold it
// only around the Run fields a hook reads or changes and release it with
// a deferred Unlock when anything in between could panic: Safe turns the
// panic into a warning through Warn, which takes the same lock, so a lock
// left held would stop the whole run instead.
func (c *Context) Lock() { c.mu.Lock() }

// Unlock releases Lock.
func (c *Context) Unlock() { c.mu.Unlock() }

// AddIssue appends a site-level issue.
func (c *Context) AddIssue(i model.Issue) {
	i.Identify()
	c.Lock()
	c.Run.Issues = append(c.Run.Issues, i)
	c.Unlock()
}

// Warn records a warning shown with the results.
func (c *Context) Warn(msg string) {
	c.Lock()
	c.Run.Warnings = append(c.Run.Warnings, msg)
	c.Unlock()
}

// Get fetches a URL outside the browser, at the run's polite pace.
func (c *Context) Get(ctx context.Context, method, raw string) (*http.Response, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if err := c.Hosts.For(u.Host).Wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, raw, nil)
	if err != nil {
		return nil, err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	return c.HTTP.Do(req)
}

// CompareContext is what a Comparer gets.
type CompareContext struct {
	Config        config.Config
	Before, After *model.Run
	BeforeDir     string // directories holding each run's files
	AfterDir      string
	Dir           string                               // where the comparison's own files go
	Save          func(name string, data []byte) error // stores a file in Dir
	// WarnFunc receives what modules pass to Warn. compare sets it, so a
	// Comparer's warnings land in the comparison's warnings.
	WarnFunc func(string)
}

// Warn adds a warning to the comparison, such as screenshots that
// couldn't be read. It is safe for concurrent use when WarnFunc is.
func (c *CompareContext) Warn(msg string) {
	if c.WarnFunc != nil {
		c.WarnFunc(msg)
	}
}

// Pairs returns pages present in both runs, matched by path.
func (c *CompareContext) Pairs() [][2]*model.Page {
	var out [][2]*model.Page
	for i := range c.After.Pages {
		a := &c.After.Pages[i]
		if b := c.Before.Page(a.Path); b != nil {
			out = append(out, [2]*model.Page{b, a})
		}
	}
	return out
}

// Page adds an issue to p, setting its key.
func Page(p *model.Page, i model.Issue) {
	i.Identify()
	p.Issues = append(p.Issues, i)
}

// SiteRoot is where the site is installed, for endpoints like
// wp-login.php and wp-json/: Base may be a page (a run started at
// /shop/), so the root comes from the WordPress assets the pages loaded
// (everything before /wp-content/ or /wp-includes/), else the host's root.
func (c *Context) SiteRoot() *url.URL {
	root := &url.URL{Scheme: c.Base.Scheme, Host: c.Base.Host, Path: "/"}
	c.Lock()
	defer c.Unlock()
	if c.Run == nil {
		return root
	}
	for _, p := range c.Run.Pages {
		for _, r := range p.Resources {
			u, err := url.Parse(r.URL)
			if err != nil || !strings.EqualFold(u.Host, c.Base.Host) {
				continue
			}
			for _, dir := range []string{"/wp-content/", "/wp-includes/"} {
				if i := strings.Index(u.Path, dir); i >= 0 {
					root.Path = u.Path[:i+1]
					return root
				}
			}
		}
	}
	return root
}
