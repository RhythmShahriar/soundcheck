// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package norodflag turns off rod's hidden command line flag. Import it
// for its side effect, before anything that imports rod:
//
//	import _ "github.com/RhythmShahriar/soundcheck/internal/norodflag"
//
// rod's lib/defaults package reads -rod and --rod from os.Args in its
// init function, wherever they appear. Any argument a user passes on, such
// as -A '--rod=monitor=127.0.0.1:9222', would then open rod's browser
// monitor with no password, or with an unknown option make the binary
// panic before main. rod skips the flag when DISABLE_ROD_FLAG is set, so
// this package sets it in its own init.
//
// That works because Go (1.21 and later) initializes packages in import
// path order whenever their dependencies allow it. This package needs only
// os, which rod's defaults package needs too, so it is ready no later than
// rod; and "github.com/RhythmShahriar/..." sorts before
// "github.com/go-rod/..." (upper case before lower), so it goes first.
package norodflag

import "os"

// Env is the variable rod checks before reading its flag.
const Env = "DISABLE_ROD_FLAG"

func init() {
	_ = os.Setenv(Env, "1") // can't fail for a valid name
}
