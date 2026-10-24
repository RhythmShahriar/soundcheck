// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
)

func TestSiteKey(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_USER", "")
	echo := deadSite(t) // stops each run right after the header
	writeConfig(t, "site: 127.0.0.1:20261\n")
	for _, cmd := range []string{"run", "snapshot"} {
		_, out, errOut := runCLI(t, cmd, "--echo="+echo, "--plain", "--modules", "health")
		if !strings.HasPrefix(out, "rhythm soundcheck 1.0.0-test - 127.0.0.1:20261\n\nSite: from .soundcheck/config.yaml (site)\nModules:") ||
			!strings.Contains(errOut, "Echo isn't running") {
			t.Errorf("%s with site:\n%s\n%s", cmd, out, errOut)
		}
		// A site on the command line wins.
		_, out, _ = runCLI(t, cmd, "127.0.0.1:20262", "--echo="+echo, "--plain", "--modules", "health")
		if !strings.HasPrefix(out, "rhythm soundcheck 1.0.0-test - 127.0.0.1:20262\n\nModules:") {
			t.Errorf("%s with an argument:\n%s", cmd, out)
		}
	}
	writeConfig(t, "")
	if code, _, errOut := runCLI(t, "run"); code != exitUsage || !strings.Contains(errOut, "run needs one site") {
		t.Errorf("no site: exit %d %q", code, errOut)
	}
	writeConfig(t, "site: \"http://\"\n")
	if code, _, errOut := runCLI(t, "run"); code != exitUsage ||
		errOut != "rhythm-soundcheck: .soundcheck/config.yaml line 1: site: \"http://\" isn't a site like staging.example.com\n" {
		t.Errorf("bad site: exit %d %q", code, errOut)
	}
}

// A site from the config file is as untrusted as the file: no password,
// header secret or flow variable goes to it.
func TestSiteKeyGetsNoSecrets(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_USER", "tester")
	t.Setenv("SOUNDCHECK_PASSWORD", "hunter2-DEMO")
	t.Setenv("SOUNDCHECK_AUTH", "auth-DEMO")
	echo := deadSite(t)

	writeConfig(t, "site: collector.invalid\n")
	_, out, errOut := runCLI(t, "run", "--echo="+echo, "--plain", "--modules", "health")
	if !strings.Contains(errOut, "rhythm-soundcheck: not logging in to collector.invalid: the site came from .soundcheck/config.yaml (site); name it on the command line to log in\n") ||
		strings.Contains(out, "Login:") {
		t.Errorf("login:\n%s\n%s", out, errOut)
	}
	// Named on the command line, it logs in (up to the dead Echo).
	_, out, _ = runCLI(t, "run", "collector.invalid", "--echo="+echo, "--plain", "--modules", "health")
	if strings.Contains(out, "Login:") {
		t.Errorf("Login comes after Echo connects; got\n%s", out)
	}

	writeConfig(t, "site: collector.invalid\nheaders:\n  X-Auth: SOUNDCHECK_AUTH\n")
	code, _, errOut := runCLI(t, "run", "--echo="+echo, "--plain", "--modules", "health")
	if code != exitUsage || !strings.Contains(errOut, "not sending the headers X-Auth to collector.invalid: the site came from .soundcheck/config.yaml (site)") {
		t.Errorf("headers: exit %d %q", code, errOut)
	}
	// On this machine it's fine.
	writeConfig(t, "site: 127.0.0.1:20263\nheaders:\n  X-Auth: SOUNDCHECK_AUTH\n")
	if _, _, errOut := runCLI(t, "run", "--echo="+echo, "--plain", "--modules", "health"); !strings.Contains(errOut, "Echo isn't running") {
		t.Errorf("loopback site: %q", errOut)
	}

	writeConfig(t, "site: collector.invalid\ninteractions:\n  levels: [flows]\n")
	_ = os.WriteFile(".soundcheck/flows/note.yaml", []byte("name: Note\nsteps:\n  - goto: \"{site}/?n=${env:SOUNDCHECK_PASSWORD}\"\n"), 0o600)
	code, _, errOut = runCLI(t, "run", "--echo="+echo, "--plain", "--modules", "health,interact", "--no-login")
	if code != exitUsage || !strings.Contains(errOut, "note.yaml: not letting it read SOUNDCHECK_PASSWORD on collector.invalid: the site came from .soundcheck/config.yaml (site)") {
		t.Errorf("flows: exit %d %q", code, errOut)
	}
	if strings.Contains(errOut, "DEMO") {
		t.Error("a value was printed")
	}
}

