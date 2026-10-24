// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package browsertest runs the visual module against real Chrome: a
// snapshot and a compare of small local sites. The tests live apart from
// package visual so each test binary stays well under Go's 10 minute
// default timeout with -race; they skip with -short or without Chrome.
package browsertest
