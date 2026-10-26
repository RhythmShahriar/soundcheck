// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// EAA mode (--eaa, or eaa: true in the config file) gathers accessibility
// evidence for the European Accessibility Act: the health, a11y and
// keyboard modules, the WCAG 2.1 rules and a statement draft in the run
// folder. Like --quick it sets what the flags and the file didn't and the
// header says what it set.

// eaaModules are the modules --eaa runs when nothing else chose them.
var eaaModules = []string{"health", "a11y", "keyboard"}

// eaaNeeds are the modules --eaa adds to a list chosen by --modules, the
// config file or --quick.
var eaaNeeds = []string{"a11y", "keyboard"}

// eaaPreset turns EAA mode on from --eaa or the file's eaa: (--eaa=false
// turns the file's off) and, when nothing chose the modules, makes
// eaaModules the list --modules applies to: a plain --modules list
// replaces it and a +list adds to it, as it adds to the default list
// without --eaa. eaaModules adds a11y and keyboard to whatever list that
// leaves.
func (p *plan) eaaPreset(set map[string]bool) {
	if set["eaa"] {
		p.cfg.EAA = p.o.eaa
	}
	if !p.cfg.EAA {
		return
	}
	plus := len(p.o.modules) > 0 && strings.HasPrefix(p.o.modules[0], "+")
	switch {
	case p.chosen.modules == "--quick":
		p.eaaKept = "--quick"
	case p.cfg.InFile("modules") && (!set["modules"] || plus):
		p.eaaKept = p.configPath
		if p.eaaKept == "" {
			p.eaaKept = "modules:"
		}
	case set["modules"] && !plus:
		p.eaaKept = "--modules"
	default:
		p.cfg.Modules = slices.Clone(eaaModules)
	}
}

// eaaModules adds a11y and keyboard to the modules under EAA mode, when
// the list chosen by --modules, the config file or --quick lacks them.
func (p *plan) eaaModules() {
	if !p.cfg.EAA {
		return
	}
	for _, m := range eaaNeeds {
		if !p.cfg.Has(m) {
			p.cfg.Modules = append(p.cfg.Modules, m)
		}
	}
}

// eaaRules settles the WCAG version and checks what EAA mode can't run
// with, in this order: --skip a11y or keyboard, a --wcag (or a11y.wcag)
// that isn't a version, WCAG 2.0, a browser other than Chrome and a
// sandbox turned off by the environment alone. Each is a usage error.
// It also reads the keyboard walk's limits, which the run records.
func (p *plan) eaaRules() error {
	if err := p.eaaSkipped(); err != nil {
		return err
	}
	if p.set["wcag"] {
		if !slices.Contains(wcag.Versions, p.o.wcag) {
			return cli.Usagef("--wcag (a11y.wcag): %q isn't one of %s", p.o.wcag, strings.Join(wcag.Versions, ", "))
		}
		p.wcag = p.o.wcag
	} else {
		v, err := a11y.Standard(p.cfg)
		if err != nil {
			return cli.UsageError{Msg: err.Error()}
		}
		p.wcag = v
	}
	return p.eaaSettle()
}

// eaaSkipped refuses --skip a11y or keyboard under EAA mode.
func (p *plan) eaaSkipped() error {
	if !p.cfg.EAA {
		return nil
	}
	skipped := slices.DeleteFunc(slices.Clone(p.o.skip), func(m string) bool { return !slices.Contains(eaaNeeds, m) })
	if len(skipped) == 0 {
		return nil
	}
	return cli.Usagef("--eaa needs the a11y and keyboard modules; drop --skip %s, or leave out --eaa to run without them", strings.Join(skipped, ","))
}

// eaaSettle checks the environment (eaaEnvironment), hands the a11y
// module the WCAG version and reads the keyboard walk's limits.
func (p *plan) eaaSettle() error {
	if err := p.eaaEnvironment(); err != nil {
		return err
	}
	// The draft each --eaa run writes reads statement:, so a mistake in
	// it is a usage error now rather than a missing draft at the end.
	if p.cfg.EAA {
		if _, err := decodeOwner(p.cfg); err != nil {
			return cli.UsageError{Msg: err.Error()}
		}
	}
	p.setWCAG(p.wcag)
	p.keyboard = nil
	if p.cfg.Has("keyboard") {
		k, err := keyboard.Options(p.cfg)
		if err != nil {
			return cli.UsageError{Msg: err.Error()}
		}
		p.keyboard = &k
	}
	return nil
}

// eaaEnvironment refuses what EAA mode can't run with besides its
// modules: WCAG 2.0, which no EN 301 549 version maps to, another engine
// than Chrome and SOUNDCHECK_NO_SANDBOX=1 without --no-sandbox
// (or with --no-sandbox=false, which the variable would override). A key
// press on a hostile page is more than a page load, so turning the
// sandbox off takes the command line.
func (p *plan) eaaEnvironment() error {
	if !p.cfg.EAA {
		return nil
	}
	if p.wcag == "2.0" {
		return cli.Usagef("--eaa maps to WCAG 2.1 AA (EN 301 549 V3.2.1); use --wcag 2.1 or 2.2, or leave out --eaa to run the 2.0 rules")
	}
	if p.engine != "" && p.engine != capture.Chrome {
		return cli.Usagef("--eaa needs Chrome: the keyboard walkthrough and axe use the Chrome DevTools protocol. Drop --browser %s, or use --modules +a11y without --eaa", p.engine)
	}
	if os.Getenv(browser.NoSandboxEnv) == "1" && !p.o.noSandbox {
		return cli.Usagef("--eaa won't turn off Chrome's sandbox from %s alone; add --no-sandbox to the command if you mean it", browser.NoSandboxEnv)
	}
	return nil
}

