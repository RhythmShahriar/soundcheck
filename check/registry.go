// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"cmp"
	"fmt"
	"slices"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/model"
)

// The registry lists every module the binary has, so the module list,
// help text, --list-checks and docs tables can come from one place
// instead of being kept in step by hand. A module registers itself from
// its package's init and the cmd blank-imports the package.
var (
	regMu    sync.Mutex
	registry []Module
)

// Register adds m to the modules Registered returns. It panics when m has
// no name or another module took the name: both are programming errors
// that must fail at start-up, not halfway through a run.
func Register(m Module) {
	regMu.Lock()
	defer regMu.Unlock()
	name := m.Name()
	if name == "" {
		panic("check: Register of a module with no name")
	}
	if slices.ContainsFunc(registry, func(r Module) bool { return r.Name() == name }) {
		panic(fmt.Sprintf("check: Register called twice for module %q", name))
	}
	registry = append(registry, m)
}

// Registered returns the registered modules in the order they registered.
// The slice is a copy; changing it doesn't change the registry.
func Registered() []Module {
	regMu.Lock()
	defer regMu.Unlock()
	return slices.Clone(registry)
}

// CheckInfo describes one check a module can report, by the Check field
// its issues carry.
type CheckInfo struct {
	ID    string      // the issue's Check, "<module>/<rule>": "health/php-error"
	Level model.Level // the level it usually reports at
	Title string      // a short name: "PHP errors and warnings"
	Doc   string      // what it means and how to fix it, a sentence or two
}

// Describer is a module that lists its checks, so they can be shown
// (--list-checks), linked to docs or skipped one by one.
type Describer interface {
	Checks() []CheckInfo
}

// Phases order the modules' hooks on each page. Modules without a Phaser
// are in PhaseInspect.
const (
	// PhaseInspect is for modules that only look at the page as loaded.
	PhaseInspect = 0
	// PhaseMutate is for modules that change the page (clicks, submits):
	// their hooks run after every other module's, which must see the
	// page as it loaded.
	PhaseMutate = 100
)

// Phaser is a module that runs in a phase other than PhaseInspect. The
// runner calls modules in ascending phase, keeping their given order
// within a phase.
type Phaser interface {
	Phase() int
}

// PhaseOf is m's phase: its Phase(), or PhaseInspect without a Phaser.
func PhaseOf(m Module) int {
	if p, ok := m.(Phaser); ok {
		return p.Phase()
	}
	return PhaseInspect
}

// SortByPhase returns a copy of modules in ascending phase. The sort is
// stable, so modules in one phase keep the order they were given in.
func SortByPhase(modules []Module) []Module {
	out := slices.Clone(modules)
	slices.SortStableFunc(out, func(a, b Module) int { return cmp.Compare(PhaseOf(a), PhaseOf(b)) })
	return out
}

// CaptureNeeds is a module that needs screenshots. Screenshots cost time
// and disk space, so the runner takes them only when a module asks.
type CaptureNeeds interface {
	// NeedsShots reports whether each viewport needs a stored screenshot.
	NeedsShots() bool
	// NeedsSecondShot reports whether each shot needs a second capture
	// moments later, which shows the regions that change on their own
	// (sliders, clocks). snapshot is whether the run is a snapshot.
	NeedsSecondShot(snapshot bool) bool
}

// Configurable is a module with settings of its own. Configure gets the
// module's block of config.yaml (the zero Node when there is none) and
// decodes it, failing on keys it doesn't know so a typo isn't ignored.
type Configurable interface {
	Configure(node yaml.Node) error
}
