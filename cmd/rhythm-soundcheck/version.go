// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"runtime/debug"
)

// version is the release this binary is, set by the release build
// (-X main.version=1.3.0) and by make build on a release tag. Any other
// build is "dev", which never looks for updates.
var version = "dev"

// nextRelease is the release this source is heading for. Raise it in the
// pull request that opens a release's work, so a source build says
// which features it has.
const nextRelease = "1.3.0"

// shownVersion is the version people see in --version, the header and
// statement drafts: the release, or for a dev build the release it is
// heading for and its commit, "dev (1.3.0+f8b0f92)", so a source build
// shows it has the features of that release.
func shownVersion() string {
	if version != "dev" {
		return version
	}
	info, _ := debug.ReadBuildInfo()
	return devVersion(info)
}

// devVersion is "dev (<nextRelease>+<commit>)" from a dev build's
// settings, with ".modified" when the tree had uncommitted changes, or
// "dev (<nextRelease>)" when the build has no commit (go build outside
// git, or -buildvcs=false).
func devVersion(info *debug.BuildInfo) string {
	var rev string
	var modified bool
	if info != nil {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	if rev == "" {
		return "dev (" + nextRelease + ")"
	}
	rev = rev[:min(len(rev), 7)]
	if modified {
		rev += ".modified"
	}
	return "dev (" + nextRelease + "+" + rev + ")"
}
