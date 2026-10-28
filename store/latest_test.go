// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

func TestLatestIsReserved(t *testing.T) {
	s := Store{Root: t.TempDir()}
	for _, name := range []string{"LATEST", "latest", "Latest"} {
		if ValidName(name) {
			t.Errorf("ValidName(%q) = true", name)
		}
		if _, err := s.NewDir(Incoming, name); err == nil {
			t.Errorf("NewDir accepted %q", name)
		}
		if _, err := s.Promote(t.TempDir(), name); err == nil {
			t.Errorf("Promote accepted %q", name)
		}
		if err := s.SetLatest(name); err == nil {
			t.Errorf("SetLatest accepted %q", name)
		}
	}
	for _, name := range []string{"latest-release", "LATEST.1", "my-latest"} {
		if !ValidName(name) {
			t.Errorf("ValidName(%q) = false", name)
		}
	}
}

func TestLatestLivesInTheRoot(t *testing.T) {
	s := Store{Root: t.TempDir()}
	if _, err := s.Promote(snapshot(t, s, "good", "x"), "good"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLatest("good"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(s.Root, Latest)); err != nil || string(b) != "good\n" {
		t.Errorf("root LATEST = %q, %v", b, err)
	}
	if _, err := os.Lstat(s.legacyLatest()); err == nil {
		t.Error("snapshots/LATEST must not be written any more")
	}
}

func TestLatestMigratesTheOldPointer(t *testing.T) {
	s := Store{Root: t.TempDir()}
	if _, err := s.Promote(snapshot(t, s, "good", "x"), "good"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.legacyLatest(), []byte("good\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := s.Snapshot("")
	if err != nil || filepath.Base(dir) != "good" {
		t.Fatalf("Snapshot(\"\") = %s, %v", dir, err)
	}
	if _, err := os.Lstat(s.legacyLatest()); err == nil {
		t.Error("the old pointer wasn't removed")
	}
	if b, _ := os.ReadFile(filepath.Join(s.Root, Latest)); string(b) != "good\n" {
		t.Errorf("root LATEST = %q", b)
	}
	if names, _ := s.Snapshots(); strings.Join(names, ",") != "good" {
		t.Errorf("snapshots = %v", names)
	}
	// Once moved, it's read from the root.
	if dir, err := s.Snapshot(""); err != nil || filepath.Base(dir) != "good" {
		t.Errorf("after migration: %s, %v", dir, err)
	}
}

func TestLatestIgnoresAnOldSnapshotNamedLatest(t *testing.T) {
	s := Store{Root: t.TempDir()}
	// What an older version left after snapshot --name LATEST.
	if err := os.MkdirAll(s.legacyLatest(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := SaveRun(s.legacyLatest(), &model.Run{Schema: model.Schema}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(""); err == nil || !strings.Contains(err.Error(), "no snapshot yet") {
		t.Errorf("Snapshot(\"\") = %v", err)
	}
	if names, _ := s.Snapshots(); len(names) != 0 {
		t.Errorf("snapshots = %v", names)
	}
	if _, err := s.Promote(snapshot(t, s, "good", "x"), "good"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLatest("good"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.legacyLatest(), "run.json")); err != nil {
		t.Error("SetLatest removed a snapshot folder")
	}
}

// failRename makes rename fail for moves out of from.
func failRename(t *testing.T, from string) {
	t.Helper()
	orig := rename
	rename = func(a, b string) error {
		if a == from {
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("injected")}
		}
		return orig(a, b)
	}
	t.Cleanup(func() { rename = orig })
}

func TestPromoteCleansUpWhenTheRenameFails(t *testing.T) {
	s := Store{Root: t.TempDir()}
	if _, err := s.Promote(snapshot(t, s, "good", "old"), "good"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"good", "fresh"} {
		dir := snapshot(t, s, name, "new")
		failRename(t, dir)
		if _, err := s.Promote(dir, name); err == nil || !strings.Contains(err.Error(), "injected") {
			t.Errorf("%s: Promote = %v", name, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(s.Root, "snapshots"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("left %s behind", e.Name())
		}
	}
	if r, err := LoadRun(filepath.Join(s.Root, "snapshots", "good")); err != nil || r.Target != "old" {
		t.Errorf("the old snapshot is gone: %+v %v", r, err)
	}
}

func TestPromoteKeepsTheOldSnapshotWhenItCantBePutBack(t *testing.T) {
	s := Store{Root: t.TempDir()}
	if _, err := s.Promote(snapshot(t, s, "good", "old"), "good"); err != nil {
		t.Fatal(err)
	}
	dir := snapshot(t, s, "good", "new")
	orig := rename
	rename = func(a, b string) error {
		if a == dir || filepath.Base(filepath.Dir(a)) != "snapshots" {
			return errors.New("injected")
		}
		return orig(a, b)
	}
	t.Cleanup(func() { rename = orig })
	_, err := s.Promote(dir, "good")
	if err == nil || !strings.Contains(err.Error(), "couldn't be put back") {
		t.Fatalf("Promote = %v", err)
	}
	aside := err.Error()[strings.LastIndex(err.Error(), " is in ")+len(" is in "):]
	if r, err := LoadRun(aside); err != nil || r.Target != "old" {
		t.Errorf("the old snapshot wasn't kept in %s: %+v %v", aside, r, err)
	}
}

func TestNewDirSaysLatestIsReserved(t *testing.T) {
	_, err := Store{Root: t.TempDir()}.NewDir(Incoming, "LATEST")
	if err == nil || err.Error() != `"LATEST" is reserved; pick another name` {
		t.Errorf("got %v", err)
	}
}

// Deleting the snapshot LATEST names moves the pointer to the snapshot
// taken last, or removes it when none is left; deleting another keeps it.
func TestForgetMovesLatest(t *testing.T) {
	s := Store{Root: t.TempDir()}
	for i, name := range []string{"p9", "p3", "p2"} {
		dir, err := s.NewDir(Incoming, name)
		if err != nil {
			t.Fatal(err)
		}
		started := time.Date(2026, 10, 1+i, 9, 0, 0, 0, time.UTC)
		if err := SaveRun(dir, &model.Run{Schema: model.Schema, Name: name, Started: started}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Promote(dir, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetLatest("p2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget("p9"); err != nil || s.latest() != "p2" {
		t.Errorf("deleting another snapshot moved LATEST to %q (%v)", s.latest(), err)
	}
	del := func(name string) {
		t.Helper()
		if err := os.RemoveAll(filepath.Join(s.Root, "snapshots", name)); err != nil {
			t.Fatal(err)
		}
		if err := s.Forget(name); err != nil {
			t.Fatal(err)
		}
	}
	del("p2")
	// p3 was taken after p9, though p9 sorts last by name.
	if dir, err := s.Snapshot(""); err != nil || filepath.Base(dir) != "p3" {
		t.Errorf("after deleting the latest: %q, %v; want p3", dir, err)
	}
	del("p3")
	del("p9")
	if _, err := os.Stat(filepath.Join(s.Root, Latest)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("LATEST is left with no snapshot: %v", err)
	}
	if _, err := s.Snapshot(""); err == nil || !strings.Contains(err.Error(), "no snapshot yet") {
		t.Errorf("no snapshot left: %v", err)
	}
}