func TestHeadersKey(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_AUTH", "auth-DEMO")
	t.Setenv("SOUNDCHECK_EMPTY", "")
	t.Setenv("CI_TOKEN", "ci-DEMO")
	writeConfig(t, "headers:\n  X-Auth: SOUNDCHECK_AUTH\n  X-Other: SOUNDCHECK_AUTH\n")
	p, err := parse([]string{"a.test", "-H", "x-other: from-flag"})
	if err != nil || p.headers["X-Auth"] != "auth-DEMO" || p.headers["x-other"] != "from-flag" || p.headers["X-Other"] != "" {
		t.Errorf("headers %v, %v", p.headers, err)
	}
	writeConfig(t, "headers:\n  Authorization: CI_TOKEN\n")
	if _, err := parse([]string{"a.test"}); err == nil || err.Error() !=
		".soundcheck/config.yaml: headers: Authorization reads CI_TOKEN; headers read only SOUNDCHECK_* variables unless you pass --allow-env CI_TOKEN" {
		t.Errorf("not allowed: %v", err)
	}
	if p, err := parse([]string{"a.test", "--allow-env", "CI_TOKEN"}); err != nil || p.headers["Authorization"] != "ci-DEMO" {
		t.Errorf("--allow-env: %v %v", p, err)
	}
	for _, env := range []string{"SOUNDCHECK_MISSING", "SOUNDCHECK_EMPTY"} {
		writeConfig(t, "headers:\n  X-Auth: "+env+"\n")
		code, _, errOut := runCLI(t, "run", "a.test")
		if code != exitUsage || errOut != "rhythm-soundcheck: .soundcheck/config.yaml: headers: X-Auth reads "+env+", which isn't set\n" {
			t.Errorf("%s: exit %d %q", env, code, errOut)
		}
	}
}

func TestFailOnKey(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	writeConfig(t, "fail_on: broke\n")
	p, err := parse([]string{"a.test"})
	if err != nil || p.o.failOn != "broke" || p.failOnFrom != ".soundcheck/config.yaml" {
		t.Fatalf("%+v %v", p, err)
	}
	if p, _ := parse([]string{"a.test", "--fail-on", "new"}); p.o.failOn != "new" || p.failOnFrom != "" {
		t.Errorf("--fail-on wins: %q %q", p.o.failOn, p.failOnFrom)
	}
	// compare uses it (a bad class for compare would stop it here).
	if code, _, errOut := runCLI(t, "compare"); code != exitUsage || !strings.Contains(errOut, "no snapshot yet") {
		t.Errorf("compare: exit %d %q", code, errOut)
	}
	// run leaves a comparison class out, with a note.
	echo := deadSite(t)
	_, _, errOut := runCLI(t, "run", "127.0.0.1:20264", "--echo="+echo, "--plain")
	if !strings.Contains(errOut, "Note: fail_on: broke in .soundcheck/config.yaml is for comparisons, so it doesn't apply here\n") ||
		!strings.Contains(errOut, "Echo isn't running") {
		t.Errorf("run: %q", errOut)
	}
	writeConfig(t, "fail_on: error\n")
	if code, _, errOut := runCLI(t, "compare"); code != exitUsage || !strings.Contains(errOut, "Note: fail_on: error in .soundcheck/config.yaml is for run and snapshot") {
		t.Errorf("compare with a level: exit %d %q", code, errOut)
	}
	// --fail-on that fits the other kind is still an error.
	writeConfig(t, "")
	if code, _, errOut := runCLI(t, "run", "a.test", "--fail-on", "broke"); code != exitUsage || !strings.Contains(errOut, `--fail-on (fail_on): "broke" is for comparisons; run and snapshot take error, warning or notice`) {
		t.Errorf("--fail-on broke on run: exit %d %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "run", "a.test", "--fail-on", "brok"); code != exitUsage || errOut !=
		"rhythm-soundcheck: --fail-on (fail_on): \"brok\" isn't one of broke, new, changed, slower, error, warning, notice, none (did you mean \"broke\"?)\n" {
		t.Errorf("--fail-on brok: exit %d %q", code, errOut)
	}
	if want := append(slices.Clone(model.Classes[:4]), func() (l []string) {
		for _, v := range report.Levels {
			l = append(l, string(v))
		}
		return l
	}()...); !slices.Equal(config.FailOnValues, want) {
		t.Errorf("FailOnValues %v, want %v", config.FailOnValues, want)
	}
}

func TestTimeoutFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	for v, want := range map[string]config.Seconds{"30": 30, "30s": 30, "1m": 60, "2m30s": 150, "45.4": 45} {
		if p, err := parse([]string{"a.test", "--timeout", v}); err != nil || p.cfg.Timeout != want {
			t.Errorf("--timeout %s: %v", v, err)
		}
	}
	for v, want := range map[string]string{
		"fast": `"fast" isn't a number of seconds or a duration like 30s`,
		"2":    "--timeout (timeout): must be at least 5 seconds",
		"-1m":  "up to a day",
		"NaN":  "up to a day",
	} {
		if code, _, errOut := runCLI(t, "run", "a.test", "--timeout", v); code != exitUsage || !strings.Contains(errOut, want) {
			t.Errorf("--timeout %s: exit %d %q, want %q", v, code, errOut, want)
		}
	}
}

func TestFlagValueErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--modules", "helth"}, `--modules (modules): "helth" isn't one of health, visual, interact, cache, speed, echo (did you mean "health"?)`},
		{[]string{"--level", "clik"}, `--level (interactions.levels): "clik" isn't one of inspect, click, submit, flows (did you mean "click"?)`},
		{[]string{"--viewport", "moble"}, `--viewport (viewports): "moble" isn't one of desktop, mobile (did you mean "mobile"?)`},
		{[]string{"--skip", "sped"}, `--skip: "sped" isn't one of health, visual, interact, cache, speed, echo (did you mean "speed"?)`},
		{[]string{"--tabs", "0"}, `--tabs (tabs): must be between 1 and 16`},
		{[]string{"--limit", "0"}, `--limit (pages.limit): must be at least 1`},
	} {
		code, _, errOut := runCLI(t, append([]string{"run", "a.test"}, tc.args...)...)
		if code != exitUsage || errOut != "rhythm-soundcheck: "+tc.want+"\n" {
			t.Errorf("%v: exit %d\n got %q\nwant %q", tc.args, code, errOut, tc.want)
		}
	}
	writeConfig(t, "rate: 2\n\npagez:\n  limit: 5\n")
	if code, _, errOut := runCLI(t, "run", "a.test"); code != exitUsage ||
		errOut != "rhythm-soundcheck: .soundcheck/config.yaml line 3: unknown setting \"pagez\" (did you mean \"pages\"?)\n" {
		t.Errorf("unknown key: exit %d %q", code, errOut)
	}
}

