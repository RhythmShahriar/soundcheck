// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package evidence writes the evidence pack of an --eaa run:
// eaa/evidence.json with the SHA-256 of every file the run recorded,
// chained to the previous --eaa run of the same site, so a later reader
// can tell the results weren't changed after the run. A pack can be
// signed with a key of the owner's (Sign, Keygen) and checked (Verify).
package evidence

import (
	"errors"

	"github.com/RhythmShahriar/soundcheck/model"
)

// ErrNotBuilt is what the parts of the package not written yet return.
var ErrNotBuilt = errors.New("not built yet")

// Ref points at an evidence pack: its run and the SHA-256 of its
// evidence.json.
type Ref struct {
	Run    string
	SHA256 string
}

// Write records the evidence pack of run in its folder dir, chained to
// the pack prev (nil for the first).
func Write(dir string, run *model.Run, prev *Ref) error { return ErrNotBuilt }

// Verify checks the evidence pack in dir against the files it lists.
func Verify(dir string) error { return ErrNotBuilt }

// Sign signs the evidence pack in dir with the private key in keyFile.
func Sign(dir, keyFile string) error { return ErrNotBuilt }

// Keygen writes a new signing key to path and returns its public key.
func Keygen(path string) (pub string, err error) { return "", ErrNotBuilt }

// Previous finds the evidence pack of the last --eaa run of target in
// the store at storeRoot, for Write to chain to. It is nil when there
// is none.
func Previous(storeRoot, target string) (*Ref, error) { return nil, nil }
