// Package model holds what Soundcheck records about a site: one Run per
// check of the site, a Page per visited URL, and the Issues the checks
// find. Runs are stored as JSON (schema rhythm-soundcheck/v1) so a later
// run can be compared with them.
package model

import (
	"crypto/sha1" //nolint:gosec // short stable IDs for file names, not security
	"encoding/hex"
	"time"

	echomodel "github.com/RhythmShahriar/echo/model"
)

// Schema identifies the JSON layout of a stored run.
const Schema = "rhythm-soundcheck/v1"

// Level says how bad an issue is. In a comparison, a new error is
// reported as BROKE and a new warning or notice as NEW.
type Level string

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
}

// Identify sets the issue's comparison key from its check, title and
// source, so the same problem on the same page matches across runs even
// when details (timestamps, line noise) differ.
func (i *Issue) Identify() {
	i.Key = i.Check + "|" + i.Title + "|" + i.Source
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
	Warnings []string       `json:"warnings,omitempty"`
	// Cache is the cache module's site summary (hit rate, layers, cached
	// and uncached TTFB); see checks/cache for the keys.
	Cache map[string]string `json:"cache,omitempty"`
	// Echo lists the messages each flow made the site send (form submits
	// are on their pages).
	Echo []EchoAction `json:"echo,omitempty"`
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
}

// PageID returns a short stable ID for a URL, used in file names.
func PageID(url string) string {
	sum := sha1.Sum([]byte(url)) //nolint:gosec // not for security
	return hex.EncodeToString(sum[:5])
}

// Page returns the page with the given path, or nil.
func (r *Run) Page(path string) *Page {
	for i := range r.Pages {
		if r.Pages[i].Path == path {
			return &r.Pages[i]
		}
	}
	return nil
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
)

// Classes lists change classes in report order.
var Classes = []string{Broke, New, Changed, Slower, Fixed}

// Change is one difference between a snapshot and the current run.
type Change struct {
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
}

// Comparison is the result of `compare`: what changed between two runs.
type Comparison struct {
	Schema   string    `json:"schema"`
	Tool     string    `json:"tool"`
	Version  string    `json:"version"`
	Before   RunRef    `json:"before"`
	After    RunRef    `json:"after"`
	Changes  []Change  `json:"changes"`
	Pages    int       `json:"pages"`
	Warnings []string  `json:"warnings,omitempty"`
	Created  time.Time `json:"created"`
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
