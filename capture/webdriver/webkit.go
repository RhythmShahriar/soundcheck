// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/RhythmShahriar/soundcheck/capture"
)

// webkitDrivers are WebKit's WebDriver servers looked for on PATH:
// WebKitGTK's and WPE's on Linux, Safari's on macOS.
var webkitDrivers = []string{"WebKitWebDriver", "WPEWebDriver", "safaridriver"}

// FindWebKit returns the WebKit driver to run: bin when set, or the
// first one on PATH. Soundcheck never downloads one.
func FindWebKit(bin string) (string, error) {
	if bin != "" {
		if _, err := os.Stat(bin); err != nil {
			return "", fmt.Errorf("webkit driver %s: %w", bin, err)
		}
		return bin, nil
	}
	for _, n := range webkitDrivers {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no WebKit driver found; install WebKitWebDriver (Linux) or enable safaridriver (macOS), or pass the path to it")
}

// LaunchWebKit starts a WebKit driver on a free local port and opens a
// session through it, asking for WebDriver BiDi. A driver that doesn't
// offer BiDi is driven with classic WebDriver: navigation, scripts and
// viewport screenshots, without events.
func LaunchWebKit(ctx context.Context, opts Options) (*Browser, error) {
	bin, err := FindWebKit(opts.Bin)
	if err != nil {
		return nil, err
	}
	b, base, err := startDriver(ctx, bin, opts.Log)
	if err != nil {
		return nil, err
	}
	always := map[string]any{"webSocketUrl": true, "acceptInsecureCerts": opts.Insecure}
	if filepath.Base(bin) != "safaridriver" && !opts.Visible {
		// Safari has no headless mode; WebKitGTK's MiniBrowser has.
		always["webkitgtk:browserOptions"] = map[string]any{"args": []string{"--automation", "--headless"}}
	}
	if opts.Proxy != "" {
		always["proxy"] = map[string]any{"proxyType": "manual", "httpProxy": hostPort(opts.Proxy), "sslProxy": hostPort(opts.Proxy)}
	}
	if err := b.startSession(ctx, base, always); err != nil {
		_ = b.Close()
		return nil, err
	}
	return b, nil
}

// startSession is openSession, tried up to twice more when the driver
// couldn't create the session or dropped the request. safaridriver gives
// up when Safari is slow to start ("The session timed out while
// connecting to a Safari instance") and closes the connection while the
// Safari of a session that just ended is still shutting down; a later
// try finds it ready.
func (b *Browser) startSession(ctx context.Context, base string, always map[string]any) error {
	for try := 1; ; try++ {
		err := b.openSession(ctx, base, always)
		if err == nil || try == sessionTries || ctx.Err() != nil || !retrySession(err) {
			return err
		}
		b.log("the WebKit driver couldn't create a session (" + err.Error() + "); trying again")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.exited:
			return err
		case <-time.After(time.Duration(try) * sessionPause):
		}
	}
}

// sessionTries and sessionPause bound startSession's tries: the pause
// grows by sessionPause after each one.
var (
	sessionTries = 3
	sessionPause = time.Second
)

// retrySession reports whether a new session may work on a second try:
// the driver said it couldn't create one, or the connection closed before
// it answered.
func retrySession(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Code == "session not created"
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET)
}

// errExited is a driver that exited before its server answered.
var errExited = errors.New("it exited before its WebDriver server answered")

// startDriver starts the WebKit driver bin on a free local port and waits
// for its server to answer, returning it with the server's base URL. The
// port is free when it is chosen. Another process may take it before the
// driver binds it, so a driver that exits before it answers is started
// once more on another port.
func startDriver(ctx context.Context, bin string, logf func(string)) (*Browser, string, error) {
	for try := 1; ; try++ {
		port, err := freePort()
		if err != nil {
			return nil, "", err
		}
		args := []string{"--port=" + strconv.Itoa(port)}
		if filepath.Base(bin) == "safaridriver" {
			args = []string{"-p", strconv.Itoa(port)}
		}
		cmd := exec.Command(bin, args...) //nolint:gosec // the user's own driver binary
		if err := cmd.Start(); err != nil {
			return nil, "", fmt.Errorf("starting %s: %w", bin, err)
		}
		exited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		b := &Browser{engine: capture.WebKit, bin: bin, cmd: cmd, exited: exited, logf: logf}
		base := "http://127.0.0.1:" + strconv.Itoa(port)
		err = waitReady(ctx, base, 20*time.Second, exited)
		if err == nil {
			return b, base, nil
		}
		_ = b.Close()
		if !errors.Is(err, errExited) || try == 2 {
			return nil, "", fmt.Errorf("starting %s: %w", bin, err)
		}
		b.log(fmt.Sprintf("%s exited on port %d; trying another port", filepath.Base(bin), port))
	}
}

// openSession creates a classic session and, when the driver returns a
// webSocketUrl, connects to it for BiDi.
func (b *Browser) openSession(ctx context.Context, base string, always map[string]any) error {
	c := &classic{base: base, http: &http.Client{}}
	var res struct {
		SessionID    string `json:"sessionId"`
		Capabilities struct {
			WebSocketURL any `json:"webSocketUrl"`
		} `json:"capabilities"`
	}
	if err := c.do(ctx, http.MethodPost, "/session", map[string]any{"capabilities": map[string]any{"alwaysMatch": always}}, &res); err != nil {
		return err
	}
	c.session = res.SessionID
	if ws, ok := res.Capabilities.WebSocketURL.(string); ok && ws != "" {
		conn, err := Dial(ctx, ws)
		if err == nil {
			b.conn = conn
			b.classic = nil
			b.closeSession = func() { c.deleteSession() }
			return b.createUserContext(ctx)
		}
		b.log("BiDi unavailable (" + err.Error() + "); using classic WebDriver")
	}
	c.tabs = map[string]bool{}
	b.classic = c
	return nil
}

// freePort asks the system for a free local port.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitReady polls a WebDriver server's /status until it answers, or
// until exited closes: the driver is gone.
func waitReady(ctx context.Context, base string, limit time.Duration, exited <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	c := &classic{base: base, http: &http.Client{Timeout: time.Second}}
	for {
		if err := c.do(ctx, http.MethodGet, "/status", nil, nil); err == nil {
			return nil
		}
		select {
		case <-exited:
			return errExited
		case <-ctx.Done():
			return errors.New("its WebDriver server didn't answer in time")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// hostPort is proxy's host:port, as WebDriver's proxy capability wants
// it; proxy may be a URL or already host:port.
func hostPort(proxy string) string {
	if u, err := url.Parse(proxy); err == nil && u.Host != "" {
		return u.Host
	}
	return proxy
}
