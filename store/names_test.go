// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
	_ "time/tzdata" // America/New_York on systems without zoneinfo
)

func TestValidNameTable(t *testing.T) {
	for name, want := range map[string]bool{
		"good": true, "after-update": true, "v1.2": true, "release_3": true,
		"console": true, "auxiliary": true, "com0": true, "com10": true, "lpt": true, "nul-test": true,
		"": false, ".hidden": false, "../etc": false, "a b": false, "a/b": false,
		"con": false, "CON": false, "Prn": false, "aux.txt": false, "nul.tar.gz": false,
		"com1": false, "COM9.log": false, "lpt1": false, "lpt9.x": false,
		"a.": false, "before..": false, "LATEST": false,
	} {
		if got := ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %v, want %v", name, got, want)
		}
	}
	if err := CheckName("aux.txt"); err == nil || err.Error() != `"aux.txt" is a device name on Windows; pick another name` {
		t.Errorf("aux.txt: %v", err)
	}
	if err := CheckName("a."); err == nil || err.Error() != `"a." ends with a dot, which Windows drops; pick another name` {
		t.Errorf("a.: %v", err)
	}
}

func TestSameButCase(t *testing.T) {
	listing := []string{"Before", "after", ".replaced-1", "LATEST"}
	for name, want := range map[string]string{
		"before": "Before", "BEFORE": "Before", "Before": "", "After": "after",
		"after": "", "other": "", "latest": "LATEST",
	} {
		if got := sameButCase(listing, name); got != want {
			t.Errorf("sameButCase(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestNamesThatDifferOnlyInCaseAreRefused(t *testing.T) {
	s := Store{Root: t.TempDir()}
	if _, err := s.Promote(snapshot(t, s, "Before", "x"), "Before"); err != nil {
		t.Fatal(err)
	}
	want := `"before" clashes with the existing snapshot "Before": macOS and Windows see names that differ only in case as the same folder; use "Before" or pick another name`
	if _, err := s.NewDir(Incoming, "before"); err == nil || err.Error() != want {
		t.Errorf("NewDir: %v", err)
	}
	// A folder taken under another name still can't replace it.
	if _, err := s.Promote(snapshot(t, s, "other", "y"), "before"); err == nil || err.Error() != want {
		t.Errorf("Promote: %v", err)
	}
	if _, err := s.NewDir(Incoming, "Before"); err != nil {
		t.Errorf("the same name must still replace it: %v", err)
	}
	if _, err := s.NewDir("runs", "before"); err != nil {
		t.Errorf("runs don't clash with snapshots: %v", err)
	}
}

func TestListSortsByTime(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	zone := localZone
	localZone = ny
	t.Cleanup(func() { localZone = zone })
	s := Store{Root: t.TempDir()}
	// Daylight saving time ends at 2:00 EDT on 1 November 2026, and the
	// clocks go back to 1:00. In local time, 1:50 EDT comes before
	// 1:10 EST, but sorts after it by name.
	edt := time.Date(2026, 11, 1, 5, 50, 0, 0, time.UTC) // 1:50 EDT
	est := time.Date(2026, 11, 1, 6, 10, 0, 0, time.UTC) // 1:10 EST
	if edt.In(ny).Format("15:04 MST") != "01:50 EDT" || est.In(ny).Format("15:04 MST") != "01:10 EST" {
		t.Fatal("zone data disagrees with the test")
	}
	want := []string{
		"good",                    // named by the user: before all stamps
		"2026-11-01T00-30-00",     // legacy local stamp: 4:30 UTC
		Stamp(edt),                // 5:50 UTC
		Stamp(edt) + "-2",         // the same millisecond, made second
		Stamp(est),                // 6:10 UTC
		Stamp(est.Add(time.Hour)), // 7:10 UTC
		"2026-11-01T03-00-00",     // legacy local stamp: 8:00 UTC, though "T03" sorts before "T05" by name
	}
	shuffled := slices.Clone(want)
	slices.Reverse(shuffled)
	for _, name := range shuffled {
		if err := os.MkdirAll(filepath.Join(s.Root, "runs", name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.list("runs")
	if !slices.Equal(got, want) {
		t.Errorf("list = %v\nwant %v", got, want)
	}
	if latest, _ := s.LatestRun(); filepath.Base(latest) != "2026-11-01T03-00-00" {
		t.Errorf("LatestRun = %s", latest)
	}
}

func TestMoveRetries(t *testing.T) {
	retry, backoff := retryRenames, renameBackoff
	retryRenames, renameBackoff = true, time.Millisecond
	t.Cleanup(func() { retryRenames, renameBackoff = retry, backoff })
	orig := rename
	t.Cleanup(func() { rename = orig })
	denied := &os.LinkError{Op: "rename", Err: syscall.EACCES}

	calls := 0
	rename = func(string, string) error {
		if calls++; calls < 3 {
			return denied
		}
		return nil
	}
	if err := move("a", "b"); err != nil || calls != 3 {
		t.Errorf("move = %v after %d tries", err, calls)
	}

	calls = 0
	rename = func(string, string) error { calls++; return denied }
	if err := move("a", "b"); !errors.Is(err, syscall.EACCES) || calls != renameTries {
		t.Errorf("move = %v after %d tries, want %d", err, calls, renameTries)
	}

	calls = 0
	rename = func(string, string) error { calls++; return &os.LinkError{Op: "rename", Err: os.ErrNotExist} }
	if err := move("a", "b"); err == nil || calls != 1 {
		t.Errorf("a missing source was tried %d times", calls)
	}

	retryRenames = false
	calls = 0
	rename = func(string, string) error { calls++; return denied }
	if _ = move("a", "b"); calls != 1 {
		t.Errorf("retried %d times with retries off", calls)
	}
}

func TestPromoteRetriesTheRename(t *testing.T) {
	retry, backoff := retryRenames, renameBackoff
	retryRenames, renameBackoff = true, time.Millisecond
	t.Cleanup(func() { retryRenames, renameBackoff = retry, backoff })
	s := Store{Root: t.TempDir()}
	dir := snapshot(t, s, "good", "x")
	orig := rename
	t.Cleanup(func() { rename = orig })
	fails := 2
	rename = func(a, b string) error {
		if fails > 0 {
			fails--
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("Access is denied.")}
		}
		return orig(a, b)
	}
	if _, err := s.Promote(dir, "good"); err != nil {
		t.Fatal(err)
	}
	if names, _ := s.Snapshots(); !slices.Equal(names, []string{"good"}) {
		t.Errorf("snapshots = %v", names)
	}
}
