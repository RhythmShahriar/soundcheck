// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

func TestSnapshotsJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	// No .soundcheck at all, then an empty snapshots folder.
	for range 2 {
		code, out, errOut := runCLI(t, "snapshots", "--json")
		if code != exitOK || out != "[]\n" || errOut != "" {
			t.Errorf("empty: exit %d %q %q", code, out, errOut)
		}
		_ = os.MkdirAll(".soundcheck/snapshots", 0o755)
	}

	east := time.FixedZone("AEST", 10*60*60)
	for i, r := range []*model.Run{
		{Schema: model.Schema, Version: "1.0.0", Target: "https://staging.shop.test", Name: "before", Started: time.Date(2026, 10, 24, 9, 30, 0, 0, east),
			Pages: []model.Page{{Path: "/"}, {Path: "/shop/"}}},
		{Schema: model.Schema, Version: "1.1.0", Target: "https://staging.shop.test", Name: "after", Started: time.Date(2026, 10, 24, 12, 0, 5, 0, time.UTC),
			Pages: []model.Page{{Path: "/"}}},
	} {
		dir := filepath.Join(".soundcheck/snapshots", r.Name)
		_ = os.MkdirAll(dir, 0o755)
		if err := store.SaveRun(dir, r); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			_ = os.WriteFile(".soundcheck/snapshots/LATEST", []byte(r.Name+"\n"), 0o600)
		}
	}
	_ = os.MkdirAll(".soundcheck/snapshots/broken", 0o755)
	_ = os.WriteFile(".soundcheck/snapshots/broken/run.json", []byte("{not json"), 0o600)

	code, out, errOut := runCLI(t, "snapshots", "--json")
	if code != exitOK || errOut != "" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	want := `[
  {
    "name": "after",
    "created": "2026-10-24T12:00:05Z",
    "site": "https://staging.shop.test",
    "pages": 1,
    "version": "1.1.0",
    "latest": false
  },
  {
    "name": "before",
    "created": "2026-10-23T23:30:00Z",
    "site": "https://staging.shop.test",
    "pages": 2,
    "version": "1.0.0",
    "latest": true
  },
  {
    "name": "broken",
    "pages": 0,
    "latest": false,
    "error": "`
	if !strings.HasPrefix(out, want) || !strings.HasSuffix(out, "\"\n  }\n]\n") {
		t.Errorf("got\n%s\nwant it to start\n%s", out, want)
	}

	if code, _, errOut := runCLI(t, "snapshots", "--jsn"); code != exitUsage ||
		errOut != "rhythm-soundcheck: unknown flag --jsn (did you mean --json?)\n" {
		t.Errorf("bad flag: exit %d %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "snapshots", "list"); code != exitUsage ||
		errOut != "rhythm-soundcheck: usage: rhythm-soundcheck snapshots [--json | delete <name>]\n" {
		t.Errorf("bad argument: exit %d %q", code, errOut)
	}
}
