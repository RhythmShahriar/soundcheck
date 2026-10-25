// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/internal/proc"
)

// Browser is a running Firefox or WebKit, driven over WebDriver BiDi or,
// for a WebKit driver without it, classic WebDriver.
type Browser struct {
	engine capture.Engine
	bin    string
	cmd    *exec.Cmd // the browser or driver process
	// exited closes when cmd has exited, for a process whose Wait is
	// already running; nil when nothing waits for it yet.
	exited  <-chan struct{}
	profile string // a temporary profile to remove on Close

	conn *Conn // BiDi; nil for classic WebDriver
	// userContext keeps the run's cookies and storage apart from the
	// default one, like Chrome's incognito session.
	userContext string

	classic *classic // classic WebDriver; nil for BiDi
	// closeSession ends a classic session that BiDi took over.
	closeSession func()
	logf         func(string)

	// createMu keeps NewTab's windows apart: see createContext.
	createMu  sync.Mutex
	closeOnce sync.Once
}

var _ capture.Driver = (*Browser)(nil)

// Engine is the browser's engine.
func (b *Browser) Engine() capture.Engine { return b.engine }

// Bin is the binary that was started: the browser, or WebKit's driver.
func (b *Browser) Bin() string { return b.bin }

// BiDi reports whether the browser is driven over WebDriver BiDi. Without
// it (classic WebDriver), tabs send no events and can't scope headers.
func (b *Browser) BiDi() bool { return b.conn != nil }

// newSession starts the BiDi session and the user context the tabs open
// in.
func (b *Browser) newSession(ctx context.Context, insecure bool) error {
	caps := map[string]any{"alwaysMatch": map[string]any{"acceptInsecureCerts": insecure, "unhandledPromptBehavior": "ignore"}}
	if err := b.conn.Call(ctx, "session.new", map[string]any{"capabilities": caps}, nil); err != nil {
		return err
	}
	return b.createUserContext(ctx)
}

func (b *Browser) createUserContext(ctx context.Context) error {
	var uc struct {
		UserContext string `json:"userContext"`
	}
	err := b.conn.Call(ctx, "browser.createUserContext", nil, &uc)
	if Unsupported(err) {
		return nil // the default user context, then
	}
	if err != nil {
		return err
	}
	b.userContext = uc.UserContext
	return nil
}

// NewTab opens a blank tab in a window of its own: a page in a
// background tab may paint and run timers less often.
func (b *Browser) NewTab(ctx context.Context) (capture.Tab, error) {
	if b.classic != nil {
		return b.classic.newTab(ctx)
	}
	id, err := b.createContext(ctx)
	if err != nil {
		return nil, err
	}
	t := newTab(b.conn, id, b.userContext)
	if err := t.subscribe(ctx); err != nil {
		_ = t.Close()
		return nil, err
	}
	return t, nil
}

// createContext opens the window behind a new tab and returns its
// context.
//
// The window opens with "background": true, which only means it doesn't
// take the focus. A foreground create makes Firefox wait for the new
// page to turn visible. It gives the page's first document 100 ms to
// appear before it calls the context discarded: on a slow machine
// (Windows runners) that fails with "no such frame" while the window is
// fine. waitShown does that wait instead, with time to spare.
//
// A Firefox that has just started can still be swapping out its first
// window. A new window can also move to another process before the
// command returns. Either way browsingContext.create fails with "no
// such frame" (the context was discarded), often with the window open
// all the same. So after each such failure createContext looks for a
// window of its user context that wasn't there before and takes it,
// closing any other a failed try left behind. Without one it tries
// again for a few seconds. On a slow machine (Windows runners) it can
// keep failing: the last try opens a tab in a window that is already
// there, in front.
func (b *Browser) createContext(ctx context.Context) (string, error) {
	// One at a time, so a window another NewTab opened is never taken.
	b.createMu.Lock()
	defer b.createMu.Unlock()
	params := map[string]any{"type": "window", "background": true}
	if b.userContext != "" {
		params["userContext"] = b.userContext
	}
	before, treeErr := b.topContexts(ctx)
	var res struct {
		Context string `json:"context"`
	}
	err := b.conn.Call(ctx, "browsingContext.create", params, &res)
	for try := 1; err != nil && discarded(err) && try <= newTabTries; try++ {
		select {
		case <-ctx.Done():
			return "", err
		case <-time.After(time.Duration(try) * newTabPause):
		}
		if treeErr == nil {
			if id := b.adoptNew(ctx, before); id != "" {
				return id, b.waitShown(ctx, id)
			}
		}
		if try == newTabTries {
			break
		}
		if try == newTabTries-1 {
			// A tab in the background would stay hidden.
			params["type"], params["background"] = "tab", false
		}
		err = b.conn.Call(ctx, "browsingContext.create", params, &res)
	}
	if err != nil {
		return "", err
	}
	return res.Context, b.waitShown(ctx, res.Context)
}

