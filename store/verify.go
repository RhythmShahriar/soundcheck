// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
)

// pngMagic is how every PNG file starts.
var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// maxListed is how many broken screenshots the error names.
const maxListed = 3

// verifyShots checks that every screenshot run.json in dir refers to is
// there: a local path inside dir to a file that starts like a PNG. A
// full disk or a crashed writer leaves a run that names files it never
// wrote, and promoting it would make later compares skip visual checks
// on those pages without a word.
func verifyShots(dir string) error {
	r, err := LoadRun(dir)
	if err != nil {
		return err
	}
	var broken []string
	for _, p := range r.Pages {
		for _, vp := range slices.Sorted(maps.Keys(p.Shots)) {
			for _, f := range []string{p.Shots[vp].File, p.Shots[vp].Second} {
				if f == "" {
					continue
				}
				if why := checkShot(dir, f); why != "" {
					broken = append(broken, f+" "+why)
				}
			}
		}
	}
	if len(broken) == 0 {
		return nil
	}
	n := len(broken)
	if n > maxListed {
		broken = append(broken[:maxListed], fmt.Sprintf("%d more", n-maxListed))
	}
	noun := "screenshots"
	if n == 1 {
		noun = "screenshot"
	}
	return fmt.Errorf("snapshot not saved: %d %s could not be written (%s)", n, noun, strings.Join(broken, ", "))
}

// checkShot says what's wrong with the screenshot at the run-relative
// path name, or returns "" when it's fine.
func checkShot(dir, name string) string {
	path, err := Inside(dir, name)
	if err != nil {
		return "is outside the snapshot"
	}
	f, err := os.Open(path) //nolint:gosec // a local path inside our own folder, checked above
	if errors.Is(err, fs.ErrNotExist) {
		return "is missing"
	}
	if err != nil {
		return "can't be read"
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, len(pngMagic))
	if _, err := io.ReadFull(f, head); err != nil || !bytes.Equal(head, pngMagic) {
		return "isn't a PNG"
	}
	return ""
}
