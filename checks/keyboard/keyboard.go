// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package keyboard is the keyboard module: on each page it walks the
// focus order with trusted Tab and Shift+Tab presses, tests menus,
// dialogs and disclosures by keyboard and reports what stops a keyboard
// user, such as a focus trap, a control the keyboard can't reach or a
// missing focus indicator (the checks in checks/keyboard/ids).
//
// It runs in Chrome only, in check.PhaseKeyboard: after interact's
// clicks and submits, so it walks the page a visitor is left with. It
// is off by default; --eaa or +keyboard turns it on.
package keyboard

import (
	"context"
	"sync"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(New()) }

// Module is the keyboard module. It keeps its options between hooks, so
// it is used as a pointer (New).
type Module struct {
	mu   sync.Mutex
	opts model.KeyboardOptions
}

// New returns the module with the default options.
func New() *Module { return &Module{opts: defaults()} }

// Name is "keyboard".
func (*Module) Name() string { return "keyboard" }

// Phase puts the module after interact's clicks and submits.
func (*Module) Phase() int { return check.PhaseKeyboard }

// Live walks the page by keyboard and records what it finds on p.
func (m *Module) Live(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page) {}

// Checks lists the module's checks, from ids.Checks.
func (*Module) Checks() []check.CheckInfo {
	out := make([]check.CheckInfo, 0, len(ids.Checks))
	for _, c := range ids.Checks {
		out = append(out, check.CheckInfo{ID: c.ID, Level: c.Level, Title: c.Title, Doc: c.Doc})
	}
	return out
}
