// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package intercept pauses a tab's requests at one point, so several
// owners can change or hold them: capture adds the run's -H headers to
// the site's own requests, Echo tags form submits and interact holds
// what clicks would send. Chrome gives a tab a single Fetch domain. An
// owner that enabled or disabled it alone would undo the others, so
// owners register handlers here instead: Fetch is enabled with the first
// handler on a tab and disabled with the last. Each paused request
// passes through every handler and is continued (or failed) once.
package intercept

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
	"github.com/go-rod/rod/lib/proto"
)

// IsBlocked reports whether errorText, from Network.loadingFailed, is
// that of a request a handler blocked. Chrome words it
// "net::ERR_BLOCKED_BY_CLIENT.Inspector".
func IsBlocked(errorText string) bool {
	return strings.HasPrefix(errorText, "net::ERR_BLOCKED_BY_CLIENT")
}

// Request is a paused request the handlers may change.
type Request struct {
	URL     *url.URL // nil when Chrome's URL doesn't parse
	Method  string
	Type    proto.NetworkResourceType
	headers []*proto.FetchHeaderEntry
	changed bool
	blocked bool
	body    string
	partial bool // the request has a body Chrome didn't pass on (too long)
}

// Body returns the request's body. ok is false when it has one Chrome
// didn't pass to the interceptor, as it does for long bodies and files.
func (r *Request) Body() (body string, ok bool) { return r.body, !r.partial }

