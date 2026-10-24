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
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// BlockedError is how Chrome reports a request a handler blocked, in
// Network.loadingFailed's errorText.
const BlockedError = "net::ERR_BLOCKED_BY_CLIENT"

// Request is a paused request the handlers may change.
type Request struct {
	URL     *url.URL // nil when Chrome's URL doesn't parse
	Method  string
	Type    proto.NetworkResourceType
	headers []*proto.FetchHeaderEntry
	changed bool
	blocked bool
}

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

// Block fails the request as blocked by the client (BlockedError), so
// it never reaches the network.
func (r *Request) Block() { r.blocked = true }

// Blocked reports whether a handler blocked the request.
func (r *Request) Blocked() bool { return r.blocked }

// Handler sees each paused request. Handlers run in the order they were
// added, one request at a time. They must not block.
type Handler func(*Request)

type tabState struct {
	tab      *rod.Page
	handlers []entry
	stop     func()
	done     chan struct{}
}

type entry struct {
	id int
	h  Handler
}

var (
	mu   sync.Mutex
	tabs = map[proto.TargetTargetID]*tabState{}
	next int
)

// Add registers h for every request tab makes from now on, until remove
// is called. The first handler on a tab enables Fetch interception; the
// last one removed disables it, which lets any request still paused go
// on.
func Add(tab *rod.Page, h Handler) (remove func(), err error) {
	mu.Lock()
	defer mu.Unlock()
	next++
	id := next
	st := tabs[tab.TargetID]
	if st == nil {
		st, err = start(tab)
		if err != nil {
			return func() {}, err
		}
		tabs[tab.TargetID] = st
	}
	st.handlers = append(st.handlers, entry{id, h})
	var once sync.Once
	return func() { once.Do(func() { st.remove(id) }) }, nil
}

// start enables interception of every request on tab and runs the loop
// that passes paused requests through the handlers.
func start(tab *rod.Page) (*tabState, error) {
	err := proto.FetchEnable{Patterns: []*proto.FetchRequestPattern{
		{URLPattern: "*", RequestStage: proto.FetchRequestStageRequest},
	}}.Call(tab)
	if err != nil {
		return nil, err
	}
	// Its own context: the loop lives as long as a handler does, not as
	// long as whichever owner came first.
	ctx, cancel := context.WithCancel(context.Background())
	st := &tabState{tab: tab, stop: cancel, done: make(chan struct{})}
	wait := tab.Context(ctx).EachEvent(func(e *proto.FetchRequestPaused) { st.handle(e) })
	go func() { wait(); close(st.done) }()
	return st, nil
}

func (st *tabState) handle(e *proto.FetchRequestPaused) {
	mu.Lock()
	hs := slices.Clone(st.handlers)
	mu.Unlock()
	r := &Request{Method: e.Request.Method, Type: e.ResourceType}
	r.URL, _ = url.Parse(e.Request.URL)
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
		// A header Chrome refuses must not leave the request paused
		// forever: send it as it was.
		if err := (proto.FetchContinueRequest{RequestID: e.RequestID, Headers: r.headers}).Call(st.tab); err != nil {
			_ = proto.FetchContinueRequest{RequestID: e.RequestID}.Call(st.tab)
		}
	default:
		_ = proto.FetchContinueRequest{RequestID: e.RequestID}.Call(st.tab)
	}
}

func (st *tabState) remove(id int) {
	mu.Lock()
	st.handlers = slices.DeleteFunc(st.handlers, func(x entry) bool { return x.id == id })
	last := len(st.handlers) == 0 && tabs[st.tab.TargetID] == st
	if last {
		delete(tabs, st.tab.TargetID)
		// Disabling first lets any request still paused go on. It is done
		// under the lock so a handler added meanwhile can't have its
		// fresh Fetch.enable undone.
		_ = proto.FetchDisable{}.Call(st.tab)
	}
	mu.Unlock()
	if last {
		st.stop()
		<-st.done
	}
}
