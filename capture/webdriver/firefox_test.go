// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWaitListening(t *testing.T) {
	ctx := context.Background()
	out := "1700000 Marionette INFO Listening\nWebDriver BiDi listening on ws://127.0.0.1:41234\nmore\n"
	u, err := waitListening(ctx, strings.NewReader(out), time.Second)
	if err != nil || u != "ws://127.0.0.1:41234" {
		t.Errorf("waitListening = %q, %v", u, err)
	}
	_, err = waitListening(ctx, strings.NewReader("a\nb\nError: no DISPLAY\n"), time.Second)
	if err == nil || !strings.Contains(err.Error(), "exited before") || !strings.Contains(err.Error(), "no DISPLAY") {
		t.Errorf("an early exit = %v", err)
	}
	r, w, _ := os.Pipe()
	defer func() { _ = w.Close(); _ = r.Close() }()
	if _, err := waitListening(ctx, r, 50*time.Millisecond); err == nil || !strings.Contains(err.Error(), "in time") {
		t.Errorf("a silent browser = %v", err)
	}
}

func TestProxyPrefs(t *testing.T) {
	p := map[string]any{}
	if err := proxyPrefs(p, "http://127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	if p["network.proxy.type"] != 1 || p["network.proxy.http"] != "127.0.0.1" || p["network.proxy.ssl_port"] != 8080 ||
		p["network.proxy.allow_hijacking_localhost"] != true {
		t.Errorf("http proxy prefs = %v", p)
	}
	p = map[string]any{}
	if err := proxyPrefs(p, "socks5://10.0.0.1:1080"); err != nil || p["network.proxy.socks"] != "10.0.0.1" || p["network.proxy.socks_port"] != 1080 {
		t.Errorf("socks proxy prefs = %v, %v", p, err)
	}
	if err := proxyPrefs(map[string]any{}, "127.0.0.1"); err == nil {
		t.Error("a proxy without a port was accepted")
	}
}

func TestWritePrefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.js")
	if err := writePrefs(path, map[string]any{"a.b": true, "c": "x\"y", "n": 4}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`user_pref("a.b", true);`, `user_pref("c", "x\"y");`, `user_pref("n", 4);`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("user.js lacks %s:\n%s", want, data)
		}
	}
}

func TestFindBinaries(t *testing.T) {
	if _, err := FindFirefox("/no/such/firefox"); err == nil {
		t.Error("FindFirefox accepted a missing binary")
	}
	if _, err := FindWebKit("/no/such/WebKitWebDriver"); err == nil {
		t.Error("FindWebKit accepted a missing binary")
	}
	bin := filepath.Join(t.TempDir(), "firefox")
	_ = os.WriteFile(bin, nil, 0o600)
	if got, err := FindFirefox(bin); err != nil || got != bin {
		t.Errorf("FindFirefox(%s) = %q, %v", bin, got, err)
	}
	if hostPort("http://127.0.0.1:8080") != "127.0.0.1:8080" || hostPort("10.0.0.1:3128") != "10.0.0.1:3128" {
		t.Error("hostPort")
	}
}
