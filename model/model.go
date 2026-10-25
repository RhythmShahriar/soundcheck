// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package model holds what Soundcheck records about a site: one Run per
// check of the site, a Page per visited URL and the Issues the checks
// find. Runs are stored as JSON (schema rhythm-soundcheck/v1) so a later
// run can be compared with them.
package model

import (
	"crypto/sha1" //nolint:gosec // short stable IDs for file names, not security
	"encoding/hex"
	"strings"
	"sync"
	"time"

	echomodel "github.com/RhythmShahriar/echo/model"
)

// Schema identifies the JSON layout of a stored run.
const Schema = "rhythm-soundcheck/v1"

// Level says how bad an issue is. In a comparison, a new error is
// reported as BROKE and a new warning or notice as NEW.
type Level string

// The issue levels, worst first.
const (
	Error   Level = "error"   // something no longer works: a 5xx page, a fatal error, a form that fails
	Warning Level = "warning" // a problem visitors may hit: a JS error, a PHP warning, a dead click
	Notice  Level = "notice"  // worth knowing: a deprecation, a missing lazy-load
)

// Rank orders levels, most serious first.
func (l Level) Rank() int {
	switch l {
	case Error:
		return 0
	case Warning:
		return 1
	}
	return 2
}

// Issue is one problem found on a page or across the site.
type Issue struct {
	Check  string   `json:"check"` // the module and rule, e.g. "health/php-error"
	Level  Level    `json:"level"`
	Title  string   `json:"title"`            // short, grouped across pages: "PHP Warning: Undefined array key \"id\""
	Detail string   `json:"detail,omitempty"` // page-specific evidence
	Source string   `json:"source,omitempty"` // where it comes from: "themes/x/functions.php:212", a script URL, a plugin slug
	Fix    string   `json:"fix,omitempty"`
	Key    string   `json:"key"`             // stable identity used to compare runs; see Identify
	Pages  []string `json:"pages,omitempty"` // set on site-level issues
	// FromSite says a site-wide check (check.SiteChecker) put the issue
	// on its page, such as the page cache tests, which fetch the page
	// again outside the browser. The runner sets it. A re-visit of the
	// page doesn't run those checks, so compare.Confirm can't judge the
	// issue by it. It isn't stored: Confirm runs on a fresh comparison.
	FromSite bool `json:"-"`
}

// Identify sets the issue's comparison key from its check, title and
// source, so the same problem on the same page matches across runs even
// when details (timestamps, line noise) differ. A reworded title keys on
// the wording it was released with (see KeyTitle).
func (i *Issue) Identify() {
	i.Key = IssueKey(i.Check, i.Title, i.Source)
}

// IssueKey is the comparison key of an issue with check, title and
// source (see Identify). A change knows only those three, so compare's
// confirm pass and accept find the issues a change comes from by it.
// A reworded title keys on its released wording (see KeyTitle).
//
// An accessibility issue (a11y/<rule>) is keyed by its check and source
// alone: its title is axe-core's help text for the rule, which a new
// axe release can reword without the violation being new.
func IssueKey(check, title, source string) string {
	if strings.HasPrefix(check, "a11y/") {
		return check + "|" + source
	}
	return check + "|" + KeyTitle(title) + "|" + source
}

// keyTitles maps a reworded issue title to the one it was released with.
// Snapshots store issue keys and accepted change IDs, both built from the
// title, so a new wording alone would turn every stored issue into a
// FIXED one plus a NEW one and break accepted IDs. Keying on the old
// wording keeps old snapshots comparing; only the displayed text changes.
var keyTitles = map[string]string{
	"Form says sent but no message went out": "Form says sent, but no message went out", // style:ignore: the v1.2.0 title, kept for keys
}

// KeyTitle is the title an issue's key and its change's ID are built
// from: the released wording of a reworded title, else title itself.
func KeyTitle(title string) string {
	if old, ok := keyTitles[title]; ok {
		return old
	}
	return title
}
}

// Request is one network request the page made that went wrong, or that
// a check cares about.
type Request struct {
	URL    string `json:"url"`
	Method string `json:"method,omitempty"`
	Type   string `json:"type,omitempty"` // Document, Script, XHR, Fetch, Image…
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"` // net::ERR_… when it never got a response
}

// Resource is a loaded subresource, for speed and cache checks.
type Resource struct {
	URL      string `json:"url"`
	Type     string `json:"type"`
	Status   int    `json:"status"`
	Bytes    int64  `json:"bytes"`              // transferred (encoded) size
	Protocol string `json:"protocol,omitempty"` // h2, http/1.1…
	Encoding string `json:"encoding,omitempty"` // content-encoding
	Cache    string `json:"cache,omitempty"`    // cache-control
	Mime     string `json:"mime,omitempty"`
	Unused   int64  `json:"unused,omitempty"` // bytes of CSS/JS never used, when coverage ran
}

