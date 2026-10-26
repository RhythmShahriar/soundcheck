// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/config"
)

// Versions are the WCAG versions the module can test against, oldest
// first.
var Versions = []string{"2.0", "2.1", "2.2"}

// Settings are the a11y: block of the config file (see Configure).
type Settings struct {
	// WCAG is the WCAG version whose A and AA rules run: 2.0, 2.1 or 2.2.
	WCAG string `yaml:"wcag"`
	// MaxElements is how many elements one rule reports per page, 1-200.
	// A theme with a low-contrast footer would otherwise add hundreds of
	// issues. axe lists elements in document order, so the same ones are
	// kept on every run.
	MaxElements int `yaml:"max_elements"`
	// IFrames audits same-origin iframes too. Frames from other sites are
	// never audited: they are someone else's (maps, videos, consent
	// banners) and would each need a copy of axe of their own.
	IFrames bool `yaml:"iframes"`
}

// DefaultSettings are the settings without an a11y: block.
var DefaultSettings = Settings{WCAG: "2.1", MaxElements: 20}

// Limits of max_elements.
const (
	minElements = 1
	maxElements = 200
)

// VersionError is the message for a WCAG version that isn't one of
// Versions. The --wcag flag and a11y.wcag share it, so it names both.
// It suggests no version: "3.0" is one edit from "2.0" without being a
// typo of it.
func VersionError(v string) error {
	return fmt.Errorf("--wcag (a11y.wcag): %q isn't one of %s", v, strings.Join(Versions, ", "))
}

// decode reads an a11y: block over the defaults. A bad wcag value is
// VersionError, any other bad setting a config.SettingError.
func decode(node yaml.Node) (Settings, error) {
	s := DefaultSettings
	if err := config.DecodeBlock("a11y", node, &s); err != nil {
		return DefaultSettings, err
	}
	if !slices.Contains(Versions, s.WCAG) {
		return DefaultSettings, VersionError(s.WCAG)
	}
	if s.MaxElements < minElements || s.MaxElements > maxElements {
		return DefaultSettings, &config.SettingError{Key: "a11y.max_elements", Line: config.KeyLine(node, "max_elements"),
			Msg: fmt.Sprintf("must be a number from %d to %d", minElements, maxElements)}
	}
	return s, nil
}

// Configure checks the a11y: block of the config file
// (check.Configurable), so a typo in it stops the command before
// anything runs. The module reads the block again from the run's
// config on each page (see settingsOf): it has no state of its own.
func (Module) Configure(node yaml.Node) error {
	_, err := decode(node)
	return err
}

// settingsOf is the a11y: block of cfg. The CLI checked it before the
// run (Configure), so a bad block falls back to the defaults.
func settingsOf(cfg config.Config) Settings {
	s, err := decode(cfg.Block("a11y"))
	if err != nil {
		return DefaultSettings
	}
	return s
}

// Standard returns the WCAG version the accessibility checks use for
// cfg: 2.0, 2.1 or 2.2, from a11y.wcag in the config file, 2.1 when the
// file doesn't set it. --wcag wins over it; the CLI checks the flag
// with VersionError.
func Standard(cfg config.Config) (string, error) {
	s, err := decode(cfg.Block("a11y"))
	if err != nil {
		return "", err
	}
	return s.WCAG, nil
}

// tags are the axe-core tags of the WCAG version's A and AA rules. Each
// version adds to the one before: 2.1 runs autocomplete-valid,
// avoid-inline-spacing and the other rules for its new criteria. A run
// with no version recorded (one made before 1.3) used the 2.0 rules.
func tags(version string) []string {
	out := []string{"wcag2a", "wcag2aa"}
	if version == "2.1" || version == "2.2" {
		out = append(out, "wcag21a", "wcag21aa")
	}
	if version == "2.2" {
		out = append(out, "wcag22aa")
	}
	return out
}
