// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
	verifyEvidence = func(dir string) ([]string, error) {
		data, err := os.ReadFile(filepath.Join(dir, eaaDir, evidenceFile))
		if err != nil {
			return nil, err
		}
		var p fakePack
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		for name, sum := range p.Files {
			got, err := os.ReadFile(filepath.Join(dir, name))
			if h := sha256.Sum256(got); err != nil || hex.EncodeToString(h[:]) != sum {
				return nil, fmt.Errorf("evidence: %s changed after it was recorded (sha256 differs)", name)
			}
		}
		return nil, nil
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

	// A recorded file that can't be read is exit 3, not a mismatch.
	verify := verifyEvidence
	verifyEvidence = func(string) ([]string, error) {
		return nil, &fs.PathError{Op: "open", Path: filepath.Join(dir, "keyboard", "p1.json"), Err: fs.ErrPermission}
	}
	code, _, errOut = runCLI(t, "evidence", "verify", dir)
	if code != exitError || !strings.Contains(errOut, "permission denied") {
		t.Errorf("unreadable: exit %d %q", code, errOut)
	}
	verifyEvidence = verify

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

// With the evidence package itself.
func TestEvidenceCommandReal(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "")
	dir := storeRun(t, time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), model.Options{EAA: true, WCAG: "2.1"})
	run, err := store.LoadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePack(dir, run, nil, evidenceExtra{Chrome: "153.0.8010.52"}); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runCLI(t, "evidence", "verify"); code != exitOK || !strings.Contains(out, "not signed") {
		t.Errorf("verify unsigned: %d %q %s", code, out, errOut)
	}
	code, out, errOut := runCLI(t, "evidence", "keygen", "-o", "sign.key")
	if code != exitOK {
		t.Fatalf("keygen: %d %s", code, errOut)
	}
	pub := strings.TrimSpace(strings.TrimPrefix(out, "public key: "))
	if code, out, errOut := runCLI(t, "evidence", "verify", "--pub", pub); code != exitFindings || !strings.Contains(errOut, "isn't signed") {
		t.Errorf("--pub on an unsigned pack: %d %q %s", code, out, errOut)
	}
	if code, _, errOut := runCLI(t, "evidence", "sign", "--key", "sign.key"); code != exitOK {
		t.Errorf("sign: %d %s", code, errOut)
	}
	if code, out, errOut := runCLI(t, "evidence", "verify"); code != exitOK || !strings.Contains(out, "signed with key "+pub+";") {
		t.Errorf("verify: %d %q %s", code, out, errOut)
	}
	if code, out, errOut := runCLI(t, "evidence", "verify", "--pub", pub); code != exitOK || !strings.Contains(out, "the --pub key") {
		t.Errorf("verify --pub: %d %q %s", code, out, errOut)
	}
	_, other, _ := runCLI(t, "evidence", "keygen", "-o", "other.key")
	otherPub := strings.TrimSpace(strings.TrimPrefix(other, "public key: "))
	if code, _, errOut := runCLI(t, "evidence", "verify", "--pub", otherPub); code != exitFindings || !strings.Contains(errOut, "not the --pub key") {
		t.Errorf("verify --pub with another key: %d %s", code, errOut)
	}
	if code, _, _ := runCLI(t, "evidence", "verify", "--pub", "not-a-key"); code != exitUsage {
		t.Errorf("--pub not a key: exit %d", code)
	}
	// A key that is base64 but the wrong length, or has a stray
	// character, says which.
	if code, _, errOut := runCLI(t, "evidence", "verify", "--pub", base64.StdEncoding.EncodeToString(make([]byte, 31))); code != exitUsage ||
		!strings.Contains(errOut, "isn't a 32-byte Ed25519 public key (decoded 31 bytes)") {
		t.Errorf("short key: exit %d, %q", code, errOut)
	}
	if code, _, errOut := runCLI(t, "evidence", "verify", "--pub", "AAAA-"+strings.Repeat("A", 39)); code != exitUsage ||
		!strings.Contains(errOut, "isn't standard base64 (it goes wrong at character 5)") {
		t.Errorf("stray character: exit %d, %q", code, errOut)
	}
	if code, _, _ := runCLI(t, "evidence", "sign", "--key", "sign.key", "--pub", pub); code != exitUsage {
		t.Errorf("--pub with sign: exit %d", code)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCLI(t, "evidence", "verify", dir); code != exitFindings || !strings.Contains(errOut, "run.json changed after it was recorded") {
		t.Errorf("tampered: %d %s", code, errOut)
	}
}

