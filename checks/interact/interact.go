// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package interact checks that a page's controls work, at the levels set
// in interactions.levels:
//
//   - inspect reads the DOM: unnamed fields, forms that post to missing
//     pages or lack a WordPress nonce, duplicate ids, required fields
//     without labels, links and buttons with no click handler.
//   - click clicks menus, tabs, accordions, modals, sliders and other
//     controls, and reports dead clicks, JS errors and failed requests
//     they cause. Destructive-looking controls and /wp-admin are never
//     clicked.
//   - submit (opt-in) submits forms with fake data: empty first, expecting
//     validation, then valid, expecting success. It refuses production
//     URLs unless a form is allowlisted.
//   - flows runs the YAML flows in the flows folder.
package interact

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Module is the interactions module. Use a pointer (New or
// &interact.Module{}): it remembers checked form actions across pages.
type Module struct {
	mu      sync.Mutex
	actions map[string]int // form action URL → status
	warned  map[string]bool
}

// New returns the module.
func New() *Module { return &Module{} }

func (*Module) Name() string { return "interact" }

// Live runs the browser levels on a loaded page.
func (m *Module) Live(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page) {
	cfg := cc.Config
	if p.Error != "" || p.Status >= 400 || (!cfg.Level("inspect") && !cfg.Level("click") && !cfg.Level("submit")) {
		return
	}
	tab = tab.Context(ctx)
	f, err := collect(tab, cfg.Interactions.Allow, cfg.Interactions.Forms)
	if err != nil {
		cc.Warn(fmt.Sprintf("interact %s: %v", p.Path, err))
		return
	}
	admin := isAdmin(p.Path)
	clicked := map[string]bool{}
	set := &issueSet{}
	if cfg.Level("click") && !admin {
		clicked = m.clicks(ctx, cc, tab, p, f, set)
	}
	if cfg.Level("inspect") {
		m.inspect(ctx, cc, p, f, clicked, set)
	}
	set.flush(p)
	if cfg.Level("submit") && !admin {
		m.submits(ctx, cc, tab, p, f)
	}
}

// isAdmin reports paths Soundcheck never clicks or submits on.
func isAdmin(path string) bool {
	return strings.HasPrefix(path, "/wp-admin") || strings.HasPrefix(path, "/wp-login.php")
}

// inspect reports what the DOM shows without touching anything.
// Elements level 2 clicked are judged by the click instead.
func (m *Module) inspect(ctx context.Context, cc *check.Context, p *model.Page, f *facts, clicked map[string]bool, set *issueSet) {
	for _, u := range limit(f.Unnamed, 20) {
		check.Page(p, model.Issue{Check: "interact/unnamed-field", Level: model.Warning,
			Title:  "Form field has no name: " + u.Label,
			Detail: "in " + u.Form + " (" + u.Sel + ")",
			Fix:    "Give the field a name attribute; without one the browser never sends its value."})
	}
	for _, u := range limit(f.Unlabeled, 20) {
		check.Page(p, model.Issue{Check: "interact/unlabeled", Level: model.Notice,
			Title:  "Required field has no label: " + u.Label,
			Detail: u.Sel,
			Fix:    "Add a <label for> (or aria-label) so screen readers and validation messages can name the field."})
	}
	for _, d := range limit(f.DupIDs, 10) {
		check.Page(p, model.Issue{Check: "interact/duplicate-id", Level: model.Notice,
			Title:  fmt.Sprintf("Duplicate id: %q", d.ID),
			Detail: fmt.Sprintf("%d elements share it", d.N),
			Fix:    "Ids must be unique; scripts, labels and anchors only find the first element with an id."})
	}
	for _, n := range limit(f.NoHandler, 20) {
		if clicked[n.Sel] {
			continue
		}
		set.add(deadIssue(n.Label, n.Link, "Nothing on the page listens for clicks on it or its parents."), n.Sel)
	}
	base, _ := url.Parse(p.URL)
	for _, fm := range f.Forms {
		target, err := url.Parse(fm.Action)
		sameSite := fm.Action != "" && err == nil && base != nil && strings.EqualFold(target.Host, base.Host)
		if sameSite && strip(fm.Action) != strip(p.URL) && strip(fm.Action) != strip(p.FinalURL) {
			code := m.probe(ctx, cc, strip(fm.Action))
			if code == http.StatusNotFound || code == http.StatusGone || (code == http.StatusMethodNotAllowed && fm.Method == "get") {
				check.Page(p, model.Issue{Check: "interact/form-action", Level: model.Error,
					Title:  fmt.Sprintf("Form sends to a missing page (%d): %s", code, shortURL(base, strip(fm.Action))),
					Detail: fm.Name,
					Fix:    "Point the form's action at a page that exists, or restore the handler (a deactivated plugin often causes this)."})
			}
		}
		postsHere := fm.Method == "post" && (fm.Action == "" || sameSite)
		if f.WP && postsHere && !fm.Nonce && fm.Profile == "" && !fm.Comment && !fm.Password && !fm.Search {
			check.Page(p, model.Issue{Check: "interact/no-nonce", Level: model.Warning,
				Title: "Form has no nonce: " + fm.Name,
				Fix:   "Add wp_nonce_field() to the form and check_admin_referer()/wp_verify_nonce() where it's handled, so other sites can't post it for your visitors."})
		}
		p.Interact = append(p.Interact, model.Interaction{Kind: "form", Selector: fm.Sel, Label: fm.Name, Outcome: "ok",
			Detail: strings.ToUpper(fm.Method) + " " + orDefault(shortURL(base, fm.Action), "(this page)")})
	}
}

// deadIssue is titled by the visible label only, so identical controls
// share one issue and the title stays the same across runs.
func deadIssue(label string, link bool, detail string) model.Issue {
	what, fix := "Button", "Wire up its click handler, or remove the button if it's no longer needed."
	if link {
		what, fix = "Link", "Give the link a real href, or attach the script that should handle it."
	}
	return model.Issue{Check: "interact/dead-click", Level: model.Warning, Title: what + " does nothing: " + orDefault(label, "(no label)"),
		Detail: detail, Fix: fix}
}

// probe checks a form action with HEAD, falling back to GET. It never
// POSTs. Results are cached for the run.
func (m *Module) probe(ctx context.Context, cc *check.Context, raw string) int {
	m.mu.Lock()
	if code, ok := m.actions[raw]; ok {
		m.mu.Unlock()
		return code
	}
	m.mu.Unlock()
	code := 0
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		resp, err := cc.Get(pctx, method, raw)
		if err != nil {
			cancel()
			break
		}
		_ = resp.Body.Close()
		cancel()
		code = resp.StatusCode
		if code != http.StatusMethodNotAllowed && code != http.StatusNotImplemented {
			break
		}
	}
	m.mu.Lock()
	if m.actions == nil {
		m.actions = map[string]int{}
	}
	m.actions[raw] = code
	m.mu.Unlock()
	return code
}

// warnOnce records a run warning the first time key is seen.
func (m *Module) warnOnce(cc *check.Context, key, msg string) {
	m.mu.Lock()
	if m.warned == nil {
		m.warned = map[string]bool{}
	}
	seen := m.warned[key]
	m.warned[key] = true
	m.mu.Unlock()
	if !seen {
		cc.Warn(msg)
	}
}

func strip(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment, u.RawQuery = "", ""
	return u.String()
}

func limit[T any](s []T, n int) []T { return s[:min(len(s), n)] }

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// settle waits a moment, or until ctx ends.
func settle(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// reload navigates tab back to the page and re-arms the click guard.
func reload(ctx context.Context, tab *rod.Page, raw string) error {
	t := tab.Context(ctx).Timeout(30 * time.Second)
	if err := t.Navigate(raw); err != nil {
		return err
	}
	_ = t.WaitLoad()
	t.WaitRequestIdle(500*time.Millisecond, nil, nil, nil)()
	_, err := t.Eval(guardJS)
	return err
}
