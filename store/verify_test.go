// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// pngBytes is a real 1x1 PNG.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// shotSnapshot fills an incoming folder with a run of n pages, each with
// a desktop screenshot and its second capture.
func shotSnapshot(t *testing.T, s Store, name, tag string, n int) string {
	t.Helper()
	dir, err := s.NewDir(Incoming, name)
	if err != nil {
		t.Fatal(err)
	}
	r := &model.Run{Schema: model.Schema, Name: name, Target: tag}
	for i := range n {
		shot := model.Shot{File: fmt.Sprintf("shots/p%d-desktop.png", i), Second: fmt.Sprintf("shots/p%d-desktop.2.png", i)}
		for _, f := range []string{shot.File, shot.Second} {
			if err := Saver(dir)(f, pngBytes(t)); err != nil {
				t.Fatal(err)
			}
		}
		r.Pages = append(r.Pages, model.Page{Path: fmt.Sprintf("/p%d", i), Shots: map[string]model.Shot{"desktop": shot}})
	}
	if err := SaveRun(dir, r); err != nil {
		t.Fatal(err)
	}
	return dir
}

// promoteOld takes a good snapshot "good" tagged "old" and points LATEST
// at it.
func promoteOld(t *testing.T, s Store) {
	t.Helper()
	if _, err := s.Promote(shotSnapshot(t, s, "good", "old", 1), "good"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLatest("good"); err != nil {
		t.Fatal(err)
	}
}

// stillOld fails unless LATEST still points to the old snapshot.
func stillOld(t *testing.T, s Store) {
	t.Helper()
	dir, err := s.Snapshot("")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := LoadRun(dir); err != nil || r.Target != "old" {
		t.Errorf("LATEST doesn't point to the old snapshot any more: %+v %v", r, err)
	}
}

func TestPromoteRefusesMissingScreenshots(t *testing.T) {
	s := Store{Root: t.TempDir()}
	promoteOld(t, s)
	dir := shotSnapshot(t, s, "good", "new", 2)
	if err := os.Remove(filepath.Join(dir, "shots", "p1-desktop.png")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Promote(dir, "good")
	want := "snapshot not saved: 1 screenshot could not be written (shots/p1-desktop.png is missing)"
	if err == nil || err.Error() != want {
		t.Fatalf("Promote = %v\nwant %s", err, want)
	}
	stillOld(t, s)
	if _, err := os.Stat(filepath.Join(dir, "run.json")); err != nil {
		t.Error("the refused snapshot must stay where it was")
	}
}

func TestPromoteNamesTheBrokenScreenshots(t *testing.T) {
	s := Store{Root: t.TempDir()}
	promoteOld(t, s)
	dir := shotSnapshot(t, s, "good", "new", 3)
	_ = os.Remove(filepath.Join(dir, "shots", "p0-desktop.png"))
	_ = os.WriteFile(filepath.Join(dir, "shots", "p0-desktop.2.png"), nil, 0o600)              // empty: the disk filled up
	_ = os.WriteFile(filepath.Join(dir, "shots", "p1-desktop.png"), []byte("<html>"), 0o600)   // not an image
	_ = os.WriteFile(filepath.Join(dir, "shots", "p2-desktop.2.png"), []byte("\x89PN"), 0o600) // cut short
	r, _ := LoadRun(dir)
	r.Pages = append(r.Pages, model.Page{Path: "/x", Shots: map[string]model.Shot{"desktop": {File: "../good/shots/p0-desktop.png"}}})
	if err := SaveRun(dir, r); err != nil {
		t.Fatal(err)
	}
	_, err := s.Promote(dir, "good")
	want := "snapshot not saved: 5 screenshots could not be written (shots/p0-desktop.png is missing, shots/p0-desktop.2.png isn't a PNG, shots/p1-desktop.png isn't a PNG, 2 more)"
	if err == nil || err.Error() != want {
		t.Fatalf("Promote = %v\nwant %s", err, want)
	}
	stillOld(t, s)
	if why := checkShot(dir, "../good/shots/p0-desktop.png"); why != "is outside the snapshot" {
		t.Errorf("outside path: %q", why)
	}
	if why := checkShot(dir, "/etc/passwd"); why != "is outside the snapshot" {
		t.Errorf("absolute path: %q", why)
	}
}

func TestPromoteAcceptsIntactScreenshots(t *testing.T) {
	s := Store{Root: t.TempDir()}
	promoteOld(t, s)
	final, err := s.Promote(shotSnapshot(t, s, "good", "new", 3), "good")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetLatest("good"); err != nil {
		t.Fatal(err)
	}
	if r, err := LoadRun(final); err != nil || r.Target != "new" || len(r.Pages) != 3 {
		t.Errorf("promoted %+v %v", r, err)
	}
}

func TestPromoteNeedsARun(t *testing.T) {
	s := Store{Root: t.TempDir()}
	dir, err := s.NewDir(Incoming, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Promote(dir, "empty"); err == nil || !strings.Contains(err.Error(), "no run in") {
		t.Errorf("Promote = %v", err)
	}
}
