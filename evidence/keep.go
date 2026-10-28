// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/RhythmShahriar/soundcheck/store"
)

// KeptDir is the folder of the store where packs are kept with the
// files they list, each in a folder named by the SHA-256 of its
// evidence.json, when the run they belong to changes or goes: an accept
// into a snapshot, a snapshot taken again under its name or deleted.
// Later packs are chained to those packs as they were, so Verify checks
// a link against the kept copy when the run itself no longer matches.
const KeptDir = "evidence"

// Keep copies the evidence pack of the run folder dir and every file it
// lists to <store>/evidence/<sha256>/, before dir is changed or deleted.
// It returns that folder relative to the store, such as
// evidence/3f2a…, or "" when dir has no pack. A pack kept already is
// left as it is.
func Keep(dir string) (string, error) {
	data, p, err := readPack(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	sha := sum(data)
	rel := KeptDir + "/" + sha
	root := storeRoot(dir)
	final := filepath.Join(root, KeptDir, sha)
	if _, err := os.Lstat(filepath.Join(final, filepath.FromSlash(File))); err == nil {
		return rel, nil
	}
	if err := os.MkdirAll(filepath.Join(root, KeptDir), 0o700); err != nil {
		return "", err
	}
	if err := noSymlink(root, KeptDir); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Join(root, KeptDir), ".keep-*")
	if err != nil {
		return "", err
	}
	files := []string{File, SigFile, PubFile}
	for _, f := range p.Files {
		files = append(files, f.Path)
	}
	for _, f := range files {
		err := copyFile(dir, tmp, f)
		if errors.Is(err, fs.ErrNotExist) && (f == SigFile || f == PubFile) {
			continue
		}
		if err != nil {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("evidence: keeping %s: %w", f, err)
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.RemoveAll(tmp)
		if _, serr := os.Lstat(filepath.Join(final, filepath.FromSlash(File))); serr == nil {
			return rel, nil // kept by another process meanwhile
		}
		return "", err
	}
	return rel, nil
}

// copyFile copies the regular file rel of dir to the same place in to.
func copyFile(dir, to, rel string) error {
	src := filepath.Join(dir, filepath.FromSlash(rel))
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s isn't a regular file", rel)
	}
	in, err := os.Open(src) //nolint:gosec // a file the pack lists, checked by Decode
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	dst := filepath.Join(to, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a new file in our own folder
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// now is time.Now; tests replace it.
var now = time.Now

// Rerecord writes the pack of dir again after reason (such as "accept")
// changed the files it lists. Call Keep first: the new pack names the one
// it replaces in Amends. Rerecord refuses when that one isn't kept.
// The new pack keeps the chain link, browser version and -k setting of
// the old one. A signature of the old pack doesn't sign the new one, so
// it is removed (the kept copy has it); signed reports whether there was
// one, to be signed again.
func Rerecord(dir, reason string) (signed bool, err error) {
	data, old, err := readPack(dir)
	if err != nil {
		return false, err
	}
	sha := sum(data)
	if _, err := os.Lstat(filepath.Join(storeRoot(dir), KeptDir, sha, filepath.FromSlash(File))); err != nil {
		return false, fmt.Errorf("evidence: the pack of %s isn't kept; keep it before changing the run", runID(dir))
	}
	run, err := store.LoadRun(dir)
	if err != nil {
		return false, fmt.Errorf("evidence: %w", err)
	}
	amends := &Amendment{SHA256: sha, Reason: reason, At: now().UTC().Truncate(time.Second)}
	if err := writePack(dir, run, old.Previous, Extra{Chrome: old.Chrome, Insecure: old.Insecure}, amends); err != nil {
		return false, err
	}
	for _, rel := range []string{SigFile, PubFile} {
		err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel)))
		switch {
		case err == nil:
			signed = true
		case !errors.Is(err, fs.ErrNotExist):
			return signed, err
		}
	}
	return signed, nil
}

// kept reads the pack kept for sha, when there is one: its folder, the
// evidence.json bytes and the pack. The bytes must hash to sha.
func kept(root, sha string) (string, []byte, *Pack, error) {
	if b, err := hex.DecodeString(sha); err != nil || len(b) != 32 {
		return "", nil, nil, fs.ErrNotExist
	}
	dir := filepath.Join(root, KeptDir, sha)
	data, err := readRaw(dir)
	if err != nil {
		return "", nil, nil, err
	}
	if sum(data) != sha {
		return "", nil, nil, fmt.Errorf("evidence: %s/%s/%s changed after it was kept (sha256 differs)", KeptDir, sha, File)
	}
	p, err := Decode(data)
	if err != nil {
		return "", nil, nil, err
	}
	return dir, data, p, nil
}

// short is a SHA-256 cut to the 12 characters a note shows.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// keptName is how notes and errors name a kept pack's folder.
func keptName(sha string) string { return path.Join(KeptDir, sha) }

// amendNote describes the amendment of a pack Verify checked, when it
// has one. It checks the pack it replaced is kept as it was.
func amendNote(root, id string, p *Pack) (string, error) {
	a := p.Amends
	if a == nil {
		return "", nil
	}
	_, _, _, err := kept(root, a.SHA256)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("%s was recorded again after %s on %s; the pack it replaced (%s) isn't kept", id, a.Reason, a.At.Format("2006-01-02"), short(a.SHA256)), nil
	case err != nil:
		return "", err
	}
	return fmt.Sprintf("%s was recorded again after %s on %s; the pack it replaced is kept in %s", id, a.Reason, a.At.Format("2006-01-02"), keptName(a.SHA256)), nil
}
