// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package canary runs rhythm-soundcheck --eaa end to end on a site that
// hides canary values everywhere a run could pick one up: no output,
// file or report may hold any of them. It holds tests only
// (see package e2e).
package canary