// waitShown waits until the page in context id is no longer hidden, for
// up to shownWait. Until its first document is there, Firefox answers
// "no such frame"; a page that stays hidden is used all the same.
func (b *Browser) waitShown(ctx context.Context, id string) error {
	deadline := time.Now().Add(shownWait)
	for {
		var res struct {
			Result struct {
				Value any `json:"value"`
			} `json:"result"`
		}
		err := b.conn.Call(ctx, "script.evaluate", map[string]any{
			"expression": "document.visibilityState", "awaitPromise": false,
			"target": map[string]string{"context": id},
		}, &res)
		switch {
		case err != nil && !discarded(err):
			return err
		case err == nil && res.Result.Value != "hidden":
			return nil
		case time.Now().After(deadline):
			if err != nil {
				return err
			}
			b.log("a new window is still hidden; using it all the same")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(shownPause):
		}
	}
}

// shownWait bounds waitShown; it asks again every shownPause.
var (
	shownWait  = 10 * time.Second
	shownPause = 50 * time.Millisecond
)

// newTabTries and newTabPause bound NewTab's tries of a discarded
// context: the pause grows by newTabPause after each one, about 7s in all.
var (
	newTabTries = 7
	newTabPause = 250 * time.Millisecond
)

// topContexts returns the top-level contexts (windows and tabs) of the
// browser's user context.
func (b *Browser) topContexts(ctx context.Context) (map[string]bool, error) {
	var res struct {
		Contexts []struct {
			Context     string `json:"context"`
			UserContext string `json:"userContext"`
		} `json:"contexts"`
	}
	if err := b.conn.Call(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0}, &res); err != nil {
		return nil, err
	}
	want := b.userContext
	if want == "" {
		want = "default"
	}
	ids := map[string]bool{}
	for _, c := range res.Contexts {
		if c.UserContext == want || c.UserContext == "" {
			ids[c.Context] = true
		}
	}
	return ids, nil
}

// adoptNew returns a top-level context of the browser's user context
// that isn't in before, or "" when there is none. Any others that
// appeared since are closed: failed tries opened them.
func (b *Browser) adoptNew(ctx context.Context, before map[string]bool) string {
	now, err := b.topContexts(ctx)
	if err != nil {
		return ""
	}
	var ids []string
	for id := range now {
		if !before[id] {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	for _, id := range ids[1:] {
		_ = b.conn.Call(ctx, "browsingContext.close", map[string]any{"context": id}, nil)
	}
	return ids[0]
}

// discarded reports whether err says the browsing context it used is gone.
func discarded(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == "no such frame"
}

// Close ends the session and stops the browser, then removes its
// temporary profile.
func (b *Browser) Close() error {
	b.closeOnce.Do(func() {
		if b.conn != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = b.conn.Call(ctx, "browser.close", nil, nil)
			cancel()
			_ = b.conn.Close()
		}
		if b.closeSession != nil {
			b.closeSession()
		}
		if b.classic != nil {
			b.classic.deleteSession()
		}
		if b.cmd != nil && b.cmd.Process != nil {
			// Firefox exits after browser.close; a WebDriver server
			// would keep running.
			stop(b.cmd, b.exited, b.engine == capture.Firefox)
		}
		if b.profile != "" {
			_ = os.RemoveAll(b.profile)
		}
	})
	return nil
}

// stop ends the process and the processes it started: with wait it gets
// a moment to exit by itself first. exited closes when the process has
// exited; when it is nil stop waits for the process itself.
func stop(cmd *exec.Cmd, exited <-chan struct{}, wait bool) {
	if exited == nil {
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		exited = done
	}
	if wait {
		select {
		case <-exited:
			return
		case <-time.After(3 * time.Second):
		}
	}
	_ = proc.KillTree(cmd.Process)
	<-exited
}

func (b *Browser) log(msg string) {
	if b.logf != nil {
		b.logf(msg)
	}
}
