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
	want := []string{"--proxy-bypass-list=<-loopback>;127.0.0.1/32", "--disable-notifications"}
	got := args(Options{Switches: map[string]string{"proxy-bypass-list": "<-loopback>;127.0.0.1/32", "disable-notifications": ""}})
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

// Every launch carries the switches that keep Chrome's own services
// off the network. Resolver rules given as a switch are kept ahead
// of Soundcheck's own.
func TestLaunchKeepsChromeQuiet(t *testing.T) {
	needChrome(t)
	b, err := Launch(context.Background(), Options{NoDownload: true,
		Switches: map[string]string{"host-resolver-rules": "MAP example.test 127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	res, err := proto.BrowserGetBrowserCommandLine{}.Call(b)
	if err != nil {
		t.Fatal(err)
	}
	got := res.Arguments
	for _, w := range []string{"--disable-component-update", "--disable-domain-reliability", "--no-pings",
		"--gcm-checkin-url=" + nowhere, "--gaia-url=" + nowhere, "--component-updater=url-source=" + nowhere} {
		if !slices.Contains(got, w) {
			t.Errorf("%s isn't on the command line %q", w, got)
		}
	}
	var rules, features string
	for _, a := range got {
		if v, ok := strings.CutPrefix(a, "--host-resolver-rules="); ok {
			rules = v
		}
		if v, ok := strings.CutPrefix(a, "--disable-features="); ok {
			features = v
		}
	}
	if !strings.HasPrefix(rules, "MAP example.test 127.0.0.1, ") || !strings.Contains(rules, "MAP update.googleapis.com ~NOTFOUND") {
		t.Errorf("host-resolver-rules = %q", rules)
	}
	for _, f := range []string{"PushMessaging", "MediaRouter", "OptimizationGuideModelDownloading",
		"AutofillServerCommunication", "CertificateTransparencyComponentUpdater", "DnsOverHttps"} {
		if !slices.Contains(strings.Split(features, ","), f) {
			t.Errorf("%s isn't in --disable-features=%s", f, features)
		}
	}
}

func TestResolverRules(t *testing.T) {
	got := resolverRules("")
	if !strings.HasPrefix(got, "MAP update.googleapis.com ~NOTFOUND, ") || strings.Count(got, "MAP ") != len(serviceHosts) {
		t.Errorf("resolverRules(\"\") = %q", got)
	}
	if got := resolverRules("MAP a.test 127.0.0.1"); !strings.HasPrefix(got, "MAP a.test 127.0.0.1, MAP update.googleapis.com") {
		t.Errorf("given rules aren't first: %q", got)
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
