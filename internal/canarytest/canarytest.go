// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package canarytest makes canaries for tests: values a run must never
// write anywhere. Each is assembled at runtime from a random part, so no
// file in the repository holds one and a hit can't come from anywhere
// but the run.
package canarytest

import (
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Canaries maps a canary's name to its value.
type Canaries map[string]string

// New makes one canary for each name: "cnry", the name without dashes
// and 16 random hex digits.
func New(t testing.TB, names ...string) Canaries {
	t.Helper()
	b := make([]byte, 8)
	c := Canaries{}
	for _, n := range names {
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		c[n] = "cnry" + strings.ReplaceAll(n, "-", "") + hex.EncodeToString(b)
	}
	return c
}

// Sweep returns "file: name" for each canary found in a file under the
// roots or in the extra texts. Screenshots are left out: they show what
// the page showed and are tested on their own (S1, M5).
func (c Canaries) Sweep(t testing.TB, texts map[string]string, roots ...string) []string {
	t.Helper()
	var hits []string
	look := func(where, text string) {
		for _, n := range slices.Sorted(maps.Keys(c)) {
			if strings.Contains(text, c[n]) {
				hits = append(hits, where+": "+n)
			}
		}
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.EqualFold(filepath.Ext(path), ".png") {
				return err
			}
			data, err := os.ReadFile(path) //nolint:gosec // a file the test's own run wrote
			if err != nil {
				return err
			}
			look(filepath.ToSlash(path), string(data))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for where, text := range texts {
		look(where, text)
	}
	return hits
}
