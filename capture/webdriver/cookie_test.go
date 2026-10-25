// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/internal/testenv"
)

// A --cookie is set for the site's host alone, as in Chrome: a
// subdomain doesn't get it.
func TestFirefoxCookieIsHostOnly(t *testing.T) {
	b := launchFirefox(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	var mu sync.Mutex
	got := map[string]string{} // host and path → Cookie
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.Host+r.URL.Path] = r.Header.Get("Cookie")
		mu.Unlock()
		// Firefox resolves every *.localhost name to the loopback.
		fmt.Fprintf(w, `<!doctype html><img src="http://sub.localhost:%d/sub.png"><img src="/own.png">`, port)
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(30*time.Second))
	defer cancel()
	tab, err := b.NewTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tab.Close() }()
	site, _ := url.Parse(fmt.Sprintf("http://localhost:%d/", port))
	release, err := tab.ScopeHeaders(ctx, site, map[string]string{"Cookie": "gate=1"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := tab.Navigate(ctx, site.String()); err != nil {
		t.Fatal(err)
	}
	own, sub := fmt.Sprintf("localhost:%d/own.png", port), fmt.Sprintf("sub.localhost:%d/sub.png", port)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		_, a := got[own]
		_, b := got[sub]
		mu.Unlock()
		if a && b || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if got[own] != "gate=1" {
		t.Errorf("the site: %q (requests %v)", got[own], got)
	}
	if c, ok := got[sub]; !ok || c != "" {
		t.Errorf("a subdomain: %q, %v (requests %v)", c, ok, got)
	}
}