func TestInitLoads(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, errOut := runCLI(t, "init"); code != exitOK {
		t.Fatal(errOut)
	}
	c, err := config.Load(".soundcheck/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"site", "fail_on", "headers", "timeout", "pages.limit", "echo.quiet"} {
		if !c.InFile(key) {
			t.Errorf("init doesn't show %s", key)
		}
	}
	if c.Timeout != 30 || c.Site != "" || c.FailOn != "" || len(c.Headers) != 0 {
		t.Errorf("%+v", c)
	}
}

func TestFailOnNoneAndCase(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	for _, v := range []string{"none", "None", "NONE"} {
		if p, err := parse([]string{"a.test", "--fail-on", v}); err != nil || p.o.failOn != "" || !p.failOnSet {
			t.Errorf("--fail-on %s: %+v %v", v, p, err)
		}
	}
	for _, v := range []string{"broke", "BROKE", "Broke"} {
		if p, err := parse([]string{"a.test", "--fail-on", v}); err != nil || p.o.failOn != "broke" {
			t.Errorf("--fail-on %s: %v", v, err)
		}
	}
	code, _, errOut := runCLI(t, "compare", "--fail-on", "nope")
	if want := "rhythm-soundcheck: --fail-on (fail_on): \"nope\" isn't one of broke, new, changed, slower, error, warning, notice, none (did you mean \"none\"?)\n"; code != exitUsage || errOut != want {
		t.Errorf("--fail-on nope: exit %d %q", code, errOut)
	}

	// none fits run and compare alike, and turns off the config's fail_on.
	dir := storedComparison(t, model.Broke)
	writeConfig(t, "fail_on: broke\n")
	if code, _, errOut := runCLI(t, "report", dir, "--plain"); code != exitFindings {
		t.Errorf("fail_on: broke: exit %d %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "report", dir, "--plain", "--fail-on", "BROKE"); code != exitFindings {
		t.Errorf("--fail-on BROKE: exit %d %q", code, errOut)
	}
	for _, args := range [][]string{{"--fail-on", "none"}, {"--fail-on", "None"}} {
		if code, _, errOut := runCLI(t, append([]string{"report", dir, "--plain"}, args...)...); code != exitOK || strings.Contains(errOut, "tip:") {
			t.Errorf("%v: exit %d %q", args, code, errOut)
		}
	}
	writeConfig(t, "fail_on: None\n")
	if code, _, errOut := runCLI(t, "report", dir, "--plain"); code != exitOK || strings.Contains(errOut, "tip:") || strings.Contains(errOut, "Note:") {
		t.Errorf("fail_on: None: exit %d %q", code, errOut)
	}
	if p, err := parse([]string{"a.test"}); err != nil || p.o.failOn != "" || p.failOnFrom != "" {
		t.Errorf("fail_on: None for run: %+v %v", p, err)
	}
	writeConfig(t, "fail_on: Error\n")
	if p, err := parse([]string{"a.test"}); err != nil || p.o.failOn != "error" {
		t.Errorf("fail_on: Error: %v", err)
	}
}
