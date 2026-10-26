// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package keyboard is the keyboard module: on each page it walks the
// focus order with trusted Tab and Shift+Tab presses, tests menus,
// dialogs and disclosures by keyboard and reports what stops a keyboard
// user, such as a focus trap, a control the keyboard can't reach or a
// missing focus indicator (the checks in checks/keyboard/ids).
//
// It runs in Chrome only, in check.PhaseKeyboard: after interact's
// clicks and submits, so it walks the page a visitor is left with. It
// is off by default; --eaa or +keyboard turns it on.
//
// The work is split in two. The recorder (record.go) presses keys and
// writes down what happened as a trace, saved to keyboard/<pageid>.json.
// Classify (classify.go) turns a trace into issues without a browser, so
// every rule has table tests and a fuzz target of its own.
package keyboard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(New()) }

// pageLimit is the wall time one page's walk may take. A walk that runs
// out records the page as timed out (model.AuditTimeout) with what it
// saw so far.
const pageLimit = 60 * time.Second

// Module is the keyboard module. It keeps its options between hooks, so
// it is used as a pointer (New).
type Module struct {
	mu   sync.Mutex
	opts model.KeyboardOptions
	set  Settings

	// limit and walk are pageLimit and record unless a test sets them,
	// to time a page out in a second or make the recorder fail.
	limit time.Duration
	walk  func(ctx context.Context, w *walker) (*Trace, error)
}

// New returns the module with the default options.
func New() *Module { return &Module{opts: defaults(), set: DefaultSettings()} }

// Name is "keyboard".
func (*Module) Name() string { return "keyboard" }

// Phase puts the module after interact's clicks and submits.
func (*Module) Phase() int { return check.PhaseKeyboard }

// Checks lists the module's checks, from ids.Checks.
func (*Module) Checks() []check.CheckInfo {
	out := make([]check.CheckInfo, 0, len(ids.Checks))
	for _, c := range ids.Checks {
		out = append(out, check.CheckInfo{ID: c.ID, Level: c.Level, Title: c.Title, Doc: c.Doc})
	}
	return out
}

// Live walks the page by keyboard and records what it finds on p: the
// issues, a summary (p.Keyboard) and whether the walk finished
// (p.Audits["keyboard"]). Every page the module is given gets an audit
// status, so a report can tell a page with no keyboard problems from a
// page that wasn't walked.
func (m *Module) Live(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page) {
	if reason := skipReason(tab, p); reason != "" {
		setAudit(p, model.Audit{Status: model.AuditSkipped, Reason: reason})
		return
	}
	s := m.settings()
	start := time.Now()
	pctx, cancel := context.WithTimeout(ctx, m.pageLimit())
	defer cancel()
	w := &walker{cc: cc, tab: tab, page: p, set: s, start: start}
	t, err := m.safeWalk(pctx, w)
	if ctx.Err() != nil {
		return // the run stopped: the runner records the page as canceled
	}
	audit := model.Audit{Status: model.AuditOK}
	switch {
	case errors.Is(pctx.Err(), context.DeadlineExceeded):
		audit = model.Audit{Status: model.AuditTimeout, Reason: fmt.Sprintf("the walk took over %s", seconds(m.pageLimit()))}
		cc.Warn(fmt.Sprintf("keyboard: %s: stopped after %s; the rest of the page wasn't walked", p.Path, seconds(m.pageLimit())))
	case err != nil:
		audit = model.Audit{Status: model.AuditError, Reason: err.Error()}
		cc.Warn(fmt.Sprintf("keyboard: %s: %v", p.Path, err))
	case t != nil && t.Capped:
		audit = model.Audit{Status: model.AuditCapped, Reason: fmt.Sprintf("stopped after %d Tab stops (keyboard.max_tabs)", s.MaxTabs)}
		cc.Warn(fmt.Sprintf("keyboard: %s: stopped after %d Tab stops (keyboard.max_tabs); raise it if the page has more", p.Path, s.MaxTabs))
	}
	if t == nil {
		setAudit(p, audit)
		return
	}
	// A walk that didn't finish can't prove a trap: it may simply not
	// have got past a long run of stops.
	if !audit.Finished() {
		t.Capped = true
	}
	for _, i := range Classify(*t, s.options(), wcagVersion(cc)) {
		check.Page(p, i)
	}
	sum := summary(t, w)
	sum.MS = int(time.Since(start).Milliseconds())
	if err := w.save(t); err != nil {
		cc.Warn(fmt.Sprintf("keyboard: %s: can't save the trace: %v", p.Path, err))
	} else {
		sum.Evidence = tracePath(p)
	}
	p.Keyboard = sum
	setAudit(p, audit)
}

