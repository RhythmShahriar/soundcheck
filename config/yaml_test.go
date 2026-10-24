// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"slices"
	"strings"
	"testing"
)

func TestFriendlyErrors(t *testing.T) {
	for body, want := range map[string]string{
		"rate: 2\n\npagez:\n  limit: 5\n":            `config.yaml line 3: unknown setting "pagez" (did you mean "pages"?)`,
		"pages:\n  limits: 5\n":                      `config.yaml line 2: unknown setting "pages.limits" (did you mean "pages.limit"?)`,
		"wallpaper: red\n":                           `config.yaml line 1: unknown setting "wallpaper"`,
		"trust:\n  allow_env: [X]\n":                 `config.yaml line 1: unknown setting "trust"`,
		"rate: fast\n":                               `config.yaml line 1: rate: "fast" isn't a number`,
		"pages:\n  limit: [1, 2]\n":                  `config.yaml line 2: pages.limit: takes a number, not a list`,
		"pages: 5\n":                                 `config.yaml line 1: pages: "5" isn't a group of settings, each on its own indented line`,
		"pages:\n  ignore_robots: maybe\n":           `config.yaml line 2: pages.ignore_robots: "maybe" isn't true or false`,
		"modules: health\n":                          `config.yaml line 1: modules: "health" isn't a list like [a, b]`,
		"headers: [a]\n":                             `config.yaml line 1: headers: takes name: value pairs, not a list`,
		"rate: 5\n  tabs: 2\n":                       `config.yaml line 2: this isn't valid YAML: mapping values are not allowed in this context`,
		"- a\n- b\n":                                 `config.yaml line 1: the file must be a list of settings like "pages:" and "rate: 5"`,
		"rate: 5\nmodules: [helth]\n":                `config.yaml line 2: modules: "helth" isn't one of health, visual, interact, cache, speed, a11y, seo, echo, exec (did you mean "health"?)`,
		"viewports: [desktop, moble]\n":              `config.yaml line 1: viewports: "moble" isn't one of desktop, mobile (did you mean "mobile"?)`,
		"viewports: [tablet]\n":                      `config.yaml line 1: viewports: "tablet" isn't one of desktop, mobile`,
		"interactions:\n  levels: [clik]\n":          `config.yaml line 2: interactions.levels: "clik" isn't one of inspect, click, submit, flows (did you mean "click"?)`,
		"tabs: 99\n":                                 `config.yaml line 1: tabs: must be between 1 and 16`,
		"echo:\n  token_env: GITHUB_TOKEN\n":         `config.yaml line 2: echo.token_env: "GITHUB_TOKEN" must start with RHYTHM_ECHO_`,
		"interactions:\n  levels: [a, +b]\n":         `config.yaml line 2: interactions.levels: put + before the first value`,
		"production: ['http://']\n":                  `config.yaml line 1: production: "http://" isn't a site`,
		"modules: [health, echo]\n":                  `config.yaml line 1: modules: echo needs Echo's address`,
		"modules: [+exec]\n":                         `config.yaml line 1: modules: exec runs the commands listed under exec: in the config; there are none`,
		"ignore:\n  - reason: x\n":                   `ignore: rule 1 has nothing to match: give it a check, a title or pages`,
		"ignore:\n  - check: a/b\n    until: soon\n": `ignore: rule 1: until "soon" isn't a date like 2026-12-31`,
		"ignore:\n  - check: a/b\n    page: [/x]\n":  `config.yaml line 3: unknown setting "ignore.page" (did you mean "ignore.pages"?)`,
		"ignore: x\n":                                `ignore: "x" isn't a list like [a, b]`,
	} {
		_, err := Load(write(t, body))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q:\n got %v\nwant %s", body, err, want)
		}
	}
}

func TestTimeout(t *testing.T) {
	for body, want := range map[string]Seconds{
		"timeout: 30\n": 30, "timeout: 30s\n": 30, "timeout: 1m\n": 60, "timeout: 7.6\n": 8, "timeout: 1500ms0s\n": -1,
		"timeout: 2m30s\n": 150, "timeout: \"45\"\n": 45, "": 30,
	} {
		c, err := Load(write(t, body))
		switch {
		case want < 0:
			if err == nil {
				t.Errorf("%q: no error", body)
			}
		case err != nil || c.Timeout != want:
			t.Errorf("%q: %d %v, want %d", body, c.Timeout, err, want)
		}
	}
	for body, want := range map[string]string{
		"timeout: fast\n":   `line 1: timeout: "fast" isn't a number of seconds or a duration like 30s`,
		"timeout: -5s\n":    `line 1: timeout: "-5s" isn't a number of seconds`,
		"timeout: .nan\n":   `line 1: timeout: ".nan" isn't a number of seconds`,
		"timeout: 1e30\n":   "up to a day",
		"timeout: [30]\n":   "line 1: timeout: takes a number of seconds or a duration like 30s",
		"timeout: 2s\n":     "line 1: timeout: must be at least 5 seconds",
		"timeout: 4\n":      "line 1: timeout: must be at least 5 seconds",
		"timeout: 4.4s\n":   "must be at least 5 seconds",
		"timeout: 5000ms\n": "",
	} {
		_, err := Load(write(t, body))
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v", body, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%q: %v, want %q", body, err, want)
		}
	}
}

