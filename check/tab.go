// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"context"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Tab is the part of a browser tab most modules need. A module written
// against it (TabPreparer, TabLive) can be unit tested with a fake tab
// (check/checktest) instead of Chrome; Rod is the way out to everything
// else the browser can do.
type Tab interface {
	// Eval runs js, a function expression such as
	// "() => document.title", with args and decodes its JSON result into
	// out (nil discards it).
	Eval(ctx context.Context, js string, out any, args ...any) error
	// Screenshot returns a PNG of the viewport, or of the whole page when
	// full is set.
	Screenshot(ctx context.Context, full bool) ([]byte, error)
	// On calls fn for each event the page sends from now on, one at a
	// time, until ctx ends or stop is called.
	On(ctx context.Context, fn func(Event)) (stop func())
	// Rod is the underlying tab, nil for a fake. A module that uses it
	// needs Chrome to test.
	Rod() *rod.Page
}

// EventKind is what an Event reports.
type EventKind string

// The events a Tab sends.
const (
	Console   EventKind = "console"   // a console call: console.error("…")
	Exception EventKind = "exception" // an uncaught exception
	Response  EventKind = "response"  // a response to one of the page's requests
)

// Event is something that happened in a tab.
type Event struct {
	Kind   EventKind
	Level  string // Console: log, info, warning, error…
	Text   string // Console: the arguments joined with spaces; Exception: the message
	URL    string // Response: the request's URL; Exception: the script, when known
	Line   int    // Exception: the line in URL, from 1, when known
	Type   string // Response: Document, Script, XHR, Fetch, Image…
	Status int    // Response: the HTTP status
}

// TabPreparer is Preparer against a Tab: it runs on a fresh tab before it
// navigates, e.g. to watch its events with Tab.On.
type TabPreparer interface {
	PrepareTab(ctx context.Context, cc *Context, tab Tab) error
}

// TabLive is Live against a Tab: it runs while the page is loaded at the
// desktop viewport, right after the module's Live hook if it has one.
type TabLive interface {
	LiveTab(ctx context.Context, cc *Context, tab Tab, p *model.Page)
}

// RodTab is t as a Tab.
func RodTab(t *rod.Page) Tab { return rodTab{t} }

type rodTab struct{ p *rod.Page }

func (t rodTab) Rod() *rod.Page { return t.p }

func (t rodTab) Eval(ctx context.Context, js string, out any, args ...any) error {
	res, err := t.p.Context(ctx).Eval(js, args...)
	if err != nil || out == nil {
		return err
	}
	return res.Value.Unmarshal(out)
}

func (t rodTab) Screenshot(ctx context.Context, full bool) ([]byte, error) {
	return t.p.Context(ctx).Screenshot(full, &proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng})
}

func (t rodTab) On(ctx context.Context, fn func(Event)) func() {
	ctx, stop := context.WithCancel(ctx)
	wait := t.p.Context(ctx).EachEvent(
		func(e *proto.RuntimeConsoleAPICalled) {
			fn(Event{Kind: Console, Level: string(e.Type), Text: consoleText(e.Args)})
		},
		func(e *proto.RuntimeExceptionThrown) {
			d := e.ExceptionDetails
			msg := d.Text
			if d.Exception != nil && d.Exception.Description != "" {
				msg = d.Exception.Description
			}
			ev := Event{Kind: Exception, Text: msg, URL: d.URL}
			if d.URL != "" {
				ev.Line = d.LineNumber + 1
			}
			fn(ev)
		},
		func(e *proto.NetworkResponseReceived) {
			fn(Event{Kind: Response, URL: e.Response.URL, Type: string(e.Type), Status: e.Response.Status})
		},
	)
	go wait()
	return stop
}

// consoleText joins a console call's arguments the way the console shows
// them: objects by their description, strings as they are.
func consoleText(args []*proto.RuntimeRemoteObject) string {
	var parts []string
	for _, a := range args {
		switch {
		case a.Description != "":
			parts = append(parts, a.Description)
		case a.Value.Nil():
		case a.Value.Str() != "":
			parts = append(parts, a.Value.Str())
		default:
			parts = append(parts, a.Value.JSON("", ""))
		}
	}
	return strings.Join(parts, " ")
}