// setWCAG makes v the version the a11y module reads (a11y.wcag): the
// 2.0 rules for an empty one, a snapshot from before 1.3.
func (p *plan) setWCAG(v string) {
	if v == "" {
		v = "2.0"
	}
	p.cfg.SetBlockValue("a11y", "wcag", v)
}

// eaaHeader is the header's Preset line under EAA mode: "--eaa: modules
// health, a11y, keyboard; WCAG 2.1", with what chose the list when --eaa
// only added to it.
func (s *session) eaaHeader() string {
	if !s.cfg.EAA {
		return ""
	}
	mods := strings.Join(s.cfg.Modules, ", ")
	if s.eaaKept != "" {
		mods += " (" + s.eaaKept + " kept; a11y and keyboard added)"
	}
	return fmt.Sprintf("--eaa: modules %s; WCAG %s", mods, cmpOr(s.wcag, "2.0"))
}

// runKeyboard is the keyboard walk's limits for the runner to record,
// nil when the keyboard module doesn't run.
func (s *session) runKeyboard() *model.KeyboardOptions {
	if !s.cfg.Has("keyboard") {
		return nil
	}
	return s.keyboard
}

// recordEAA writes what the run was made with into its options: EAA
// mode, the WCAG version and the keyboard walk's limits, unless the
// runner recorded them already.
func (s *session) recordEAA(run *model.Run) {
	if run == nil {
		return
	}
	o := &run.Options
	o.EAA = o.EAA || s.cfg.EAA
	if o.WCAG == "" {
		o.WCAG = s.wcag
	}
	if o.Keyboard == nil && s.cfg.Has("keyboard") {
		o.Keyboard = s.keyboard
	}
}

// inheritEAA gives a comparison the snapshot's EAA mode and WCAG version
// where the user didn't choose them, as inherit does for the modules. A
// snapshot with no version stored (one from before 1.3) is compared with
// the 2.0 rules it ran, except under EAA mode, which needs 2.1 or later.
// Under EAA mode a11y and keyboard are added to the inherited modules.
// It prints the notes about a snapshot taken without --eaa or with the
// 2.0 rules and returns the settings that still differ from the
// snapshot, for --strict.
func (s *session) inheritEAA(snap *model.Run, changes []settingChange) ([]settingChange, error) {
	cfg := &s.cfg
	eaaFrom := ""
	switch {
	case s.set["eaa"]:
		eaaFrom = "--eaa"
	case cfg.InFile("eaa"):
		eaaFrom = s.configPath
	default:
		cfg.EAA = snap.Options.EAA
	}
	switch {
	case s.set["wcag"]:
		if snap.Options.WCAG != "" && snap.Options.WCAG != s.wcag {
			changes = append(changes, settingChange{name: "wcag", before: []string{snap.Options.WCAG}, after: []string{s.wcag}, from: "--wcag"})
		}
	case snap.Options.WCAG != "":
		s.wcag = snap.Options.WCAG
	case !cfg.EAA:
		s.wcag = ""
	}
	if cfg.EAA {
		before := slices.Clone(cfg.Modules)
		s.eaaModules()
		if !slices.Equal(before, cfg.Modules) {
			changes = addedByEAA(changes, snap.Options.Modules, cfg.Modules)
		}
		if !snap.Options.EAA {
			changes = append(changes, settingChange{name: "eaa", before: []string{"off"}, after: []string{"on"}, from: eaaFrom})
		}
	}
	// EAA mode may have come from the snapshot, bringing its rules about
	// --skip, the browser and the sandbox.
	if err := cmp.Or(s.eaaSkipped(), s.eaaSettle()); err != nil {
		return changes, err
	}
	if !s.o.quiet {
		if cfg.EAA && !snap.Options.EAA {
			fmt.Fprintf(s.stderr, "note: snapshot %q was taken without --eaa; keyboard issues will all be new. Take a new snapshot with --eaa\n", snap.Name)
		}
		if snap.Options.WCAG == "" && cfg.Has("a11y") {
			fmt.Fprintf(s.stderr, "note: snapshot %q used the WCAG 2.0 rules; take a new snapshot to add the %s rules\n", snap.Name, cmpOr(s.wcag, "2.1"))
		}
	}
	return changes, nil
}

// addedByEAA records that EAA mode added modules to a comparison's list:
// it updates the modules change inherit found, or adds one when the list
// had matched the snapshot's.
func addedByEAA(changes []settingChange, before, after []string) []settingChange {
	for i, c := range changes {
		if c.name == "modules" {
			changes[i].after = slices.Clone(after)
			if c.from == "" {
				changes[i].from = "--eaa"
			}
			return changes
		}
	}
	if len(before) == 0 || sameSet(before, after) {
		return changes
	}
	return append(changes, settingChange{name: "modules", before: before, after: slices.Clone(after), from: "--eaa"})
}

// tipEAAFile marks a project that has seen the --eaa tip, in the store.
const tipEAAFile = ".tip-eaa"

// eaaTip suggests --eaa once per project, after a check that ran the
// a11y module without it. It stays out of -q, machine output (--json,
// --md and --junit go to stdout without the live view) and CI, where
// nobody reads a tip twice.
func (s *session) eaaTip() string {
	if !s.checked || !s.live || s.o.quiet || s.cfg.EAA || !s.cfg.Has("a11y") || os.Getenv("CI") != "" {
		return ""
	}
	// O_EXCL makes the marker the test: whoever creates it shows the tip.
	f, err := os.OpenFile(filepath.Join(s.st.Root, tipEAAFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a marker in the project's store
	if err != nil {
		return ""
	}
	_ = f.Close()
	return "tip: --eaa adds a keyboard walkthrough and an accessibility-statement draft"
}
