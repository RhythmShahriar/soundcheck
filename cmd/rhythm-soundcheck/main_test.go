// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() {
	// No update checks or first-run notices from tests.
	os.Setenv("RHYTHM_NO_UPDATE_CHECK", "1")
	version = "1.0.0-test"
}

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersionAndHelp(t *testing.T) {
	for _, a := range []string{"--version", "-v", "version"} {
		code, out, _ := runCLI(t, a)
		if code != exitOK || strings.TrimSpace(out) != "rhythm-soundcheck 1.0.0-test" {
			t.Errorf("%s: %d %q", a, code, out)
		}
	}
	code, out, _ := runCLI(t, "--help")
	if code != exitOK || !strings.Contains(out, "rhythm soundcheck snapshot <url>") {
		t.Errorf("help: %d", code)
	}
	if code, _, _ := runCLI(t, "frobnicate"); code != exitUsage {
		t.Errorf("unknown command: %d", code)
	}
}

func TestUsageErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"snapshot"}, "snapshot needs one site"},
		{[]string{"snapshot", "a.test", "--json", "--md"}, "pick one output format"},
		{[]string{"snapshot", "a.test", "-o", "x.pdf"}, "can't tell the format"},
		{[]string{"snapshot", "a.test", "--level", "explode"}, "--level"},
		{[]string{"snapshot", "a.test", "--skip", "nope"}, "--skip"},
		{[]string{"snapshot", "a.test", "--tabs", "99"}, "--tabs"},
		{[]string{"snapshot", "a.test", "-H", "bad header"}, "bad header"},
		{[]string{"snapshot", "a.test", "--proxy", "ftp://x"}, "bad --proxy"},
		{[]string{"snapshot", "a.test", "--name", "../x"}, "--name"},
		{[]string{"snapshot", "a.test", "--fail-on", "broke"}, "--fail-on"},
		{[]string{"compare"}, "no snapshot yet"},
		{[]string{"compare", "--fail-on", "fixed"}, "--fail-on"},
		{[]string{"staging.test"}, "to compare two sites"},
		{[]string{"a.test", "b.test", "--against", "c.test"}, "--against compares one site"},
		{[]string{"snapshot", "a.test", "--config", "missing.yaml"}, "--config"},
	} {
		code, _, errOut := runCLI(t, tc.args...)
		if code != exitUsage || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errOut, tc.want)
		}
	}
}

func TestConfigTemplateLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(configTemplate), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	def := config.Default()
	if got.Pages.Limit != def.Pages.Limit || got.Rate != def.Rate || got.Visual.Threshold != def.Visual.Threshold ||
		len(got.Modules) != len(def.Modules) || got.Flows != def.Flows || got.Echo != def.Echo {
		t.Errorf("the template's values must match the defaults:\n%+v\n%+v", got, def)
	}
}

func TestInit(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, errOut := runCLI(t, "init"); code != exitOK {
		t.Fatal(errOut)
	}
	if code, _, _ := runCLI(t, "init"); code != exitUsage {
		t.Error("init must not overwrite without --force")
	}
	if code, _, _ := runCLI(t, "init", "--force"); code != exitOK {
		t.Error("init --force")
	}
}

func TestFlagsOverrideConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	_ = os.MkdirAll(".soundcheck", 0o755)
	_ = os.WriteFile(".soundcheck/config.yaml", []byte("rate: 2\nmodules: [health, visual]\n"), 0o600)
	p, err := parse([]string{"a.test", "--skip", "visual", "--tabs", "2", "--level", "inspect", "--cookie", "a=b", "-H", "X-Test: 1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.cfg.Rate != 2 || strings.Join(p.cfg.Modules, ",") != "health" || p.cfg.Tabs != 2 ||
		strings.Join(p.cfg.Interactions.Levels, ",") != "inspect" {
		t.Errorf("cfg = %+v", p.cfg)
	}
	if p.headers["Cookie"] != "a=b" || p.headers["X-Test"] != "1" {
		t.Errorf("headers = %v", p.headers)
	}
	if !strings.HasPrefix(p.ua, "rhythm-soundcheck/1.0.0-test") {
		t.Errorf("ua = %q", p.ua)
	}
}

func TestEchoFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	for _, tc := range []struct {
		env  string
		args []string
		url  string // "" means Echo stays off
	}{
		{"", []string{"a.test"}, ""},
		{"", []string{"a.test", "--echo"}, config.EchoURL},
		{"", []string{"--echo", "a.test"}, config.EchoURL}, // a bare --echo never eats the site
		{"", []string{"a.test", "--echo=http://127.0.0.1:9025"}, "http://127.0.0.1:9025"},
		{"", []string{"a.test", "--modules", "health", "--echo"}, config.EchoURL},
		{"1", []string{"a.test"}, config.EchoURL},
		{"http://10.0.0.5:8025", []string{"a.test"}, "http://10.0.0.5:8025"},
		{"off", []string{"a.test"}, ""},
		{"1", []string{"a.test", "--echo=false"}, ""},
		{"1", []string{"a.test", "--skip", "echo"}, ""},
		{"1", []string{"a.test", "--echo=http://127.0.0.1:9"}, "http://127.0.0.1:9"},
	} {
		t.Setenv("SOUNDCHECK_ECHO", tc.env)
		p, err := parse(tc.args)
		if err != nil {
			t.Errorf("%q %v: %v", tc.env, tc.args, err)
			continue
		}
		if p.cfg.Echo.URL != tc.url || p.cfg.Has("echo") != (tc.url != "") || len(p.args) != 1 || p.args[0] != "a.test" {
			t.Errorf("%q %v: url %q, modules %v, args %v; want %q", tc.env, tc.args, p.cfg.Echo.URL, p.cfg.Modules, p.args, tc.url)
		}
	}
	t.Setenv("SOUNDCHECK_ECHO", "")
	for args, want := range map[string]string{
		"a.test --modules echo":                "--echo",
		"a.test --echo=127.0.0.1:80":           "--echo (echo.url)",
		"compare --echo http://127.0.0.1:8025": `--echo takes its address with "=": --echo=http://127.0.0.1:8025`,
	} {
		if _, err := parse(strings.Fields(args)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", args, err, want)
		}
	}

	// Configured in the file, Echo is on even when modules lists the others.
	_ = os.MkdirAll(".soundcheck", 0o755)
	_ = os.WriteFile(".soundcheck/config.yaml", []byte("modules: [health, interact]\necho:\n  url: http://127.0.0.1:9100\n"), 0o600)
	p, err := parse([]string{"a.test"})
	if err != nil || strings.Join(p.cfg.Modules, ",") != "health,interact,echo" {
		t.Errorf("from the config: %v %v", p.cfg.Modules, err)
	}
}

func TestEchoNotRunning(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html></html>") }))
	defer srv.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	code, _, errOut := runCLI(t, "snapshot", srv.URL, "--echo="+dead.URL, "--plain")
	if code != exitUsage || !strings.Contains(errOut, "start it with: rhythm echo up") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(".soundcheck/snapshots"); err == nil {
		entries, _ := os.ReadDir(".soundcheck/snapshots")
		if len(entries) > 0 {
			t.Errorf("a snapshot was saved without Echo: %v", entries)
		}
	}
}

