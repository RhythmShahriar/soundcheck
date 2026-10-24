// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
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
		"pagez:\n  limit: 5\n":           `line 1: unknown setting "pagez" (did you mean "pages"?)`,
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
		"echo:\n  urll: x\n":                          `line 2: unknown setting "echo.urll" (did you mean "echo.url"?)`,
		"echo:\n  url: 127.0.0.1:8025\n":              "isn't an http(s) URL",
		"echo:\n  url: ftp://x\n":                     "isn't an http(s) URL",
		"echo:\n  quiet: 10ms\n":                      "echo.quiet",
		"echo:\n  quiet: 5s\n  timeout: 2s\n":         "echo.timeout",
		"echo:\n  timeout: 5m\n":                      "echo.timeout",
		"echo:\n  quiet: 1500\n":                      `line 2: echo.quiet: "1500" isn't a duration like 1500ms or 10s`,
		"echo:\n  token_env: \"A B\"\n":               "token_env",
		"echo:\n  token_env: AWS_SECRET_ACCESS_KEY\n": "must start with RHYTHM_ECHO_",
		"echo:\n  token_env: GITHUB_TOKEN\n":          "must start with RHYTHM_ECHO_",
		"echo:\n  token_env: RHYTHM_ECHO_CI\n":        "",
		"echo:\n  token_env: \"\"\n":                  "",
		"modules: [health, echo]\n":                   "echo needs Echo's address",
		"echo:\n  url: http://x\nmodules: [echo]\n":   "",
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

func TestLoginURL(t *testing.T) {
	for body, want := range map[string]string{
		"login:\n  url: /my-login/\n":                       "",
		"login:\n  url: https://example.com/wp-login.php\n": "", // checked against the site when logging in
		"login:\n  url: ftp://example.com/\n":               "login.url",
		"login:\n  url: \"http://\"\n":                      "login.url",
		"login:\n  url: \"://x\"\n":                         "login.url",
	} {
		_, err := Load(write(t, body))
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v", body, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: err = %v, want %q", body, err, want)
		}
	}
	if _, err := Load(write(t, "trust:\n  allow_env: [GITHUB_TOKEN]\n")); err == nil {
		t.Error("the config file must not set trust")
	}
}

func TestRate(t *testing.T) {
	for body, ok := range map[string]bool{
		"rate: .nan\n": false, "rate: .inf\n": false, "rate: -.inf\n": false, "rate: -1\n": false, "rate: 0\n": false,
		"rate: 1e-300\n": false, "rate: 0.009\n": false, "rate: 1001\n": false,
		"rate: 0.01\n": true, "rate: 1\n": true, "rate: 5\n": true, "rate: 1000\n": true,
	} {
		_, err := Load(write(t, body))
		switch {
		case ok && err != nil:
			t.Errorf("%q: %v", body, err)
		case !ok && (err == nil || !strings.Contains(err.Error(), "config.yaml line 1: rate: ") || !strings.Contains(err.Error(), "between 0.01 and 1000")):
			t.Errorf("%q: err = %v, want a rate error", body, err)
		}
	}
}

// Every rate Soundcheck accepts is one the kit's limiter accepts too.
func TestRateAgreesWithKit(t *testing.T) {
	for _, r := range []float64{MinRate, 0.5, 1, 5, MaxRate} {
		if err := CheckRate(r); err != nil {
			t.Errorf("CheckRate(%v) = %v", r, err)
		}
		if err := ratelimit.Validate(r); err != nil {
			t.Errorf("ratelimit.Validate(%v) = %v", r, err)
		}
	}
	for _, r := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, 0, 1e-300} {
		if CheckRate(r) == nil {
			t.Errorf("CheckRate(%v) = nil", r)
		}
	}
}

func TestInFile(t *testing.T) {
	c, err := Load(write(t, "modules: [health, visual, interact, cache, speed]\ninteractions:\n  max_clicks: 5\npages:\n  limit: 300\n"))
	if err != nil {
		t.Fatal(err)
	}
	// Set to the default value still counts: the user chose it.
	for _, k := range []string{"modules", "interactions", "interactions.max_clicks", "pages.limit"} {
		if !c.InFile(k) {
			t.Errorf("InFile(%q) = false", k)
		}
	}
	for _, k := range []string{"viewports", "interactions.levels", "rate"} {
		if c.InFile(k) {
			t.Errorf("InFile(%q) = true", k)
		}
	}
	for _, body := range []string{"", "# only a comment\n"} {
		c, err := Load(write(t, body))
		if err != nil || c.InFile("modules") {
			t.Errorf("%q: InFile(modules) = %t, %v", body, c.InFile("modules"), err)
		}
	}
	if c, _ := Load(filepath.Join(t.TempDir(), "missing.yaml")); c.InFile("modules") {
		t.Error("no file: InFile(modules) = true")
	}
}
