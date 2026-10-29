// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package e2e runs rhythm-soundcheck end to end: each test starts the
// built binary on a test site in Chrome, then reads what it printed and
// wrote. These are the slowest tests of the command. They live in their
// own package, with their own test binary and timeout, so the command's
// other tests stay quick on a slow runner. The package holds tests only.
package e2e
