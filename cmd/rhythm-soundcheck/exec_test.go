// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/defaults"
)

// asCLI makes the test binary run as rhythm-soundcheck, so tests can
// start it as a real process: to send it signals, or to check what
// package init functions read from os.Args.
const asCLI = "RHYTHM_SOUNDCHECK_TEST_AS_CLI"

// rodEnv is rodEnv, spelled out: importing norodflag here would
// add it to the test binary and hide main forgetting to import it.
const rodEnv = "DISABLE_ROD_FLAG"

// rodReport makes the test binary print what rod's init read and exit.
const rodReport = "RHYTHM_SOUNDCHECK_TEST_ROD"

// argsReport makes the test binary write its arguments to args.txt and
// exit, standing in for Soundcheck where a command starts it (fleet). A
// fleet passes a site SOUNDCHECK_* variables only from <PREFIX>_ copies.
const argsReport = "SOUNDCHECK_TEST_ARGS"

func TestMain(m *testing.M) {
	switch {
	case os.Getenv(asCLI) == "1":
		main()
	case os.Getenv(argsReport) == "1":
		if err := os.WriteFile("args.txt", []byte(strings.Join(os.Args[1:], "\n")), 0o600); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case os.Getenv(rodReport) == "1":
		// Before main's run() resets rod: this is what init order left.
		fmt.Printf("env=%s monitor=%s\n", os.Getenv(rodEnv), defaults.Monitor)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// cliCommand returns the test binary set up to run as the CLI with args,
// in dir.
func cliCommand(t *testing.T, dir string, args ...string) (cmd *exec.Cmd, stdout, stderr *bytes.Buffer) {
	t.Helper()
	cmd = exec.Command(os.Args[0], args...) //nolint:gosec // the test binary itself
	cmd.Dir = dir
	cmd.Env = append(cleanEnv(), asCLI+"=1")
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd, stdout, stderr
}

// cleanEnv is the environment without DISABLE_ROD_FLAG, as if the user
// never set it, so only the binary's own norodflag import can set it.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, rodEnv+"=") {
			env = append(env, kv)
		}
	}
	return env
}
