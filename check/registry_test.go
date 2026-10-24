// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/model"
)

type named string

func (n named) Name() string { return string(n) }

type phased struct {
	named
	phase int
}

func (p phased) Phase() int { return p.phase }

// resetRegistry empties the registry for one test.
func resetRegistry(t *testing.T) {
	t.Helper()
	regMu.Lock()
	saved := registry
	registry = nil
	regMu.Unlock()
	t.Cleanup(func() {
		regMu.Lock()
		registry = saved
		regMu.Unlock()
	})
}

func names(mods []Module) []string {
	var out []string
	for _, m := range mods {
		out = append(out, m.Name())
	}
	return out
}

func TestRegister(t *testing.T) {
	resetRegistry(t)
	Register(named("health"))
	Register(named("visual"))
	got := Registered()
	if want := []string{"health", "visual"}; !slices.Equal(names(got), want) {
		t.Fatalf("Registered() = %v, want %v", names(got), want)
	}
	got[0] = named("changed")
	if Registered()[0].Name() != "health" {
		t.Error("changing the returned slice changed the registry")
	}
}

func TestRegisterRefusesDuplicatesAndNoName(t *testing.T) {
	resetRegistry(t)
	Register(named("health"))
	for _, c := range []struct {
		m    Module
		want string
	}{
		{named("health"), `Register called twice for module "health"`},
		{named(""), "Register of a module with no name"},
	} {
		func() {
			defer func() {
				if v, _ := recover().(string); !strings.Contains(v, c.want) {
					t.Errorf("Register(%q) panicked with %q, want %q", c.m.Name(), v, c.want)
				}
			}()
			Register(c.m)
		}()
	}
	if len(Registered()) != 1 {
		t.Errorf("a refused module was registered: %v", names(Registered()))
	}
}

// Modules run in ascending phase. Within a phase they keep the order
// they were given in; one without a Phaser is in PhaseInspect.
func TestSortByPhase(t *testing.T) {
	in := []Module{
		phased{"interact", PhaseMutate},
		named("health"),
		phased{"early", -10},
		named("visual"),
		phased{"cache", PhaseInspect},
		phased{"forms", PhaseMutate},
	}
	got := SortByPhase(in)
	if want := []string{"early", "health", "visual", "cache", "interact", "forms"}; !slices.Equal(names(got), want) {
		t.Errorf("SortByPhase = %v, want %v", names(got), want)
	}
	if in[0].Name() != "interact" {
		t.Error("SortByPhase changed its argument")
	}
	if PhaseOf(named("health")) != PhaseInspect || PhaseOf(phased{"interact", PhaseMutate}) != PhaseMutate {
		t.Error("PhaseOf is wrong")
	}
}

type liveModule struct{ named }

func (liveModule) Live(context.Context, *Context, *rod.Page, *model.Page) {}

type prepModule struct{ named }

func (prepModule) Prepare(context.Context, *Context, *rod.Page) error { return nil }

type tabModule struct {
	named
	chrome bool
}

func (tabModule) LiveTab(context.Context, *Context, Tab, *model.Page) {}
func (m tabModule) NeedsChrome() bool                                 { return m.chrome }

func TestChromeOnly(t *testing.T) {
	for _, tc := range []struct {
		m    Module
		want bool
	}{
		{named("health"), false},
		{liveModule{"interact"}, true},
		{prepModule{"speed"}, true},
		{tabModule{"tab", false}, false},
		{tabModule{"rod", true}, true},
	} {
		if got := ChromeOnly(tc.m); got != tc.want {
			t.Errorf("ChromeOnly(%s) = %v, want %v", tc.m.Name(), got, tc.want)
		}
	}
}
