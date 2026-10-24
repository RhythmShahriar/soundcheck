// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
)

// asCLI makes the test binary run as rhythm-soundcheck, so tests can
// start it as a real process: to send it signals, or to check what
// package init functions read from os.Args.
const asCLI = "RHYTHM_SOUNDCHECK_TEST_AS_CLI"

func TestMain(m *testing.M) {
	if os.Getenv(asCLI) == "1" {
		main()
	}
	os.Exit(m.Run())
}

// cliCommand returns the test binary set up to run as the CLI with args,
// in dir.
func cliCommand(t *testing.T, dir string, args ...string) (cmd *exec.Cmd, stdout, stderr *bytes.Buffer) {
	t.Helper()
	cmd = exec.Command(os.Args[0], args...) //nolint:gosec // the test binary itself
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), asCLI+"=1")
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd, stdout, stderr
}
