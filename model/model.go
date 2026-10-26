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
	// WCAG lists the WCAG success criteria the issue fails, such as
	// "2.1.2", for the run's WCAG version. Checks that map to none leave
	// it empty.
	WCAG []string `json:"wcag,omitempty"`
	// EN301549 lists the matching EN 301 549 clauses: "9." plus each
	// WCAG criterion.
	EN301549 []string `json:"en301549,omitempty"`
	// Impact is how much the problem stops a disabled visitor: critical,
	// serious, moderate or minor, as axe-core rates its rules.
	Impact string `json:"impact,omitempty"`
	// FocusPath lists the selectors of the last keyboard focus stops
	// before the problem, for keyboard issues. Selectors only: never
	// names or text.
	FocusPath []string `json:"focus_path,omitempty"`
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
// axe release can reword without the violation being new. A keyboard
// issue (keyboard/<check>) is keyed the same way, so a clearer title in
// a later release doesn't turn a known problem into a new one.
func IssueKey(check, title, source string) string {
	if strings.HasPrefix(check, "a11y/") || strings.HasPrefix(check, "keyboard/") {
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

	// Audits says, by module (a11y, keyboard), whether the module's audit
	// of the page finished. A comparison doesn't call an issue fixed or
	// new on a page whose audit didn't finish on both sides.
	Audits map[string]Audit `json:"audits,omitempty"`
	// Keyboard summarizes the keyboard walk of the page, when the
	// keyboard module ran.
	Keyboard *KeyboardSummary `json:"keyboard,omitempty"`

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
	// KeyFlows records the key flows (flows run by keyboard only) the run
	// ran and the ones it skipped, when the keyboard module ran.
	KeyFlows *KeyFlows `json:"key_flows,omitempty"`

	// EAA is the accessibility summary of an --eaa run, filled for output
	// like Summary and not stored in run.json.
	EAA *EAASummary `json:"eaa,omitempty"`

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
	// EAA says the run was made with --eaa: the accessibility and
	// keyboard modules ran for an accessibility statement draft.
	EAA bool `json:"eaa,omitempty"`
	// WCAG is the WCAG version the accessibility checks used: 2.0, 2.1
	// or 2.2. Empty is a run made before 1.3, which used the 2.0 rules.
	WCAG string `json:"wcag,omitempty"`
	// A11y records the axe-core rules the run applied, so a comparison
	// can tell a rule the snapshot never ran from a new problem.
	A11y *A11yRules `json:"a11y,omitempty"`
	// Keyboard records the keyboard walk's limits.
	Keyboard *KeyboardOptions `json:"keyboard,omitempty"`
}

// A11yRules is the axe-core build and rule set an accessibility audit
// used.
type A11yRules struct {
	Axe         string   `json:"axe"`          // axe-core's version
	AxeSHA256   string   `json:"axe_sha256"`   // of the vendored axe.min.js
	Tags        []string `json:"tags"`         // the axe tags run, such as wcag21aa
	Rules       []string `json:"rules"`        // every rule id run, sorted
	MaxElements int      `json:"max_elements"` // elements kept per rule per page
}

// KeyboardOptions are the keyboard walk's limits, from the keyboard
// block of the config file.
type KeyboardOptions struct {
	MaxTabs   int    `json:"max_tabs"`   // Tab presses per page before the walk stops
	Widgets   int    `json:"widgets"`    // menus, dialogs and disclosures tested per page
	TrapAfter int    `json:"trap_after"` // presses on one element or region that count as a trap
	SkipLink  string `json:"skip_link"`  // expected, required or off
}

// Statuses of a module's audit of a page (Audit.Status).
const (
	AuditOK      = "ok"      // finished
	AuditCapped  = "capped"  // finished at a limit such as max_tabs
	AuditTimeout = "timeout" // ran out of time
	AuditError   = "error"   // failed
	AuditSkipped = "skipped" // not run on this page
)

// Audit is whether a module's audit of a page finished.
type Audit struct {
	Status string   `json:"status"`           // one of the Audit* statuses
	Reason string   `json:"reason,omitempty"` // why it didn't finish, or what capped it
	Frames []string `json:"frames,omitempty"` // frames audited besides the page, by host
	// CappedRules counts, by rule, the elements found beyond the ones
	// kept, for rules that hit the per-rule limit on this page.
	CappedRules map[string]int `json:"capped_rules,omitempty"`
}

// Finished reports whether the audit ran to the end: ok, or capped at
// a limit it records.
func (a Audit) Finished() bool { return a.Status == AuditOK || a.Status == AuditCapped }

// KeyboardSummary is what the keyboard walk of a page found, besides
// its issues. The full trace is in keyboard/<pageid>.json.
type KeyboardSummary struct {
	Stops    int      `json:"stops"`               // Tab stops the walk passed
	Wrapped  bool     `json:"wrapped"`             // the walk came back to its first stop
	Capped   bool     `json:"capped,omitempty"`    // the walk stopped at max_tabs
	SkipLink string   `json:"skip_link,omitempty"` // the skip link's selector, when there is one
	Widgets  int      `json:"widgets,omitempty"`   // menus, dialogs and disclosures tested
	Frames   []string `json:"frames,omitempty"`    // cross-origin frames walked, by host
	Evidence string   `json:"evidence,omitempty"`  // the trace file, relative to the run's folder
	// Crops maps an issue key to a picture of the element, relative to
	// the run's folder.
	Crops map[string]string `json:"crops,omitempty"`
	// WidgetKinds lists the kinds of widget tested (menu, dialog,
	// details, disclosure), sorted.
	WidgetKinds []string `json:"widget_kinds,omitempty"`
	MS          int      `json:"ms,omitempty"` // how long the walk took, in milliseconds
}

// KeyFlows is what became of a run's key flows.
type KeyFlows struct {
	Run     int           `json:"run"` // key flows that ran
	Skipped []SkippedFlow `json:"skipped,omitempty"`
}

// SkippedFlow is a key flow that didn't run and why.
type SkippedFlow struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// EvidenceNote is said with every accessibility result, in every
// output: the tests are evidence, not a verdict on the site.
const EvidenceNote = "Automated and keyboard tests find some accessibility problems, not all. This is evidence for your own review, not a statement that the site meets WCAG, EN 301 549 or the European Accessibility Act."

// EAASummary sums up an --eaa run against the WCAG criteria. It is
// filled for output (wcag.Summarize) and never stored.
type EAASummary struct {
	WCAG         string       `json:"wcag"`     // 2.1 or 2.2
	EN301549     string       `json:"en301549"` // the matching EN 301 549 version, such as V3.2.1
	Axe          string       `json:"axe"`      // axe-core's version
	Criteria     EAACriteria  `json:"criteria"`
	Keyboard     EAAKeyboard  `json:"keyboard"`
	NotAudited   []NotAudited `json:"not_audited,omitempty"`
	Open         int          `json:"open"` // accessibility and keyboard problems found
	Interrupted  bool         `json:"interrupted"`
	EvidenceNote string       `json:"evidence_note"` // always EvidenceNote
}

// EAACriteria sorts the criteria of the WCAG version by what the tests
// found.
type EAACriteria struct {
	Failed          []CriterionCount `json:"failed"`
	NoFailuresFound []string         `json:"no_failures_found"` // tested, nothing found: not a pass
	NotTested       []string         `json:"not_tested"`        // the tests can't judge it; a person must
}

// CriterionCount is a criterion with failures and how many.
type CriterionCount struct {
	ID     string `json:"id"`
	Issues int    `json:"issues"`
	Pages  int    `json:"pages"`
}

// EAAKeyboard counts what the keyboard tests covered.
type EAAKeyboard struct {
	PagesWalked   int           `json:"pages_walked"`
	WidgetsTested int           `json:"widgets_tested"`
	FlowsRun      int           `json:"flows_run"`
	FlowsSkipped  []SkippedFlow `json:"flows_skipped,omitempty"`
}

// NotAudited is a page a module couldn't audit and why.
type NotAudited struct {
	Path   string `json:"path"`
	Module string `json:"module"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
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
	// WCAG, EN301549, Impact and FocusPath are the issue's (see Issue),
	// for a change grouped from issues.
	WCAG      []string `json:"wcag,omitempty"`
	EN301549  []string `json:"en301549,omitempty"`
	Impact    string   `json:"impact,omitempty"`
	FocusPath []string `json:"focus_path,omitempty"`
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
	// EAA is the accessibility summary of the current run, when it was
	// made with --eaa. It is filled for output and not stored.
	EAA *EAASummary `json:"eaa,omitempty"`
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
