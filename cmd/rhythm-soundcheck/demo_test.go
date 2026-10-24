// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestDemo(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome twice")
	}
	t.Chdir(t.TempDir())
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // where the demo keeps its snapshot, on Unix
	code, out, errOut := runCLI(t, "demo", "--plain", "--no-download")
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)^\[BROKE\] Form submission fails \(HTTP 500\).* - /contact/$`),
		regexp.MustCompile(`(?m)^\[BROKE\] Page not found \(404\) - /pricing/$`),
		regexp.MustCompile(`(?m)^Now try it on your staging site: rhythm-soundcheck snapshot staging.example.com$`),
	} {
		if !want.MatchString(out) {
			t.Errorf("no %s in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "full report:") {
		t.Errorf("points at a report it removes:\n%s", out)
	}
	if _, err := os.Stat(".soundcheck"); !os.IsNotExist(err) {
		t.Errorf("the demo wrote to the project's .soundcheck: %v", err)
	}
	if left, _ := os.ReadDir(tmp); len(left) > 0 {
		t.Errorf("the demo left %s in the temp folder", left[0].Name())
	}
}

func TestDemoOptions(t *testing.T) {
	for _, tc := range []struct {
		args []string
		keep bool
		rest string
		err  string
	}{
		{nil, false, "", ""},
		{[]string{"--plain", "-v", "--no-download"}, false, "--plain -v --no-download", ""},
		{[]string{"--keep", "--plain"}, true, "--plain", ""},
		{[]string{"--keep=true"}, true, "", ""},
		{[]string{"--open"}, true, "--open", ""}, // the report must outlive the demo
		{[]string{"--browser", "/usr/bin/chromium"}, false, "--browser /usr/bin/chromium", ""},
		{[]string{"example.com"}, false, "", "takes no URL"},
		{[]string{"--modules", "health"}, false, "", "demo doesn't take --modules; it takes --plain"},
		{[]string{"--fail-on", "broke", "--level", "click"}, false, "", "demo doesn't take --fail-on, --level"},
		{[]string{"--frobnicate"}, false, "", "unknown flag --frobnicate"},
	} {
		keep, rest, err := demoOptions(tc.args)
		switch {
		case tc.err != "":
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%v: err %v, want %q", tc.args, err, tc.err)
			}
		case err != nil:
			t.Errorf("%v: %v", tc.args, err)
		case keep != tc.keep || strings.Join(rest, " ") != tc.rest:
			t.Errorf("%v: keep %v rest %q, want %v %q", tc.args, keep, rest, tc.keep, tc.rest)
		}
	}
}

func TestDemoUsage(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, errOut := runCLI(t, "demo", "example.com"); code != exitUsage || !strings.Contains(errOut, "rhythm-soundcheck: demo checks its own site") {
		t.Errorf("exit %d\n%s", code, errOut)
	}
	if code, out, _ := runCLI(t, "demo", "--help"); code != exitOK || !strings.Contains(out, "rhythm-soundcheck demo") {
		t.Errorf("demo --help: exit %d", code)
	}
	if _, err := os.Stat(".soundcheck"); !os.IsNotExist(err) {
		t.Errorf("a refused demo wrote .soundcheck: %v", err)
	}
}
