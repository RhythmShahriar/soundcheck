// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Under a loose umask (0002, as many desktops set) reports stay 0600
// and the files meant to be committed stay 0644.
func TestModesUnderUmask(t *testing.T) {
	old := syscall.Umask(0o002)
	t.Cleanup(func() { syscall.Umask(old) })
	t.Chdir(t.TempDir())
	write := func(w io.Writer) error { _, err := io.WriteString(w, "x"); return err }
	if err := writeFile("r/report.html", write); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate("e/eaa/draft.md", write); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCLI(t, "init"); code != exitOK {
		t.Fatal(errOut)
	}
	for p, want := range map[string]os.FileMode{"r/report.html": 0o600, "e/eaa/draft.md": 0o600, ".soundcheck/config.yaml": 0o644} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %o, want %o", p, fi.Mode().Perm(), want)
		}
	}
}

// A symlink at a report's path is refused: the link and what it points
// at stay as they were.
func TestWriteFileRefusesSymlink(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("target.txt", []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", "out.md"); err != nil {
		t.Fatal(err)
	}
	err := writeFile("out.md", func(w io.Writer) error { _, err := io.WriteString(w, "x"); return err })
	if !errors.Is(err, errSymlink) {
		t.Errorf("err = %v", err)
	}
	if data, _ := os.ReadFile("target.txt"); string(data) != "keep" {
		t.Errorf("target changed: %q", data)
	}
	if fi, err := os.Lstat("out.md"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link replaced: %v", err)
	}
	// Through the CLI: the other -o file is still written and the exit
	// is 3.
	dir := storedComparison(t, "new")
	code, _, errOut := runCLI(t, "report", dir, "--plain", "-o", "out.md", "-o", "ok.md")
	if code != exitError || !strings.Contains(errOut, "rhythm-soundcheck: writing out.md: refusing to write through a symlink\n") {
		t.Errorf("exit %d %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(".", "ok.md")); err != nil {
		t.Error(err)
	}
}
