// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package model

import "testing"

func TestGlob(t *testing.T) {
	for _, tc := range []struct {
		pattern, s string
		want       bool
	}{
		{"/team/*", "/team/alice/", true},
		{"/team/*", "/team/", true},
		{"/team/*", "/teams/", false},
		{"health/*", "health/favicon", true},
		{"health/*", "visual/page", false},
		{"*", "", true},
		{"*/page", "visual/page", true},
		{"*.php", "/promo.php", true},
		{"*.php", "/promo.html", false},
		{"/a*b*c", "/abc", true},
		{"/a*b*c", "/acb", false},
		{"ab*ba", "aba", false}, // the prefix and suffix can't share a letter
		{"Page looks different (*)", "Page looks different (mobile)", true},
		{"/", "/", true},
		{"/", "/a", false},
		{"", "", true},
		{"", "/", false},
	} {
		if got := Glob(tc.pattern, tc.s); got != tc.want {
			t.Errorf("Glob(%q, %q) = %v, want %v", tc.pattern, tc.s, got, tc.want)
		}
	}
}