func TestNewKeys(t *testing.T) {
	c, err := Load(write(t, "site: staging.example.com\nfail_on: broke\nheaders:\n  X-Staging-Auth: SOUNDCHECK_AUTH\n  Authorization: CI_TOKEN\n"))
	if err != nil || c.Site != "staging.example.com" || c.FailOn != "broke" || c.Headers["X-Staging-Auth"] != "SOUNDCHECK_AUTH" ||
		c.Headers["Authorization"] != "CI_TOKEN" {
		t.Fatalf("%+v %v", c, err)
	}
	if c := Default(); c.Site != "" || c.FailOn != "" || len(c.Headers) != 0 {
		t.Errorf("defaults: %+v", c)
	}
	for _, v := range append(slices.Clone(FailOnValues), "none") {
		if _, err := Load(write(t, "fail_on: "+v+"\n")); err != nil {
			t.Errorf("fail_on %s: %v", v, err)
		}
	}
	// Any case, stored lowercased.
	for _, v := range []string{"BROKE", "Broke", " broke", "None"} {
		c, err := Load(write(t, "fail_on: \""+v+"\"\n"))
		if want := strings.ToLower(strings.TrimSpace(v)); err != nil || c.FailOn != want {
			t.Errorf("fail_on %q: %q %v, want %q", v, c.FailOn, err, want)
		}
	}
	for body, want := range map[string]string{
		"site: \"http://\"\n":                     `line 1: site: "http://" isn't a site like staging.example.com`,
		"site: ftp://example.com\n":               `site: "ftp://example.com" isn't a site`,
		"fail_on: brok\n":                         `line 1: fail_on: "brok" isn't one of broke, new, changed, slower, error, warning, notice, none (did you mean "broke"?)`,
		"fail_on: NOPE\n":                         `fail_on: "nope" isn't one of broke, new, changed, slower, error, warning, notice, none (did you mean "none"?)`,
		"headers:\n  Bad Name: SOUNDCHECK_A\n":    `line 1: headers: "Bad Name" isn't a header name like X-Staging-Auth`,
		"headers:\n  X-Auth: \"secret-value\"\n":  `headers: X-Auth: "secret-value" isn't the name of an environment variable`,
		"headers:\n  X-Auth: \"$SOUNDCHECK_A\"\n": `isn't the name of an environment variable`,
	} {
		_, err := Load(write(t, body))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q:\n got %v\nwant %s", body, err, want)
		}
	}
}

func TestSuggest(t *testing.T) {
	opts := []string{"pages", "viewports", "modules", "interactions", "rate", "tabs"}
	for in, want := range map[string]string{
		"pagez": "pages", "Pages": "pages", "modlues": "modules", "viewport": "viewports", "intractions": "interactions",
		"rat": "rate", "tab": "tabs", "tbas": "tabs", "paegs": "pages", "x": "", "wallpaper": "", "speedometer": "", "": "",
	} {
		if got := Suggest(in, opts); got != want {
			t.Errorf("Suggest(%q) = %q, want %q", in, got, want)
		}
	}
	for _, tc := range []struct {
		a, b string
		d    int
	}{{"", "", 0}, {"a", "", 1}, {"kitten", "sitting", 3}, {"flaw", "lawn", 2}, {"bücher", "bucher", 1},
		// A swap of neighbors is one edit (positive), letters further
		// apart are not (negative).
		{"submti", "submit", 1}, {"ab", "ba", 1}, {"abc", "cba", 2}, {"ca", "abc", 3}} {
		if d := distance(tc.a, tc.b); d != tc.d {
			t.Errorf("distance(%q, %q) = %d, want %d", tc.a, tc.b, d, tc.d)
		}
	}
	if err := OneOf("health", Modules); err != nil {
		t.Error(err)
	}
}