// Header returns the value of the named request header, or "".
func (r *Request) Header(name string) string {
	for _, h := range r.headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// SetHeader sets a request header, replacing any value it had.
func (r *Request) SetHeader(name, value string) {
	r.changed = true
	r.headers = slices.DeleteFunc(r.headers, func(h *proto.FetchHeaderEntry) bool { return strings.EqualFold(h.Name, name) })
	r.headers = append(r.headers, &proto.FetchHeaderEntry{Name: name, Value: value})
}

// Block fails the request as blocked by the client (see IsBlocked), so
// it never reaches the network.
func (r *Request) Block() { r.blocked = true }

// Blocked reports whether a handler blocked the request.
func (r *Request) Blocked() bool { return r.blocked }

// Handler sees each paused request. Handlers run in the order they were
// added, one request at a time. They must not block.
type Handler func(*Request)

// unsafe are the request headers Fetch.continueRequest refuses with
// "Unsafe header: <name>", checked against Chrome by
// TestUnsafeHeadersRefused. Chrome's network stack owns them.
var unsafe = map[string]bool{
	"host": true, "content-length": true, "connection": true, "keep-alive": true,
	"transfer-encoding": true, "upgrade": true, "te": true, "trailer": true, "proxy-authorization": true,
}

// Unsafe reports whether Chrome refuses to let an interceptor set the
// request header name. Handlers may still SetHeader it: it is left out
// when the request goes on.
func Unsafe(name string) bool {
	name = strings.ToLower(name)
	return unsafe[name] || strings.HasPrefix(name, "proxy-")
}

// refusedHeader is the header named in Chrome's error for a header it
// refused, or "".
func refusedHeader(err error) string {
	var ce *cdp.Error
	if !errors.As(err, &ce) {
		return ""
	}
	name, ok := strings.CutPrefix(ce.Message, "Unsafe header: ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(name)
}

// tabState is the interception of one tab. Its own lock, not the
// package's, guards the handlers and Fetch: a tab whose renderer hangs
// mid-call then stalls only itself.
type tabState struct {
	tab      *rod.Page
	mu       sync.Mutex
	handlers []entry
	dead     bool     // the last handler left; Add starts a new tabState
	patterns []string // the URL patterns Fetch pauses; nil until started
	stop     func()
	done     chan struct{}
}

type entry struct {
	id int
	h  Handler
	// patterns are the URLs the handler needs paused; nil is all.
	patterns []string
}

var (
	mu   sync.Mutex // guards tabs and next
	tabs = map[proto.TargetTargetID]*tabState{}
	next int
)

// Add registers h for every request tab makes from now on, until remove
// is called. The first handler on a tab enables Fetch interception; the
// last one removed disables it, which lets any request still paused go
// on.
func Add(tab *rod.Page, h Handler) (remove func(), err error) { return AddFor(tab, nil, h) }

// AddFor is Add for a handler that needs only the requests whose URL
// matches one of patterns paused (Fetch's URL patterns: "*" any run of
// characters, "?" one). Pausing a request costs it a round trip to
// Soundcheck, which a page's timings would show, so a handler that
// cares about a few URLs leaves the rest alone. It still sees every
// request another handler has paused and decides for itself. Empty
// patterns are every URL.
func AddFor(tab *rod.Page, patterns []string, h Handler) (remove func(), err error) {
	if len(patterns) == 0 {
		patterns = nil
	}
	for {
		mu.Lock()
		next++
		id := next
		st := tabs[tab.TargetID]
		if st == nil {
			st = &tabState{tab: tab}
			tabs[tab.TargetID] = st
		}
		mu.Unlock()

		st.mu.Lock()
		if st.dead {
			// Its last handler left while this one waited for the lock.
			st.mu.Unlock()
			continue
		}
		// Whichever Add gets the lock first starts it, which needn't be
		// the one that made it.
		want := union(st.handlers, patterns)
		if st.stop == nil {
			if err := st.start(want); err != nil {
				st.dead = true
				st.mu.Unlock()
				st.forget()
				return func() {}, err
			}
		} else if !slices.Equal(want, st.patterns) {
			// Fetch.enable again only widens what is paused. Patterns are
			// never narrowed while handlers stay: a request paused under
			// the old ones still reaches them.
			if err := enable(st.tab, want); err != nil {
				st.mu.Unlock()
				return func() {}, err
			}
			st.patterns = want
		}
		st.handlers = append(st.handlers, entry{id, h, patterns})
		st.mu.Unlock()
		var once sync.Once
		return func() { once.Do(func() { st.remove(id) }) }, nil
	}
}

// union is the patterns Fetch pauses for the handlers plus one with
// more: every URL when any of them needs all, else each pattern once.
func union(handlers []entry, more []string) []string {
	if more == nil {
		return []string{"*"}
	}
	all := slices.Clone(more)
	for _, x := range handlers {
		if x.patterns == nil {
			return []string{"*"}
		}
		all = append(all, x.patterns...)
	}
	slices.Sort(all)
	return slices.Compact(all)
}

// enable pauses the requests of tab whose URL matches patterns.
func enable(tab *rod.Page, patterns []string) error {
	ps := make([]*proto.FetchRequestPattern, 0, len(patterns))
	for _, p := range patterns {
		ps = append(ps, &proto.FetchRequestPattern{URLPattern: p, RequestStage: proto.FetchRequestStageRequest})
	}
	return proto.FetchEnable{Patterns: ps}.Call(tab)
}

// start enables interception of the requests matching patterns on the
// tab and runs the loop that passes paused requests through the
// handlers. st.mu is held.
func (st *tabState) start(patterns []string) error {
	if err := enable(st.tab, patterns); err != nil {
		return err
	}
	st.patterns = patterns
	// Its own context: the loop lives as long as a handler does, not as
	// long as whichever owner came first.
	ctx, cancel := context.WithCancel(context.Background())
	st.stop, st.done = cancel, make(chan struct{})
	wait := st.tab.Context(ctx).EachEvent(func(e *proto.FetchRequestPaused) { st.handle(e) })
	go func() { wait(); close(st.done) }()
	return nil
}

// forget drops st from tabs, unless a newer tabState took its place.
func (st *tabState) forget() {
	mu.Lock()
	if tabs[st.tab.TargetID] == st {
		delete(tabs, st.tab.TargetID)
	}
	mu.Unlock()
}

func (st *tabState) handle(e *proto.FetchRequestPaused) {
	st.mu.Lock()
	hs := slices.Clone(st.handlers)
	st.mu.Unlock()
	r := &Request{Method: e.Request.Method, Type: e.ResourceType}
	r.URL, _ = url.Parse(e.Request.URL)
	r.body = e.Request.PostData
	if r.body == "" {
		for _, p := range e.Request.PostDataEntries {
			r.body += string(p.Bytes)
		}
	}
	r.partial = e.Request.HasPostData && r.body == ""
	for k, v := range e.Request.Headers {
		r.headers = append(r.headers, &proto.FetchHeaderEntry{Name: k, Value: v.Str()})
	}
	slices.SortFunc(r.headers, func(a, b *proto.FetchHeaderEntry) int { return strings.Compare(a.Name, b.Name) })
	for _, x := range hs {
		x.h(r)
	}
	switch {
	case r.blocked:
		_ = proto.FetchFailRequest{RequestID: e.RequestID, ErrorReason: proto.NetworkErrorReasonBlockedByClient}.Call(st.tab)
	case r.changed:
		st.continueWith(e.RequestID, r.headers)
	default:
		_ = proto.FetchContinueRequest{RequestID: e.RequestID}.Call(st.tab)
	}
}

// continueWith lets a paused request go on with headers. Headers Chrome
// refuses are left out, whether known (see Unsafe) or named in its
// error, so one of them can't cost the request the others. Overriding
// the headers without Host keeps the Host Chrome would send. A request
// Chrome still won't take goes on as it was, so it isn't paused forever.
func (st *tabState) continueWith(id proto.FetchRequestID, headers []*proto.FetchHeaderEntry) {
	hs := slices.DeleteFunc(slices.Clone(headers), func(h *proto.FetchHeaderEntry) bool { return Unsafe(h.Name) })
	for range len(hs) + 1 {
		err := proto.FetchContinueRequest{RequestID: id, Headers: hs}.Call(st.tab)
		if err == nil {
			return
		}
		name := refusedHeader(err)
		n := len(hs)
		hs = slices.DeleteFunc(hs, func(h *proto.FetchHeaderEntry) bool { return name != "" && strings.EqualFold(h.Name, name) })
		if len(hs) == n {
			break
		}
	}
	_ = proto.FetchContinueRequest{RequestID: id}.Call(st.tab)
}

func (st *tabState) remove(id int) {
	st.mu.Lock()
	st.handlers = slices.DeleteFunc(st.handlers, func(x entry) bool { return x.id == id })
	last := len(st.handlers) == 0 && !st.dead
	if last {
		st.dead = true
		// Disabling first lets any request still paused go on. The loop is
		// stopped before the lock is let go, so a fresh tabState's loop
		// never shares a paused request with this one.
		_ = proto.FetchDisable{}.Call(st.tab)
		st.stop()
		st.forget()
	}
	st.mu.Unlock()
	if last {
		<-st.done
	}
}
