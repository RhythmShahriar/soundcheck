// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Settings are the keyboard: block of the config file (see Configure).
type Settings struct {
	// MaxTabs is how many Tab presses a page gets before the walk stops
	// and records the page as capped, 10 to 2000.
	MaxTabs int `yaml:"max_tabs"`
	// Widgets is how many menus, dialogs and disclosures are tested on a
	// page, 0 to 200. 0 tests none.
	Widgets int `yaml:"widgets"`
	// TrapAfter is how many presses on one element or region count as a
	// trap, 3 to 20.
	TrapAfter int `yaml:"trap_after"`
	// SkipLink is how a page without a skip link is reported: expected
	// (a notice), required (a warning that fails WCAG 2.4.1) or off.
	SkipLink string `yaml:"skip_link"`
	// Pages are the paths, with * for any run of characters, whose
	// widgets are tested besides the first page of each URL group.
	Pages []string `yaml:"pages"`
	// Ignore are CSS selectors of elements the walk steps over:
	// recorded, never judged or pressed. A cookie banner or a chat
	// widget is someone else's.
	Ignore []string `yaml:"ignore"`
}

// Skip link settings (Settings.SkipLink).
const (
	SkipLinkExpected = "expected"
	SkipLinkRequired = "required"
	SkipLinkOff      = "off"
)

// DefaultSettings are the settings without a keyboard: block.
func DefaultSettings() Settings {
	return Settings{
		MaxTabs: 150, Widgets: 10, TrapAfter: 4, SkipLink: SkipLinkExpected,
		Pages: []string{"/cart*", "/checkout*", "/my-account*", "/contact*"},
	}
}

// maxSelector caps a selector in keyboard.ignore, in runes, like the
// selectors the walk records.
const maxSelector = 300

// defaults are the keyboard walk's limits when the config file sets
// none.
func defaults() model.KeyboardOptions { return DefaultSettings().options() }

// options are the limits of s the run records.
func (s Settings) options() model.KeyboardOptions {
	return model.KeyboardOptions{MaxTabs: s.MaxTabs, Widgets: s.Widgets, TrapAfter: s.TrapAfter, SkipLink: s.SkipLink}
}

// decode reads a keyboard: block over the defaults and checks each
// value, naming the setting and its line when one is out of range.
func decode(node yaml.Node) (Settings, error) {
	s := DefaultSettings()
	if err := config.DecodeBlock("keyboard", node, &s); err != nil {
		return s, err
	}
	bad := func(key, msg string) error {
		return &config.SettingError{Key: "keyboard." + key, Line: config.KeyLine(node, key), Msg: msg}
	}
	between := func(key string, v, lo, hi int) error {
		if v < lo || v > hi {
			return bad(key, fmt.Sprintf("must be a whole number from %d to %d, not %d", lo, hi, v))
		}
		return nil
	}
	if err := between("max_tabs", s.MaxTabs, 10, 2000); err != nil {
		return s, err
	}
	if err := between("widgets", s.Widgets, 0, 200); err != nil {
		return s, err
	}
	if err := between("trap_after", s.TrapAfter, 3, 20); err != nil {
		return s, err
	}
	s.SkipLink = strings.ToLower(strings.TrimSpace(s.SkipLink))
	if !slices.Contains([]string{SkipLinkExpected, SkipLinkRequired, SkipLinkOff}, s.SkipLink) {
		return s, bad("skip_link", "must be expected, required or off")
	}
	for _, p := range s.Pages {
		if !strings.HasPrefix(p, "/") {
			return s, bad("pages", fmt.Sprintf("%q isn't a path: write it from the site's root, such as /checkout*", p))
		}
	}
	for _, sel := range s.Ignore {
		if strings.TrimSpace(sel) == "" || strings.ContainsAny(sel, "\n\r") || utf8.RuneCountInString(sel) > maxSelector {
			return s, bad("ignore", fmt.Sprintf("%q isn't a CSS selector: give one selector of at most %d characters on one line", sel, maxSelector))
		}
	}
	return s, nil
}

// Configure reads the keyboard: block of the config file
// (check.Configurable).
func (m *Module) Configure(node yaml.Node) error {
	s, err := decode(node)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.set, m.opts = s, s.options()
	m.mu.Unlock()
	return nil
}

// settings are the settings in use.
func (m *Module) settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.set.MaxTabs == 0 { // a Module{} that New didn't make
		return DefaultSettings()
	}
	return m.set
}

// Options returns the keyboard walk's limits for cfg, from the keyboard
// block of the config file, for the CLI to record in the run's options.
// Its error is Configure's for the same block.
func Options(cfg config.Config) (model.KeyboardOptions, error) {
	s, err := decode(cfg.Block("keyboard"))
	if err != nil {
		return model.KeyboardOptions{}, err
	}
	return s.options(), nil
}
