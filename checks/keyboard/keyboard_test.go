// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// The module is registered, Chrome only, after interact and lists every
// keyboard check.
func TestModule(t *testing.T) {
	m := New()
	if !slices.ContainsFunc(check.Registered(), func(r check.Module) bool { return r.Name() == "keyboard" }) {
		t.Error("keyboard isn't registered")
	}
	if check.PhaseOf(m) != check.PhaseKeyboard || check.PhaseOf(m) <= check.PhaseMutate {
		t.Errorf("phase %d", check.PhaseOf(m))
	}
	if !check.ChromeOnly(m) {
		t.Error("keyboard isn't Chrome only")
	}
	if got := m.Checks(); len(got) != len(ids.Checks) || got[0].ID != ids.Trap {
		t.Errorf("Checks() = %v", got)
	}
	if slices.Contains(config.DefaultModules, "keyboard") {
		t.Error("keyboard is on by default")
	}
}

func TestOptionsDefaults(t *testing.T) {
	o, err := Options(config.Default())
	want := model.KeyboardOptions{MaxTabs: 150, Widgets: 10, TrapAfter: 4, SkipLink: "expected"}
	if err != nil || o != want {
		t.Errorf("Options(default) = %+v, %v; want %+v", o, err, want)
	}
}
