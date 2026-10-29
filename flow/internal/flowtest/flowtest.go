// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package flowtest holds the helpers the flow package's browser tests
// share with the key flow tests in package keyflow: a Chrome of its own
// per test, warmed up first, plus test sites on 127.0.0.1.
package flowtest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
	"github.com/go-rod/rod"
)

// Listen serves h on a free port of 20360-20379, the ports the flow
// package's key flow tests use, so they don't take another suite's.
func Listen(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	for port := 20360; port <= 20379; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	t.Fatal("ports 20360-20379 are all taken")
	return nil
}

// NeedChrome skips the test with -short or without Chrome.
func NeedChrome(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// CanaryEnv is a variable a flow may read (SOUNDCHECK_*) holding a
// password assembled at run time, so no literal secret is in the source.
const CanaryEnv = "SOUNDCHECK_KEYS_CANARY"

// Canary is its value.
var Canary = "Cn" + "ry-" + strings.Repeat("7", 6) + "-pw"

// IsolatedTab starts a Chrome of its own for one subtest and opens a tab
// in it, warmed up on warm (see WarmTab). On a Windows runner a subtest
// whose tab was closed without navigating left the next tab in the same
// Chrome unable to load its first page, so the subtests don't share a
// browser.
func IsolatedTab(t *testing.T, warm string) *rod.Page {
	t.Helper()
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return WarmTab(t, b, warm)
}

// WarmTab opens a tab in b and loads warm in it before the flow runs.
//
// The first page a fresh Chrome loads starts its renderer and network
// stack. On a busy Windows runner that has taken longer than any step
// timeout, sometimes with no request sent at all. So the flow's own
// start page failed with "step 0: context deadline exceeded". Here the
// first load gets a budget of its own. A tab that doesn't load in time
// is closed and a new one tried, as a stuck first navigation doesn't
// recover. warm is on the test's own server; the flow's pages are on
// 127.0.0.1 too, the same site to Chrome, so step 0 still opens a page
// through Run without the cold start.
func WarmTab(t *testing.T, b *browser.Browser, warm string) *rod.Page {
	t.Helper()
	var err error
	for try := 1; try <= 3; try++ {
		var tab *rod.Page
		if tab, err = browser.NewTab(b.Browser); err != nil {
			continue
		}
		start := time.Now()
		p := tab.Timeout(testenv.Timeout(10 * time.Second))
		if err = p.Navigate(warm); err == nil {
			err = p.WaitLoad()
		}
		if err == nil {
			if d := time.Since(start); d > 5*time.Second {
				t.Logf("the first page took %s to load", d.Round(time.Millisecond))
			}
			t.Cleanup(func() { _ = tab.Close() })
			return tab
		}
		t.Logf("warming up the tab, try %d: %v", try, err)
		_ = tab.Close()
	}
	t.Fatalf("Chrome loaded no page: %v", err)
	return nil
}

// HostOf is the host of a URL.
func HostOf(raw string) string { u, _ := url.Parse(raw); return u.Host }
