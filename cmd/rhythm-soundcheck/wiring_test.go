// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/store"
	"github.com/RhythmShahriar/soundcheck/wpupdate"
)

// config.Modules, the registry and the builders name the same modules,
// so a new module can't be registered and left out of the CLI.
func TestModulesMatchRegistry(t *testing.T) {
	var registered []string
	for _, m := range check.Registered() {
		registered = append(registered, m.Name())
	}
	slices.Sort(registered)
	known := slices.Sorted(slices.Values(config.Modules))
	if !slices.Equal(registered, known) {
		t.Errorf("registered %v, config.Modules %v", registered, known)
	}
	var built []string
	for name := range builders {
		built = append(built, name)
	}
	slices.Sort(built)
	if !slices.Equal(built, known) {
		t.Errorf("builders %v, config.Modules %v", built, known)
	}
	for _, m := range config.DefaultModules {
		if !slices.Contains(config.Modules, m) {
			t.Errorf("default module %s isn't known", m)
		}
	}
}

// Every "Check:" literal in checks/ is listed by its module's Describer,
// so --list-checks shows every check a run can report.
func TestDescribersCoverChecks(t *testing.T) {
	listed := map[string]bool{}
	for _, m := range check.Registered() {
		if d, ok := m.(check.Describer); ok {
			for _, c := range d.Checks() {
				listed[c.ID] = true
			}
		}
	}
	lit := regexp.MustCompile(`Check: *"([a-z0-9]+/[a-z0-9/-]+)"`)
	files, err := filepath.Glob("../../checks/*/*.go")
	if err != nil || len(files) == 0 {
		t.Fatal(files, err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range lit.FindAllStringSubmatch(string(data), -1) {
			if !listed[m[1]] {
				t.Errorf("%s: %s isn't in its module's Checks()", f, m[1])
			}
		}
	}
}

func TestListChecks(t *testing.T) {
	code, out, _ := runCLI(t, "--list-checks")
	if code != exitOK || !strings.HasPrefix(out, "health:") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"health: on by default\n  health/unreachable", "a11y: off unless asked for (+a11y)", "seo/sitemap",
		"health/php-error", "interact/dead-click", "exec: on when the config lists exec: commands", "a11y/<axe rule id>"} {
		if !strings.Contains(out, want) {
			t.Errorf("--list-checks has no %q:\n%s", want, out)
		}
	}
}

