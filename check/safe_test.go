// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"strings"
	"testing"
)

func writeNilMap() {
	var m map[string]int
	m["boom"] = 1
}

func TestSafe(t *testing.T) {
	var warnings []string
	warn := func(s string) { warnings = append(warnings, s) }

	ran := false
	Safe("health", "CheckPage", warn, func() { ran = true })
	if !ran || len(warnings) != 0 {
		t.Fatalf("a hook that returns: ran %v, warnings %v", ran, warnings)
	}

	Safe("health", "CheckPage", warn, writeNilMap)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v", warnings)
	}
	w := warnings[0]
	if !strings.HasPrefix(w, "health: panic in CheckPage: assignment to entry in nil map (") {
		t.Errorf("warning = %q", w)
	}
	// The first frame is where the panic happened, not Safe or the runtime.
	if !strings.Contains(w, "(check.writeNilMap safe_test.go:") || strings.Contains(w, "runtime.") {
		t.Errorf("frames in %q", w)
	}
	if n := strings.Count(w, ", "); n != 2 {
		t.Errorf("want 3 frames, got %d in %q", n+1, w)
	}

	warnings = nil
	Safe("speed", "Live", warn, func() { panic("bad page") })
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "speed: panic in Live: bad page (check.TestSafe.func") {
		t.Errorf("warnings = %v", warnings)
	}
}
