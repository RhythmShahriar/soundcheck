// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package testenv sizes the browser tests for the machine they run on.
// A hosted CI runner starts a browser and loads a page several times
// slower than a developer machine, more so on Windows and macOS and
// under the race detector, while other test binaries run browsers of
// their own. A wait in a test is how long an operation may take, never
// what the test checks, so tests write it as Timeout(d) with d the time
// it takes on a quiet machine.
package testenv

import (
	"math"
	"os"
	"runtime"
	"strconv"
	"time"
)

// ScaleEnv sets Scale: a number of 1 or more, e.g. SOUNDCHECK_TEST_SCALE=4
// for a very slow machine.
const ScaleEnv = "SOUNDCHECK_TEST_SCALE"

// Scale is the factor test waits are multiplied by. ScaleEnv sets it.
// Without it Scale is 1, or on CI (CI set, as GitHub Actions does) 2 on
// Linux and 3 on Windows and macOS, whose hosted runners have fewer and
// slower cores.
func Scale() float64 { return scale(os.Getenv, runtime.GOOS) }

func scale(getenv func(string) string, goos string) float64 {
	if v, err := strconv.ParseFloat(getenv(ScaleEnv), 64); err == nil && v >= 1 && !math.IsInf(v, 0) {
		return v
	}
	if ci := getenv("CI"); ci == "" || ci == "false" || ci == "0" {
		return 1
	}
	if goos == "linux" {
		return 2
	}
	return 3
}

// Timeout is d scaled for this machine.
func Timeout(d time.Duration) time.Duration { return time.Duration(float64(d) * Scale()) }

// Seconds is a whole number of seconds scaled for this machine, rounded
// up, for settings counted in seconds such as config.Config.Timeout.
func Seconds[T ~int](n T) T { return T(math.Ceil(float64(n) * Scale())) }
