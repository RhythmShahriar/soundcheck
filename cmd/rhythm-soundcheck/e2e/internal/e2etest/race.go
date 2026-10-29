// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build race

package e2etest

// raceFlag builds the binary with the race detector when the tests run
// with it, as the command's own tests did when they ran it in process.
var raceFlag = []string{"-race"}
