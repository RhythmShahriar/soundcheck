// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"strings"
	"testing"
)

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
		{[]string{"--eaa", "--no-sandbox"}, false, "--eaa --no-sandbox", ""},
		{[]string{"example.com"}, false, "", "takes no URL"},
		{[]string{"--modules", "health"}, false, "", "demo doesn't take --modules; it takes --plain"},
		{[]string{"--fail-on", "broke", "--level", "click"}, false, "", "demo doesn't take --fail-on, --level"},
		{[]string{"--frobnicate"}, false, "", "unknown flag --frobnicate"},
	} {
		keep, _, rest, err := demoOptions(tc.args)
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
