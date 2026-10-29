// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package e2e

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/demo"
)

func TestDemo(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome twice")
	}
	t.Chdir(t.TempDir())
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // where the demo keeps its snapshot, on Unix
	code, out, errOut := runCLI(t, "demo", "--plain", "--no-download")
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)^\[BROKE\] Form submission fails \(HTTP 500\).* - /contact/$`),
		regexp.MustCompile(`(?m)^\[BROKE\] Page not found \(404\) - /pricing/$`),
		regexp.MustCompile(`(?m)^Now try it on your staging site: rhythm-soundcheck snapshot staging.example.com$`),
	} {
		if !want.MatchString(out) {
			t.Errorf("no %s in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "if this is expected") || !strings.Contains(out, "On your own site, compare shows an id with each change; rhythm-soundcheck accept <id> makes") {
		t.Errorf("accept hints that can't work in the demo:\n%s", out)
	}
	if strings.Contains(out, "full report:") {
		t.Errorf("points at a report it removes:\n%s", out)
	}
	if _, err := os.Stat(".soundcheck"); !os.IsNotExist(err) {
		t.Errorf("the demo wrote to the project's .soundcheck: %v", err)
	}
	if left := leftIn(tmp); left != "" {
		t.Errorf("the demo left %s in the temp folder", left)
	}
}

// leftIn is the first entry in dir that isn't Chrome's own, or "".
// Chrome removes its com.google.Chrome.* files a moment after it exits,
// so those get a short wait before they count. rod keeps the folders it
// makes a browser profile in (rod/user-data) for every launch and
// removes the profile itself: they count only when a file is left in
// them. On macOS Soundcheck starts Chrome with rod's leakless guard
// (browser/process_darwin.go says why), which leakless unpacks once
// into leakless-<arch>-<version>/leakless and keeps for every later
// launch: that folder doesn't count when it holds only the guard.
func leftIn(dir string) string {
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, _ := os.ReadDir(dir)
		chrome := false
		for _, e := range entries {
			if e.Name() == "rod" {
				if f := fileIn(filepath.Join(dir, "rod")); f != "" {
					return filepath.ToSlash(filepath.Join("rod", f))
				}
				continue
			}
			if leaklessDir.MatchString(e.Name()) && onlyLeakless(filepath.Join(dir, e.Name())) {
				continue
			}
			if !strings.HasPrefix(e.Name(), "com.google.Chrome.") {
				return e.Name()
			}
			chrome = true
		}
		if !chrome || time.Now().After(deadline) {
			return ""
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// demo --eaa plants a keyboard trap and lost focus next to the usual
// breaks and shows the trap as BROKE and the lost focus as NEW. The
// opening hours dialog's plain button opens it with showModal(), so
// this also proves the walk finds such a dialog.
func TestDemoEAA(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome twice")
	}
	t.Chdir(t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	code, out, errOut := runCLI(t, "demo", "--eaa", "--plain", "--no-download")
	if code != exitOK && code != exitError {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{demo.Describe(true), "BROKE] Page not found (404) - /pricing/", "With --eaa on your own site"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "statement: ") {
		t.Errorf("points at a draft it removes:\n%s", out)
	}
	if trap := regexp.MustCompile(`(?m)^\[BROKE\] Keyboard focus is trapped.* - /$`); !trap.MatchString(out) {
		t.Errorf("no BROKE trap in:\n%s", out)
	}
	for _, want := range []string{"[NEW] Focus is lost after closing - /about/\n", "  After Escape on \"Opening hours\" focus went to a removed element.\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if _, err := os.Stat(".soundcheck"); !os.IsNotExist(err) {
		t.Errorf("the demo wrote to the project's .soundcheck: %v", err)
	}
}

// fileIn is the first file under dir, relative to it, or "" when dir
// holds only folders.
func fileIn(dir string) string {
	var found string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}
		if !d.IsDir() {
			found, _ = filepath.Rel(dir, p)
		}
		return nil
	})
	return found
}

// leaklessDir is the name of the folder leakless unpacks its guard into.
var leaklessDir = regexp.MustCompile(`^leakless-[a-z0-9]+-[0-9a-f]{32}$`)

// onlyLeakless reports whether dir holds the leakless guard and nothing
// else.
func onlyLeakless(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) == 1 && !entries[0].IsDir() &&
		(entries[0].Name() == "leakless" || entries[0].Name() == "leakless.exe")
}

// leftIn lets rod's empty profile folders and the leakless guard pass,
// and nothing else: a file in rod's folders, anything more next to the
// guard or any other entry is left over.
func TestLeftIn(t *testing.T) {
	guard := "leakless-arm64-adb80298fa6a3af7ced8b1c9b5f18007"
	for _, tc := range []struct {
		name  string
		files []string
		dirs  []string
		want  string
	}{
		{"empty", nil, nil, ""},
		{"rod folders", nil, []string{"rod/user-data"}, ""},
		{"leakless guard", []string{guard + "/leakless"}, nil, ""},
		{"file in rod", []string{"rod/user-data/x/Cookies"}, nil, "rod/user-data/x/Cookies"},
		{"more next to the guard", []string{guard + "/leakless", guard + "/run.json"}, nil, guard},
		{"look-alike", []string{"leakless-arm64-snapshot/leakless"}, nil, "leakless-arm64-snapshot"},
		{"snapshot", []string{"soundcheck-demo-1/run.json"}, nil, "soundcheck-demo-1"},
	} {
		dir := t.TempDir()
		for _, d := range tc.dirs {
			if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		for _, f := range tc.files {
			p := filepath.Join(dir, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := leftIn(dir); got != tc.want {
			t.Errorf("%s: leftIn = %q, want %q", tc.name, got, tc.want)
		}
	}
}
