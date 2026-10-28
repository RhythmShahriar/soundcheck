// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package statement drafts an accessibility statement from an --eaa
// run: the criteria the tests found failures of, the ones they found
// none of and the ones a person must check, with every place the owner
// has to fill in marked [TO DO]. A draft is evidence for the owner's own
// statement. It never says a site meets WCAG, EN 301 549 or the
// European Accessibility Act.
//
// The owner's own text (who they are, how to reach them, known issues
// and manual results) lives under statement: in the config file, so a
// new draft never loses it.
package statement

import (
	"errors"
	"time"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

func init() { config.RegisterBlock("statement") }

// Build's refusals. The CLI words them for the person running it.
var (
	// ErrMissingOwner: statement.owner, statement.website or
	// statement.contact.email isn't set. A statement without a way to
	// report problems isn't one.
	ErrMissingOwner = errors.New("statement needs statement.owner, statement.website and statement.contact.email")
	// ErrNoEAAData: the run wasn't made with --eaa.
	ErrNoEAAData = errors.New("the run has no --eaa results")
	// ErrInterrupted: the run stopped before it finished, so a draft
	// would leave problems out.
	ErrInterrupted = errors.New("the run was interrupted")
)

// Input is what a draft is made from.
type Input struct {
	Run *model.Run
	// Pages streams the run's pages, which may come from disk one by
	// one; nil is Run.Pages. Build calls it again after the caller's
	// wcag.Summarize, so it must start over on every call (store.Pages
	// does): a single-use iterator gives a draft with no pages.
	Pages    func(func(*model.Page) bool)
	Site     []model.Issue    // the run's site-level issues; nil is Run.Issues
	Ignored  []model.Change   // changes ignore rules and accepts left out: listed with their reasons
	Summary  model.EAASummary // wcag.Summarize of the run
	Owner    Owner
	Version  string    // Soundcheck's version
	Layout   string    // LayoutModel (the default) or LayoutService
	Now      time.Time // the draft's date
	Insecure bool      // the run didn't verify TLS certificates (-k)
	// Hide lists text that must never reach the draft, such as the login
	// account and a staging host name. The run's own host is always
	// hidden: the draft names the site by Owner.Website. A two-character
	// string is masked only as a whole word or path segment and a single
	// character not at all.
	Hide []string
}

// Layouts of a draft.
const (
	// LayoutModel follows the model accessibility statement of
	// Implementing Decision (EU) 2018/1523.
	LayoutModel = "model"
	// LayoutService is the service information of the European
	// Accessibility Act, Annex V.
	LayoutService = "service"
)

// Layouts lists the layouts, the default first.
var Layouts = []string{LayoutModel, LayoutService}

// Owner is the statement: block of the config file: the owner's own
// text, which the tests can't know.
type Owner struct {
	Owner        string        `yaml:"owner"`
	Website      string        `yaml:"website"` // the public address
	Contact      Contact       `yaml:"contact"`
	ResponseDays int           `yaml:"response_days"`
	Country      string        `yaml:"country"` // ISO 3166 alpha-2
	Enforcement  string        `yaml:"enforcement"`
	Scope        []string      `yaml:"scope"`   // page globs the statement covers; empty is every checked page
	Service      string        `yaml:"service"` // a description of the service, for the service layout
	KnownIssues  []KnownIssue  `yaml:"known_issues"`
	Manual       []ManualCheck `yaml:"manual"`
	// ListPrivatePaths lists paths behind a login in the draft; by
	// default they are only counted.
	ListPrivatePaths bool `yaml:"list_private_paths"`
}

// Contact is how visitors report accessibility problems.
type Contact struct {
	Email   string `yaml:"email"`
	Phone   string `yaml:"phone"`
	Address string `yaml:"address"`
}

// KnownIssue is a problem the owner already knows about, with the date
// they plan to fix it by.
type KnownIssue struct {
	Criterion string `yaml:"criterion"`
	Text      string `yaml:"text"`
	Until     string `yaml:"until"` // YYYY-MM-DD, optional
}

// ManualCheck is a criterion a person checked, with the result.
type ManualCheck struct {
	Criterion string `yaml:"criterion"`
	Result    string `yaml:"result"` // ManualFailures, ManualNoFailures or ManualNotChecked
	By        string `yaml:"by"`
	Date      string `yaml:"date"` // YYYY-MM-DD
}

// Draft is a statement draft, ready to write in any format. Everything
// in it is structured data: criterion numbers, counts, check ids, the
// owner's own text and paths without query strings, with tokens
// masked. Nothing a page showed reaches it.
type Draft struct {
	Rows     []Row    // one per criterion of the run's WCAG version
	Pages    int      // pages checked
	Todo     int      // [TO DO] markers left for the owner, counted as a renderer writes them
	Notes    []string // what wasn't tested and why
	Standard string   // wcag.Standard of the run's version
	Layout   string   // LayoutModel or LayoutService, from Input.Layout

	Version  string // Soundcheck's version
	Date     string // the draft's date, YYYY-MM-DD
	Owner    Owner  // the statement: block; Owner.Website names the site
	WCAG     string // 2.1 or 2.2
	EN301549 string // the matching EN 301 549 version, such as V3.2.1
	Axe      string // axe-core's version
	AxeRules int    // axe rules the run applied

	// Checked lists the paths of the checked pages, sorted and unique,
	// when they may be listed. PrivatePages counts the pages left out
	// of every list because they were fetched logged in (see
	// Owner.ListPrivatePaths).
	Checked      []string
	PrivatePages int

	// Failed and NotTested count the criteria the tests found failures
	// of and couldn't test (Row.Tests), whatever the owner recorded: the
	// status sentence says what Soundcheck's tests did.
	Failed    int
	NotTested int

	KnownIssues []Known
	Ignored     []Ignored
	// NotAudited lists the pages a module couldn't audit. Path is empty
	// for a page behind a login that isn't listed.
	NotAudited []model.NotAudited
	Frames     []string // hosts of frames the audits didn't enter
	Keyboard   model.EAAKeyboard

	// Status is the conformance status. It is always nil: choosing it is
	// the owner's decision after their own review, never Soundcheck's.
	Status       *string
	EvidenceNote string // always model.EvidenceNote
}

// Results of a Row.
const (
	ResultFailures   = "failures_found"    // the tests found failures
	ResultNoFailures = "no_failures_found" // the tests ran and found none: not a pass
	ResultNotTested  = "not_tested"        // nothing tested it; a person must
	ResultManual     = "manual"            // the owner recorded a result (Row.Manual)
)

// Row is one criterion and what the tests found.
type Row struct {
	wcag.Criterion
	Result string   // one of the Result* values
	Tests  string   // what the tests found, before a manual entry: ResultFailures, ResultNoFailures or ResultNotTested
	Issues int      // problems found, each counted once per element
	Pages  int      // pages with a problem
	Paths  []string // up to 3 of those pages, when they may be listed
	Manual *Manual  // the owner's own result, when the config gives one
}

// Manual is the result a person recorded for a criterion: one of the
// Manual* values, who and when.
type Manual struct {
	Result, By, Date string
}

// Known is a known issue of the owner's with its criterion's title.
type Known struct {
	KnownIssue
	Title string
}

// Ignored is a problem the run found that an ignore rule or an accept
// left out, with the owner's reason.
type Ignored struct {
	Criteria []string // the criteria it fails
	Check    string
	Pages    []string // up to 3 pages, when they may be listed
	Reason   string
}
