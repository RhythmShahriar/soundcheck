// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/evidence"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// fakePack is the part of an evidence pack the fake evidence package
// reads: the files it vouches for and whether the run skipped TLS.
type fakePack struct {
	Files    map[string]string `json:"files"` // path in the run folder -> sha256
	Insecure bool              `json:"insecure"`
}

// fakeEvidence stands in for the evidence package: verify checks the
// files' hashes, sign writes eaa/evidence.sig and keygen a key file.
func fakeEvidence(t *testing.T) {
	t.Helper()
	oldV, oldS, oldK := verifyEvidence, signEvidence, keygenEvidence
	t.Cleanup(func() { verifyEvidence, signEvidence, keygenEvidence = oldV, oldS, oldK })
	verifyEvidence = func(dir string) error {
		data, err := os.ReadFile(filepath.Join(dir, eaaDir, evidenceFile))
		if err != nil {
			return err
		}
		var p fakePack
		if err := json.Unmarshal(data, &p); err != nil {
			return err
		}
		for name, sum := range p.Files {
			got, err := os.ReadFile(filepath.Join(dir, name))
			if h := sha256.Sum256(got); err != nil || hex.EncodeToString(h[:]) != sum {
				return fmt.Errorf("evidence: %s changed after it was recorded (sha256 differs)", name)
			}
		}
		return nil
	}
	signEvidence = func(dir, key string) error {
		if _, err := os.ReadFile(key); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, eaaDir, "evidence.sig"), []byte("sig"), 0o600)
	}
	keygenEvidence = func(path string) (string, error) {
		return "MCowBQYDK2VwAyEA" + strings.Repeat("A", 8), os.WriteFile(path, []byte("private"), 0o600)
	}
}

