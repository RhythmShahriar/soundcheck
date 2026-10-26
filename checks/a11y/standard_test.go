// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
)

// load reads a config file with body, as the CLI does.
func load(t *testing.T, body string) (config.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(path)
}

func TestStandard(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       string // the version, or the error's text
		err        bool
	}{
		{name: "no block", body: "pages:\n  limit: 5\n", want: "2.1"},
		{name: "empty file", body: "", want: "2.1"},
		{name: "block without wcag", body: "a11y:\n  max_elements: 50\n", want: "2.1"},
		{name: "2.2 quoted", body: "a11y:\n  wcag: \"2.2\"\n", want: "2.2"},
		{name: "2.0 bare", body: "a11y:\n  wcag: 2.0\n", want: "2.0"},
		{name: "2.1 bare", body: "a11y:\n  wcag: 2.1\n", want: "2.1"},
		{name: "major only", body: "a11y:\n  wcag: \"2\"\n", err: true,
			want: `--wcag (a11y.wcag): "2" isn't one of 2.0, 2.1, 2.2`},
		{name: "no such version", body: "a11y:\n  wcag: \"3.0\"\n", err: true,
			want: `--wcag (a11y.wcag): "3.0" isn't one of 2.0, 2.1, 2.2`},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := load(t, c.body)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Standard(cfg)
			if c.err {
				if err == nil || err.Error() != c.want {
					t.Errorf("Standard() = %q, %v; want the error %q", got, err, c.want)
				}
				return
			}
			if err != nil || got != c.want {
				t.Errorf("Standard() = %q, %v; want %q", got, err, c.want)
			}
		})
	}
	if v, err := Standard(config.Default()); err != nil || v != "2.1" {
		t.Errorf("Standard(Default()) = %q, %v", v, err)
	}
}

// Every setting of the block is checked before anything runs: Load
// leaves the block to the module and the CLI hands it over with
// check.Configure.
func TestConfigure(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       string // part of the error; "" for none
	}{
		{name: "defaults", body: ""},
		{name: "all set", body: "a11y:\n  wcag: \"2.2\"\n  max_elements: 200\n  iframes: true\n"},
		{name: "lowest cap", body: "a11y:\n  max_elements: 1\n"},
		{name: "no elements", body: "a11y:\n  max_elements: 0\n", want: "a11y.max_elements: must be a number from 1 to 200"},
		{name: "too many", body: "a11y:\n  max_elements: 201\n", want: "a11y.max_elements: must be a number from 1 to 200"},
		{name: "not a number", body: "a11y:\n  max_elements: lots\n", want: "max_elements"},
		{name: "typo", body: "a11y:\n  max_element: 30\n", want: `did you mean "a11y.max_elements"`},
		{name: "unknown key", body: "a11y:\n  rules: [label]\n", want: "a11y.rules"},
		{name: "not a group", body: "a11y: yes\n", want: "takes a group of settings"},
		{name: "bad version", body: "a11y:\n  wcag: \"2\"\n", want: `--wcag (a11y.wcag): "2" isn't one of 2.0, 2.1, 2.2`},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := load(t, c.body)
			if err != nil {
				t.Fatal(err)
			}
			err = check.Configure(Module{}, cfg)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("Configure: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("Configure = %v, want an error with %q", err, c.want)
			}
		})
	}
}

func TestSettingsOf(t *testing.T) {
	cfg, err := load(t, "a11y:\n  max_elements: 7\n  iframes: true\n")
	if err != nil {
		t.Fatal(err)
	}
	if s := settingsOf(cfg); s != (Settings{WCAG: "2.1", MaxElements: 7, IFrames: true}) {
		t.Errorf("settingsOf = %+v", s)
	}
	if s := settingsOf(config.Default()); s != DefaultSettings {
		t.Errorf("settingsOf(Default()) = %+v", s)
	}
}

func TestTags(t *testing.T) {
	for v, want := range map[string][]string{
		"2.0": {"wcag2a", "wcag2aa"},
		"":    {"wcag2a", "wcag2aa"}, // a run before 1.3
		"2.1": {"wcag2a", "wcag2aa", "wcag21a", "wcag21aa"},
		"2.2": {"wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"},
	} {
		if got := tags(v); !slices.Equal(got, want) {
			t.Errorf("tags(%q) = %v, want %v", v, got, want)
		}
	}
}
