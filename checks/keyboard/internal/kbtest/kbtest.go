// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package kbtest holds the helpers of the keyboard module's browser
// tests that live in packages of their own (mouseonly, pagewide): test
// sites on 127.0.0.1, a Chrome per test and the module run on a tab.
package kbtest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// NeedBrowser skips the test with -short or without Chrome.
func NeedBrowser(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// Serve starts h on the first free port of 127.0.0.1:40500-40599.
func Serve(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	for port := 40500; port < 40600; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv := httptest.NewUnstartedServer(h)
		_ = srv.Listener.Close()
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	t.Fatal("no free port in 40500-40599")
	return nil
}

// Pages serves each path's HTML and counts the loads of each.
type Pages struct {
	HTML  map[string]string
	mu    sync.Mutex
	loads map[string]int
}

func (s *Pages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, ok := s.HTML[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	if s.loads == nil {
		s.loads = map[string]int{}
	}
	s.loads[r.URL.Path]++
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, body)
}

// Count is how many times path was loaded.
func (s *Pages) Count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads[path]
}

// Chrome is one browser for a test, closed with it.
type Chrome struct {
	t    *testing.T
	sess *rod.Browser
}

// Launch starts a Chrome for the test, closed with it.
func Launch(t *testing.T) *Chrome {
	t.Helper()
	NeedBrowser(t)
	// The browser lives as long as its context: the whole test.
	ctx, cancel := context.WithCancel(context.Background())
	b, err := browser.Launch(ctx, browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(); cancel() })
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	return &Chrome{t: t, sess: sess}
}

// Open loads raw in a new foreground tab and returns it with the page
// record the runner would have made.
func (c *Chrome) Open(raw string) (*rod.Page, *model.Page) {
	c.t.Helper()
	tab, err := browser.NewTab(c.sess)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { _ = tab.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(20*time.Second))
	defer cancel()
	if err := tab.Context(ctx).Navigate(raw); err != nil {
		c.t.Fatal(err)
	}
	if err := tab.Context(ctx).WaitLoad(); err != nil {
		c.t.Fatal(err)
	}
	path := strings.TrimPrefix(raw, "http://")
	path = path[strings.Index(path, "/"):]
	return tab, &model.Page{ID: model.PageID(raw), URL: raw, Path: path, Status: 200}
}

// RunContext is a check.Context that stores files in a temporary run
// folder and collects warnings.
func RunContext(t *testing.T) *check.Context {
	t.Helper()
	cc := &check.Context{Run: &model.Run{}, Dir: t.TempDir()}
	cc.Save = store.Saver(cc.Dir)
	return cc
}

// Links is a clean page with n links and no trap.
func Links(n int, extra string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><title>Links</title></head><body><main>`)
	for i := range n {
		fmt.Fprintf(&b, `<a href="#s%d" id="l%d">Link %d</a> `, i, i, i)
	}
	b.WriteString(extra + `</main></body></html>`)
	return b.String()
}

// Live runs the module on the open tab, as the runner would.
func Live(t *testing.T, m *keyboard.Module, cc *check.Context, tab *rod.Page, p *model.Page) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(90*time.Second))
	defer cancel()
	m.Live(ctx, cc, tab, p)
}

// Reports reports whether p has the issue with key.
func Reports(p *model.Page, key string) bool {
	return slices.ContainsFunc(p.Issues, func(i model.Issue) bool {
		if i.Key == "" {
			i.Identify()
		}
		return i.Key == key
	})
}

// NoResponse is true for a press that did nothing the walk can tie to it.
func NoResponse(effect string) bool { return effect == "" || effect == keyboard.PressUnknown }
