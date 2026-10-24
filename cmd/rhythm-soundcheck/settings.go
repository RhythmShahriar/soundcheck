// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// chosen says where the user set each setting compare may take from the
// snapshot: the flag ("--level") or the config file's path. Empty means
// not set, so the snapshot's value is used.
type chosen struct {
	modules, levels, viewports string
}

// chosenSettings reads which of the inheritable settings the flags
// (set, from flag.Visit) or the config file set.
func chosenSettings(set map[string]bool, cfg config.Config, configPath string) chosen {
	from := func(flagName, key string) string {
		switch {
		case set[flagName]:
			return "--" + flagName
		case cfg.InFile(key):
			return configPath
		}
		return ""
	}
	return chosen{modules: from("modules", "modules"), levels: from("level", "interactions.levels"),
		viewports: from("viewport", "viewports")}
}

// settingChange is one setting a comparison runs differently from its
// snapshot.
type settingChange struct {
	name          string // modules, levels or viewports
	before, after []string
	from          string // the flag or file that set it, or ""
}

func (c settingChange) String() string {
	s := fmt.Sprintf("%s %s -> %s", c.name, list(c.before), list(c.after))
	if c.from != "" {
		s += " (" + c.from + ")"
	}
	return s
}

// inherit gives cfg the snapshot's modules, levels and viewports where
// the user didn't choose them, so a plain compare checks what the
// snapshot checked: one taken with --level submit is compared with submit
// too. --skip still applies to inherited modules, and echo stays on or off
// as --echo, SOUNDCHECK_ECHO and echo.url say, since it needs Echo
// running now. It returns the settings that still differ from the
// snapshot. A snapshot from before Soundcheck stored its options changes
// nothing.
func inherit(cfg *config.Config, snap model.Options, ch chosen, skip []string) []settingChange {
	if ch.modules == "" && len(snap.Modules) > 0 {
		var mods []string
		for _, m := range known(snap.Modules, config.Modules) {
			if m != "echo" && !slices.Contains(skip, m) {
				mods = append(mods, m)
			}
		}
		if cfg.Has("echo") {
			mods = append(mods, "echo")
		}
		cfg.Modules = mods // empty when --skip left none: cmdCompare says so
	}
	if l := known(snap.Levels, config.Levels); ch.levels == "" && len(l) > 0 {
		cfg.Interactions.Levels = l
	}
	if v := known(snap.Viewports, []string{"desktop", "mobile"}); ch.viewports == "" && len(v) > 0 {
		cfg.Viewports = v
	}

	var changes []settingChange
	add := func(name string, before, after []string, from string) {
		if len(before) > 0 && !sameSet(before, after) {
			changes = append(changes, settingChange{name: name, before: before, after: after, from: from})
		}
	}
	add("modules", snap.Modules, cfg.Modules, ch.modules)
	add("levels", snap.Levels, cfg.Interactions.Levels, ch.levels)
	add("viewports", snap.Viewports, cfg.Viewports, ch.viewports)
	return changes
}

func joinChanges(changes []settingChange) string {
	parts := make([]string, len(changes))
	for i, c := range changes {
		parts[i] = c.String()
	}
	return strings.Join(parts, "; ")
}

// snapshotSettings describes a snapshot's settings for the Settings line.
func snapshotSettings(name string, o model.Options) string {
	var parts []string
	for _, p := range []struct {
		name string
		v    []string
	}{{"modules", o.Modules}, {"levels", o.Levels}, {"viewports", o.Viewports}} {
		if len(p.v) > 0 {
			parts = append(parts, p.name+" "+list(p.v))
		}
	}
	return fmt.Sprintf("from snapshot %q (%s)", name, strings.Join(parts, "; "))
}

// known keeps the values this version knows, in their order: a snapshot
// from a newer Soundcheck may name more.
func known(values, all []string) []string {
	var out []string
	for _, v := range values {
		if slices.Contains(all, v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

func list(v []string) string {
	if len(v) == 0 {
		return "none"
	}
	return strings.Join(v, ",")
}
