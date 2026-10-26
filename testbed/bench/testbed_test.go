// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
	"github.com/RhythmShahriar/soundcheck/store"
)

// These tests need the testbed running (make -C testbed up) and Chrome:
//
//	go test -tags testbed -v ./testbed/bench/
//
// They skip when either is missing.

// setup loads the manifest and skips unless the sites answer.
func setup(t *testing.T) (string, *Manifest) {
	t.Helper()
	dir, err := Testbed()
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir + "/scenarios.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sitesFromEnv(m)
	// The https site has a self-signed certificate made at start.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // the local testbed
	for _, base := range m.Sites {
		resp, err := client.Get(base) //nolint:noctx // the local testbed
		if err != nil {
			t.Skipf("testbed isn't running (make -C testbed up): %v", err)
		}
		_ = resp.Body.Close()
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	t.Cleanup(func() { _ = Toggle(dir, "all", "good") })
	if err := Toggle(dir, "all", "good"); err != nil {
		t.Fatal(err)
	}
	return dir, m
}

// healthScenarios are the scenarios the health module alone should catch.
var healthScenarios = []string{"s01", "s02", "s03", "s04", "s05", "s06", "s07", "s08", "s09", "s10", "s16", "s17"}

// TestHealthScenarios runs snapshot → break → compare with the health
// module through the runner and compare packages, the way the CLI will,
// and checks each scenario's expectation from scenarios.yaml. s15 is
// included because health's "not responsive" check can catch it when
// screenshots are taken.
func TestHealthScenarios(t *testing.T) {
	dir, m := setup(t)
	scenarios, err := m.Pick(append(slices.Clone(healthScenarios), "s15"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	modules := []check.Module{health.Module{}}
	score, total := 0, 0
	var board []string
	for _, s := range scenarios {
		cfg := config.Default()
		cfg.Modules = []string{"health"}
		cfg.Viewports = []string{"desktop"}
		cfg.Pages.Include = s.Include
		cfg.Pages.Limit = 20
		mods := modules
		if s.ID == "s15" {
			// Screenshots (the visual module's capture) give health the
			// rendered mobile width.
			mods = []check.Module{health.Module{}, shotsOnly{}}
			cfg.Viewports = []string{"desktop", "mobile"}
		}
		res, err := runScenario(ctx, dir, m, s, cfg, mods, t.TempDir())
		if err != nil {
			t.Fatalf("%s: %v", s.ID, err)
		}
		line := fmt.Sprintf("%s %-55s %-12s %s%s", s.ID, s.Name, res.Outcome, res.Found, res.Detail)
		if len(res.Others) > 0 {
			line += "  [also: " + strings.Join(res.Others, "; ") + "]"
		}
		board = append(board, line)
		if slices.Contains(healthScenarios, s.ID) {
			total++
			if res.Outcome == Caught {
				score++
			} else {
				t.Errorf("%s (%s): %s; expected %v", s.ID, s.Name, res.Outcome, s.Expect)
			}
		}
	}
	t.Logf("health scenarios caught: %d/%d\n%s", score, total, strings.Join(board, "\n"))
}

// runScenario snapshots the good state, breaks the scenario, re-checks the
// snapshot's pages and compares.
func runScenario(ctx context.Context, dir string, m *Manifest, s Scenario, cfg config.Config, modules []check.Module, root string) (Result, error) {
	defer func() { _ = Toggle(dir, s.ID, "good") }()
	st := store.Store{Root: root}
	opts := func(name, d string, snap bool, paths []string) runner.Options {
		return runner.Options{Config: cfg, Target: m.URL(s), Snapshot: snap, Name: name, Dir: d, Paths: paths,
			Version: "testbed", Modules: modules, Browser: browser.Options{NoDownload: true}}
	}
	if err := Toggle(dir, s.ID, "good"); err != nil {
		return Result{}, err
	}
	snapDir, err := st.NewDir("snapshots", "good")
	if err != nil {
		return Result{}, err
	}
	before, err := runner.Run(ctx, opts("good", snapDir, true, nil))
	if err != nil {
		return Result{}, err
	}
	var paths []string
	for _, p := range before.Pages {
		paths = append(paths, p.Path)
	}

	if err := Toggle(dir, s.ID, "broken"); err != nil {
		return Result{}, err
	}
	runDir, err := st.NewDir("runs", "broken")
	if err != nil {
		return Result{}, err
	}
	after, err := runner.Run(ctx, opts("", runDir, false, paths))
	if err != nil {
		return Result{}, err
	}
	cmp := compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: before, After: after,
		BeforeDir: snapDir, AfterDir: runDir}, modules, "testbed")
	res := Evaluate(s, Observed{Comparison: cmp})
	if res.Outcome != Caught {
		// What the broken run saw, to tell a testbed problem from a check's blind spot.
		for _, p := range after.Pages {
			var loaded []string
			for _, r := range p.Resources {
				loaded = append(loaded, fmt.Sprintf("%s %d", r.URL, r.Status))
			}
			res.Detail += fmt.Sprintf(" %s: status %d, %d chars, resources %v;", p.Path, p.Status, p.TextLength, loaded)
		}
	}
	return res, nil
}

// TestGoodStateIsClean checks that, with every scenario good, the health
// module finds nothing above a notice on any scenario page, so a caught
// scenario is caught because of its broken state alone. Its --eaa pass
// runs health, a11y and keyboard with cfg.EAA set: the a11y and keyboard
// modules must report nothing at any level and finish every audit.
// Before the axe and keyboard lanes are merged that is the WCAG 2.0
// rules and an empty walk; TestGoodStateAudit holds the bar meanwhile.
func TestGoodStateIsClean(t *testing.T) {
	_, m := setup(t)
	for _, eaa := range []bool{false, true} {
		cfg := config.Default()
		cfg.Modules = []string{"health"}
		cfg.Viewports = []string{"desktop"}
		cfg.Pages.Limit = 60
		modules := []check.Module{health.Module{}}
		if eaa {
			cfg.EAA = true
			cfg.Modules = []string{"health", "a11y", "keyboard"}
			modules = []check.Module{health.Module{}, a11y.Module{}, keyboard.New()}
		}
		for site, base := range m.Sites {
			// The https site's certificate is self-signed (-k).
			insecure := strings.HasPrefix(base, "https:")
			run, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: base, Dir: t.TempDir(), Version: "testbed",
				Insecure: insecure, Modules: modules, Browser: browser.Options{NoDownload: true, Insecure: insecure}})
			if err != nil {
				t.Fatal(err)
			}
			if len(run.Pages) < 3 {
				t.Errorf("%s: only %d pages discovered", site, len(run.Pages))
			}
			for _, i := range run.AllIssues() {
				eaaIssue := strings.HasPrefix(i.Check, "a11y/") || strings.HasPrefix(i.Check, "keyboard/")
				if i.Level != model.Notice || eaa && eaaIssue {
					t.Errorf("%s %v (eaa %v): %s %s %s %s", site, i.Pages, eaa, i.Level, i.Check, i.Title, i.Source)
				}
			}
			for _, p := range run.Pages {
				for name, au := range p.Audits {
					if !au.Finished() {
						t.Errorf("%s %s: the %s audit %s: %s", site, p.Path, name, au.Status, au.Reason)
					}
				}
			}
			t.Logf("%s (eaa %v): %d pages, clean", site, eaa, len(run.Pages))
		}
	}
}