// Metrics are lab timings from the visit.
type Metrics struct {
	TTFB     float64 `json:"ttfb_ms"`
	FCP      float64 `json:"fcp_ms,omitempty"`
	LCP      float64 `json:"lcp_ms,omitempty"`
	CLS      float64 `json:"cls"`
	TBT      float64 `json:"tbt_ms"`
	Load     float64 `json:"load_ms"`
	Bytes    int64   `json:"bytes"`
	Requests int     `json:"requests"`
}

// Shot is a stored screenshot.
type Shot struct {
	File   string `json:"file"`             // relative to the run directory
	Second string `json:"second,omitempty"` // a second capture moments later, to learn dynamic regions
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Dynamic lists regions that changed between two captures of the same
	// page (sliders, clocks, ads), so comparisons ignore them.
	Dynamic []Rect `json:"dynamic,omitempty"`
	// Masked lists regions hidden by ignore selectors.
	Masked []Rect `json:"masked,omitempty"`
}

// Rect is an area of a screenshot, in pixels from its top left.
type Rect struct{ X, Y, W, H int }

// Interaction is one element Soundcheck clicked or submitted.
type Interaction struct {
	Kind     string `json:"kind"`     // form (inspected), click or submit
	Selector string `json:"selector"` // stable CSS selector
	Label    string `json:"label"`    // visible text or aria-label
	Outcome  string `json:"outcome"`  // ok, dead, error, validation, success, failed, skipped
	Detail   string `json:"detail,omitempty"`
}

// Page is everything recorded about one URL.
type Page struct {
	ID         string            `json:"id"`  // stable, from the URL
	URL        string            `json:"url"` // as discovered
	Path       string            `json:"path"`
	Group      string            `json:"group,omitempty"`
	FinalURL   string            `json:"final_url,omitempty"`
	Status     int               `json:"status"`
	Redirects  []string          `json:"redirects,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"` // main document, lowercased names
	Title      string            `json:"title,omitempty"`
	Robots     string            `json:"robots,omitempty"` // meta robots + X-Robots-Tag
	TextHash   string            `json:"text_hash,omitempty"`
	TextLength int               `json:"text_length"`
	Console    []string          `json:"console,omitempty"` // console errors and uncaught exceptions
	Failed     []Request         `json:"failed,omitempty"`
	Resources  []Resource        `json:"resources,omitempty"`
	Links      []string          `json:"links,omitempty"` // same-site links in the rendered page
	Metrics    Metrics           `json:"metrics"`
	Shots      map[string]Shot   `json:"shots,omitempty"` // by viewport: desktop, mobile
	Interact   []Interaction     `json:"interactions,omitempty"`
	Cache      map[string]string `json:"cache,omitempty"` // cache facts a module recorded (hit, age…)
	Issues     []Issue           `json:"issues,omitempty"`
	Error      string            `json:"error,omitempty"` // the page couldn't be visited at all
	Duration   float64           `json:"duration_ms"`

	// Echo lists the messages each successful form submit on the page
	// made the site send, when Echo ran (the echo module).
	Echo []EchoAction `json:"echo,omitempty"`

	// Text is the rendered page's visible text, for checks during this
	// run. It isn't stored: snapshots keep TextHash and TextLength.
	Text string `json:"-"`
}

// CanceledError is a page's Error when the run was stopped (Ctrl+C, a
// signal) before its visit finished. The engine records exactly this
// text, the message of context.Canceled.
const CanceledError = "context canceled"

// Canceled reports whether the page's visit was cut short because the run
// was stopped. Such a page wasn't checked: what it recorded says nothing
// about the site.
func (p *Page) Canceled() bool {
	return p.Error == CanceledError || strings.HasSuffix(p.Error, ": "+CanceledError)
}

// EchoAction is one thing Soundcheck did (a form submit, a flow) and the
// messages Echo saw the site send because of it.
type EchoAction struct {
	Action   string              `json:"action"` // stable across runs: "form:/contact/#form.wpcf7-form", "flow:Checkout"
	Label    string              `json:"label"`  // the form's or flow's name
	Kind     string              `json:"kind"`   // form or flow
	Page     string              `json:"page"`   // the page submitted on, or the flow's start
	Messages []echomodel.Message `json:"messages"`
}