func needBrowser(t *testing.T) {
	t.Helper()
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// shop serves a small site that breaks the way a bad update would.
func shop(broken *atomic.Bool) *httptest.Server {
	page := func(w http.ResponseWriter, title, body string) {
		fmt.Fprintf(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width"><title>%s</title>
<link rel="icon" href="data:,"></head><body><nav><a href="/">Home</a> <a href="/shop/">Shop</a> <a href="/about/">About</a></nav>%s</body></html>`, title, body)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		page(w, "Brew", "<h1>Welcome</h1><p>Fresh coffee, roasted daily.</p>")
	})
	mux.HandleFunc("/shop/", func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			page(w, "WordPress › Error", "<p>There has been a critical error on this website.</p>")
			return
		}
		page(w, "Shop", "<h1>Shop</h1><p>Beans and grinders.</p>")
	})
	mux.HandleFunc("/about/", func(w http.ResponseWriter, r *http.Request) {
		body := "<h1>About</h1><p>Since 2009.</p>"
		if broken.Load() {
			body += `<br /><b>Deprecated</b>:  Creation of dynamic property Brew::$x is deprecated in <b>/var/www/html/wp-content/plugins/brew-tools/brew.php</b> on line <b>40</b><br />`
		}
		page(w, "About", body)
	})
	return httptest.NewServer(mux)
}

func TestSnapshotThenCompare(t *testing.T) {
	needBrowser(t)
	t.Chdir(t.TempDir())
	var broken atomic.Bool
	srv := shop(&broken)
	defer srv.Close()
	common := []string{"--no-download", "--viewport", "desktop", "--modules", "health", "--rate", "50"}

	code, out, errOut := runCLI(t, append([]string{"snapshot", srv.URL, "--name", "good", "--plain"}, common...)...)
	if code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, `Snapshot "good" saved`) {
		t.Errorf("snapshot output:\n%s", out)
	}
	if _, err := os.Stat(".soundcheck/snapshots/good/run.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(".soundcheck/.incoming/good"); err == nil {
		t.Error("the incoming folder should be gone")
	}
	code, out, _ = runCLI(t, "snapshots")
	if code != exitOK || !strings.Contains(out, "good") || !strings.Contains(out, "(latest)") {
		t.Errorf("snapshots:\n%s", out)
	}

	// Nothing changed: exit 0 even with --fail-on new.
	code, out, errOut = runCLI(t, append([]string{"compare", "--fail-on", "new", "--plain"}, common...)...)
	if code != exitOK || !strings.Contains(out, "Nothing changed") {
		t.Fatalf("unchanged compare: exit %d\n%s\n%s", code, out, errOut)
	}

	broken.Store(true)
	code, out, errOut = runCLI(t, append([]string{"compare", "--fail-on", "broke", "--json", "-o", "result.json", "--plain"}, common...)...)
	if code != exitFindings {
		t.Fatalf("broken compare: exit %d, want 1\n%s\n%s", code, out, errOut)
	}
	data, err := os.ReadFile("result.json")
	if err != nil {
		t.Fatal(err)
	}
	var c model.Comparison
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	var classes []string
	for _, ch := range c.Changes {
		classes = append(classes, ch.Class+":"+ch.Title)
	}
	got := strings.Join(classes, "\n")
	if c.Count(model.Broke) == 0 || !strings.Contains(got, "broke:") || !strings.Contains(got, "Deprecated") {
		t.Errorf("want BROKE for /shop/ and NEW for the deprecation:\n%s", got)
	}
	if !strings.Contains(out, "[BROKE]") || !strings.Contains(out, "Something broke") {
		t.Errorf("live view alongside -o:\n%s", out)
	}

	// The run folder has the comparison and a standalone HTML report.
	latest, err := filepath.Glob(".soundcheck/runs/*/comparison.json")
	if err != nil || len(latest) != 2 {
		t.Fatalf("runs: %v %v", latest, err)
	}
	html, err := os.ReadFile(filepath.Join(filepath.Dir(latest[1]), "report.html"))
	if err != nil || !bytes.Contains(html, []byte("Something broke")) {
		t.Errorf("report.html: %v", err)
	}

	// report re-renders the last result in any format.
	code, out, _ = runCLI(t, "report", "--junit")
	var suites struct {
		Failures int `xml:"failures,attr"`
	}
	if code != exitOK || xml.Unmarshal([]byte(out), &suites) != nil || suites.Failures == 0 {
		t.Errorf("report --junit: exit %d failures %d\n%s", code, suites.Failures, out)
	}
	code, out, _ = runCLI(t, "report", "--md")
	if code != exitOK || !strings.Contains(out, "BROKE") {
		t.Errorf("report --md:\n%s", out)
	}
}

func TestAgainst(t *testing.T) {
	needBrowser(t)
	t.Chdir(t.TempDir())
	var good, bad atomic.Bool
	bad.Store(true)
	live, staging := shop(&good), shop(&bad)
	defer live.Close()
	defer staging.Close()
	code, out, errOut := runCLI(t, staging.URL, "--against", live.URL, "--no-download", "--viewport", "desktop",
		"--modules", "health", "--rate", "50", "--plain", "--fail-on", "new")
	if code != exitFindings || !strings.Contains(out, "[BROKE]") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
}

func TestRunOnce(t *testing.T) {
	needBrowser(t)
	t.Chdir(t.TempDir())
	var broken atomic.Bool
	broken.Store(true)
	srv := shop(&broken)
	defer srv.Close()
	code, out, errOut := runCLI(t, "run", srv.URL, "--no-download", "--viewport", "desktop", "--modules", "health",
		"--rate", "50", "--md", "--fail-on", "error")
	if code != exitFindings || !strings.Contains(out, "## ♪ Soundcheck") || !strings.Contains(out, "Server error 500") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
}

func TestFlowsInit(t *testing.T) {
	t.Chdir(t.TempDir())
	code, out, errOut := runCLI(t, "flows", "init")
	if code != exitOK || !strings.Contains(out, "example.yaml") {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(".soundcheck", "flows", "example.yaml")); err != nil {
		t.Error(err)
	}
	if code, _, _ := runCLI(t, "flows"); code != exitUsage {
		t.Error("flows without init must be a usage error")
	}
}

func TestLoginOnlyFromEnv(t *testing.T) {
	t.Setenv("SOUNDCHECK_USER", "")
	t.Setenv("SOUNDCHECK_PASSWORD", "")
	if f, who := loginFunc(config.Default(), false); f != nil || who != "" {
		t.Error("no credentials, no login")
	}
	t.Setenv("SOUNDCHECK_USER", "admin")
	t.Setenv("SOUNDCHECK_PASSWORD", "s3cret")
	f, who := loginFunc(config.Default(), false)
	if f == nil || who != "ad***" {
		t.Errorf("who = %q", who)
	}
	if f, _ := loginFunc(config.Default(), true); f != nil {
		t.Error("--no-login must win")
	}
}

func TestFlagNameEcho(t *testing.T) {
	if got := flagName("modules: echo needs Echo's address: --echo, SOUNDCHECK_ECHO or echo.url"); strings.Count(got, "--echo") != 1 {
		t.Errorf("flag named twice: %q", got)
	}
	if got := flagName(`echo.url: "x" isn't an http(s) URL`); !strings.HasPrefix(got, "--echo (echo.url):") {
		t.Errorf("echo.url error not pointed at --echo: %q", got)
	}
}

func TestProductionHeader(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	// A dead Echo stops the run right after the header, before any browsing.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	_, out, _ := runCLI(t, "run", "127.0.0.1:20260", "--echo="+dead.URL, "--plain")
	if !strings.Contains(out, "Production: none listed: submit and flows run on any site\n") {
		t.Errorf("no production sites:\n%s", out)
	}
	_ = os.MkdirAll(".soundcheck", 0o755)
	_ = os.WriteFile(".soundcheck/config.yaml", []byte("production: ['https://WWW.Example.com.:443/', '*.shop.test']\n"), 0o600)
	_, out, _ = runCLI(t, "run", "127.0.0.1:20260", "--echo="+dead.URL, "--plain")
	if !strings.Contains(out, "Production: example.com, *.shop.test: forms are not submitted there\n") {
		t.Errorf("production sites:\n%s", out)
	}
	_ = os.WriteFile(".soundcheck/config.yaml", []byte("production: ['http://']\n"), 0o600)
	code, _, errOut := runCLI(t, "run", "127.0.0.1:20260")
	if code != exitUsage || !strings.Contains(errOut, `production: "http://" isn't a site`) {
		t.Errorf("bad entry: exit %d, %q", code, errOut)
	}
}
