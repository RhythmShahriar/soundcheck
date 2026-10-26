// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/internal/focus"
	"github.com/RhythmShahriar/soundcheck/model"
)

// maxReloads caps the page loads one walk adds.
const maxReloads = 2

// errReloads is the walk's error when a page needed more reloads than
// maxReloads.
var errReloads = errors.New("the page needed more than 2 reloads")

// walker is one page's walk in progress.
type walker struct {
	cc    *check.Context
	tab   *rod.Page
	page  *model.Page
	set   Settings
	start time.Time

	trace   *Trace
	crops   map[string]string // issue key → picture, for the summary
	reloads int
	loaded  time.Time // when the page walked was loaded
}

// partial is the trace recorded so far, or nil before the walk began.
func (w *walker) partial() *Trace { return w.trace }

// begin starts the page's trace.
func (w *walker) begin() *Trace {
	w.trace = &Trace{Schema: Schema, Page: tracePage(w.page), Viewport: "desktop", MainIndex: -1,
		Forward: []Step{}, Backward: []Step{}, Widgets: []WidgetResult{}, Listeners: []Candidate{}, Held: []string{}, Events: []string{}}
	w.loaded = w.start
	return w.trace
}

// save stores t as the page's trace file.
func (w *walker) save(t *Trace) error {
	data, err := t.Encode()
	if err != nil {
		return err
	}
	if w.cc.Save == nil {
		return errors.New("the run stores no files")
	}
	return w.cc.Save(tracePath(w.page), data)
}

// interacted reports whether interact clicked or submitted on the page,
// which leaves menus open and forms sent: the walk then needs the page
// as a visitor gets it.
func interacted(p *model.Page) bool {
	for _, i := range p.Interact {
		if i.Kind == "click" || i.Kind == "submit" {
			return true
		}
	}
	return false
}

// reload loads the page again, at the run's polite pace, like the
// runner's own visits.
func (w *walker) reload(ctx context.Context) error {
	if w.reloads >= maxReloads {
		return errReloads
	}
	w.reloads++
	raw := w.page.FinalURL
	if raw == "" {
		raw = w.page.URL
	}
	if w.cc.Hosts != nil {
		host := ""
		if u, err := url.Parse(raw); err == nil {
			host = u.Host
		}
		if err := w.cc.Hosts.For(host).Wait(ctx); err != nil {
			return err
		}
	}
	tab := w.tab.Context(ctx)
	if err := tab.Navigate(raw); err != nil {
		return err
	}
	// As in capture.Visit, a page that never quite finishes loading is
	// still walked as it is.
	_ = tab.WaitLoad()
	tab.WaitRequestIdle(700*time.Millisecond, nil, nil, nil)()
	w.loaded = time.Now()
	return ctx.Err()
}

// record walks the page with Tab and returns its trace.
func record(ctx context.Context, w *walker) (*Trace, error) {
	t := w.begin()
	if interacted(w.page) {
		if err := w.reload(ctx); err != nil {
			return t, err
		}
	}
	for range w.set.MaxTabs {
		if err := focus.Press(ctx, w.tab, focus.Tab); err != nil {
			return t, err
		}
		s, err := focus.Active(ctx, w.tab)
		if err != nil {
			return t, err
		}
		t.Forward = append(t.Forward, Step{Stop: s, AtMS: int(time.Since(w.loaded).Milliseconds())})
		if t.Wrapped() {
			return t, nil
		}
	}
	t.Capped = true
	return t, nil
}