// Run is one check of a site.
type Run struct {
	Schema   string         `json:"schema"`
	Kind     string         `json:"kind"` // always KindRun; see Complete
	Tool     string         `json:"tool"`
	Version  string         `json:"version"`
	Target   string         `json:"target"`
	Name     string         `json:"name,omitempty"` // snapshot name
	Started  time.Time      `json:"started"`
	Duration float64        `json:"duration_ms"`
	Options  Options        `json:"options"`
	Pages    []Page         `json:"pages"`
	Issues   []Issue        `json:"issues,omitempty"`  // site-level issues
	Skipped  map[string]int `json:"skipped,omitempty"` // discovery: URLs left out, by reason
	Warnings []string       `json:"warnings"`
	// Cache is the cache module's site summary (hit rate, layers, cached
	// and uncached TTFB); see checks/cache for the keys.
	Cache map[string]string `json:"cache,omitempty"`
	// Echo lists the messages each flow made the site send (form submits
	// are on their pages).
	Echo []EchoAction `json:"echo,omitempty"`
	// Interrupted says the run was stopped before it finished, so it
	// holds partial results; see Page.Canceled.
	Interrupted bool `json:"interrupted,omitempty"`

	// Summary and Groups are filled for JSON output (report.JSON), not
	// stored in run.json: the counts and grouped list the terminal shows,
	// so scripts don't have to group issues themselves.
	Summary *RunSummary `json:"summary,omitempty"`
	Groups  []Group     `json:"groups,omitempty"`

	index *pageIndex // see Page
}

// Options records what a run did, so a comparison can warn when two runs
// weren't made the same way.
type Options struct {
	Viewports []string `json:"viewports"`
	Levels    []string `json:"levels"` // inspect, click, submit, flows
	Modules   []string `json:"modules"`
	Limit     int      `json:"limit"`
	LoggedIn  bool     `json:"logged_in,omitempty"`
	Echo      bool     `json:"echo,omitempty"`      // messages were recorded through Echo
	EchoMode  string   `json:"echo_mode,omitempty"` // Echo's mode: seal or observe
	EchoURL   string   `json:"echo_url,omitempty"`  // where Echo ran, for its live view
	// Engine is the browser engine pages were captured in: firefox or
	// webkit. Empty is Chrome, which every run before engines had.
	Engine string `json:"engine,omitempty"`
}

// EngineName is the run's browser engine, with Chrome for empty.
func (o Options) EngineName() string {
	if o.Engine == "" {
		return "chrome"
	}
	return o.Engine
}

// PageID returns a short stable ID for a URL, used in file names.
func PageID(url string) string {
	sum := sha1.Sum([]byte(url)) //nolint:gosec // not for security
	return hex.EncodeToString(sum[:5])
}

// Page returns the page with the given path, or nil. When two pages
// share a path, it returns the first.
//
// Lookups go through a map built on first use, so comparing two big runs
// stays linear. The map is rebuilt when Pages grows, shrinks or is
// replaced and when a page it points at has moved; changing a page's
// Path in place without that isn't seen. Concurrent readers are safe;
// writers to Pages need their own lock, as before.
func (r *Run) Page(path string) *Page {
	// The fast path only reads, so parallel comparers don't wait on
	// each other once the index is built.
	indexMu.RLock()
	idx := r.index
	if idx.valid(r.Pages) {
		i, ok := idx.byPath[path]
		if !ok {
			indexMu.RUnlock()
			return nil
		}
		if r.Pages[i].Path == path {
			indexMu.RUnlock()
			return &r.Pages[i]
		}
	}
	indexMu.RUnlock()

	indexMu.Lock()
	defer indexMu.Unlock()
	idx = r.index
	if !idx.valid(r.Pages) {
		idx = newPageIndex(r.Pages)
		r.index = idx
	}
	i, ok := idx.byPath[path]
	if ok && r.Pages[i].Path != path { // reordered in place
		idx = newPageIndex(r.Pages)
		r.index = idx
		i, ok = idx.byPath[path]
	}
	if !ok {
		return nil
	}
	return &r.Pages[i]
}

// indexMu guards every run's page index. One lock for all runs keeps Run
// free of a mutex, so runs can still be copied by value (a copy builds
// its own index on first use).
var indexMu sync.RWMutex

// pageIndex maps paths to positions in the Pages slice it was built from.
type pageIndex struct {
	base   *Page // first element, to notice a replaced slice
	n      int
	byPath map[string]int
}

func newPageIndex(pages []Page) *pageIndex {
	idx := &pageIndex{base: first(pages), n: len(pages), byPath: make(map[string]int, len(pages))}
	for i := range pages {
		if _, ok := idx.byPath[pages[i].Path]; !ok {
			idx.byPath[pages[i].Path] = i
		}
	}
	return idx
}

