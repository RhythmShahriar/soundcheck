// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
)

func TestStandard(t *testing.T) {
	v, err := Standard(config.Default())
	if err != nil || !slices.Contains([]string{"2.0", "2.1", "2.2"}, v) {
		t.Errorf("Standard(default) = %q, %v", v, err)
	}
}
