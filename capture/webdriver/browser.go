// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/RhythmShahriar/soundcheck/capture"
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
	params := map[string]any{"type": "window", "background": false}
	if b.userContext != "" {
		params["userContext"] = b.userContext
	}
	var res struct {
		Context string `json:"context"`
	}
	if err := b.conn.Call(ctx, "browsingContext.create", params, &res); err != nil {
		return nil, err
	}
	t := newTab(b.conn, res.Context, b.userContext)
	if err := t.subscribe(ctx); err != nil {
		_ = t.Close()
		return nil, err
	}
	return t, nil
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

// stop ends the process: with wait it gets a moment to exit by itself
// first. exited closes when the process has exited; when it is nil stop
// waits for the process itself.
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
	_ = cmd.Process.Kill()
	<-exited
}

func (b *Browser) log(msg string) {
	if b.logf != nil {
		b.logf(msg)
	}
}
