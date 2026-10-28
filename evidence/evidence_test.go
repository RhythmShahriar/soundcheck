// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

const target = "http://127.0.0.1:37064/"

// saveRun stores an --eaa run of target at root/kind/name with a
// keyboard trace and a focus crop, as the CLI leaves it before writing
// the pack.
func saveRun(t testing.TB, root, kind, name string, started time.Time, eaa bool, target string) (string, *model.Run) {
	t.Helper()
	dir := filepath.Join(root, kind, name)
	if err := os.MkdirAll(filepath.Join(dir, "keyboard"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := &model.Run{Tool: "rhythm-soundcheck", Version: "1.3.0", Target: target, Started: started, Duration: 1500,
		Options: model.Options{EAA: eaa, WCAG: "2.1", Modules: []string{"a11y", "keyboard"},
			A11y: &model.A11yRules{Axe: "4.14.0", AxeSHA256: strings.Repeat("ab", 32), Rules: []string{"image-alt"}}},
		Pages: []model.Page{
			{ID: "p1", Path: "/?session=abc", Audits: map[string]model.Audit{"a11y": {Status: model.AuditOK}, "keyboard": {Status: model.AuditOK}},
				Keyboard: &model.KeyboardSummary{Stops: 3, Evidence: "keyboard/p1.json"},
				Issues:   []model.Issue{{Check: "keyboard/trap", Title: "trap", Source: "#card"}}},
			{ID: "p2", Path: "/big/", Audits: map[string]model.Audit{"a11y": {Status: model.AuditTimeout}}},
		}}
	if err := store.SaveRun(dir, run); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "keyboard/p1.json", `{"forward":[{"selector":"a"}]}`)
	write(t, dir, "eaa/focus/p1-1.png", "\x89PNG\r\n\x1a\nfake")
	return dir, run
}

func write(t testing.TB, dir, rel, data string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readPackFile(t *testing.T, dir string) *Pack {
	t.Helper()
	_, p, err := readPack(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteAndVerify(t *testing.T) {
	root := t.TempDir()
	dir, run := saveRun(t, root, "snapshots", "eaa-1", time.Unix(1000, 0).UTC(), true, target)
	if err := WriteWith(dir, run, nil, Extra{Chrome: "153.0.8010.52", Insecure: true}); err != nil {
		t.Fatal(err)
	}
	p := readPackFile(t, dir)
	var paths []string
	for _, f := range p.Files {
		paths = append(paths, f.Path)
	}
	if !slices.Equal(paths, []string{"eaa/focus/p1-1.png", "keyboard/p1.json", "pages.jsonl", "run.json"}) {
		t.Errorf("files = %v", paths)
	}
	if p.Schema != Schema || p.Run != "snapshots/eaa-1" || p.Version != "1.3.0" || p.Axe != (Axe{"4.14.0", strings.Repeat("ab", 32)}) ||
		p.Chrome != "153.0.8010.52" || !p.Insecure || p.Interrupted || p.Previous != nil || !p.Options.EAA ||
		!p.Finished.Equal(p.Started.Add(1500*time.Millisecond)) || p.EAA.WCAG != "2.1" || p.EAA.EvidenceNote != model.EvidenceNote {
		t.Errorf("pack = %+v", p)
	}
	if len(p.Pages) != 2 || p.Pages[0].Path != "/" || p.Pages[1].Audits["a11y"] != model.AuditTimeout {
		t.Errorf("pages = %+v", p.Pages)
	}
	if _, err := Verify(dir); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, rel := range []string{"eaa", File} {
			fi, err := os.Stat(filepath.Join(dir, rel))
			if err != nil || (fi.IsDir() && fi.Mode().Perm() != 0o700) || (!fi.IsDir() && fi.Mode().Perm() != 0o600) {
				t.Errorf("%s: %v %v", rel, fi.Mode(), err)
			}
		}
	}

	// A run passed without its pages is read from the folder.
	head := *run
	head.Pages = nil
	if err := Write(dir, &head, nil); err != nil {
		t.Fatal(err)
	}
	if p := readPackFile(t, dir); len(p.Pages) != 2 || p.Insecure {
		t.Errorf("streamed pages = %+v", p.Pages)
	}
}

// Every way a file can change after the run, Verify names the file.
func TestVerifyNamesTheFile(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(t *testing.T, dir string)
		want   string
	}{
		"trace byte": {func(t *testing.T, dir string) { write(t, dir, "keyboard/p1.json", `{"forward":[{"selector":"b"}]}`) },
			"evidence: keyboard/p1.json changed after it was recorded (sha256 differs)"},
		"run.json value": {func(t *testing.T, dir string) {
			rewriteRun(t, dir, func(m map[string]any) { m["target"] = "http://other.test/" })
		},
			"evidence: run.json changed after it was recorded (sha256 differs)"},
		"pages": {func(t *testing.T, dir string) { write(t, dir, "pages.jsonl", "{}\n") },
			"evidence: pages.jsonl changed after it was recorded (sha256 differs)"},
		"deleted crop": {func(t *testing.T, dir string) { _ = os.Remove(filepath.Join(dir, "eaa", "focus", "p1-1.png")) },
			"evidence: eaa/focus/p1-1.png was deleted after it was recorded"},
		"added trace": {func(t *testing.T, dir string) { write(t, dir, "keyboard/p9.json", "{}") },
			"evidence: keyboard/p9.json was added after the pack was recorded"},
	} {
		t.Run(name, func(t *testing.T) {
			dir, run := saveRun(t, t.TempDir(), "runs", "r1", time.Unix(1000, 0), true, target)
			if err := Write(dir, run, nil); err != nil {
				t.Fatal(err)
			}
			tc.change(t, dir)
			if _, err := Verify(dir); err == nil || err.Error() != tc.want {
				t.Errorf("Verify = %v, want %s", err, tc.want)
			}
		})
	}
	// run.json written again with other spacing and key order is the
	// same run.
	dir, run := saveRun(t, t.TempDir(), "runs", "r1", time.Unix(1000, 0), true, target)
	if err := Write(dir, run, nil); err != nil {
		t.Fatal(err)
	}
	rewriteRun(t, dir, func(map[string]any) {})
	if _, err := Verify(dir); err != nil {
		t.Errorf("reformatted run.json: %v", err)
	}
}

// rewriteRun decodes run.json, changes it and writes it back indented.
func rewriteRun(t *testing.T, dir string, change func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	change(m)
	out, _ := json.MarshalIndent(m, "", "    ")
	write(t, dir, "run.json", string(out))
}

// Three runs chain; Verify walks back to the first and reports a gap or
// a changed earlier pack.
func TestChain(t *testing.T) {
	chain := func(t *testing.T) (string, []string) {
		root := t.TempDir()
		var dirs []string
		var prev *Ref
		for i, name := range []string{"a", "b", "c"} {
			dir, run := saveRun(t, root, "runs", name, time.Unix(int64(1000+i), 0), true, target)
			if err := Write(dir, run, prev); err != nil {
				t.Fatal(err)
			}
			var err error
			if prev, err = Previous(root, target); err != nil || prev == nil || prev.Run != "runs/"+name {
				t.Fatalf("Previous after %s = %+v, %v", name, prev, err)
			}
			dirs = append(dirs, dir)
		}
		return root, dirs
	}
	_, dirs := chain(t)
	if _, err := Verify(dirs[2]); err != nil {
		t.Fatal(err)
	}
	if p := readPackFile(t, dirs[2]); p.Previous == nil || p.Previous.Run != "runs/b" {
		t.Errorf("previous = %+v", p.Previous)
	}

	_, dirs = chain(t)
	if err := os.RemoveAll(dirs[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dirs[2]); err == nil || err.Error() != "evidence: the run before runs/c is missing from the chain" {
		t.Errorf("gap: %v", err)
	}

	_, dirs = chain(t)
	write(t, dirs[0], File, strings.Replace(string(must(os.ReadFile(filepath.Join(dirs[0], File)))), `"interrupted": false`, `"interrupted": true`, 1))
	if _, err := Verify(dirs[2]); err == nil || err.Error() != "evidence: runs/a/eaa/evidence.json changed after it was recorded (sha256 differs)" {
		t.Errorf("changed first pack: %v", err)
	}

	_, dirs = chain(t)
	write(t, dirs[0], "keyboard/p1.json", "{}")
	if _, err := Verify(dirs[2]); err == nil || err.Error() != "evidence: runs/a/keyboard/p1.json changed after it was recorded (sha256 differs)" {
		t.Errorf("changed first trace: %v", err)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Previous picks the newest --eaa run of the same target with a pack,
// snapshots included; it skips the rest.
func TestPrevious(t *testing.T) {
	root := t.TempDir()
	if p, err := Previous(root, target); p != nil || err != nil {
		t.Errorf("empty store: %v, %v", p, err)
	}
	packed := func(kind, name string, sec int64, eaa bool, tgt string) {
		dir, run := saveRun(t, root, kind, name, time.Unix(sec, 0), eaa, tgt)
		if err := Write(dir, run, nil); err != nil {
			t.Fatal(err)
		}
	}
	packed("runs", "old", 1000, true, target)
	packed("snapshots", "eaa-1", 2000, true, strings.TrimSuffix(target, "/"))
	packed("runs", "plain", 3000, false, target)              // not --eaa
	packed("runs", "other", 4000, true, "http://other.test/") // another site
	saveRun(t, root, "runs", "current", time.Unix(5000, 0), true, target)
	p, err := Previous(root, target)
	if err != nil || p == nil || p.Run != "snapshots/eaa-1" {
		t.Fatalf("Previous = %+v, %v", p, err)
	}
	if want := sum(must(os.ReadFile(filepath.Join(root, "snapshots", "eaa-1", File)))); p.SHA256 != want {
		t.Errorf("sha = %s, want %s", p.SHA256, want)
	}

	// Writing the old run's pack again chains back, never forward.
	if p, err := PreviousBefore(root, target, time.Unix(2000, 0)); err != nil || p == nil || p.Run != "runs/old" {
		t.Errorf("PreviousBefore(eaa-1) = %+v, %v", p, err)
	}
	if p, err := PreviousBefore(root, target, time.Unix(1000, 0)); err != nil || p != nil {
		t.Errorf("PreviousBefore(old) = %+v, %v", p, err)
	}
}

func TestSign(t *testing.T) {
	dir, run := saveRun(t, t.TempDir(), "runs", "r1", time.Unix(1000, 0), true, target)
	if err := Write(dir, run, nil); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "soundcheck.key")
	pub, err := Keygen(key)
	if err != nil || len(pub) != 44 {
		t.Fatalf("Keygen = %q, %v", pub, err)
	}
	if fi, _ := os.Stat(key); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode %v", fi.Mode())
	}
	if _, err := Keygen(key); err == nil {
		t.Error("Keygen replaced a key")
	}
	if err := Sign(dir, key); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(must(os.ReadFile(filepath.Join(dir, PubFile))))); got != pub {
		t.Errorf("evidence.pub = %s, want %s", got, pub)
	}
	if _, err := Verify(dir); err != nil {
		t.Fatalf("signed pack: %v", err)
	}
	if got, err := SigningKey(dir); err != nil || got != pub {
		t.Errorf("SigningKey = %q, %v, want %s", got, err, pub)
	}
	// Any change to evidence.json, even one that keeps it valid, breaks
	// the signature.
	data := must(os.ReadFile(filepath.Join(dir, File)))
	write(t, dir, File, string(data)+"\n")
	if _, err := Verify(dir); err == nil || err.Error() != "evidence: eaa/evidence.sig doesn't match eaa/evidence.json (signature invalid)" {
		t.Errorf("tampered pack: %v", err)
	}
	write(t, dir, File, string(data))
	write(t, dir, SigFile, strings.Repeat("A", 86)+"==\n")
	if _, err := Verify(dir); err == nil || !strings.Contains(err.Error(), "signature invalid") {
		t.Errorf("tampered signature: %v", err)
	}
	_ = os.Remove(filepath.Join(dir, SigFile))
	if _, err := Verify(dir); err == nil || !strings.Contains(err.Error(), "without eaa/evidence.sig") {
		t.Errorf("signature removed: %v", err)
	}

	// A -k run is refused.
	if err := WriteWith(dir, run, nil, Extra{Insecure: true}); err != nil {
		t.Fatal(err)
	}
	if err := Sign(dir, key); !errors.Is(err, ErrInsecure) || err.Error() != "can't sign a run made with -k: TLS wasn't verified" {
		t.Errorf("Sign of a -k run = %v", err)
	}
	if err := Sign(dir, filepath.Join(dir, "run.json")); err == nil {
		t.Error("signed with a file that isn't a key")
	}
}

func TestKeygenRefusesALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	write(t, dir, "victim", "keep")
	link := filepath.Join(dir, "key")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Keygen(link); err == nil {
		t.Error("Keygen wrote through a link")
	}
	if string(must(os.ReadFile(victim))) != "keep" {
		t.Error("the link's target changed")
	}
	// Write refuses an eaa folder that is a link.
	rdir, run := saveRun(t, t.TempDir(), "runs", "r1", time.Unix(1000, 0), true, target)
	_ = os.RemoveAll(filepath.Join(rdir, "eaa"))
	if err := os.Symlink(dir, filepath.Join(rdir, "eaa")); err != nil {
		t.Fatal(err)
	}
	if err := Write(rdir, run, nil); err == nil {
		t.Error("Write followed the eaa link")
	}
}

// A crafted pack can't make Verify read files outside the run.
func TestDecodeRefuses(t *testing.T) {
	ok := `{"schema":"rhythm-soundcheck/evidence/v1","files":[{"path":"run.json","sha256":"` + strings.Repeat("0", 64) + `"}]}`
	if _, err := Decode([]byte(ok)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"schema":"rhythm-soundcheck/v1"}`,
		`{"schema":"rhythm-soundcheck/evidence/v1","unknown":1}`,
		`{"schema":"rhythm-soundcheck/evidence/v1","files":[{"path":"../../etc/passwd","sha256":"` + strings.Repeat("0", 64) + `"}]}`,
		`{"schema":"rhythm-soundcheck/evidence/v1","files":[{"path":"keyboard/../../x","sha256":"` + strings.Repeat("0", 64) + `"}]}`,
		`{"schema":"rhythm-soundcheck/evidence/v1","files":[{"path":"/etc/passwd","sha256":"` + strings.Repeat("0", 64) + `"}]}`,
		`{"schema":"rhythm-soundcheck/evidence/v1","files":[{"path":"report.html","sha256":"` + strings.Repeat("0", 64) + `"}]}`,
		`{"schema":"rhythm-soundcheck/evidence/v1","files":[{"path":"run.json","sha256":"zz"}]}`,
		`{"schema":"rhythm-soundcheck/evidence/v1","previous":{"run":"../x","sha256":""}}`,
		`{"schema":"rhythm-soundcheck/evidence/v1","previous":{"run":"runs/../../x","sha256":""}}`,
	} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("Decode accepted %s", bad)
		}
	}
}

// FuzzEvidence: decoding any evidence.json never panics and a pack it
// accepts lists only the run's own files.
func FuzzEvidence(f *testing.F) {
	dir, run := saveRun(f, f.TempDir(), "runs", "r1", time.Unix(1000, 0), true, target)
	if err := Write(dir, run, &Ref{Run: "runs/r0", SHA256: strings.Repeat("1", 64)}); err != nil {
		f.Fatal(err)
	}
	f.Add(must(os.ReadFile(filepath.Join(dir, File))))
	f.Add([]byte(`{"schema":"rhythm-soundcheck/evidence/v1","files":[{"path":"keyboard/a","sha256":"` + strings.Repeat("0", 64) + `"}]}`))
	f.Add([]byte(`{"schema":"rhythm-soundcheck/evidence/v1","pages":[{"audits":{"a":1}}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Decode(data)
		if err != nil {
			return
		}
		for _, file := range p.Files {
			if strings.Contains(file.Path, "..") || filepath.IsAbs(file.Path) || !recorded(file.Path) {
				t.Fatalf("accepted %q", file.Path)
			}
		}
		_ = verifyPack(dir, p, "")
	})
}

// SigningKey shows who signed: a pack re-signed with another key still
// verifies while its key differs from the owner's.
func TestSigningKey(t *testing.T) {
	dir, run := saveRun(t, t.TempDir(), "runs", "r1", time.Unix(1000, 0), true, target)
	if err := Write(dir, run, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := SigningKey(dir); got != "" || err != nil {
		t.Errorf("unsigned: %q, %v", got, err)
	}
	keys := t.TempDir()
	owner, _ := Keygen(filepath.Join(keys, "owner.key"))
	other, _ := Keygen(filepath.Join(keys, "other.key"))
	if err := Sign(dir, filepath.Join(keys, "owner.key")); err != nil {
		t.Fatal(err)
	}
	if err := Sign(dir, filepath.Join(keys, "other.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err != nil {
		t.Fatalf("re-signed: %v", err)
	}
	if got, _ := SigningKey(dir); got != other || got == owner {
		t.Errorf("SigningKey = %s, want the second key %s", got, other)
	}
	write(t, dir, PubFile, "c2hvcnQ=\n")
	if _, err := SigningKey(dir); err == nil {
		t.Error("a short key passed")
	}
}
