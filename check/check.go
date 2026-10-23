// Package check defines how Soundcheck's modules plug into a run. A
// module implements any of the hook interfaces below; the runner calls
// each hook at its point in a page visit or at the end of the run.
package check

import (
	"context"
	"net/http"
	"net/url"
	"sync"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/config"
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

// Comparer reports differences between two runs beyond new and fixed
// issues: visual changes, slowdowns, lost content.
type Comparer interface {
	Compare(ctx context.Context, cmp *CompareContext) []model.Change
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

	mu sync.Mutex
}

// Lock serializes access to Run from hooks running in parallel.
func (c *Context) Lock()   { c.mu.Lock() }
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
