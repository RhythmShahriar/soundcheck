// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pii

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	// Assembled at run time: a literal key would trip push protection.
	key := "sk_" + "live_" + strings.Repeat("4eC39HqLyjWDarjtT1zdp7dc", 1)
	cases := []struct{ in, want string }{
		{"", ""},
		{"Search", "Search"},
		{"Signed in as jane.doe+shop@example.co.uk", "Signed in as ***@***"},
		{"a@b.de and c@d.org", "***@*** and ***@***"},
		{"Not an address: @home or user@localhost", "Not an address: @home or user@localhost"},
	}
	for _, c := range cases {
		if got := Redact(c.in); got != c.want {
			t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := Redact("Key " + key); strings.Contains(got, key) || !strings.HasPrefix(got, "Key ") {
		t.Errorf("secret kept: %q", got)
	}
}
