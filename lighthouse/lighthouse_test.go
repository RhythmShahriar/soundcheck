// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package lighthouse

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// fakeNPX puts an npx on PATH that records its arguments and, unless
// fail is set, writes the result file where --output-path points.
func fakeNPX(t *testing.T, fail bool) (argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake npx is a shell script")
	}
	bin := t.TempDir()
	argsFile = filepath.Join(t.TempDir(), "args")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + argsFile + `"
echo "scanning..."
`
	if fail {
		script += "echo 'Error: site unreachable' >&2\nexit 1\n"
	} else {
		script += `out=""
while [ $# -gt 0 ]; do if [ "$1" = "--output-path" ]; then out="$2"; fi; shift; done
echo '{"summary":{"score":0.9}}' > "$out/ci-result.json"
`
	}
	if err := os.WriteFile(filepath.Join(bin, "npx"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return argsFile
}

func TestAvailable(t *testing.T) {
	fakeNPX(t, false)
	if !Available() {
		t.Error("npx on PATH not found")
	}
	t.Setenv("PATH", t.TempDir())
	if Available() {
		t.Error("found npx on an empty PATH")
	}
}

func TestRun(t *testing.T) {
	argsFile := fakeNPX(t, false)
	out := filepath.Join(t.TempDir(), "lighthouse")
	var progress strings.Builder
	if err := RunWith(context.Background(), "https://shop.example/", out, Options{Output: &progress}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(argsFile)
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"--yes", Package, "--site", "https://shop.example/", "--reporter", "jsonExpanded", "--output-path", out}
	if !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(out, Result)); err != nil {
		t.Error(err)
	}
	if !strings.Contains(progress.String(), "scanning") {
		t.Errorf("progress = %q", progress.String())
	}
}

func TestRunFailures(t *testing.T) {
	fakeNPX(t, true)
	err := Run(context.Background(), "https://shop.example/", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "site unreachable") {
		t.Errorf("err = %v", err)
	}
	if err := Run(context.Background(), "javascript:alert(1)", t.TempDir()); err == nil {
		t.Error("accepted a non-http URL")
	}
	t.Setenv("PATH", t.TempDir())
	if err := Run(context.Background(), "https://shop.example/", t.TempDir()); err == nil || !strings.Contains(err.Error(), "npx not found") {
		t.Errorf("err = %v", err)
	}
}