// a11y and seo are off by default and turned on with +a11y and +seo;
// the run then gets the modules, in config order, with interact last.
func TestA11yAndSEOModules(t *testing.T) {
	t.Chdir(t.TempDir())
	p, err := parse([]string{"a.test"})
	if err != nil {
		t.Fatal(err)
	}
	if p.cfg.Has("a11y") || p.cfg.Has("seo") {
		t.Errorf("default modules: %v", p.cfg.Modules)
	}
	p, err = parse([]string{"a.test", "--modules", "+a11y,seo"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range check.SortByPhase(p.modules(p.cfg)) {
		names = append(names, m.Name())
	}
	if got := strings.Join(names, ","); got != "health,visual,cache,speed,a11y,seo,interact" {
		t.Errorf("modules = %s", got)
	}
}

// The project's own rules are loaded for health. A broken rule file
// stops the command before anything runs.
func TestProjectRules(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(".soundcheck/rules", 0o750); err != nil {
		t.Fatal(err)
	}
	rule := "id: health/acme-banner\nmatch: text\npattern: \"Maintenance mode\"\nlevel: error\ntitle: Maintenance banner shown\n"
	if err := os.WriteFile(".soundcheck/rules/banner.yaml", []byte(rule), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".soundcheck/rules/banner_test.yaml", []byte(bannerSamples), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := parse([]string{"a.test"})
	if err != nil {
		t.Fatal(err)
	}
	if p.rules.Rule("health/acme-banner") == nil {
		t.Error("the project's rule isn't loaded")
	}
	code, out, errOut := runCLI(t, "rules", "list")
	if code != exitOK || !strings.Contains(out, "health/acme-banner") || !strings.Contains(out, "built in") ||
		!strings.Contains(out, filepath.Join(".soundcheck", "rules", "banner.yaml")) {
		t.Errorf("rules list: %d\n%s\n%s", code, out, errOut)
	}
	if err := os.WriteFile(".soundcheck/rules/broken.yaml", []byte("id: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCLI(t, "run", "a.test"); code != exitUsage || !strings.Contains(errOut, "rules") {
		t.Errorf("broken rule: exit %d %q", code, errOut)
	}
}

func TestRulesTest(t *testing.T) {
	t.Chdir(t.TempDir())
	// No project rules: the built-in rules' samples run.
	code, out, errOut := runCLI(t, "rules", "test")
	if code != exitOK || !strings.Contains(out, "the built-in rules:") || !strings.Contains(out, "0 failures") {
		t.Errorf("rules test: %d\n%s\n%s", code, out, errOut)
	}
	dir := t.TempDir()
	rule := "id: health/acme-banner\nmatch: text\npattern: \"Maintenance mode\"\nlevel: error\ntitle: Maintenance banner shown\n"
	samples := "positive:\n  - text: \"Down for maintenance\"\nnegative:\n  - text: \"Open as usual\"\n"
	_ = os.WriteFile(filepath.Join(dir, "banner.yaml"), []byte(rule), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "banner_test.yaml"), []byte(samples), 0o600)
	code, out, _ = runCLI(t, "rules", "test", dir)
	if code != exitFindings || !strings.Contains(out, "FAIL") {
		t.Errorf("failing sample: exit %d\n%s", code, out)
	}
	if code, _, _ := runCLI(t, "rules", "nope"); code != exitUsage {
		t.Errorf("rules nope: exit %d", code)
	}
}

const bannerSamples = "positive:\n  - text: \"Site in Maintenance mode\"\nnegative:\n  - text: \"Open as usual\"\n"

// --browser takes an engine, a Chrome path or engine=path.
func TestBrowserFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		arg, engine, bin, err string
	}{
		{"firefox", "firefox", "", ""},
		{"WebKit", "webkit", "", ""},
		{"chrome", "chrome", "", ""},
		{"firefox=/opt/firefox/firefox", "firefox", "/opt/firefox/firefox", ""},
		{"/usr/bin/google-chrome", "", "/usr/bin/google-chrome", ""},
		{"opera=/x", "", "", `--browser: "opera" isn't chrome, firefox or webkit`},
		{"firefox=", "", "", "firefox= needs the path to its binary"},
		{"opera", "", "", `--browser: "opera" isn't chrome, firefox or webkit`},
		{"firefx", "", "", `--browser: "firefx" isn't chrome, firefox or webkit (did you mean firefox?)`},
		{"Chrom=/x", "", "", `isn't chrome, firefox or webkit (did you mean chrome?)`},
		{"sh", "", "sh", ""},                     // a program on PATH, as a Chrome binary
		{"./chrome-bin", "", "./chrome-bin", ""}, // a path, checked when the browser starts
	} {
		p, err := parse([]string{"a.test", "--browser", tc.arg})
		switch {
		case tc.err != "":
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err %v, want %q", tc.arg, err, tc.err)
			}
		case err != nil:
			t.Errorf("%s: %v", tc.arg, err)
		case string(p.engine) != tc.engine || p.bin != tc.bin:
			t.Errorf("%s: engine %q bin %q", tc.arg, p.engine, p.bin)
		}
	}
	if p, _ := parse([]string{"a.test", "--browser", "firefox"}); p.engine != capture.Firefox {
		t.Error("firefox isn't capture.Firefox")
	}
}

