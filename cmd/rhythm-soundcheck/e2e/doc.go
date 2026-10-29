// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package e2e runs rhythm-soundcheck end to end: each test starts the
// built binary on a test site in Chrome, then reads what it printed and
// wrote. These are the slowest tests of the command. They live apart
// from it and are split by feature into this package (the demo) and
// eaa, canary and mouseonly, each with its own test binary and timeout,
// so every package stays well under Go's 10 minute default on a slow
// runner. internal/e2etest builds the binary and holds the helpers
// they share. The packages hold tests only.
package e2e
