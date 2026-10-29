// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package e2etest builds rhythm-soundcheck once per test binary and
// runs it for the end-to-end test packages under e2e: each package has
// its own test binary and timeout, so none comes near Go's 10 minute
// default on a slow runner. It holds the helpers those tests share.
package e2etest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/model"
)

// The exit codes, as the command names them.
const (
	ExitOK       = cli.ExitOK
	ExitFindings = cli.ExitFindings
	ExitUsage    = cli.ExitUsage
	ExitError    = cli.ExitUnavailable
)

// bin is the rhythm-soundcheck binary TestMain builds for the tests.
var bin string

// Main is a test package's TestMain: it builds the command once, named
// as installed and with the version the command's own tests give it,
// runs the tests and removes the binary afterwards.
func Main(m *testing.M) {
	os.Exit(func() int {
		dir, err := os.MkdirTemp("", "soundcheck-e2e-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer func() { _ = os.RemoveAll(dir) }()
		bin = filepath.Join(dir, "rhythm-soundcheck")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		args := append([]string{"build"}, raceFlag...)
		args = append(args, "-ldflags", "-X main.version=1.0.0-test", "-o", bin, "github.com/RhythmShahriar/soundcheck/cmd/rhythm-soundcheck")
		build := exec.Command(goTool(), args...) //nolint:gosec // the go tool building this module's own command
		build.Stdout, build.Stderr = os.Stderr, os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "building rhythm-soundcheck:", err)
			return 1
		}
		return m.Run()
	}())
}

// goTool is the go command of the toolchain running the tests.
func goTool() string {
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if p := filepath.Join(runtime.GOROOT(), "bin", name); fileExists(p) { //nolint:staticcheck // the toolchain that built this test
		return p
	}
	return "go"
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// RunCLI runs the binary with args in the current folder and the test's
// environment, with no update checks and no rhythm CLI around it. It
// returns the exit code and what it printed, with project paths in /
// form (see slashes).
func RunCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, args...) //nolint:gosec // the binary TestMain built
	cmd.Dir = wd
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, cli.InvokedAsEnv+"=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "RHYTHM_NO_UPDATE_CHECK=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatalf("running rhythm-soundcheck: %v", err)
	}
	return code, slashes(out.String()), slashes(errb.String())
}

// slashes turns the Windows path separators in the paths of the project's
// own files (.soundcheck\config.yaml) and of the tests' out\ folder into
// /, so the tests can spell them one way. The CLI itself prints paths the
// way the OS writes them. Other backslashes, such as escaped control
// characters, stay as they are.
func slashes(s string) string {
	if runtime.GOOS != "windows" {
		return s
	}
	return projectPath.ReplaceAllStringFunc(s, filepath.ToSlash)
}

var projectPath = regexp.MustCompile(`(?:\.soundcheck|\bout)(?:\\[\w.~-]+)+`)

// NeedBrowser skips the test when no Chrome or Chromium is installed.
func NeedBrowser(t *testing.T) {
	t.Helper()
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// ReadRunJSON reads the run.json of a run or snapshot folder.
func ReadRunJSON(t *testing.T, dir string) model.Run {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "run.json")) //nolint:gosec // a run folder the test made
	if err != nil {
		t.Fatal(err)
	}
	var r model.Run
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// CompareJSON runs compare --json with args and reads the comparison.
func CompareJSON(t *testing.T, args ...string) (int, *model.Comparison) {
	t.Helper()
	code, out, errOut := RunCLI(t, append([]string{"compare", "--json"}, args...)...)
	var c model.Comparison
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("compare: exit %d: %v\n%s\n%s", code, err, out, errOut)
	}
	return code, &c
}

// WriteConfig writes .soundcheck/config.yaml in the current folder, with
// an empty flows folder next to it.
func WriteConfig(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(".soundcheck/flows", 0o755); err != nil { //nolint:gosec // a project folder in the test's own temporary folder
		t.Fatal(err)
	}
	if err := os.WriteFile(".soundcheck/config.yaml", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// EAAPage writes a page of the EAA fixture sites: a title, a nav and
// body, with a label on every field unless body leaves it out.
func EAAPage(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>%s</title><link rel="icon" href="data:,"></head><body>
<header><nav aria-label="Main"><a href="/">Home</a> <a href="/checkout/">Checkout</a> <a href="/about/">About</a> <a href="/help/">Help</a></nav></header>
<main><h1>%s</h1>%s</main></body></html>`, title, title, body)
}

// EAACommon are the flags the browser tests share: one viewport, no
// download, a quick rate and plain output.
var EAACommon = []string{"--no-download", "--viewport", "desktop", "--rate", "50", "--plain"}

// ChangeList shows the changes of c one per line, for a failure message.
func ChangeList(c *model.Comparison) string {
	var b strings.Builder
	for _, ch := range c.Changes {
		fmt.Fprintf(&b, "\n  %s %s %v", ch.Class, ch.Check, ch.Pages)
	}
	return b.String()
}
