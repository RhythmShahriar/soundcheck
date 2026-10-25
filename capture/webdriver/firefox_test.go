// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestWindowsFirefox(t *testing.T) {
	x64, x86 := t.TempDir(), t.TempDir()
	env := map[string]string{"ProgramFiles": x64, "ProgramFiles(x86)": x86}
	if got := windowsFirefox(func(k string) string { return env[k] }); got != "" {
		t.Errorf("no Firefox installed: %q", got)
	}
	want := filepath.Join(x86, "Mozilla Firefox", "firefox.exe")
	_ = os.MkdirAll(filepath.Dir(want), 0o750)
	_ = os.WriteFile(want, nil, 0o600)
	if got := windowsFirefox(func(k string) string { return env[k] }); got != want {
		t.Errorf("32-bit Firefox: %q", got)
	}
	if got := windowsFirefox(func(string) string { return "" }); got != "" {
		t.Errorf("no Program Files: %q", got)
	}
}

// A Firefox whose BiDi server doesn't start in time is started again,
// firefoxTries times in all; the last slow start is the error. A binary
// that can't be run isn't tried again.
func TestLaunchFirefoxSlowStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake Firefox is a shell script")
	}
	limit, pause := firefoxStart, firefoxPause
	firefoxStart, firefoxPause = 200*time.Millisecond, time.Millisecond
	t.Cleanup(func() { firefoxStart, firefoxPause = limit, pause })
	dir := t.TempDir()
	starts := filepath.Join(dir, "starts")
	bin := filepath.Join(dir, "firefox")
	script := "#!/bin/sh\necho start >> '" + starts + "'\nexec sleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { //nolint:gosec // a test script that must run
		t.Fatal(err)
	}
	var notes []string
	_, err := LaunchFirefox(context.Background(), Options{Bin: bin, Log: func(s string) { notes = append(notes, s) }})
	if !errors.Is(err, errSlowStart) {
		t.Errorf("LaunchFirefox = %v, want errSlowStart", err)
	}
	b, _ := os.ReadFile(starts)
	if n := strings.Count(string(b), "start"); n != firefoxTries || len(notes) != firefoxTries-1 || !strings.Contains(notes[0], "starting it again") {
		t.Errorf("%d starts, notes %q", n, notes)
	}

	notes = nil
	noExec := filepath.Join(dir, "not-a-program")
	if err := os.WriteFile(noExec, []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LaunchFirefox(context.Background(), Options{Bin: noExec, Log: func(s string) { notes = append(notes, s) }}); !errors.Is(err, errCantStart) || len(notes) != 0 {
		t.Errorf("a binary that can't run: %v, notes %q", err, notes)
	}
}

// On Windows the launcher process waits for the browser, so stopping
// what Soundcheck started stops Firefox; elsewhere there is no launcher.
func TestFirefoxArgs(t *testing.T) {
	win := strings.Join(firefoxArgs("p", false, "windows"), " ")
	if !strings.Contains(win, "--wait-for-browser") || !strings.Contains(win, "--headless") || !strings.HasSuffix(win, "about:blank") {
		t.Errorf("windows: %s", win)
	}
	linux := strings.Join(firefoxArgs("p", true, "linux"), " ")
	if strings.Contains(linux, "--wait-for-browser") || strings.Contains(linux, "--headless") || !strings.Contains(linux, "--profile p") {
		t.Errorf("linux, visible: %s", linux)
	}
}
