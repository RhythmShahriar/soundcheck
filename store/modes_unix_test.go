// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Only the user who runs Soundcheck reads what it stores, whatever the
// umask: a group-writable umask (0002, common on shared hosts) leaves
// folders 0700 and files 0600.
func TestStoreModes(t *testing.T) {
	old := syscall.Umask(0o002)
	defer syscall.Umask(old)
	s := Store{Root: filepath.Join(t.TempDir(), ".soundcheck")}
	dir, err := s.NewDir("runs", "x")
	if err != nil {
		t.Fatal(err)
	}
	r := &model.Run{Schema: model.Schema, Target: "https://example.com/", Pages: []model.Page{{Path: "/", URL: "https://example.com/"}}}
	if err := SaveRun(dir, r); err != nil {
		t.Fatal(err)
	}
	save := Saver(dir)
	for _, name := range []string{"shots/home-desktop.png", "keyboard/trace.json", "eaa/statement.html"} {
		if err := save(name, []byte("data")); err != nil {
			t.Fatal(err)
		}
	}
	// Saving again replaces the file, as before.
	if err := save("shots/home-desktop.png", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "shots", "home-desktop.png")); string(b) != "new" {
		t.Errorf("the second save left %q", b)
	}
	for _, d := range []string{s.Root, filepath.Join(s.Root, "runs"), dir, filepath.Join(dir, "shots"), filepath.Join(dir, "keyboard"), filepath.Join(dir, "eaa")} {
		if m := mode(t, d); m != 0o700 {
			t.Errorf("%s: %o, want 700", d, m)
		}
	}
	for _, f := range []string{"run.json", PagesFile, "shots/home-desktop.png", "keyboard/trace.json", "eaa/statement.html"} {
		if m := mode(t, filepath.Join(dir, f)); m != 0o600 {
			t.Errorf("%s: %o, want 600", f, m)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "shots"))
	if len(entries) != 1 {
		t.Errorf("shots holds %d files, temp files left behind?", len(entries))
	}
}

func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// A symlink planted in a run folder doesn't turn a save into a write
// outside it: not as a folder on the way, not as the file itself and not
// as the run folder.
func TestSaverRefusesSymlinks(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), ".soundcheck")}
	dir, err := s.NewDir("runs", "x")
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	victim := filepath.Join(outside, "authorized_keys")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "keyboard")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "shots"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "shots", "a.png")); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(s.Root, "runs", "y")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ dir, name, want string }{
		{dir, "keyboard/trace.json", "refusing to write through a symlink: " + filepath.Join(dir, "keyboard")},
		{dir, "keyboard/deep/trace.json", "refusing to write through a symlink: " + filepath.Join(dir, "keyboard")},
		{dir, "shots/a.png", "refusing to write through a symlink: " + filepath.Join(dir, "shots", "a.png")},
		{linked, "a.png", "refusing to write through a symlink: " + linked},
	} {
		err := Saver(c.dir)(c.name, []byte("payload"))
		if err == nil || err.Error() != c.want {
			t.Errorf("save %s in %s: %v, want %q", c.name, c.dir, err, c.want)
		}
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 1 {
		t.Errorf("files outside: %v", entries)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Errorf("the file the link points at holds %q", b)
	}
	// What isn't a link still saves.
	if err := Saver(dir)("shots/b.png", []byte("ok")); err != nil {
		t.Error(err)
	}
}

// A link higher up, .soundcheck/runs pointing elsewhere, is refused too:
// by NewDir before it makes a folder there and by a save into a run
// folder below it. The .soundcheck folder itself may be a link.
func TestStoreRefusesLinkedKindFolders(t *testing.T) {
	base := t.TempDir()
	s := Store{Root: filepath.Join(base, ".soundcheck")}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	runs := filepath.Join(s.Root, "runs")
	if err := os.Symlink(outside, runs); err != nil {
		t.Fatal(err)
	}
	want := "refusing to write through a symlink: " + runs
	if _, err := s.NewDir("runs", "x"); err == nil || err.Error() != want {
		t.Errorf("NewDir: %v, want %q", err, want)
	}
	// A run folder made before the link was planted (or by hand).
	if err := os.Mkdir(filepath.Join(outside, "y"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Saver(filepath.Join(runs, "y"))("shots/a.png", []byte("payload")); err == nil || err.Error() != want {
		t.Errorf("save: %v, want %q", err, want)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Errorf("files outside: %v", entries)
	}

	// A linked .soundcheck is the user's choice and works.
	elsewhere := t.TempDir()
	linkedRoot := filepath.Join(t.TempDir(), ".soundcheck")
	if err := os.Symlink(elsewhere, linkedRoot); err != nil {
		t.Fatal(err)
	}
	dir, err := Store{Root: linkedRoot}.NewDir("runs", "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := Saver(dir)("shots/a.png", []byte("ok")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "runs", "x", "shots", "a.png")); err != nil {
		t.Error(err)
	}
}
