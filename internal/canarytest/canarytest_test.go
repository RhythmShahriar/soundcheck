// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package canarytest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The sweep finds a canary in a file, in a nested folder or in printed
// text; it skips screenshots.
func TestSweepFinds(t *testing.T) {
	dir := t.TempDir()
	c := New(t, "a", "b", "c", "query")
	if err := os.MkdirAll(filepath.Join(dir, "x", "y"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"x/y/run.json": `{"v":"` + c["a"] + `"}`, "shot.png": c["b"], "clean.md": "nothing",
		"pages.jsonl": `{"links":["/a/?t=` + c["query"] + `"]}`, "report.md": "/a/?t=" + c["query"]} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := c.Sweep(t, map[string]string{"stderr": "note: " + c["c"]}, dir)
	want := []string{filepath.ToSlash(filepath.Join(dir, "x", "y", "run.json")) + ": a", "stderr: c",
		filepath.ToSlash(filepath.Join(dir, "report.md")) + ": query", filepath.ToSlash(filepath.Join(dir, "pages.jsonl")) + ": query"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("hits %q, want %q", got, want)
	}
	if c["a"] == c["b"] || !strings.HasPrefix(c["a"], "cnrya") {
		t.Errorf("canaries %q", c)
	}
}
