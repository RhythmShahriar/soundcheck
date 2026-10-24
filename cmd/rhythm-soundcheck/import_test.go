// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
)

// fixture returns the absolute path of an importer test file, so tests
// can change into a temporary project folder.
func fixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "importer", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportBackstop(t *testing.T) {
	src, site := fixture(t, "backstop.json"), fixture(t, "backstop-site.json")
	t.Chdir(t.TempDir())
	code, out, errOut := runCLI(t, "import", src)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	for _, want := range []string{"Wrote .soundcheck/config.yaml from ", "  site: https://garris.github.io", "  pages.extra: /BackstopJS/",
		"Not imported:\n", "  - onReadyScript puppet/onReady.js: scripts aren't run", "Next: rhythm-soundcheck snapshot, then"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	cfg, err := config.Load(filepath.Join(config.Dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Site != "https://garris.github.io" || strings.Join(cfg.Pages.Extra, ",") != "/BackstopJS/" {
		t.Errorf("config = %+v", cfg)
	}

	// An existing config is kept unless --force.
	before, _ := os.ReadFile(filepath.Join(config.Dir, "config.yaml"))
	code, _, errOut = runCLI(t, "import", site)
	if code != exitUsage || errOut != "rhythm-soundcheck: .soundcheck/config.yaml already exists (--force replaces it)\n" {
		t.Errorf("without --force: exit %d %q", code, errOut)
	}
	if after, _ := os.ReadFile(filepath.Join(config.Dir, "config.yaml")); string(after) != string(before) {
		t.Error("the config changed without --force")
	}
	if code, _, errOut := runCLI(t, "import", site, "--force"); code != exitOK {
		t.Errorf("--force: exit %d %s", code, errOut)
	}
}

func TestImportLostPixel(t *testing.T) {
	src := fixture(t, "lostpixel.config.ts")
	t.Chdir(t.TempDir())
	code, _, errOut := runCLI(t, "import", src)
	if code != exitUsage || !strings.Contains(errOut, "is code, which Soundcheck never runs; add --pages-only") {
		t.Errorf("without --pages-only: exit %d %q", code, errOut)
	}
	code, out, errOut := runCLI(t, "import", src, "--pages-only", "--site", "staging.app.test")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if !strings.Contains(out, "pages.extra: /app, /next-app, /pricing") {
		t.Errorf("output:\n%s", out)
	}
	cfg, err := config.Load(filepath.Join(config.Dir, "config.yaml"))
	if err != nil || cfg.Site != "staging.app.test" || strings.Join(cfg.Pages.Extra, " ") != "/app /next-app /pricing" {
		t.Errorf("config = %+v, %v", cfg, err)
	}
}

func TestImportRefused(t *testing.T) {
	malformed, foreign := fixture(t, "malformed.json"), fixture(t, "foreign.json")
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{malformed}, malformed + ": line 4: invalid character"},
		{[]string{malformed, "--pages-only"}, "--pages-only is for a Lost Pixel config"},
		{[]string{"missing.json"}, "missing.json"},
		{[]string{}, "usage: rhythm-soundcheck import <backstop.json>"},
		{[]string{malformed, foreign}, "usage: "},
		{[]string{foreign, "--site", "://"}, `"://" isn't a site`},
		{[]string{foreign, "--forse"}, "unknown flag --forse (did you mean --force?)"},
	} {
		code, _, errOut := runCLI(t, append([]string{"import"}, tc.args...)...)
		if code != exitUsage || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d %q, want %q", tc.args, code, errOut, tc.want)
		}
	}
	if _, err := os.Stat(config.Dir); !os.IsNotExist(err) {
		t.Errorf("a refused import wrote %s: %v", config.Dir, err)
	}

	// Only foreign hosts: written, with every URL listed and no pages.
	code, out, errOut := runCLI(t, "import", foreign, "--site", "staging.shop.test")
	if code != exitOK || !strings.Contains(out, "https://blog.other.test/ isn't on staging.shop.test") || strings.Contains(out, "pages.extra") {
		t.Errorf("foreign hosts: exit %d\n%s\n%s", code, out, errOut)
	}
}

// Importing never starts a process: a node, npx or shell put first on
// PATH would leave a mark if anything ran it.
func TestImportRunsNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the trap programs are shell scripts")
	}
	src, backstop := fixture(t, "lostpixel.config.ts"), fixture(t, "backstop.json")
	bin, mark := t.TempDir(), filepath.Join(t.TempDir(), "ran")
	for _, name := range []string{"node", "npx", "npm", "tsx", "ts-node", "deno", "bun", "sh", "bash", "esbuild", "git"} {
		script := "#!/bin/sh\necho " + name + " >> " + mark + "\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil { //nolint:gosec // a test program
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Chdir(t.TempDir())
	if code, _, errOut := runCLI(t, "import", src, "--pages-only"); code != exitOK {
		t.Fatalf("exit %d %s", code, errOut)
	}
	if code, _, errOut := runCLI(t, "import", backstop, "--force"); code != exitOK {
		t.Fatalf("exit %d %s", code, errOut)
	}
	if ran, err := os.ReadFile(mark); err == nil {
		t.Errorf("import started %s", ran)
	}
}

// A backstop.json from someone else can't drive the terminal: escapes in
// its selectors and URLs are printed as text.
func TestImportEscapesControlCharacters(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "backstop.json")
	const data = `{"scenarios": [
		{"label": "Home", "url": "https://shop.test/", "hideSelectors": [".ad\u001b]0;owned\u0007"]},
		{"label": "Bad", "url": "https://shop.test/\u001b[2J"}
	]}`
	if err := os.WriteFile(src, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	code, out, errOut := runCLI(t, "import", src)
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("control characters reached the terminal:\n%q", out)
	}
	for _, want := range []string{`.ad\x1b]0;owned\a`, `https://shop.test/\x1b[2J`} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"plain .cookie-banner, é and 日本": "plain .cookie-banner, é and 日本",
		"a\x1b[31mred":                   `a\x1b[31mred`,
		"tab\there":                      `tab\there`,
		"rtl\u202eevil":                  `rtl\u202eevil`,
	} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
	}
}