// TestClickScenarios drives the pages health can't judge (s12, s13) in
// the browser, to prove their good and broken states behave as the
// manifest says, until the interact module can be benchmarked on them.
func TestClickScenarios(t *testing.T) {
	dir, m := setup(t)
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// click loads url, clicks selector and returns JS run afterwards plus
	// any uncaught exception.
	click := func(url, selector, after string) (string, string) {
		tab, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tab.Close() }()
		var (
			mu        sync.Mutex
			exception string
		)
		go tab.EachEvent(func(e *proto.RuntimeExceptionThrown) {
			mu.Lock()
			exception = e.ExceptionDetails.Exception.Description
			mu.Unlock()
		})()
		tab = tab.Timeout(20 * time.Second)
		if err := tab.Navigate(url); err != nil {
			t.Fatal(err)
		}
		tab.MustWaitLoad()
		tab.MustElement(selector).MustClick()
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return tab.MustEval(after).String(), firstLine(exception)
	}
	s12, _ := m.Pick([]string{"s12", "s13"})
	for _, state := range []string{"good", "broken"} {
		if err := Toggle(dir, "s12", state); err != nil {
			t.Fatal(err)
		}
		got, _ := click(m.URL(s12[0]), ".menu-toggle", `() => document.getElementById('browse-panel').hidden + ' ' + document.querySelector('.menu-toggle').getAttribute('aria-expanded')`)
		want := map[string]string{"good": "false true", "broken": "true false"}[state]
		if got != want {
			t.Errorf("s12 %s: panel hidden/expanded = %q, want %q", state, got, want)
		}
		if err := Toggle(dir, "s13", state); err != nil {
			t.Fatal(err)
		}
		text, exc := click(m.URL(s12[1]), ".tb-wishlist", `() => document.querySelector('.tb-wishlist-status').textContent`)
		switch state {
		case "good":
			if text != "Added to your wishlist." || exc != "" {
				t.Errorf("s13 good: status %q, exception %q", text, exc)
			}
		case "broken":
			if !strings.Contains(exc, "WishlistStore is not defined") || text != "" {
				t.Errorf("s13 broken: status %q, exception %q", text, exc)
			}
		}
		t.Logf("%s: s12 panel %q; s13 status %q, exception %q", state, got, text, exc)
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// TestStateFromEnv serves the plain-PHP site with PHP's built-in server
// and a state folder from TESTBED_STATE, the way the README shows. It
// checks that toggle.sh switches s03's deprecated notice on and off.
// It needs php but not Docker or Chrome.
func TestStateFromEnv(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php isn't installed")
	}
	dir, err := Testbed()
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	t.Setenv("TESTBED_STATE", state) // for toggle.sh and the server
	const addr = "127.0.0.1:20342"
	// The CLI's php.ini may hide errors; the Docker image shows them in
	// PHP's own HTML format. Xdebug (on GitHub's runners) would replace
	// that format with its own table, so it is switched off.
	srv := exec.Command(php, "-d", "display_errors=1", "-d", "error_reporting=E_ALL", "-d", "html_errors=1", "-d", "xdebug.mode=off", "-S", addr, "-t", filepath.Join(dir, "php")) //nolint:gosec // our own test server
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _ = srv.Wait() })

	get := func() string {
		t.Helper()
		var last error
		for range 50 {
			resp, err := http.Get("http://" + addr + "/report.php") //nolint:gosec,noctx // the local test server
			if err == nil {
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				return string(body)
			}
			last = err
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("php -S didn't answer: %v", last)
		return ""
	}
	const notice = "Deprecated</b>:  strlen()"
	for _, step := range []struct {
		state  string
		broken bool
	}{{"good", false}, {"broken", true}, {"good", false}} {
		if err := Toggle(dir, "s03", step.state); err != nil {
			t.Fatal(err)
		}
		_, statErr := os.Stat(filepath.Join(state, "s03"))
		if (statErr == nil) != step.broken {
			t.Fatalf("toggle.sh s03 %s: state file in TESTBED_STATE exists = %v", step.state, statErr == nil)
		}
		body := get()
		if strings.Contains(body, notice) != step.broken {
			t.Errorf("s03 %s: deprecated notice shown = %v, want %v\n%s", step.state, !step.broken, step.broken, body)
		}
		if !strings.Contains(body, "Monthly report") {
			t.Errorf("s03 %s: not the report page:\n%s", step.state, body)
		}
	}
}

