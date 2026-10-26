// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

// needChrome skips tests that launch a real browser where none is
// installed.
func needChrome(t *testing.T) {
	t.Helper()
	if _, _, err := Find(Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome installed:", err)
	}
}

func TestLaunchLogsTheVersion(t *testing.T) {
	needChrome(t)
	var logs []string
	b, err := Launch(context.Background(), Options{NoDownload: true, Log: func(m string) { logs = append(logs, m) }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`).MatchString(b.Version) {
		t.Errorf("Version = %q", b.Version)
	}
	if len(logs) == 0 || logs[len(logs)-1] != "Browser: Chrome "+b.Version+" ("+b.Bin+")" {
		t.Errorf("logs = %q", logs)
	}
	if strings.Contains(strings.Join(logs, "\n"), "WARNING") {
		t.Errorf("warned with the sandbox on: %q", logs)
	}
}

// Switches reach Chrome's command line, with and without a value.
func TestLaunchSetsSwitches(t *testing.T) {
	needChrome(t)
	args := func(o Options) []string {
		t.Helper()
		o.NoDownload = true
		b, err := Launch(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		res, err := proto.BrowserGetBrowserCommandLine{}.Call(b)
		if err != nil {
			t.Fatal(err)
		}
		return res.Arguments
	}
	want := []string{"--proxy-bypass-list=<-loopback>;127.0.0.1/32", "--no-pings"}
	got := args(Options{Switches: map[string]string{"proxy-bypass-list": "<-loopback>;127.0.0.1/32", "no-pings": ""}})
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s isn't on the command line %q", w, got)
		}
	}
	got = args(Options{})
	for _, w := range want {
		if slices.Contains(got, w) {
			t.Errorf("%s is on the command line without the switch", w)
		}
	}
}

// TestLaunchHintsAtTheSandboxSetting starts a "Chrome" that fails the
// way Chrome does in a container that blocks its sandbox.
func TestLaunchHintsAtTheSandboxSetting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake browser is a shell script")
	}
	old := geteuid
	geteuid = func() int { return 1000 }
	t.Cleanup(func() { geteuid = old })
	t.Setenv(NoSandboxEnv, "")
	bin := filepath.Join(t.TempDir(), "chrome")
	script := "#!/bin/sh\necho '[1:1:FATAL:zygote_host_impl_linux.cc(127)] No usable sandbox!' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // an executable test fixture
		t.Fatal(err)
	}
	_, err := Launch(t.Context(), Options{Bin: bin})
	if err == nil || !strings.Contains(err.Error(), "No usable sandbox") || !strings.Contains(err.Error(), "pass --no-sandbox, or set "+NoSandboxEnv+"=1, if this container can't give Chrome its sandbox") {
		t.Fatalf("err = %v", err)
	}
}
