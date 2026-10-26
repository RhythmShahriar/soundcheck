// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// stopped leaves a snapshot called r1 in the store as Ctrl+C does: one
// finished page and the settings it ran with.
func stopped(t *testing.T, o model.Options) string {
	t.Helper()
	dir := filepath.Join(".soundcheck", store.Incoming, "r1")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(model.Page{ID: model.PageID("/"), Path: "/", Status: 200, Title: "Home"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, store.PagesFile), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveResumeOptions(dir, o); err != nil {
		t.Fatal(err)
	}
	// A folder without an owner file counts as in use until it's a day
	// old; its last page is still recent enough to resume.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	return dir
}

var stoppedWith = model.Options{Viewports: []string{"desktop"}, Levels: []string{"inspect", "click"}, Modules: []string{"health"}, Engine: "firefox"}

// --resume carries on with the stopped snapshot's settings, so its
// pages and the new ones are checked alike.
func TestResumeInheritsTheStoppedSnapshotsSettings(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := stopped(t, stoppedWith)
	p, err := parse([]string{"snapshot", "127.0.0.1:22200", "--name", "r1", "--resume"})
	if err != nil {
		t.Fatal(err)
	}
	s := &session{plan: p, st: store.Store{Root: ".soundcheck"}}
	if err := s.resumeSettings("r1", dir); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(p.cfg.Modules, ",") + " | " + strings.Join(p.cfg.Interactions.Levels, ",") + " | " +
		strings.Join(p.cfg.Viewports, ",") + " | " + string(p.engine)
	if want := "health | inspect,click | desktop | firefox"; got != want {
		t.Errorf("settings\n got %s\nwant %s", got, want)
	}
}

// Settings given on the command line that differ from the stopped
// snapshot's are refused before anything is visited.
func TestResumeRefusesOtherSettings(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--viewport", "mobile"}, "viewports desktop -> mobile (--viewport)"},
		{[]string{"--modules", "health,visual"}, "modules health -> health,visual (--modules)"},
		{[]string{"--browser", "chrome"}, "browser firefox -> chrome (--browser)"},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			t.Chdir(t.TempDir())
			stopped(t, stoppedWith)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `<!doctype html><title>Home</title>`)
			}))
			defer srv.Close()
			args := append([]string{"snapshot", srv.URL, "--name", "r1", "--resume", "--plain"}, tc.args...)
			code, out, errOut := runCLI(t, args...)
			if code != exitUsage || !strings.Contains(errOut, `snapshot "r1" stopped with other settings`) || !strings.Contains(errOut, tc.want) {
				t.Errorf("exit %d, want %d naming %q\n%s\n%s", code, exitUsage, tc.want, errOut, out)
			}
		})
	}
}

// --resume keeps a snapshot's EAA mode and WCAG version: its pages are
// audited one way or the run is refused.
func TestResumeKeepsEAAMode(t *testing.T) {
	eaaWith := model.Options{Viewports: []string{"desktop"}, Levels: []string{"inspect", "click"},
		Modules: []string{"health", "a11y", "keyboard"}, EAA: true, WCAG: "2.1"}
	for _, tc := range []struct {
		name string
		args []string
		want string // "" when it may resume
	}{
		{"same", []string{"--eaa"}, ""},
		{"eaa left out", []string{"--modules", "health,a11y,keyboard"}, "eaa on (WCAG 2.1) -> off"},
		{"other version", []string{"--eaa", "--wcag", "2.2"}, "eaa on (WCAG 2.1) -> on (WCAG 2.2)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeConfig(t, "")
			dir := stopped(t, eaaWith)
			p, err := parse(append([]string{"snapshot", "127.0.0.1:22200", "--name", "r1", "--resume"}, tc.args...))
			if err != nil {
				t.Fatal(err)
			}
			s := &session{plan: p, st: store.Store{Root: ".soundcheck"}}
			err = s.resumeSettings("r1", dir)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("err %v, want one naming %q", err, tc.want)
			}
		})
	}
}
