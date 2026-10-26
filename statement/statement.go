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
	"io"
	"time"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

func init() { config.RegisterBlock("statement") }

// ErrNotBuilt is what the parts of the package not written yet return.
var ErrNotBuilt = errors.New("not built yet")

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
	Run      *model.Run
	Pages    func(func(*model.Page) bool) // the run's pages, which may come from disk one by one
	Ignored  []model.Change               // changes ignore rules and accepts left out: listed with their reasons
	Summary  model.EAASummary             // wcag.Summarize of the run
	Owner    Owner
	Version  string    // Soundcheck's version
	Layout   string    // model (the EU model statement) or service (EAA service information)
	Now      time.Time // the draft's date
	Insecure bool      // the run didn't verify TLS certificates (-k)
}

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
	Until     string `yaml:"until"`
}

// ManualCheck is a criterion a person checked, with the result.
type ManualCheck struct {
	Criterion string `yaml:"criterion"`
	Result    string `yaml:"result"` // failures found, no failures found or not checked
	By        string `yaml:"by"`
	Date      string `yaml:"date"`
}

// Draft is a statement draft, ready to write in any format.
type Draft struct {
	Rows     []Row    // one per criterion of the run's WCAG version
	Pages    int      // pages checked
	Todo     int      // [TO DO] markers left for the owner
	Notes    []string // what wasn't tested and why
	Standard string   // wcag.Standard of the run's version
	Layout   string   // model or service, from Input.Layout
}

// Row is one criterion and what the tests found.
type Row struct {
	wcag.Criterion
	Result string   // failures found, no failures found by these tests, or not tested
	Issues int      // problems found, each counted once per page and element
	Pages  int      // pages with a problem
	Paths  []string // up to 3 of those pages
	Manual *Manual  // the owner's own result, when the config gives one
}

// Manual is the result a person recorded for a criterion.
type Manual struct {
	Result, By, Date string
}

// Build makes the draft. It refuses a run without --eaa results, an
// interrupted run and an owner without the required keys.
func Build(in Input) (*Draft, error) { return nil, ErrNotBuilt }

// Markdown writes d as Markdown.
func Markdown(w io.Writer, d *Draft) error { return ErrNotBuilt }

// HTML writes d as a standalone HTML page.
func HTML(w io.Writer, d *Draft) error { return ErrNotBuilt }

// JSON writes d as JSON.
func JSON(w io.Writer, d *Draft) error { return ErrNotBuilt }

// EARL writes d as JSON-LD that the W3C WCAG-EM Report Tool imports.
func EARL(w io.Writer, d *Draft) error { return ErrNotBuilt }

// DecodeOwner reads the statement: block of cfg.
func DecodeOwner(cfg config.Config) (Owner, error) { return Owner{}, nil }