// A pack of a run made with -k records it through the real package, so
// sign refuses it.
func TestEvidenceRefusesInsecurePack(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "")
	dir := storeRun(t, time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), model.Options{EAA: true, WCAG: "2.1"})
	run, err := store.LoadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePack(dir, run, nil, evidenceExtra{Insecure: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, eaaDir, evidenceFile))
	if err != nil || !strings.Contains(string(data), `"insecure": true`) && !strings.Contains(string(data), `"insecure":true`) {
		t.Fatalf("pack doesn't record -k: %v %s", err, data)
	}
	if code, _, errOut := runCLI(t, "evidence", "keygen", "-o", "sign.key"); code != exitOK {
		t.Fatalf("keygen: %d %s", code, errOut)
	}
	if code, _, errOut := runCLI(t, "evidence", "sign", "--key", "sign.key"); code != exitUsage || !strings.Contains(errOut, "made with -k") {
		t.Errorf("sign of a -k pack: %d %s", code, errOut)
	}
}

// evidence verify with no argument picks the newest pack by the start
// time the pack records, not by run.json, which is the file a pack
// vouches for. Editing the newest run's run.json or its pack's start
// time never lets verify pass over it in silence.
func TestEvidenceVerifyDefaultIgnoresRunJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "")
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	older := storeRun(t, at, model.Options{EAA: true, WCAG: "2.1"})
	if err := writePack(older, must(store.LoadRun(older)), nil, evidenceExtra{}); err != nil {
		t.Fatal(err)
	}
	newer := storeRun(t, at.Add(time.Hour), model.Options{EAA: true, WCAG: "2.1"})
	prev := &evidence.Ref{Run: "runs/" + filepath.Base(older), SHA256: sha(must(os.ReadFile(filepath.Join(older, eaaDir, evidenceFile))))}
	if err := writePack(newer, must(store.LoadRun(newer)), prev, evidenceExtra{}); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runCLI(t, "evidence", "verify"); code != exitOK || !strings.Contains(slashes(out), slashes(newer)) || errOut != "" {
		t.Fatalf("clean: exit %d %q %q", code, out, errOut)
	}

	// The newest run's run.json says it wasn't an --eaa run.
	runJSON := filepath.Join(newer, "run.json")
	orig := must(os.ReadFile(runJSON))
	edited := strings.Replace(string(orig), `"eaa": true`, `"eaa": false`, 1)
	edited = strings.Replace(edited, `"eaa":true`, `"eaa":false`, 1)
	if edited == string(orig) {
		t.Fatalf("run.json has no eaa option:\n%s", orig)
	}
	if err := os.WriteFile(runJSON, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, "evidence", "verify")
	if code != exitFindings || strings.Contains(out, "Verified") || !strings.Contains(errOut, "run.json changed after it was recorded") {
		t.Errorf("run.json edited: exit %d %q %q", code, out, errOut)
	}
	if err := os.WriteFile(runJSON, orig, 0o600); err != nil {
		t.Fatal(err)
	}

	// The newest pack claims an older start: the older run is chosen,
	// but the newer one is named and verify fails.
	packFile := filepath.Join(newer, eaaDir, evidenceFile)
	var pack map[string]any
	if err := json.Unmarshal(must(os.ReadFile(packFile)), &pack); err != nil {
		t.Fatal(err)
	}
	pack["started"] = at.Add(-time.Hour)
	if err := os.WriteFile(packFile, must(json.Marshal(pack)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runCLI(t, "evidence", "verify")
	if code != exitFindings || !strings.Contains(slashes(errOut), slashes(newer)+": run.json doesn't match its evidence pack") ||
		!strings.Contains(errOut, "verify passed over 1 newer run") {
		t.Errorf("pack start edited: exit %d %q %q", code, out, errOut)
	}
}
