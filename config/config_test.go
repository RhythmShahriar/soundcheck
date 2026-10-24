// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil || c.Pages.Limit != 300 || !c.Has("visual") || c.Level("submit") {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	c, err = Load(write(t, "pages:\n  limit: 50\nvisual:\n  ignore: ['.slider']\nproduction: ['https://shop.example/']\ninteractions:\n  levels: [inspect, click, submit]\n"))
	if err != nil || c.Pages.Limit != 50 || c.Pages.PerGroup != 3 || c.Visual.Ignore[0] != ".slider" || !c.Level("submit") {
		t.Fatalf("override: %+v %v", c, err)
	}
	if !c.IsProduction("https://SHOP.example/contact/") || c.IsProduction("https://shop.example.staging.test/") {
		t.Error("IsProduction")
	}
	if _, err := Load(write(t, "")); err != nil {
		t.Errorf("empty file: %v", err)
	}
	for body, want := range map[string]string{
		"pagez:\n  limit: 5\n":           "field pagez not found",
		"viewports: [tablet]\n":          "tablet",
		"modules: [health, magic]\n":     "magic",
		"interactions:\n  levels: [x]\n": "interactions.levels",
		"tabs: 99\n":                     "tabs",
	} {
		if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", body, err, want)
		}
	}
}

func TestEcho(t *testing.T) {
	c := Default()
	if c.Has("echo") || c.Echo.URL != "" || c.Echo.TokenEnv != "RHYTHM_ECHO_TOKEN" || c.Echo.Quiet != 1500*time.Millisecond || c.Echo.Timeout != 10*time.Second {
		t.Fatalf("echo defaults: %+v", c.Echo)
	}
	c, err := Load(write(t, "echo:\n  url: http://127.0.0.1:9025\n  quiet: 2s\nmodules: [interact, echo]\n"))
	if err != nil || c.Echo.URL != "http://127.0.0.1:9025" || c.Echo.Quiet != 2*time.Second ||
		c.Echo.Timeout != 10*time.Second || c.Echo.TokenEnv != "RHYTHM_ECHO_TOKEN" || !c.Has("echo") {
		t.Fatalf("echo override: %+v %v", c.Echo, err)
	}
	for body, want := range map[string]string{
		"echo:\n  urll: x\n":                        "field urll not found",
		"echo:\n  url: 127.0.0.1:8025\n":            "isn't an http(s) URL",
		"echo:\n  url: ftp://x\n":                   "isn't an http(s) URL",
		"echo:\n  quiet: 10ms\n":                    "echo.quiet",
		"echo:\n  quiet: 5s\n  timeout: 2s\n":       "echo.timeout",
		"echo:\n  timeout: 5m\n":                    "echo.timeout",
		"echo:\n  quiet: 1500\n":                    "time.Duration",
		"echo:\n  token_env: \"A B\"\n":             "token_env",
		"modules: [health, echo]\n":                 "echo needs Echo's address",
		"echo:\n  url: http://x\nmodules: [echo]\n": "",
	} {
		_, err := Load(write(t, body))
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v", body, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: err = %v, want %q", body, err, want)
		}
	}
}
