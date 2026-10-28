// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import "testing"

// The key-flow trace counts key presses with the right noun.
func TestKeyPresses(t *testing.T) {
	for n, want := range map[int]string{0: "0 key presses", 1: "1 key press", 4: "4 key presses"} {
		if got := keyPresses(n); got != want {
			t.Errorf("keyPresses(%d) = %q, want %q", n, got, want)
		}
	}
}
