// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package testenv

import (
	"testing"
	"time"
)

func TestScale(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		goos string
		want float64
	}{
		{nil, "linux", 1},
		{nil, "windows", 1},
		{map[string]string{"CI": "true"}, "linux", 2},
		{map[string]string{"CI": "true"}, "windows", 3},
		{map[string]string{"CI": "true"}, "darwin", 3},
		{map[string]string{"CI": "false"}, "darwin", 1},
		{map[string]string{"CI": "0"}, "windows", 1},
		{map[string]string{ScaleEnv: "4.5"}, "linux", 4.5},
		{map[string]string{"CI": "true", ScaleEnv: "1"}, "windows", 1},
		// Values that would shrink or break the waits are ignored.
		{map[string]string{"CI": "true", ScaleEnv: "0.5"}, "windows", 3},
		{map[string]string{ScaleEnv: "fast"}, "linux", 1},
		{map[string]string{ScaleEnv: "-2"}, "linux", 1},
		{map[string]string{ScaleEnv: "+Inf"}, "linux", 1},
		{map[string]string{ScaleEnv: "NaN"}, "linux", 1},
	} {
		if got := scale(func(k string) string { return tc.env[k] }, tc.goos); got != tc.want {
			t.Errorf("%v on %s: Scale = %v, want %v", tc.env, tc.goos, got, tc.want)
		}
	}
}

func TestTimeoutAndSeconds(t *testing.T) {
	t.Setenv(ScaleEnv, "2.5")
	if got := Timeout(2 * time.Second); got != 5*time.Second {
		t.Errorf("Timeout(2s) = %s", got)
	}
	if got := Seconds(3); got != 8 {
		t.Errorf("Seconds(3) = %d, want 8 (rounded up)", got)
	}
	t.Setenv(ScaleEnv, "1")
	t.Setenv("CI", "")
	if Timeout(time.Second) != time.Second || Seconds(5) != 5 {
		t.Error("a scale of 1 changed the waits")
	}
}