// safeWalk runs the walk, turning a panic in it (a probe that broke on
// a page nobody foresaw) into an error, so the page is recorded as not
// walked instead of losing its audit status to the runner's recover.
func (m *Module) safeWalk(ctx context.Context, w *walker) (t *Trace, err error) {
	defer func() {
		if r := recover(); r != nil {
			t, err = w.partial(), fmt.Errorf("the walk failed: %v", r)
		}
	}()
	walk := m.walk
	if walk == nil {
		walk = record
	}
	return walk(ctx, w)
}

func (m *Module) pageLimit() time.Duration {
	if m.limit > 0 {
		return m.limit
	}
	return pageLimit
}

// skipReason says why a page isn't walked, or "" when it is.
func skipReason(tab *rod.Page, p *model.Page) string {
	switch {
	case tab == nil:
		return "needs Chrome"
	case p.Error != "":
		return "the page didn't load"
	case p.Status >= 400:
		return fmt.Sprintf("HTTP %d", p.Status)
	case isAdmin(p.Path):
		return "admin pages are never walked"
	}
	return ""
}

// isAdmin reports paths Soundcheck never presses keys on, like interact
// never clicks there.
func isAdmin(path string) bool {
	return strings.HasPrefix(path, "/wp-admin") || strings.HasPrefix(path, "/wp-login.php")
}

func setAudit(p *model.Page, a model.Audit) {
	if p.Audits == nil {
		p.Audits = map[string]model.Audit{}
	}
	p.Audits["keyboard"] = a
}

// wcagVersion is the WCAG version the run reports against: 2.2 when
// the run asked for it, else 2.1, the version the keyboard checks map to
// by default (2.0 has the same keyboard criteria).
func wcagVersion(cc *check.Context) string {
	if cc.Run != nil && cc.Run.Options.WCAG == "2.2" {
		return "2.2"
	}
	return "2.1"
}

// summary is what p.Keyboard records of a trace.
func summary(t *Trace, w *walker) *model.KeyboardSummary {
	sum := &model.KeyboardSummary{Stops: t.Stops(), Wrapped: t.Wrapped(), Capped: t.Capped, Widgets: len(t.Widgets)}
	if t.SkipLink != nil {
		sum.SkipLink = t.SkipLink.Selector
	}
	sum.Frames = t.FrameSummary()
	kinds := map[string]bool{}
	for _, r := range t.Widgets {
		if slices.Contains(widgetKinds, r.Kind) {
			kinds[r.Kind] = true
		}
	}
	for k := range kinds {
		sum.WidgetKinds = append(sum.WidgetKinds, k)
	}
	slices.Sort(sum.WidgetKinds)
	if len(w.crops) > 0 {
		sum.Crops = w.crops
	}
	return sum
}

// widgetKinds are the kinds of widget the walk tests.
var widgetKinds = []string{"details", "dialog", "disclosure", "menu"}

// seconds writes d as whole seconds, the way the limits are set.
func seconds(d time.Duration) string {
	return fmt.Sprintf("%d s", int(d.Round(time.Second)/time.Second))
}
