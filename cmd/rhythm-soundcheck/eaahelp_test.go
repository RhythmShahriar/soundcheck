// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// golden compares out with testdata/<name>, rewriting it with -update.
func golden(t *testing.T, name, out string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Errorf("%s changed; if that's intended, run with -update\n--- got\n%s", name, out)
	}
}

// The EAA help topics are kept as goldens, fit 80 columns and carry the
// fixed sentences word for word.
func TestEAAHelpTopics(t *testing.T) {
	for _, name := range []string{"eaa", "keyboard", "statement", "evidence"} {
		code, out, _ := runCLI(t, "help", name)
		if code != exitOK {
			t.Fatalf("help %s: exit %d", name, code)
		}
		golden(t, "help-"+name+".golden", out)
		if name == "eaa" || name == "keyboard" {
			for i, line := range strings.Split(out, "\n") {
				if n := len([]rune(line)); n > 80 {
					t.Errorf("help %s line %d is %d wide: %q", name, i+1, n, line)
				}
			}
		}
	}
	_, out, _ := runCLI(t, "help", "eaa")
	flat := strings.Join(strings.Fields(out), " ")
	for _, s := range append([]string{model.EvidenceNote, wcagEM}, limits...) {
		if !strings.Contains(flat, s) {
			t.Errorf("help eaa lacks %q", s)
		}
	}
	tested, person, all := coverage("2.1")
	if all != len(wcag.Criteria("2.1")) || tested+person != all {
		t.Errorf("coverage = %d, %d, %d", tested, person, all)
	}
	if want := fmt.Sprintf("Of the %d WCAG 2.1 A and AA success criteria, the tests look for failures of %d", all, tested); !strings.Contains(flat, want) {
		t.Errorf("help eaa lacks the counts %q", want)
	}

	_, out, _ = runCLI(t, "help", "keyboard")
	flat = strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"keyboard/trap (error, wcag 2.1.2): Keyboard focus is trapped.",
		"keyboard/focus-obscured (notice, wcag 2.4.11 with --wcag 2.2)", "keyboard/escape (notice, advisory)", "run on /wp-admin or /wp-login.php"} {
		if !strings.Contains(flat, want) {
			t.Errorf("help keyboard lacks %q", want)
		}
	}
	_, out, _ = runCLI(t, "help", "evidence")
	if !strings.Contains(out, "cosign sign-blob eaa/evidence.json") ||
		!strings.Contains(strings.Join(strings.Fields(out), " "), limits[7]) {
		t.Errorf("help evidence:\n%s", out)
	}
}

// --list-checks names the criteria of the a11y and keyboard checks and
// says advisory for one that fails none; other modules' lines stay as
// they were.
func TestListChecksCriteria(t *testing.T) {
	code, out, _ := runCLI(t, "--list-checks")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	golden(t, "list-checks.golden", out)
	lines := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			lines[f[0]] = strings.Join(f, " ")
		}
	}
	for id, want := range map[string]string{
		"keyboard/trap":           "wcag 2.1.2",
		"keyboard/not-reachable":  "wcag 2.1.1",
		"keyboard/escape":         "advisory",
		"keyboard/focus-obscured": "wcag 2.4.11 with --wcag 2.2",
		"a11y/<axe":               "wcag from the rule's tags",
	} {
		if !strings.HasSuffix(lines[id], want) {
			t.Errorf("%s: %q, want it to end in %q", id, lines[id], want)
		}
	}
	if l := lines["health/php-error"]; strings.Contains(l, "wcag") || strings.Contains(l, "advisory") {
		t.Errorf("health/php-error: %q", l)
	}
	if !strings.Contains(out, "keyboard: off unless asked for (+keyboard); on with --eaa") {
		t.Errorf("keyboard module line:\n%s", out)
	}
	// The checks lane axe adds to the a11y module, each with its criteria.
	for id, want := range map[string]string{"a11y/placeholder-label": "wcag 3.3.2", "a11y/reflow": "wcag 1.4.10", "a11y/overlay": "advisory"} {
		l, ok := lines[id]
		if !ok {
			t.Logf("pending: axe (%s isn't listed yet)", id)
			continue
		}
		if !strings.HasSuffix(l, want) {
			t.Errorf("%s: %q, want %q", id, l, want)
		}
	}
}

func TestWrap(t *testing.T) {
	for _, tc := range []struct {
		text, first, indent string
		width               int
		want                string
	}{
		{"a b c", "", "", 3, "a b\nc\n"},
		{"one two", "  - ", "    ", 9, "  - one\n    two\n"},
		{"https://example.test/a/very/long/path x", "", "", 10, "https://example.test/a/very/long/path\nx\n"},
		{"", "", "", 10, "\n"},
	} {
		if got := wrap(tc.text, tc.first, tc.indent, tc.width); got != tc.want {
			t.Errorf("wrap(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}
