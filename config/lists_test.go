// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"
)

func TestAddList(t *testing.T) {
	base := []string{"health", "visual"}
	for _, tc := range []struct {
		values []string
		want   string // "" with err
		err    string
	}{
		{nil, "", ""},
		{[]string{"cache"}, "cache", ""},
		{[]string{"+echo"}, "health,visual,echo", ""},
		{[]string{"+speed", "cache", "+visual"}, "health,visual,speed,cache", ""},
		{[]string{"+"}, "health,visual", ""},
		{[]string{"cache", "+speed"}, "", "put + before the first value to add to the list, e.g. +speed"},
	} {
		got, err := AddList(base, tc.values)
		switch {
		case tc.err != "":
			if err == nil || err.Error() != tc.err {
				t.Errorf("%v: err %v, want %q", tc.values, err, tc.err)
			}
		case err != nil || strings.Join(got, ",") != tc.want:
			t.Errorf("%v: %v %v, want %s", tc.values, got, err, tc.want)
		}
	}
	if strings.Join(base, ",") != "health,visual" {
		t.Errorf("base changed: %v", base)
	}
}

func TestCumulative(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		"inspect":          "inspect",
		"click":            "inspect,click",
		"submit":           "inspect,click,submit",
		"flows":            "inspect,click,submit,flows",
		"inspect,flows":    "inspect,click,submit,flows",
		"submit,click":     "inspect,click,submit",
		"click,boom,boom":  "inspect,click,boom",
		"inspect,click,ok": "inspect,click,ok",
	} {
		var levels []string
		if in != "" {
			levels = strings.Split(in, ",")
		}
		if got := strings.Join(Cumulative(levels), ","); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestLoadLists(t *testing.T) {
	for body, want := range map[string]string{
		"interactions:\n  levels: [submit]\n":                     "levels inspect,click,submit",
		"interactions:\n  levels: [+flows]\n":                     "levels inspect,click,submit,flows",
		"modules: [+echo]\necho:\n  url: http://127.0.0.1:8025\n": "modules health,visual,interact,cache,speed,echo",
		"modules: [health]\n":                                     "modules health",
	} {
		c, err := Load(write(t, body))
		if err != nil {
			t.Errorf("%q: %v", body, err)
			continue
		}
		got := "modules " + strings.Join(c.Modules, ",")
		if strings.HasPrefix(want, "levels") {
			got = "levels " + strings.Join(c.Interactions.Levels, ",")
		}
		if got != want {
			t.Errorf("%q: %s, want %s", body, got, want)
		}
	}
	for body, want := range map[string]string{
		"modules: [health, +echo]\n":             "modules: put + before the first value",
		"interactions:\n  levels: [x, +click]\n": "interactions.levels: put + before the first value",
		"modules: [+magic]\n":                    `modules: "magic" isn't one of`,
	} {
		if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err %v, want %q", body, err, want)
		}
	}
}
