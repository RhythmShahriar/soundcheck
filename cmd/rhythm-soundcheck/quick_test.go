// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

func TestQuick(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	for _, tc := range []struct {
		config            string
		args              []string
		modules, viewport string
		chosen            chosen
		header            string
	}{
		{"", nil, "health,visual,interact,cache,speed", "desktop,mobile", chosen{}, ""},
		{"", []string{"--quick"}, "health,interact", "desktop",
			chosen{modules: "--quick", viewports: "--quick"}, "--quick: viewport desktop; modules health, interact"},
		{"", []string{"--quick", "--viewport", "mobile"}, "health,interact", "mobile",
			chosen{modules: "--quick", viewports: "--viewport"}, "--quick: modules health, interact (--viewport kept)"},
		{"", []string{"--quick", "--modules", "health,speed"}, "health,speed", "desktop",
			chosen{modules: "--modules", viewports: "--quick"}, "--quick: viewport desktop (--modules kept)"},
		{"", []string{"--quick", "--modules", "+visual"}, "health,visual,interact,cache,speed", "desktop",
			chosen{modules: "--modules", viewports: "--quick"}, "--quick: viewport desktop (--modules kept)"},
		{"", []string{"--quick", "--modules", "speed", "--viewport", "mobile"}, "speed", "mobile",
			chosen{modules: "--modules", viewports: "--viewport"}, "--quick: nothing, --viewport and --modules set it all"},
		{"", []string{"--quick", "--skip", "interact"}, "health", "desktop",
			chosen{modules: "--quick", viewports: "--quick"}, "--quick: viewport desktop; modules health, interact"},
		// --quick is a flag, so it wins over the config file.
		{"modules: [visual]\nviewports: [mobile]\n", []string{"--quick"}, "health,interact", "desktop",
			chosen{modules: "--quick", viewports: "--quick"}, "--quick: viewport desktop; modules health, interact"},
		{"modules: [visual]\n", nil, "visual", "desktop,mobile", chosen{modules: ".soundcheck/config.yaml"}, ""},
	} {
		writeConfig(t, tc.config)
		p, err := parse(append([]string{"a.test"}, tc.args...))
		if err != nil {
			t.Errorf("%q %v: %v", tc.config, tc.args, err)
			continue
		}
		m, v := strings.Join(p.cfg.Modules, ","), strings.Join(p.cfg.Viewports, ",")
		if m != tc.modules || v != tc.viewport || p.chosen != tc.chosen || p.quick != tc.header {
			t.Errorf("%q %v: modules %s viewports %s chosen %+v header %q\nwant %s %s %+v %q",
				tc.config, tc.args, m, v, p.chosen, p.quick, tc.modules, tc.viewport, tc.chosen, tc.header)
		}
	}

	// compare keeps --quick's settings over the snapshot's and says so.
	writeConfig(t, "")
	p, err := parse([]string{"--quick"})
	if err != nil {
		t.Fatal(err)
	}
	snap := model.Options{Modules: []string{"health", "visual"}, Viewports: []string{"desktop", "mobile"}}
	changes := inherit(&p.cfg, snap, p.chosen, nil)
	if got := joinChanges(changes); got != "modules health,visual -> health,interact (--quick); viewports desktop,mobile -> desktop (--quick)" {
		t.Errorf("compare --quick: %s", got)
	}

	// The header shows it.
	echo := deadSite(t)
	_, out, _ := runCLI(t, "run", "127.0.0.1:20260", "--quick", "--echo="+echo, "--plain")
	if want := `rhythm-soundcheck 1.0.0-test - 127.0.0.1:20260

Preset: --quick: viewport desktop; modules health, interact
Modules: health, interact, echo
Levels: inspect, click
Viewports: desktop
Production: none listed: submit and flows run on any site
`; out != want {
		t.Errorf("header:\n%s\nwant\n%s", out, want)
	}
}
