// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

func TestEffectiveLevelsAndModules(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	for _, tc := range []struct {
		config  string
		args    []string
		modules string
		levels  string
	}{
		{"", nil, "health,visual,interact,cache,speed", "inspect,click"},
		{"", []string{"--level", "submit"}, "health,visual,interact,cache,speed", "inspect,click,submit"},
		{"", []string{"--level", "flows"}, "health,visual,interact,cache,speed", "inspect,click,submit,flows"},
		{"", []string{"--level", "inspect"}, "health,visual,interact,cache,speed", "inspect"},
		{"", []string{"--level", "+submit"}, "health,visual,interact,cache,speed", "inspect,click,submit"},
		{"", []string{"--level", "+flows", "--level", "click"}, "health,visual,interact,cache,speed", "inspect,click,submit,flows"},
		{"", []string{"--modules", "health,interact"}, "health,interact", "inspect,click"},
		{"", []string{"--modules", "+echo", "--echo"}, "health,visual,interact,cache,speed,echo", "inspect,click"},
		{"modules: [health]\n", []string{"--modules", "+visual"}, "health,visual", "inspect,click"},
		{"modules: [health]\n", []string{"--modules", "speed"}, "speed", "inspect,click"},
		{"interactions:\n  levels: [submit]\n", nil, "health,visual,interact,cache,speed", "inspect,click,submit"},
		{"interactions:\n  levels: [inspect]\n", []string{"--level", "+click"}, "health,visual,interact,cache,speed", "inspect,click"},
		{"interactions:\n  levels: [+flows]\n", []string{"--skip", "visual"}, "health,interact,cache,speed", "inspect,click,submit,flows"},
	} {
		writeConfig(t, tc.config)
		p, err := parse(append([]string{"a.test"}, tc.args...))
		if err != nil {
			t.Errorf("%q %v: %v", tc.config, tc.args, err)
			continue
		}
		if m, l := strings.Join(p.cfg.Modules, ","), strings.Join(p.cfg.Interactions.Levels, ","); m != tc.modules || l != tc.levels {
			t.Errorf("%q %v: modules %s levels %s, want %s and %s", tc.config, tc.args, m, l, tc.modules, tc.levels)
		}
	}
	writeConfig(t, "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--level", "click,+submit"}, "--level (interactions.levels): put + before the first value to add to the list, e.g. +submit"},
		{[]string{"--modules", "health", "--modules", "+echo"}, "--modules (modules): put + before the first value to add to the list, e.g. +echo"},
		{[]string{"--level", "+boom"}, `--level (interactions.levels): "boom" isn't one of`},
		{[]string{"--modules", "+echo"}, "echo needs Echo's address"},
	} {
		code, _, errOut := runCLI(t, append([]string{"run", "a.test"}, tc.args...)...)
		if code != exitUsage || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d %q, want 2 and %q", tc.args, code, errOut, tc.want)
		}
	}
}

func TestSettingsHeader(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	// A dead Echo stops the run right after the header, before any browsing.
	echo := deadSite(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, `rhythm-soundcheck 1.0.0-test - 127.0.0.1:20260

Modules: health, visual, interact, cache, speed, echo
Levels: inspect, click
Viewports: desktop, mobile
Production: none listed: submit and flows run on any site
`},
		{[]string{"--level", "submit", "--viewport", "mobile", "--skip", "visual,cache,speed"}, `rhythm-soundcheck 1.0.0-test - 127.0.0.1:20260

Modules: health, interact, echo
Levels: inspect, click, submit
Viewports: mobile
Production: none listed: submit and flows run on any site
`},
		{[]string{"--modules", "health"}, `rhythm-soundcheck 1.0.0-test - 127.0.0.1:20260

Modules: health, echo
Levels: none: the interact module is off
Viewports: desktop, mobile
Production: none listed: nothing in this run submits forms
`},
	} {
		_, out, _ := runCLI(t, append([]string{"run", "127.0.0.1:20260", "--echo=" + echo, "--plain"}, tc.args...)...)
		if out != tc.want {
			t.Errorf("%v:\n%s\nwant\n%s", tc.args, out, tc.want)
		}
	}
}
