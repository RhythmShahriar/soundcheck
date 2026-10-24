// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// An external test package: a test file inside package norodflag that
// imported rod would make rod a dependency of norodflag itself and so
// initialize it first.
package norodflag_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/defaults"

	"github.com/RhythmShahriar/soundcheck/internal/norodflag"
)

// child makes the test binary print what rod read and exit, so a test
// can start it with a -rod argument (go test itself refuses the flag).
const child = "NORODFLAG_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(child) == "1" {
		fmt.Printf("env=%s monitor=%s show=%t\n", os.Getenv(norodflag.Env), defaults.Monitor, defaults.Show)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestInitOrder imports rod's defaults package next to this one the way
// main does and passes rod a flag: init order must still put norodflag
// first, so rod never reads it.
func TestInitOrder(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-A", "--rod=monitor=127.0.0.1:20261,show") //nolint:gosec // the test binary itself
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, norodflag.Env+"=") { // as if the user never set it
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, child+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "env=1 monitor= show=false" {
		t.Errorf("rod read its flag before norodflag's init: %s", got)
	}
}
