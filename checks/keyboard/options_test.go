// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// load reads a config file holding body, the way the CLI reads
// .soundcheck/config.yaml.
func load(t *testing.T, body string) (config.Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(path)
}

// Each setting takes the values of its range and refuses the ones just
// outside, naming the setting and its line.
func TestSettingsBounds(t *testing.T) {
	cases := []struct {
		yaml string
		err  string // part of the error, "" for none
		want func(Settings) bool
	}{
		{yaml: "max_tabs: 10", want: func(s Settings) bool { return s.MaxTabs == 10 }},
		{yaml: "max_tabs: 2000", want: func(s Settings) bool { return s.MaxTabs == 2000 }},
		{yaml: "max_tabs: 9", err: `keyboard.max_tabs: must be a whole number from 10 to 2000, not 9`},
		{yaml: "max_tabs: 2001", err: "keyboard.max_tabs: must be a whole number from 10 to 2000, not 2001"},
		{yaml: "max_tabs: lots", err: "keyboard.max_tabs"},
		{yaml: "widgets: 0", want: func(s Settings) bool { return s.Widgets == 0 }},
		{yaml: "widgets: 200", want: func(s Settings) bool { return s.Widgets == 200 }},
		{yaml: "widgets: -1", err: "keyboard.widgets: must be a whole number from 0 to 200, not -1"},
		{yaml: "widgets: 201", err: "keyboard.widgets: must be a whole number from 0 to 200, not 201"},
		{yaml: "trap_after: 3", want: func(s Settings) bool { return s.TrapAfter == 3 }},
		{yaml: "trap_after: 20", want: func(s Settings) bool { return s.TrapAfter == 20 }},
		{yaml: "trap_after: 2", err: "keyboard.trap_after: must be a whole number from 3 to 20, not 2"},
		{yaml: "trap_after: 21", err: "keyboard.trap_after: must be a whole number from 3 to 20, not 21"},
		{yaml: "skip_link: required", want: func(s Settings) bool { return s.SkipLink == SkipLinkRequired }},
		{yaml: "skip_link: OFF", want: func(s Settings) bool { return s.SkipLink == SkipLinkOff }},
		{yaml: "skip_link: maybe", err: "keyboard.skip_link: must be expected, required or off"},
		{yaml: `pages: ["/shop/*"]`, want: func(s Settings) bool { return slices.Equal(s.Pages, []string{"/shop/*"}) }},
		{yaml: `pages: ["shop/*"]`, err: `keyboard.pages: "shop/*" isn't a path`},
		{yaml: `ignore: ["#cookie-banner", ".chat > button"]`, want: func(s Settings) bool { return len(s.Ignore) == 2 }},
		{yaml: `ignore: [" "]`, err: "keyboard.ignore:"},
		{yaml: `ignore: ["a\nb"]`, err: "keyboard.ignore:"},
		{yaml: "ignore: [\"" + strings.Repeat("a", 301) + "\"]", err: "at most 300 characters"},
		{yaml: "max_tab: 100", err: `unknown setting "keyboard.max_tab" (did you mean "keyboard.max_tabs"?)`},
		{yaml: "skiplink: off", err: `(did you mean "keyboard.skip_link"?)`},
	}
	for _, c := range cases {
		t.Run(c.yaml, func(t *testing.T) {
			cfg, err := load(t, "keyboard:\n  "+c.yaml+"\n")
			if err == nil {
				err = New().Configure(cfg.Block("keyboard"))
			}
			if c.err != "" {
				var se *config.SettingError
				if err == nil || !strings.Contains(err.Error(), c.err) || !errors.As(err, &se) {
					t.Fatalf("error %v, want one with %q", err, c.err)
				}
				if se.Line != 2 {
					t.Errorf("error on line %d, want 2", se.Line)
				}
				if _, oerr := Options(cfg); oerr == nil && cfg.Block("keyboard").Kind != 0 {
					t.Error("Options takes what Configure refuses")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s, err := decode(cfg.Block("keyboard"))
			if err != nil || !c.want(s) {
				t.Errorf("settings %+v, %v", s, err)
			}
		})
	}
}

// The settings a block leaves out keep their defaults. Options gives
// the CLI the same limits Configure hands the module.
func TestOptionsFromFile(t *testing.T) {
	cfg, err := load(t, "keyboard:\n  max_tabs: 300\n  skip_link: required\n  ignore: ['#chat']\n")
	if err != nil {
		t.Fatal(err)
	}
	o, err := Options(cfg)
	want := model.KeyboardOptions{MaxTabs: 300, Widgets: 10, TrapAfter: 4, SkipLink: "required"}
	if err != nil || o != want {
		t.Errorf("Options = %+v, %v; want %+v", o, err, want)
	}
	m := New()
	if err := check.Configure(m, cfg); err != nil {
		t.Fatal(err)
	}
	s := m.settings()
	if s.MaxTabs != 300 || !slices.Equal(s.Ignore, []string{"#chat"}) || !slices.Equal(s.Pages, DefaultSettings().Pages) {
		t.Errorf("settings %+v", s)
	}
	if m.opts != want {
		t.Errorf("module options %+v, want %+v", m.opts, want)
	}
	// A refused value leaves the module as it was.
	bad, err := load(t, "keyboard:\n  trap_after: 1\n")
	if err == nil {
		err = m.Configure(bad.Block("keyboard"))
	}
	if err == nil || m.settings().MaxTabs != 300 {
		t.Errorf("Configure(trap_after: 1) = %v, settings %+v", err, m.settings())
	}
	if _, err := Options(bad); err == nil {
		t.Error("Options took trap_after: 1")
	}
}

// A Module{} that New didn't make walks with the defaults.
func TestZeroModuleSettings(t *testing.T) {
	var m Module
	if s := m.settings(); s.MaxTabs != 150 || s.TrapAfter != 4 || s.SkipLink != "expected" {
		t.Errorf("settings %+v", s)
	}
}

// --list-checks shows every keyboard check, the two the key flows raise
// included, with its level and title.
func TestChecksList(t *testing.T) {
	got := New().Checks()
	if len(got) != 15 {
		t.Fatalf("%d checks", len(got))
	}
	for _, id := range []string{"keyboard/trap", "keyboard/flow", "keyboard/error-not-linked", "keyboard/focus-obscured"} {
		i := slices.IndexFunc(got, func(c check.CheckInfo) bool { return c.ID == id })
		if i < 0 || got[i].Title == "" || got[i].Level == "" || got[i].Doc == "" {
			t.Errorf("%s: %+v", id, got)
		}
	}
}
