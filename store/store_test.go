// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

func TestSnapshotLifecycle(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), ".soundcheck")}
	if _, err := s.Snapshot(""); err == nil || !strings.Contains(err.Error(), "no snapshot yet") {
		t.Fatalf("empty store: %v", err)
	}
	for _, name := range []string{"good", "after-update"} {
		dir, err := s.NewDir(Incoming, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := Saver(dir)("shots/a.png", []byte("png")); err != nil {
			t.Fatal(err)
		}
		if err := SaveRun(dir, &model.Run{Schema: model.Schema, Name: name, Target: "http://x.test/"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Promote(dir, name); err != nil {
			t.Fatal(err)
		}
		if err := s.SetLatest(name); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := s.Snapshot("")
	if err != nil || filepath.Base(dir) != "after-update" {
		t.Fatalf("latest = %s, %v", dir, err)
	}
	r, err := LoadRun(dir)
	if err != nil || r.Name != "after-update" {
		t.Fatalf("LoadRun: %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shots", "a.png")); err != nil {
		t.Error("files didn't move with the snapshot")
	}
	names, _ := s.Snapshots()
	if strings.Join(names, ",") != "after-update,good" {
		t.Errorf("snapshots = %v", names)
	}
	if _, err := s.Snapshot("missing"); err == nil {
		t.Error("a missing snapshot must be an error")
	}
	if _, err := s.Snapshot("../etc"); err == nil {
		t.Error("path names must be rejected")
	}
}

func TestSaverStaysInside(t *testing.T) {
	save := Saver(t.TempDir())
	for _, bad := range []string{"../x", "/etc/passwd", "a/../../x"} {
		if err := save(bad, nil); err == nil {
			t.Errorf("Saver wrote %q", bad)
		}
	}
}

func TestContained(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", ".", "..", "../x.png", "/etc/passwd", "a/../../x", "shots/../../x"} {
		if p, err := Contained(dir, bad); err == nil {
			t.Errorf("Contained(%q) = %q, want an error", bad, p)
		}
	}
	for rel, want := range map[string]string{"shots/a.png": "shots/a.png", "a/../b.png": "b.png", "..x.png": "..x.png", "./c.png": "c.png"} {
		p, err := Contained(dir, rel)
		if err != nil || p != filepath.Join(dir, filepath.FromSlash(want)) {
			t.Errorf("Contained(%q) = %q, %v; want %s", rel, p, err, want)
		}
	}
	if err := Saver(dir)("..x.png", []byte("ok")); err != nil {
		t.Errorf("Saver refused a local name: %v", err)
	}
}

func TestLoadRunRejectsOtherSchemas(t *testing.T) {
	dir := t.TempDir()
	_ = SaveRun(dir, &model.Run{Schema: "rhythm-soundcheck/v0"})
	if _, err := LoadRun(dir); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Errorf("got %v", err)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	s := Store{Root: t.TempDir()}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		if _, err := s.NewDir("runs", Stamp(start.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Prune(2); err != nil {
		t.Fatal(err)
	}
	runs, _ := s.list("runs")
	if len(runs) != 2 || runs[1] != Stamp(start.Add(4*time.Minute)) {
		t.Errorf("kept %v", runs)
	}
	latest, _ := s.LatestRun()
	if filepath.Base(latest) != runs[1] {
		t.Errorf("LatestRun = %s", latest)
	}
}

func TestExcludeFromGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	t.Chdir(repo)
	s := Store{Root: ".soundcheck"}
	s.ExcludeFromGit()
	s.ExcludeFromGit() // idempotent
	data, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	ex := string(data)
	if strings.Count(ex, "/.soundcheck/snapshots/") != 1 || !strings.Contains(ex, "/.soundcheck/runs/") {
		t.Errorf("exclude file:\n%s", ex)
	}
	for _, line := range []string{"/.soundcheck/LATEST\n", "/.soundcheck/.lock\n"} {
		if strings.Count(ex, line) != 1 {
			t.Errorf("want %q once in:\n%s", line, ex)
		}
	}
	if strings.Contains(ex, "/.soundcheck/\n") {
		t.Error("config.yaml must stay visible to git")
	}
}

func TestExcludeFromGitAddsMissingLines(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	t.Chdir(repo)
	path := filepath.Join(repo, ".git", "info", "exclude")
	// What an older version wrote.
	old := "# Soundcheck snapshots and reports\n/.soundcheck/snapshots/\n/.soundcheck/runs/\n/.soundcheck/.incoming/\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	Store{Root: ".soundcheck"}.ExcludeFromGit()
	data, _ := os.ReadFile(path)
	if want := old + "/.soundcheck/LATEST\n/.soundcheck/.lock\n"; string(data) != want {
		t.Errorf("exclude file:\n%s\nwant:\n%s", data, want)
	}
}

func TestExcludeFromGitSkipsIgnoredFolders(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	_ = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("/work/\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(repo, "work", "s01"), 0o750)
	t.Chdir(filepath.Join(repo, "work", "s01"))
	Store{Root: ".soundcheck"}.ExcludeFromGit()
	data, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if strings.Contains(string(data), "soundcheck") {
		t.Errorf("an ignored folder mustn't add exclude lines:\n%s", data)
	}
}

func TestSaveRunMarksInterrupted(t *testing.T) {
	dir := t.TempDir()
	r := &model.Run{Schema: model.Schema, Pages: []model.Page{{Path: "/"}, {Path: "/b", Error: "net::ERR_FAILED"}}}
	if err := SaveRun(dir, r); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadRun(dir); got.Interrupted {
		t.Error("a failed page isn't an interrupted run")
	}
	r.Pages = append(r.Pages, model.Page{Path: "/c", Error: model.CanceledError})
	if err := SaveRun(dir, r); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadRun(dir); !got.Interrupted {
		t.Error("a run with a canceled page must be saved as interrupted")
	}
}
