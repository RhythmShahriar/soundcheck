// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/RhythmShahriar/soundcheck/check"
)

// Engine is a browser engine Soundcheck can capture pages with.
type Engine string

// The engines. Chrome is the default and the only one with the DevTools
// protocol, which some modules need (see check.ChromeOnly). Firefox and
// WebKit are driven through WebDriver (package capture/webdriver).
const (
	Chrome  Engine = "chrome"
	Firefox Engine = "firefox"
	WebKit  Engine = "webkit"
)

// Engines lists the engines, the default first.
var Engines = []Engine{Chrome, Firefox, WebKit}

// ParseEngine reads an engine's name, ignoring case. Empty is Chrome.
func ParseEngine(s string) (Engine, error) {
	switch e := Engine(strings.ToLower(strings.TrimSpace(s))); e {
	case "":
		return Chrome, nil
	case Chrome, Firefox, WebKit:
		return e, nil
	case "chromium":
		return Chrome, nil
	case "safari":
		return WebKit, nil
	}
	return "", fmt.Errorf("unknown browser engine %q (want chrome, firefox or webkit)", s)
}

// Driver is a running browser of one engine that opens tabs to capture
// pages in.
type Driver interface {
	// Engine is the browser engine the driver runs.
	Engine() Engine
	// NewTab opens a blank tab.
	NewTab(ctx context.Context) (Tab, error)
	// Close shuts the browser down.
	Close() error
}

// ErrUnsupported is wrapped by a Tab method's error when the engine (or
// the protocol it is driven with) can't do what was asked.
var ErrUnsupported = errors.New("not supported by this browser")

// Viewport is a window size to load a page at.
type Viewport struct {
	Width, Height int
	// Mobile emulates a phone (a meta viewport is honored, touch events).
	// Only Chrome can; elsewhere the page loads at the size alone.
	Mobile bool
}

// Tab is a browser tab a visit drives, in any engine: check.Tab plus
// navigation, the tab's settings and its network. Methods an engine
// can't do return an error saying so, except where they note a
// fallback.
type Tab interface {
	check.Tab
	// Navigate loads rawURL and returns once its load event fired.
	Navigate(ctx context.Context, rawURL string) error
	// URL is the address the tab shows.
	URL(ctx context.Context) (string, error)
	// SetViewport resizes the page.
	SetViewport(ctx context.Context, v Viewport) error
	// SetUserAgent replaces the browser's user agent for the tab.
	SetUserAgent(ctx context.Context, ua string) error
	// DisableCache makes every request skip the browser's cache, so each
	// page loads cold. An engine without the setting goes without.
	DisableCache(ctx context.Context) error
	// AddInitScript runs js, a statement list, in every document the tab
	// loads, before the page's own scripts.
	AddInitScript(ctx context.Context, js string) error
	// ScopeHeaders adds headers to the requests the tab sends to site and
	// to no other host, until release is called. A Cookie header is set
	// as HttpOnly cookies for site's host instead. An engine that can't
	// scope headers fails instead of sending them everywhere.
	ScopeHeaders(ctx context.Context, site *url.URL, headers map[string]string) (release func(), err error)
	// Network calls fn for each step of each request from now on, one at
	// a time, until ctx ends or stop is called.
	Network(ctx context.Context, fn func(NetEvent)) (stop func())
	// Clip returns a PNG of the page from its top-left corner, width by
	// height CSS pixels, including what is below the viewport.
	Clip(ctx context.Context, width, height int) ([]byte, error)
	// Close closes the tab.
	Close() error
}

// NetPhase is the step of a request a NetEvent reports.
type NetPhase int

// The steps of a request, in order. A request ends with NetFinished or
// NetFailed; a redirect is a NetResponse with a 3xx status followed by a
// NetRequest with the same ID.
const (
	NetRequest  NetPhase = iota // about to be sent
	NetResponse                 // the response headers arrived
	NetFinished                 // the body arrived
	NetFailed                   // it failed or was canceled
)

// NetEvent is one step of a request a tab made.
type NetEvent struct {
	Phase NetPhase
	ID    string // the same for every step of a request, redirects included
	// Document marks the tab's own page: a navigation of its top frame.
	Document bool
	URL      string
	Method   string // NetRequest
	// Type is the request's resource type in Chrome's words: Document,
	// Stylesheet, Script, Image, Font, XHR, Fetch, Media or Other.
	Type     string
	Status   int               // NetResponse
	Protocol string            // NetResponse: http/1.1, h2…
	Mime     string            // NetResponse: the media type, without parameters
	Headers  map[string]string // NetResponse: lower-case names
	Bytes    int64             // NetFinished: bytes received, headers included
	Error    string            // NetFailed
	Canceled bool              // NetFailed: canceled rather than failed
}

// ResourceType maps a request's Fetch destination ("script", "image",
// "" for fetch() and XHR) and initiator type to Chrome's resource type
// names, so the checks read the same names in every engine.
func ResourceType(destination, initiator string) string {
	switch destination {
	case "document", "iframe", "frame":
		return "Document"
	case "style":
		return "Stylesheet"
	case "script", "worker", "sharedworker", "serviceworker":
		return "Script"
	case "image":
		return "Image"
	case "font":
		return "Font"
	case "audio", "video", "track":
		return "Media"
	case "manifest":
		return "Manifest"
	}
	switch initiator {
	case "xmlhttprequest":
		return "XHR"
	case "fetch", "beacon":
		return "Fetch"
	case "img", "image", "imageset":
		return "Image"
	case "script":
		return "Script"
	case "css", "link":
		return "Stylesheet"
	}
	return "Other"
}
