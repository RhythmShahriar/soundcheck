// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"regexp"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
)

// argv0 is how the binary was started; tests replace it.
var argv0 = os.Args[0]

// invokedAs is set by the rhythm CLI when it runs this binary for
// `rhythm soundcheck`, so hints name the command the user typed.
const invokedAs = cli.InvokedAsEnv

// safeName is what a command name may contain: it is printed in help and
// hints, so control characters from the environment must not reach the
// terminal.
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

// cmdName is the command to show in help, hints and error prefixes:
// "rhythm soundcheck" through the rhythm CLI, otherwise the binary's own
// name ("rhythm-soundcheck" for go install and the GitHub Action), so a
// copied hint works where it was printed.
//
// It is the kit's cli.Name with two more guards: a name holding anything
// but letters, digits, spaces, ".", "_" and "-" is never printed (the
// binary's name, then "rhythm-soundcheck", is used instead) and a path
// with Windows separators is cut on either.
func cmdName() string {
	if v := strings.TrimSpace(os.Getenv(invokedAs)); safeName.MatchString(v) {
		return v
	}
	// Both separators: a Windows path may reach a binary built elsewhere.
	base := argv0[strings.LastIndexAny(argv0, `/\`)+1:]
	if len(base) > 4 && strings.EqualFold(base[len(base)-4:], ".exe") {
		base = base[:len(base)-4]
	}
	if !safeName.MatchString(base) {
		return "rhythm-soundcheck"
	}
	return base
}

// prefix starts an error line: "rhythm-soundcheck:".
func prefix() string { return cmdName() + ":" }

// named puts the command name into messages from other packages, which
// say "rhythm soundcheck" (store's "take one first: rhythm soundcheck
// snapshot <url>").
func named(msg string) string {
	return strings.ReplaceAll(msg, "rhythm soundcheck ", cmdName()+" ")
}
