// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// rodFlag is what a user could pass on to Chrome or a header and have
// rod read as its own flag: a browser monitor with no password.
const rodFlag = "--rod=monitor=127.0.0.1:20262"

// TestRodFlagOff fails if main stops importing internal/norodflag, or if
// init order ever runs rod's defaults package first.
func TestRodFlagOff(t *testing.T) {
	cmd := exec.Command(os.Args[0], "--version", "-A", rodFlag) //nolint:gosec // the test binary itself
	cmd.Env = append(cleanEnv(), rodReport+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "env=1 monitor=" {
		t.Errorf("rod read %s: %s", rodFlag, got)
	}
}

// TestRodMonitorClosed runs a real check with the flag in its arguments
// and looks for rod's monitor while the browser loads pages.
func TestRodMonitorClosed(t *testing.T) {
	needBrowser(t)
	if l, err := net.Listen("tcp", "127.0.0.1:20262"); err != nil {
		t.Skip("port 20262 is in use: ", err)
	} else {
		_ = l.Close()
	}
	var loads, open atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		loads.Add(1)
		if c, err := net.DialTimeout("tcp", "127.0.0.1:20262", time.Second); err == nil {
			_ = c.Close()
			open.Add(1)
		}
		fmt.Fprint(w, `<!doctype html><html><head><title>Home</title><link rel="icon" href="data:,"></head><body><h1>Home</h1></body></html>`)
	}))
	defer srv.Close()
	cmd, stdout, stderr := cliCommand(t, t.TempDir(), "run", srv.URL, "-A", rodFlag, "--plain", "--no-download",
		"--viewport", "desktop", "--modules", "health", "--rate", "50")
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\n%s\n%s", err, stdout, stderr)
	}
	if loads.Load() == 0 {
		t.Fatalf("the home page was never loaded\n%s\n%s", stdout, stderr)
	}
	if open.Load() > 0 {
		t.Errorf("rod's monitor was listening on 127.0.0.1:20262")
	}
}