// The new flags parse, belong to their commands and reach the runner.
func TestSafetyFlags(t *testing.T) {
	t.Chdir(t.TempDir())
	p, err := parse([]string{"a.test", "--follow-host", "--no-sandbox", "--confirm=off"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.o.followHost || !p.o.noSandbox || !p.o.confirm.off {
		t.Errorf("options = %+v", p.o)
	}
	if p, _ := parse([]string{"a.test", "--confirm"}); p.o.confirm.off {
		t.Error("--confirm is on")
	}
	if _, err := parse([]string{"a.test", "--confirm=maybe"}); err == nil {
		t.Error("--confirm=maybe parsed")
	}
	for _, tc := range []struct{ args []string }{
		{[]string{"run", "a.test", "--confirm=off"}},
		{[]string{"run", "a.test", "--resume"}},
		{[]string{"compare", "--resume"}},
		{[]string{"snapshot", "a.test", "--resume"}}, // no --name
	} {
		if code, _, errOut := runCLI(t, tc.args...); code != exitUsage {
			t.Errorf("%v: exit %d %q", tc.args, code, errOut)
		}
	}
}

// -H and --cookie never go to a site the config file named.
func TestHeaderFlagsNotSentToConfigSite(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "site: collector.invalid\n")
	for _, args := range [][]string{{"-H", "Authorization: Bearer SECRET123"}, {"--cookie", "auth=COOKIE9"}} {
		code, _, errOut := runCLI(t, append([]string{"run", "--plain", "--modules", "health"}, args...)...)
		if code != exitUsage || !strings.Contains(errOut, "to collector.invalid: the site came from .soundcheck/config.yaml (site); name it on the command line to send them") {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
		if strings.Contains(errOut, "SECRET123") || strings.Contains(errOut, "COOKIE9") {
			t.Error("a value was printed")
		}
	}
}

// writeComparison stores a finished comparison of snapshot good in the
// store as compare would. It returns the run folder.
func writeComparison(t *testing.T, c *model.Comparison) string {
	t.Helper()
	st := store.Store{Root: config.Dir}
	snap, err := st.NewDir(store.Incoming, "good")
	if err != nil {
		t.Fatal(err)
	}
	before := &model.Run{Target: "http://127.0.0.1:21201/", Name: "good", Started: time.Now().Add(-time.Hour),
		Pages: []model.Page{{Path: "/", URL: "http://127.0.0.1:21201/", Status: 200}}}
	if err := store.SaveRun(snap, before); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Promote(snap, "good"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLatest("good"); err != nil {
		t.Fatal(err)
	}
	dir, err := st.NewDir("runs", store.Stamp(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	after := &model.Run{Target: before.Target, Started: time.Now(), Interrupted: c.Interrupted,
		Pages: []model.Page{{Path: "/", URL: before.Target, Status: 500}}}
	if err := store.SaveRun(dir, after); err != nil {
		t.Fatal(err)
	}
	c.Before = model.RunRef{Target: before.Target, Name: "good", Started: before.Started}
	c.Complete()
	if err := store.SaveComparison(dir, c); err != nil {
		t.Fatal(err)
	}
	return dir
}

// report of an interrupted comparison exits 130, also with --fail-on, and
// says the result is partial.
func TestReportPartialExits130(t *testing.T) {
	t.Chdir(t.TempDir())
	writeComparison(t, &model.Comparison{Interrupted: true, Changes: []model.Change{
		{Class: model.Broke, Check: "health/status", Title: "Page returns HTTP 500", Pages: []string{"/"}}}})
	for _, args := range [][]string{{"report"}, {"report", "--fail-on", "changed"}} {
		code, _, errOut := runCLI(t, append(args, "--plain")...)
		if code != exitCanceled || !strings.Contains(errOut, "this result is partial (interrupted); run it again") {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
}

// JSON output says whether the result failed at --fail-on.
func TestReportJSONFailed(t *testing.T) {
	t.Chdir(t.TempDir())
	writeComparison(t, &model.Comparison{Changes: []model.Change{
		{Class: model.Broke, Check: "health/status", Title: "Page returns HTTP 500", Pages: []string{"/"}}}})
	code, out, _ := runCLI(t, "report", "--json", "--fail-on", "broke")
	var got struct {
		Summary struct {
			Failed *bool `json:"failed"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err, out)
	}
	if code != exitFindings || got.Summary.Failed == nil || !*got.Summary.Failed {
		t.Errorf("exit %d, summary.failed %v", code, got.Summary.Failed)
	}
}

func TestAcceptUsage(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, errOut := runCLI(t, "accept"); code != exitUsage || !strings.Contains(errOut, "accept <id>") {
		t.Errorf("accept: exit %d %q", code, errOut)
	}
	if code, _, _ := runCLI(t, "accept", "c1", "--page", "/x"); code != exitUsage {
		t.Errorf("ids and --page: exit %d", code)
	}
	if code, _, errOut := runCLI(t, "accept", "c1"); code != exitUsage || !strings.Contains(errOut, "nothing to show yet") {
		t.Errorf("nothing stored: exit %d %q", code, errOut)
	}
	if code, out, _ := runCLI(t, "help", "accept"); code != exitOK || !strings.Contains(out, "rhythm-soundcheck accept --page") {
		t.Errorf("help accept: %d\n%s", code, out)
	}
}

// review needs a terminal; in a script it says what to use instead.
func TestReviewNeedsATerminal(t *testing.T) {
	t.Chdir(t.TempDir())
	writeComparison(t, &model.Comparison{Changes: []model.Change{
		{Class: model.Changed, Check: "health/title", Title: "Page title changed", Pages: []string{"/"}}}})
	code, _, errOut := runCLI(t, "review")
	if code != exitUsage || !strings.Contains(errOut, "review needs an interactive terminal") {
		t.Errorf("exit %d %q", code, errOut)
	}
}

// report --serve refuses a result that isn't a comparison; --client
// needs --brand and writes the agency's page.
func TestReportServeAndClient(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := writeComparison(t, &model.Comparison{Changes: []model.Change{
		{Class: model.Broke, Check: "health/status", Title: "Page returns HTTP 500", Pages: []string{"/"}}}})
	if code, _, errOut := runCLI(t, "report", "--client"); code != exitUsage || !strings.Contains(errOut, "--client needs --brand") {
		t.Errorf("no brand: exit %d %q", code, errOut)
	}
	if err := os.WriteFile("agency.yaml", []byte("name: Acme Web\ncontact: hello@acme.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, "report", "--client", "--brand", "agency.yaml", "-o", "client.html")
	data, _ := os.ReadFile("client.html")
	if code != exitOK || !bytes.Contains(data, []byte("Prepared by Acme Web")) || !bytes.Contains(data, []byte("Something stopped working")) {
		t.Errorf("client report: exit %d %q\n%.400s", code, errOut, data)
	}
	if code, _, _ := runCLI(t, "report", "--client", "--brand", "agency.yaml", "-o", "client.md"); code != exitUsage {
		t.Errorf("client .md: exit %d", code)
	}
	if code, _, _ := runCLI(t, "report", "--brand", "agency.yaml"); code != exitUsage {
		t.Errorf("--brand alone: exit %d", code)
	}
	// A run that isn't a comparison has nothing to review.
	if err := os.Remove(filepath.Join(dir, "comparison.json")); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCLI(t, "report", "--serve"); code != exitUsage || !strings.Contains(errOut, "nothing to review") {
		t.Errorf("serve a run: exit %d %q", code, errOut)
	}
}

func TestHistoryCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, out, _ := runCLI(t, "history"); code != exitOK || !strings.Contains(out, "No history yet") {
		t.Errorf("empty: %d %q", code, out)
	}
	writeComparison(t, &model.Comparison{Changes: []model.Change{
		{Class: model.Broke, Check: "health/status", Title: "Page returns HTTP 500", Pages: []string{"/"}}}})
	code, out, errOut := runCLI(t, "history", "--backfill")
	if code != exitOK || !strings.Contains(out, "snapshot") || !strings.Contains(out, "comparison") || !strings.Contains(errOut, "Added 2 past results") {
		t.Errorf("backfill: %d\n%s\n%s", code, out, errOut)
	}
	if code, _, _ := runCLI(t, "history", "--metric", "cls"); code != exitUsage {
		t.Errorf("--metric cls: exit %d", code)
	}
}

func TestFleetCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, out, _ := runCLI(t, "fleet", "--help"); code != exitOK || !strings.Contains(out, "rhythm-soundcheck fleet <sites.yaml>") {
		t.Errorf("--help: %d %q", code, out)
	}
	if code, _, _ := runCLI(t, "fleet", "sites.yaml", "deploy"); code != exitUsage {
		t.Errorf("bad mode: exit %d", code)
	}
	_ = os.WriteFile("sites.yaml", []byte("- {name: acme, url: a.test, args: [--allow-exec]}\n"), 0o600)
	if code, _, errOut := runCLI(t, "fleet", "sites.yaml", "snapshot"); code != exitUsage || !strings.Contains(errOut, "can't set --allow-exec") {
		t.Errorf("--allow-exec in args: exit %d %q", code, errOut)
	}
}

func TestWPUpdateCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		log  wpupdate.Log
		want int
	}{
		{wpupdate.Log{Works: true, Plugins: []wpupdate.Plugin{{Slug: "a", Status: wpupdate.Updated}}}, exitOK},
		{wpupdate.Log{Works: true, Plugins: []wpupdate.Plugin{{Slug: "a", Status: wpupdate.RolledBack}}}, exitFindings},
		{wpupdate.Log{Works: false, Plugins: []wpupdate.Plugin{{Slug: "a", Status: wpupdate.Kept}}}, exitFindings},
	} {
		if got := wpUpdateExit(&tc.log); got != tc.want {
			t.Errorf("exit for %+v = %d, want %d", tc.log.Plugins, got, tc.want)
		}
	}
	if code, out, _ := runCLI(t, "wp-update", "--help"); code != exitOK || !strings.Contains(out, "wp-update <url> --wp") {
		t.Errorf("--help: %d %q", code, out)
	}
	if code, _, errOut := runCLI(t, "wp-update", "staging.test"); code != exitUsage || !strings.Contains(errOut, "--wp is missing") {
		t.Errorf("no --wp: exit %d %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "wp-update", "staging.test", "--wp", "wp; rm -rf /"); code != exitUsage || !strings.Contains(errOut, "needs a shell") {
		t.Errorf("shell --wp: exit %d %q", code, errOut)
	}
	writeConfig(t, "production: [shop.test]\n")
	if code, _, errOut := runCLI(t, "wp-update", "https://shop.test", "--wp", "wp"); code != exitUsage || !strings.Contains(errOut, "production site") {
		t.Errorf("production: exit %d %q", code, errOut)
	}
}

// exec runs only with --allow-exec; its commands come from exec: in the
// config, which turns the module on.
func TestExecModule(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "exec:\n  - name: laravel-log\n    run: [./check-log]\n    timeout: 5s\n")
	p, err := parse([]string{"a.test"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.cfg.Has("exec") || p.allowExec {
		t.Errorf("modules %v, allowExec %v", p.cfg.Modules, p.allowExec)
	}
	p, _ = parse([]string{"a.test", "--allow-exec"})
	if !p.allowExec {
		t.Error("--allow-exec")
	}
	t.Setenv("SOUNDCHECK_ALLOW_EXEC", "1")
	if p, _ := parse([]string{"a.test"}); !p.allowExec {
		t.Error("SOUNDCHECK_ALLOW_EXEC=1")
	}
	if p, _ := parse([]string{"a.test", "--skip", "exec"}); p.cfg.Has("exec") {
		t.Error("--skip exec")
	}
	writeConfig(t, "exec:\n  - name: Bad Name\n    run: [x]\n")
	if _, err := parse([]string{"a.test"}); err == nil || !strings.Contains(err.Error(), `name "Bad Name"`) {
		t.Errorf("bad command: %v", err)
	}
	writeConfig(t, "exec:\n  - name: x\n    command: [x]\n")
	if _, err := parse([]string{"a.test"}); err == nil || !strings.Contains(err.Error(), `unknown setting "exec.command"`) {
		t.Errorf("unknown key: %v", err)
	}
}

// --lighthouse never runs on a site listed as production.
func TestLighthouseRefusesProduction(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "production: [shop.test]\n")
	p, err := parse([]string{"https://shop.test", "--lighthouse"})
	if err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	s := p.begin(io.Discard, &errb)
	defer s.stop()
	s.lighthouse("https://shop.test/", t.TempDir())
	if !strings.Contains(errb.String(), "--lighthouse: https://shop.test/ is listed as production") {
		t.Errorf("stderr: %q", errb.String())
	}
}

// An ignore rule from review keeps the config file's comments and loads.
func TestAppendIgnore(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "# Our site\nrate: 2 # gentle\n")
	r := config.IgnoreRule{Check: "visual/page", Title: "Page looks different", Pages: []string{"/team/"}, Reason: "weekly photos"}
	if err := appendIgnore(".soundcheck/config.yaml", r); err != nil {
		t.Fatal(err)
	}
	if err := appendIgnore(".soundcheck/config.yaml", config.IgnoreRule{Check: "health/favicon"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(".soundcheck/config.yaml")
	if !bytes.Contains(data, []byte("# Our site")) || !bytes.Contains(data, []byte("# gentle")) {
		t.Errorf("comments lost:\n%s", data)
	}
	cfg, err := config.Load(".soundcheck/config.yaml")
	if err != nil {
		t.Fatal(err, string(data))
	}
	if len(cfg.Ignore) != 2 || cfg.Ignore[0].Pages[0] != "/team/" || cfg.Ignore[1].Check != "health/favicon" || cfg.Rate != 2 {
		t.Errorf("ignore = %+v\n%s", cfg.Ignore, data)
	}
}

// The view's accept hint names this command.
func TestAcceptHintName(t *testing.T) {
	t.Chdir(t.TempDir())
	p, err := parse([]string{"a.test", "--plain"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	s := p.begin(&out, io.Discard)
	defer s.stop()
	c := &model.Comparison{Before: model.RunRef{Name: "good"}, Changes: []model.Change{
		{ID: "ab12cd34", Class: model.Changed, Check: "health/title", Title: "Page title changed", Pages: []string{"/"}}}}
	s.view.Summary(report.Result{Run: &model.Run{}, Comparison: c}, "")
	if !strings.Contains(out.String(), "rhythm-soundcheck accept ab12cd34 if this is expected") {
		t.Errorf("no hint:\n%s", out.String())
	}
}

// help <command> describes the main commands, not just the general help.
func TestHelpTopics(t *testing.T) {
	for cmd, want := range map[string]string{
		"snapshot": "rhythm-soundcheck snapshot <url> [flags]",
		"compare":  "rhythm-soundcheck compare [url] [flags]",
		"report":   "rhythm-soundcheck report --client --brand <agency.yaml>",
		"import":   "rhythm-soundcheck import <backstop.json> [--site <url>] [--force]",
	} {
		code, out, _ := runCLI(t, "help", cmd)
		if code != exitOK || !strings.Contains(out, want) || strings.Contains(out, "Requests and browser:") {
			t.Errorf("help %s: exit %d\n%s", cmd, code, out)
		}
	}
	if _, out, _ := runCLI(t, "help", "report"); !strings.Contains(out, "agency.yaml takes four keys") || !strings.Contains(out, "accent: ") ||
		!strings.Contains(out, "contact: ") {
		t.Errorf("help report doesn't list the agency.yaml keys:\n%s", out)
	}
	_, out, _ := runCLI(t, "help")
	for _, row := range []string{"      --serve ", "      --client ", "      --brand <file> ", "      --site <url> ", "      --force ", "      --pages-only "} {
		if !strings.Contains(out, row) {
			t.Errorf("help lists no %q row", row)
		}
	}
}

// SOUNDCHECK_LIGHTHOUSE=1 turns --lighthouse on, as a fleet passes it
// from <PREFIX>_SOUNDCHECK_LIGHTHOUSE.
func TestLighthouseFromTheEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_LIGHTHOUSE", "")
	if p, err := parse([]string{"run", "127.0.0.1:22200"}); err != nil || p.o.lighthouse {
		t.Errorf("unset: lighthouse %v, %v", p != nil && p.o.lighthouse, err)
	}
	t.Setenv("SOUNDCHECK_LIGHTHOUSE", "1")
	if p, err := parse([]string{"run", "127.0.0.1:22200"}); err != nil || !p.o.lighthouse {
		t.Errorf("SOUNDCHECK_LIGHTHOUSE=1: lighthouse %v, %v", p != nil && p.o.lighthouse, err)
	}
}

// Every command takes --plain and --no-color, the subcommands too.
func TestSubcommandsTakeColorFlags(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"history", "--plain"}, {"history", "--no-color"},
		{"accept", "c1", "--plain"}, {"accept", "--no-color", "c1"},
		{"review", "--plain"}, {"review", "--no-color"},
		{"rules", "list", "--plain"}, {"rules", "--no-color", "list"}, {"rules", "test", "--plain"},
		{"snapshots", "--plain"}, {"snapshots", "--no-color"}, {"snapshots", "-q"}, {"snapshots", "--json", "--plain"},
		{"import", "missing.json", "--plain"}, {"import", "-q", "missing.json"},
		{"init", "--plain", "--force"}, {"history", "-q"}, {"rules", "list", "--quiet"},
		{"update", "--plain", "stray"}, {"update", "-q", "stray"},
	} {
		_, _, errOut := runCLI(t, args...)
		if strings.Contains(errOut, "unknown flag") {
			t.Errorf("%v: %s", args, errOut)
		}
	}
	for _, cmd := range [][]string{{"review"}, {"snapshots"}, {"flows", "init"}, {"import"}, {"update"}, {"init"},
		{"history"}, {"accept"}, {"rules"}, {"fleet"}, {"wp-update"}} {
		code, _, errOut := runCLI(t, append(cmd, "--frobnicate")...)
		if code != exitUsage || !strings.HasPrefix(errOut, "rhythm-soundcheck: unknown flag --frobnicate") {
			t.Errorf("%v --frobnicate: exit %d %s", cmd, code, errOut)
		}
		code, out, errOut := runCLI(t, cmd[0], "--help")
		if code != exitOK || !strings.Contains(out, "Usage:") || errOut != "" {
			t.Errorf("%s --help: exit %d\n%s\n%s", cmd[0], code, out, errOut)
		}
	}
	for _, args := range [][]string{{"snapshots", "--plain"}, {"snapshots", "-q"}, {"flows", "init", "--plain"}} {
		if code, out, errOut := runCLI(t, args...); code != exitOK || strings.Contains(out, "Usage") {
			t.Errorf("%v: exit %d\n%s\n%s", args, code, out, errOut)
		}
	}
	if code, out, _ := runCLI(t, "snapshots", "--json", "--plain"); code != exitOK || strings.TrimSpace(out) != "[]" {
		t.Errorf("snapshots --json --plain: exit %d %q", code, out)
	}
	for _, args := range [][]string{{"rules", "list", "--plain"}, {"rules", "test", "--plain"}} {
		if code, out, errOut := runCLI(t, args...); code != exitOK || strings.Contains(errOut, "--plain") || strings.Contains(out, "Usage") {
			t.Errorf("%v: exit %d\n%s\n%s", args, code, out, errOut)
		}
	}
}

// -H Host naming another host is a usage error found before the run
// starts: no header, no https probe, exit 2 rather than 3.
func TestHostHeaderIsAUsageError(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"run", "127.0.0.1:21210", "-H", "Host: x"},
		{"snapshot", "127.0.0.1:21210", "--header", "host: shop.test"},
		{"127.0.0.1:21210", "--against", "127.0.0.1:21211", "-H", "Host: x"},
	} {
		code, out, errOut := runCLI(t, args...)
		if code != exitUsage || !strings.Contains(errOut, "-H Host: ") || strings.Contains(errOut+out, "using http") || strings.Contains(errOut+out, "Modules") {
			t.Errorf("%v: exit %d\n%s\n%s", args, code, out, errOut)
		}
	}
	if _, err := os.Stat(".soundcheck/runs"); err == nil {
		if left, _ := os.ReadDir(".soundcheck/runs"); len(left) > 0 {
			t.Errorf("a run folder was made: %s", left[0].Name())
		}
	}
}

// A login.url on another site than the one checked is a usage error
// found before the run starts, naming login.url; a path or the site's
// own host goes ahead.
func TestLoginURLOnAnotherSite(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_USER", "admin")
	t.Setenv("SOUNDCHECK_PASSWORD", "s3cret")
	if err := os.MkdirAll(".soundcheck", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".soundcheck/config.yaml", []byte("login:\n  url: http://127.0.0.1:21201/wp-login.php\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := ".soundcheck/config.yaml: login.url is on 127.0.0.1:21201, not the site being checked (127.0.0.1:21200); the password is only typed on the site being checked"
	for _, args := range [][]string{{"run", "127.0.0.1:21200"}, {"snapshot", "127.0.0.1:21200"}, {"127.0.0.1:21201", "--against", "127.0.0.1:21200"}} {
		code, out, errOut := runCLI(t, append(args, "--plain")...)
		if code != exitUsage || !strings.Contains(errOut, want) || strings.Contains(out+errOut, "using http") {
			t.Errorf("%v: exit %d\n%s\n%s", args, code, out, errOut)
		}
	}
	s := &session{plan: &plan{cfg: config.Default()}}
	s.cfg.Login.URL = "http://www.127.0.0.1.nip.test:21200/wp-login.php"
	for site, ok := range map[string]bool{"127.0.0.1:21200": false, "http://www.127.0.0.1.nip.test:21200/": true, "127.0.0.1.nip.test:21200": true} {
		if err := s.loginHost(site); (err == nil) != ok {
			t.Errorf("%s: %v", site, err)
		}
	}
	s.cfg.Login.URL = "/wp-login.php"
	if err := s.loginHost("127.0.0.1:21200"); err != nil {
		t.Errorf("a path: %v", err)
	}
	s.o.noLogin = true
	s.cfg.Login.URL = "http://elsewhere.test/wp-login.php"
	if err := s.loginHost("127.0.0.1:21200"); err != nil {
		t.Errorf("--no-login: %v", err)
	}
}

// The speed: block goes to the speed module (check.Configurable): its
// errors stop the command before anything runs, naming the file and
// line. Its value reaches the module a comparison uses.
func TestSpeedBlockReachesTheModule(t *testing.T) {
	t.Chdir(t.TempDir())
	for body, want := range map[string]string{
		"speed:\n  slowr: 5\n":  `.soundcheck/config.yaml line 2: unknown setting "speed.slowr" (did you mean "speed.slower"?)`,
		"speed:\n  slower: 0\n": `.soundcheck/config.yaml line 2: speed.slower: must be a percentage between 1 and 1000`,
		"speed: fast\n":         `.soundcheck/config.yaml line 1: speed: takes a group of settings, each on its own indented line`,
	} {
		writeConfig(t, body)
		if code, _, errOut := runCLI(t, "run", "127.0.0.1:21212", "--plain"); code != exitUsage || !strings.Contains(errOut, want) {
			t.Errorf("%q: exit %d %q, want %s", body, code, errOut, want)
		}
	}
	writeConfig(t, "speed:\n  slower: 60\n")
	p, err := parse([]string{"a.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = speedModule.Configure(yaml.Node{}) })
	if !slices.ContainsFunc(p.modules(p.cfg), func(m check.Module) bool { return m == check.Module(speedModule) }) {
		t.Fatal("speed isn't among the modules")
	}
	before := &model.Run{Pages: []model.Page{{Path: "/", Status: 200, Metrics: model.Metrics{LCP: 1000}}}}
	after := &model.Run{Pages: []model.Page{{Path: "/", Status: 200, Metrics: model.Metrics{LCP: 1500}}}}
	if got := speedModule.Compare(context.Background(), &check.CompareContext{Config: p.cfg, Before: before, After: after}); len(got) != 0 {
		t.Errorf("50%% slower reported with speed.slower 60: %+v", got)
	}
}