// packRun stores an --eaa run with a keyboard trace and its evidence
// pack.
func packRun(t *testing.T, at time.Time, insecure bool) string {
	t.Helper()
	dir := storeRun(t, at, model.Options{EAA: true, WCAG: "2.1"})
	trace := filepath.Join("keyboard", "p1.json")
	if err := os.MkdirAll(filepath.Join(dir, "keyboard"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"schema":"rhythm-soundcheck/keyboard-trace/v1","page":"/"}`)
	if err := os.WriteFile(filepath.Join(dir, trace), body, 0o600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(body)
	data, _ := json.Marshal(fakePack{Files: map[string]string{filepath.ToSlash(trace): hex.EncodeToString(h[:])}, Insecure: insecure})
	if err := writePrivate(filepath.Join(dir, eaaDir, evidenceFile), func(w io.Writer) error { _, err := w.Write(data); return err }); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEvidenceCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "")
	fakeEvidence(t)
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	dir := packRun(t, now, false)

	code, out, errOut := runCLI(t, "evidence", "keygen", "-o", "sign.key")
	if code != exitOK || out != "public key: MCowBQYDK2VwAyEAAAAAAAAA\n" || !strings.Contains(errOut, "Private key written to sign.key") {
		t.Fatalf("keygen: exit %d %q %q", code, out, errOut)
	}
	if code, _, errOut := runCLI(t, "evidence", "keygen", "-o", "sign.key"); code != exitUsage ||
		errOut != "rhythm-soundcheck: sign.key already exists; keygen never replaces a key, so name a new file\n" {
		t.Errorf("keygen over a key: exit %d %q", code, errOut)
	}
	code, out, errOut = runCLI(t, "evidence", "sign", "--key", "sign.key")
	if code != exitOK || slashes(out) != "Signed "+slashes(filepath.Join(dir, eaaDir, evidenceFile))+"\n" {
		t.Errorf("sign: exit %d %q %q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, eaaDir, "evidence.sig")); err != nil {
		t.Error(err)
	}
	if code, out, errOut := runCLI(t, "evidence", "verify"); code != exitOK || !strings.HasPrefix(out, "Verified ") {
		t.Errorf("verify: exit %d %q %q", code, out, errOut)
	}

	// A trace changed after the run: exit 1, naming the file.
	if err := os.WriteFile(filepath.Join(dir, "keyboard", "p1.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runCLI(t, "evidence", "verify", dir)
	if code != exitFindings || errOut != "rhythm-soundcheck: evidence: keyboard/p1.json changed after it was recorded (sha256 differs)\n" {
		t.Errorf("tampered: exit %d %q", code, errOut)
	}

	// A run made with -k can't be signed.
	insecure := packRun(t, now.Add(time.Minute), true)
	if code, _, errOut := runCLI(t, "evidence", "sign", insecure, "--key", "sign.key"); code != exitUsage ||
		errOut != "rhythm-soundcheck: can't sign a run made with -k: TLS wasn't verified\n" {
		t.Errorf("sign -k run: exit %d %q", code, errOut)
	}

	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"evidence", "sign"}, exitUsage, "evidence sign needs --key <file>; make one with rhythm-soundcheck evidence keygen -o <file>"},
		{[]string{"evidence", "keygen"}, exitUsage, "evidence keygen needs -o <file> for the private key"},
		{[]string{"evidence", "check"}, exitUsage, "evidence check: want verify, sign or keygen"},
		{[]string{"evidence", "verify", "--key", "sign.key"}, exitUsage, "--key goes with evidence sign"},
		{[]string{"evidence", "verify", "-o", "x"}, exitUsage, "-o goes with evidence keygen"},
		{[]string{"evidence", "verify", "nosuch"}, exitUsage, `no snapshot named "nosuch"; see rhythm-soundcheck snapshots`},
	} {
		code, _, errOut := runCLI(t, tc.args...)
		if code != tc.code || errOut != "rhythm-soundcheck: "+tc.want+"\n" {
			t.Errorf("%v: exit %d %q\nwant %q", tc.args, code, errOut, tc.want)
		}
	}
	if code, _, errOut := runCLI(t, "evidence", "sign", dir, "--key", "nosuch.key"); code != exitError || !strings.HasPrefix(errOut, "rhythm-soundcheck: --key: ") {
		t.Errorf("missing key file: exit %d %q", code, errOut)
	}
	// A run without a pack can't be read.
	bare := storeRun(t, now.Add(2*time.Minute), model.Options{EAA: true, WCAG: "2.1"})
	code, _, errOut = runCLI(t, "evidence", "verify", bare)
	if want := "rhythm-soundcheck: " + bare + " has no evidence pack (eaa/evidence.json); only --eaa runs write one\n"; code != exitError || slashes(errOut) != slashes(want) {
		t.Errorf("no pack: exit %d %q", code, errOut)
	}
	if code, out, _ := runCLI(t, "evidence", "--help"); code != exitOK || !strings.Contains(out, "evidence keygen -o <file>") {
		t.Errorf("--help: exit %d %q", code, out)
	}
	if code, _, _ := runCLI(t, "evidence"); code != exitUsage {
		t.Errorf("no subcommand: exit %d", code)
	}
}

// With the evidence package itself (pending until it is built).
func TestEvidenceCommandReal(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := evidence.Keygen(filepath.Join(t.TempDir(), "k")); errors.Is(err, evidence.ErrNotBuilt) {
		t.Skip("pending: statement (evidence)")
	}
	writeConfig(t, "")
	dir := storeRun(t, time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), model.Options{EAA: true, WCAG: "2.1"})
	run, err := store.LoadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := evidence.Write(dir, run, nil); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCLI(t, "evidence", "keygen", "-o", "sign.key"); code != exitOK {
		t.Fatalf("keygen: %d %s", code, errOut)
	}
	if code, _, errOut := runCLI(t, "evidence", "sign", "--key", "sign.key"); code != exitOK {
		t.Errorf("sign: %d %s", code, errOut)
	}
	if code, _, errOut := runCLI(t, "evidence", "verify"); code != exitOK {
		t.Errorf("verify: %d %s", code, errOut)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCLI(t, "evidence", "verify", dir); code != exitFindings || !strings.Contains(errOut, "run.json changed after it was recorded") {
		t.Errorf("tampered: %d %s", code, errOut)
	}
}
