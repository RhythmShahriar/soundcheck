// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// keptChain is a snapshot "good" made with --eaa and two runs chained
// after it, as compare leaves them.
func keptChain(t *testing.T) (root, snap string, runs []string) {
	t.Helper()
	root = t.TempDir()
	snap, run := saveRun(t, root, "snapshots", "good", time.Unix(1000, 0), true, target)
	if err := Write(snap, run, nil); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"r1", "r2"} {
		prev, err := Previous(root, target)
		if err != nil {
			t.Fatal(err)
		}
		dir, run := saveRun(t, root, "runs", name, time.Unix(int64(1001+i), 0), true, target)
		if err := Write(dir, run, prev); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, dir)
	}
	return root, snap, runs
}

// changeRun rewrites the run in dir as accept does: run.json and the
// pages file both change.
func changeRun(t *testing.T, dir string) {
	t.Helper()
	run, err := store.LoadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	run.Pages[0].Issues = nil
	if err := store.SaveRun(dir, run); err != nil {
		t.Fatal(err)
	}
}

// After an accept into a snapshot, the snapshot's own pack and the
// chain of every later run still verify: the pack is recorded again
// and the one later runs chained to is checked in its kept copy.
func TestKeepAndRerecord(t *testing.T) {
	old := now
	now = func() time.Time { return time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })
	root, snap, runs := keptChain(t)
	before := sum(must(os.ReadFile(filepath.Join(snap, File))))

	if _, err := Rerecord(snap, "accept"); err == nil || !strings.Contains(err.Error(), "isn't kept") {
		t.Errorf("Rerecord before Keep = %v", err)
	}
	rel, err := Keep(snap)
	if err != nil || rel != "evidence/"+before {
		t.Fatalf("Keep = %q, %v", rel, err)
	}
	if again, err := Keep(snap); err != nil || again != rel {
		t.Errorf("Keep again = %q, %v", again, err)
	}
	changeRun(t, snap)
	if signed, err := Rerecord(snap, "accept"); err != nil || signed {
		t.Fatalf("Rerecord = %v, %v", signed, err)
	}
	p := readPackFile(t, snap)
	if p.Amends == nil || p.Amends.SHA256 != before || p.Amends.Reason != "accept" || p.Previous != nil {
		t.Errorf("rerecorded pack: amends %+v, previous %+v", p.Amends, p.Previous)
	}
	notes, err := Verify(snap)
	if err != nil || len(notes) != 1 || notes[0] != "snapshots/good was recorded again after accept on 2026-10-28; the pack it replaced is kept in evidence/"+before {
		t.Errorf("Verify(snapshot) = %q, %v", notes, err)
	}
	notes, err = Verify(runs[1])
	if err != nil {
		t.Fatalf("Verify(later run) = %v", err)
	}
	if want := "snapshots/good changed by accept after runs/r1 was chained to it; checked the copy kept in evidence/" + before; !slices.Contains(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}

	// A kept copy that was changed fails like the run would.
	write(t, filepath.Join(root, KeptDir, before), "keyboard/p1.json", "{}")
	if _, err := Verify(runs[1]); err == nil || err.Error() != "evidence: evidence/"+before+"/keyboard/p1.json changed after it was recorded (sha256 differs)" {
		t.Errorf("changed kept copy: %v", err)
	}
	write(t, filepath.Join(root, KeptDir, before), File, "{}")
	if _, err := Verify(runs[1]); err == nil || !strings.Contains(err.Error(), "changed after it was kept") {
		t.Errorf("changed kept pack: %v", err)
	}
}

// A snapshot deleted or taken again under its name leaves later chains
// whole when its pack was kept first. They break when it wasn't.
func TestKeepBeforeDelete(t *testing.T) {
	root, snap, runs := keptChain(t)
	sha := sum(must(os.ReadFile(filepath.Join(snap, File))))
	if _, err := Keep(snap); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(snap); err != nil {
		t.Fatal(err)
	}
	notes, err := Verify(runs[1])
	if err != nil || len(notes) != 1 || notes[0] != "snapshots/good was deleted or taken again after runs/r1 was chained to it; checked the copy kept in evidence/"+sha {
		t.Errorf("Verify = %q, %v", notes, err)
	}
	if err := os.RemoveAll(filepath.Join(root, KeptDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(runs[1]); err == nil || err.Error() != "evidence: the run before runs/r1 is missing from the chain" {
		t.Errorf("nothing kept: %v", err)
	}
	if rel, err := Keep(filepath.Join(root, "runs", "none")); err != nil || rel != "" {
		t.Errorf("Keep of a folder without a pack = %q, %v", rel, err)
	}
}

// Rerecord drops a signature of the old pack, which the kept copy
// keeps and which still verifies there.
func TestRerecordDropsTheSignature(t *testing.T) {
	_, snap, runs := keptChain(t)
	key := filepath.Join(t.TempDir(), "soundcheck.key")
	if _, err := Keygen(key); err != nil {
		t.Fatal(err)
	}
	if err := Sign(snap, key); err != nil {
		t.Fatal(err)
	}
	sha := sum(must(os.ReadFile(filepath.Join(snap, File))))
	if _, err := Keep(snap); err != nil {
		t.Fatal(err)
	}
	changeRun(t, snap)
	signed, err := Rerecord(snap, "accept")
	if err != nil || !signed {
		t.Fatalf("Rerecord = %v, %v", signed, err)
	}
	for _, f := range []string{SigFile, PubFile} {
		if _, err := os.Lstat(filepath.Join(snap, f)); err == nil {
			t.Errorf("%s is still there", f)
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(filepath.Dir(snap)), KeptDir, sha, f)); err != nil {
			t.Errorf("kept copy has no %s: %v", f, err)
		}
	}
	if _, err := Verify(runs[0]); err != nil {
		t.Errorf("Verify = %v", err)
	}
}

// A recorded-again pack matches evidence.v1.json.
func TestRerecordedPackMatchesSchema(t *testing.T) {
	data, err := model.Schemas.ReadFile("schema/evidence.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	_, snap, _ := keptChain(t)
	if _, err := Keep(snap); err != nil {
		t.Fatal(err)
	}
	if _, err := Rerecord(snap, "accept"); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(must(os.ReadFile(filepath.Join(snap, File))), &doc); err != nil {
		t.Fatal(err)
	}
	if p := validate(schema, doc); len(p) > 0 {
		t.Error(p)
	}
	if _, ok := doc["amends"]; !ok {
		t.Error("no amends")
	}
}