func (idx *pageIndex) valid(pages []Page) bool {
	return idx != nil && idx.n == len(pages) && idx.base == first(pages)
}

func first(pages []Page) *Page {
	if len(pages) == 0 {
		return nil
	}
	return &pages[0]
}

// EchoActions returns every recorded action: form submits page by page,
// then flows.
func (r *Run) EchoActions() []EchoAction {
	var out []EchoAction
	for _, p := range r.Pages {
		out = append(out, p.Echo...)
	}
	return append(out, r.Echo...)
}

// AllIssues returns page issues (with the page path in Pages) followed by
// site-level issues.
func (r *Run) AllIssues() []Issue {
	var out []Issue
	for _, p := range r.Pages {
		for _, i := range p.Issues {
			i.Pages = []string{p.Path}
			out = append(out, i)
		}
	}
	return append(out, r.Issues...)
}

// Change classes in a comparison, most urgent first.
const (
	Broke   = "broke"   // worked in the snapshot, fails now
	New     = "new"     // a problem that wasn't there before
	Changed = "changed" // looks or reads differently
	Slower  = "slower"  // measurably slower
	Fixed   = "fixed"   // a problem from the snapshot is gone
	// Unstable is a BROKE or NEW change that didn't show again when its
	// page was checked a second time (see compare.Confirm). It's
	// reported last and never fails a build: noise isn't a regression.
	Unstable = "unstable"
)

// Classes lists change classes in report order.
var Classes = []string{Broke, New, Changed, Slower, Fixed, Unstable}

// Change is one difference between a snapshot and the current run.
type Change struct {
	// ID names the change in this comparison and in the next one when
	// nothing about it changed; see ChangeID.
	ID     string            `json:"id"`
	Class  string            `json:"class"`
	Check  string            `json:"check"`
	Title  string            `json:"title"`
	Detail string            `json:"detail,omitempty"`
	Source string            `json:"source,omitempty"`
	Fix    string            `json:"fix,omitempty"`
	Pages  []string          `json:"pages"`
	Before string            `json:"before,omitempty"` // e.g. "200", "1.2 s"
	After  string            `json:"after,omitempty"`
	Score  float64           `json:"score,omitempty"` // ranking within the class: % changed, % slower
	Files  map[string]string `json:"files,omitempty"` // e.g. before/after/diff images, relative to the comparison
	// Viewport is the viewport a visual change was seen at, such as
	// "desktop", so accepting it replaces that screenshot alone. Other
	// changes leave it empty.
	Viewport string `json:"viewport,omitempty"`
	// Reason says why an ignored change was ignored: the reason its
	// ignore rule gives. Changes that count leave it empty.
	Reason string `json:"reason,omitempty"`
	// FromPageIssue says the change is a page issue that appeared or went
	// away (compare.Issues sets it), so checking the page again tells
	// whether it's real. It isn't stored: Confirm runs on a fresh
	// comparison.
	FromPageIssue bool `json:"-"`
	// Key is the issue key of a change compare.Issues grouped from
	// issues (see Issue.Identify), so its ID stays the same when the
	// issue shows up on more pages. Other changes leave it empty. It
	// isn't stored: IDs are given before a comparison is written.
	Key string `json:"-"`
}

// Comparison is the result of `compare`: what changed between two runs.
type Comparison struct {
	Schema  string   `json:"schema"`
	Kind    string   `json:"kind"` // always KindComparison; see Complete
	Tool    string   `json:"tool"`
	Version string   `json:"version"`
	Before  RunRef   `json:"before"`
	After   RunRef   `json:"after"`
	Changes []Change `json:"changes"`
	// Ignored holds the changes ignore rules matched. They're kept so a
	// report can say what was left out; they never pass or fail a build.
	Ignored  []Change  `json:"ignored"`
	Pages    int       `json:"pages"`
	Warnings []string  `json:"warnings"`
	Summary  Summary   `json:"summary"`
	Created  time.Time `json:"created"`
	// Interrupted says the current run was stopped before it finished:
	// pages it didn't finish weren't compared, so the comparison is
	// partial and must not pass or fail a build.
	Interrupted bool `json:"interrupted,omitempty"`
}

// RunRef points at a stored run.
type RunRef struct {
	Target  string    `json:"target"`
	Name    string    `json:"name,omitempty"`
	Dir     string    `json:"dir"`
	Started time.Time `json:"started"`
}

// Count returns how many changes are in a class.
func (c *Comparison) Count(class string) int {
	n := 0
	for _, ch := range c.Changes {
		if ch.Class == class {
			n++
		}
	}
	return n
}