// TestManifest checks scenarios.yaml without the testbed: every scenario
// has a unique id toggle.sh accepts, a known site, a toggle and at least
// one well-formed expectation (LoadManifest). Every scenario whose broken
// state changes the site must be implemented behind testbed_broken().
// The EAA scenarios are checked further by checkEAAScenarios.
func TestManifest(t *testing.T) {
	dir, err := Testbed()
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(filepath.Join(dir, "scenarios.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Scenarios) < 46 {
		t.Errorf("%d scenarios, want at least 46", len(m.Scenarios))
	}
	if len(m.Control.Sites) == 0 || m.Control.Limit <= 0 {
		t.Errorf("control pass not configured: %+v", m.Control)
	}

	var code strings.Builder
	for _, sub := range []string{"wp", "php"} {
		err := filepath.WalkDir(filepath.Join(dir, sub), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".php") && !strings.HasSuffix(path, ".js") {
				return err
			}
			data, err := os.ReadFile(path) //nolint:gosec // the testbed's own files
			code.Write(data)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range m.Scenarios {
		if s.ChangesSite() && !strings.Contains(code.String(), "testbed_broken( '"+s.ID+"' )") {
			t.Errorf("%s: no testbed_broken( '%s' ) in testbed/wp or testbed/php", s.ID, s.ID)
		}
		if s.Pending.Is() && s.Note == "" && s.Pending.Lane() == "" {
			t.Errorf("%s: pending without a lane or a note saying what it waits for", s.ID)
		}
		for _, r := range s.Rules {
			if _, err := os.Stat(filepath.Join(dir, r)); err != nil {
				t.Errorf("%s: rule file: %v", s.ID, err)
			}
		}
	}
	checkEAAScenarios(t, dir, m)

	// toggle.sh knows every scenario.
	state := t.TempDir()
	t.Setenv("TESTBED_STATE", state)
	if err := Toggle(dir, "all", "broken"); err != nil {
		t.Fatal(err)
	}
	for _, s := range m.Scenarios {
		if _, err := os.Stat(filepath.Join(state, s.ID)); err != nil {
			t.Errorf("toggle.sh all broken didn't break %s", s.ID)
		}
	}
	if err := Toggle(dir, "all", "good"); err != nil {
		t.Fatal(err)
	}
}

// TestWPUpdateTestbed checks what the wp-update scenarios (s47, s48)
// stand on, through the stack's WP-CLI: after a reset all ten
// testbed-update-* plugins offer 1.0 → 1.1, updating testbed-update-07
// breaks the contact page and nothing else and the rollback wp-update
// runs (wp plugin install --version=1.0 --force) fixes it. It needs the
// testbed (make -C testbed up) and Docker, not Chrome. Set
// COMPOSE_PROJECT_NAME and TESTBED_WP_PORT for a stack started with them.
func TestWPUpdateTestbed(t *testing.T) {
	dir, err := Testbed()
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(filepath.Join(dir, "scenarios.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	site := m.Sites["wp"]
	if port := os.Getenv("TESTBED_WP_PORT"); port != "" {
		site = "http://localhost:" + port
	}
	status := func(path string) int {
		t.Helper()
		resp, err := http.Get(site + path) //nolint:gosec,noctx // the local testbed
		if err != nil {
			t.Skipf("testbed isn't running (make -C testbed up): %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	status("/")
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker isn't installed")
	}
	compose := []string{"compose", "-f", filepath.Join(dir, "docker-compose.yml"), "run", "--rm", "--no-deps", "-T"}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("docker", append(slices.Clone(compose), args...)...) //nolint:gosec // our own stack
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
		}
		return stdout.String()
	}
	wp := func(args ...string) string { return run(append([]string{"--entrypoint", "wp", "cli"}, args...)...) }
	reset := func() { run("--entrypoint", "sh", "cli", "/testbed-updates/reset.sh") }
	t.Cleanup(reset)

	reset()
	var updates []struct {
		Name          string `json:"name"`
		Version       string `json:"version"`
		UpdateVersion string `json:"update_version"`
	}
	out := wp("plugin", "list", "--update=available", "--format=json", "--fields=name,version,update_version")
	at := strings.Index(out, "[")
	if at < 0 {
		t.Fatalf("plugin list printed no JSON list:\n%s", out)
	}
	if err := json.Unmarshal([]byte(out[at:]), &updates); err != nil {
		t.Fatalf("plugin list: %v\n%s", err, out)
	}
	var names []string
	for _, u := range updates {
		if strings.HasPrefix(u.Name, "testbed-update-") {
			names = append(names, u.Name)
			if u.Version != "1.0" || u.UpdateVersion != "1.1" {
				t.Errorf("%s: %s → %s, want 1.0 → 1.1", u.Name, u.Version, u.UpdateVersion)
			}
		}
	}
	slices.Sort(names)
	if len(names) != 10 || names[0] != "testbed-update-01" || names[9] != "testbed-update-10" {
		t.Fatalf("updates available: %v, want testbed-update-01 to 10", names)
	}
	if got := status("/contact/"); got != http.StatusOK {
		t.Fatalf("/contact/ at 1.0: %d, want 200", got)
	}

	wp("plugin", "update", "testbed-update-07")
	if got := status("/contact/"); got != http.StatusInternalServerError {
		t.Errorf("/contact/ after testbed-update-07 1.1: %d, want 500", got)
	}
	if got := status("/about/"); got != http.StatusOK {
		t.Errorf("/about/ after testbed-update-07 1.1: %d, want 200 (the break is on the contact page only)", got)
	}

	// The other nine 1.1s change nothing.
	wp("plugin", "install", "testbed-update-07", "--version=1.0", "--force")
	wp("plugin", "update", "--all")
	wp("plugin", "install", "testbed-update-07", "--version=1.0", "--force")
	if got := status("/contact/"); got != http.StatusOK {
		t.Errorf("/contact/ with the other nine at 1.1 and testbed-update-07 rolled back: %d, want 200", got)
	}
	if out := wp("plugin", "list", "--update=available", "--field=name"); strings.TrimSpace(out) != "testbed-update-07" {
		t.Errorf("updates after the rollback: %q, want only testbed-update-07", out)
	}
}

// shotsOnly asks the runner for screenshots without the visual module's
// comparison.
type shotsOnly struct{}

func (shotsOnly) Name() string                { return "shots" }
func (shotsOnly) NeedsShots() bool            { return true }
func (shotsOnly) NeedsSecondShot(_ bool) bool { return false }

// checkEAAScenarios checks the EAA mode scenarios (PRD 16.1): s49 to s89
// all there, each run with --eaa by the module that should catch it, a
// pending mark naming a lane, a preflight probe for every page that
// changes. The fixtures, flows and harnesses they use must be in place.
func checkEAAScenarios(t *testing.T, dir string, m *Manifest) {
	t.Helper()
	eaa := map[string]Scenario{}
	for _, s := range m.Scenarios {
		if s.EAA() {
			eaa[s.ID] = s
		}
	}
	for n := 49; n <= 89; n++ {
		id := fmt.Sprintf("s%02d", n)
		s, ok := eaa[id]
		if !ok {
			t.Errorf("%s: missing, or not run with --eaa", id)
			continue
		}
		delete(eaa, id)
		switch {
		case s.Module != "a11y" && s.Module != "keyboard" && (id != "s86" || s.Module != "health"):
			t.Errorf("%s: module %q, want a11y or keyboard (health for s86)", id, s.Module)
		case s.Pending.Is() && s.Pending.Lane() == "":
			t.Errorf("%s: pending: true; name the lane it waits for", id)
		}
		if s.ChangesSite() && !slices.ContainsFunc(probes, func(p probe) bool { return p.ID == id }) {
			t.Errorf("%s: its broken state changes the site but the preflight (preflight.go) has no probe for it", id)
		}
		for _, f := range s.Flows {
			data, err := os.ReadFile(filepath.Join(dir, f)) //nolint:gosec // the testbed's own files
			if err != nil {
				t.Errorf("%s: flow: %v", id, err)
				continue
			}
			var fl struct {
				Start    string `yaml:"start"`
				Keyboard bool   `yaml:"keyboard"`
				Steps    []any  `yaml:"steps"`
			}
			if err := yaml.Unmarshal(data, &fl); err != nil || !fl.Keyboard || !strings.HasPrefix(s.Path, fl.Start) || len(fl.Steps) == 0 {
				t.Errorf("%s: %s isn't a key flow (keyboard: true) starting at %s: %v", id, f, s.Path, err)
			}
		}
		if (len(s.Flows) > 0) != slices.Contains(s.Args, "flows") {
			t.Errorf("%s: key flows run only with --level flows; --level flows needs a flow", id)
		}
		if s.Fixture != "" {
			checkFixture(t, id, filepath.Join(dir, s.Fixture))
		}
	}
	for id := range eaa {
		t.Errorf("%s: runs with --eaa outside s49 to s89", id)
	}
	// The harnesses PRD 16.2 asks for are where it asks for them.
	want := map[string][]string{
		"canaries":  {"s68", "s84"},
		"orders":    {"s49", "s70", "s85", "s88"},
		"statement": {"s50", "s84", "s86", "s89"},
	}
	for h, ids := range want {
		for _, id := range ids {
			if i := slices.IndexFunc(m.Scenarios, func(s Scenario) bool { return s.ID == id }); i < 0 || !m.Scenarios[i].Harness.Has(h) {
				t.Errorf("%s: no harness %s", id, h)
			}
		}
	}
	if i := slices.IndexFunc(m.Scenarios, func(s Scenario) bool { return s.ID == "s89" }); i < 0 || !m.Scenarios[i].Control {
		t.Error("s89 isn't in the control pass")
	}
}

// checkFixture checks a snapshot fixture is one Soundcheck 1.2 wrote: a
// run.json with no options.wcag and no options.a11y, next to its pages.
func checkFixture(t *testing.T, id, dir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "run.json")) //nolint:gosec // the testbed's own fixture
	if err != nil {
		t.Errorf("%s: fixture: %v", id, err)
		return
	}
	var run struct {
		Version   string         `json:"version"`
		Target    string         `json:"target"`
		PagesFile string         `json:"pages_file"`
		Options   map[string]any `json:"options"`
	}
	if err := json.Unmarshal(data, &run); err != nil {
		t.Errorf("%s: fixture run.json: %v", id, err)
		return
	}
	if !strings.HasPrefix(run.Version, "1.2.") || run.Options["wcag"] != nil || run.Options["a11y"] != nil || run.Options["eaa"] != nil {
		t.Errorf("%s: fixture is version %s with options %v, want a 1.2 run without wcag, a11y or eaa", id, run.Version, run.Options)
	}
	if !strings.HasPrefix(run.Target, "http://localhost:18810/") {
		t.Errorf("%s: fixture target %s, want the php site at its default address", id, run.Target)
	}
	if _, err := os.Stat(filepath.Join(dir, run.PagesFile)); err != nil {
		t.Errorf("%s: fixture pages: %v", id, err)
	}
}
