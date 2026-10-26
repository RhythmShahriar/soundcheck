// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeNpm stands in for npm: the registry's answers come from the
// environment, so the script runs offline.
const fakeNpm = `#!/usr/bin/env bash
case "$1 $3" in
"view dist.integrity") echo "$FAKE_INTEGRITY" ;;
"view dist.tarball") echo "https://registry.npmjs.org/axe-core/-/axe-core-$FAKE_VERSION.tgz" ;;
esac
case "$1" in
init) echo '{}' > package.json ;;
install) printf '{"packages":{"node_modules/axe-core":{"integrity":"%s"}}}' "$FAKE_LOCKED" > package-lock.json ;;
audit) printf '%b' "$FAKE_AUDIT" ;;
esac
`

// fakeCurl "downloads" FAKE_TGZ to the -o file.
const fakeCurl = `#!/usr/bin/env bash
while [ $# -gt 0 ]; do
  [ "$1" = -o ] && cp "$FAKE_TGZ" "$2"
  shift
done
`

// tgz is an npm tarball holding an axe.min.js of version and its
// licenses.
func tgz(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"package/axe.min.js":            "/*! axe v" + version + "\n * fake\n */\nwindow.axe = {};\n",
		"package/LICENSE":               "Mozilla Public License, version 2.0\n",
		"package/LICENSE-3RD-PARTY.txt": "none\n",
		"package/package.json":          `{"name":"axe-core"}`,
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sri(data []byte) string {
	sum := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

// updateRepo is a copy of the files update-axe.sh reads and writes.
func updateRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, f := range []struct{ to, from string }{
		{"checks/a11y/vendor/axe.min.js", "vendor/axe.min.js"},
		{"checks/a11y/vendor/LICENSE", "vendor/LICENSE"},
		{"checks/a11y/vendor/LICENSE-3RD-PARTY.txt", "vendor/LICENSE-3RD-PARTY.txt"},
		{"sboms/axe-core.cdx.json", "../../sboms/axe-core.cdx.json"},
		{"checks/a11y/a11y.go", "a11y.go"},
		{"checks/a11y/a11y_test.go", "a11y_test.go"},
	} {
		data, err := os.ReadFile(f.from)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(repo, filepath.FromSlash(f.to))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// The update script installs a release only when its tarball matches
// npm's integrity and npm vouches for its signature and provenance.
// Then the vendored file, the version, the test's hash and the SBOM all
// move together.
func TestUpdateAxeScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a bash script")
	}
	for _, tool := range []string{"bash", "node", "openssl", "tar", "sed"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool, " isn't installed")
		}
	}
	script, err := filepath.Abs("../../scripts/update-axe.sh")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for name, body := range map[string]string{"npm": fakeNpm, "curl": fakeCurl} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil { //nolint:gosec // a fake tool the script runs
			t.Fatal(err)
		}
	}
	good := tgz(t, "4.15.0")
	tgzPath := filepath.Join(bin, "axe-core.tgz")
	if err := os.WriteFile(tgzPath, good, 0o600); err != nil {
		t.Fatal(err)
	}
	verified := "audited 1 package in 0s\\n\\n1 package has a verified registry signature\\n\\n1 package has a verified attestation\\n"
	run := func(t *testing.T, version string, env ...string) (string, string, error) {
		t.Helper()
		repo := updateRepo(t)
		cmd := exec.Command("bash", script, version)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GITHUB_ACTOR=",
			"FAKE_VERSION=4.15.0", "FAKE_TGZ="+tgzPath, "FAKE_INTEGRITY="+sri(good), "FAKE_LOCKED="+sri(good), "FAKE_AUDIT="+verified)
		cmd.Env = append(cmd.Env, env...)
		out, err := cmd.CombinedOutput()
		return repo, string(out), err
	}

	t.Run("verified", func(t *testing.T) {
		repo, out, err := run(t, "4.15.0")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		js, _ := os.ReadFile(filepath.Join(repo, "checks/a11y/vendor/axe.min.js"))
		if !strings.HasPrefix(string(js), "/*! axe v4.15.0\n") {
			t.Errorf("vendored file = %.40q", js)
		}
		sum := sha256.Sum256(js)
		digest := hex.EncodeToString(sum[:])
		src, _ := os.ReadFile(filepath.Join(repo, "checks/a11y/a11y.go"))
		if !strings.Contains(string(src), "const axeVersion = \"4.15.0\"\n") {
			t.Error("a11y.go keeps the old axeVersion")
		}
		test, _ := os.ReadFile(filepath.Join(repo, "checks/a11y/a11y_test.go"))
		if !strings.Contains(string(test), "const axeSHA256 = \""+digest+"\"\n") || !strings.Contains(string(test), "axe-core 4.15.0 npm") {
			t.Error("a11y_test.go keeps the old hash or version")
		}
		var bom cdx
		data, _ := os.ReadFile(filepath.Join(repo, "sboms/axe-core.cdx.json"))
		if err := json.Unmarshal(data, &bom); err != nil {
			t.Fatal(err)
		}
		if c := bom.Components[0]; c.Version != "4.15.0" || c.Purl != "pkg:npm/axe-core@4.15.0" || c.Hashes[0].Content != digest {
			t.Errorf("SBOM component = %+v", c)
		}
		if strings.Contains(string(data), "4.14.0") {
			t.Error("the SBOM still names 4.14.0")
		}
		for _, want := range []string{"axe-core 4.15.0 from https://registry.npmjs.org/axe-core/-/axe-core-4.15.0.tgz", "integrity: " + sri(good), "provenance: verified"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})

	for _, c := range []struct {
		name, version, want string
		env                 []string
	}{
		{"tampered tarball", "4.15.0", "the tarball's integrity is", []string{"FAKE_INTEGRITY=" + sri([]byte("other"))}},
		{"no provenance", "4.15.0", "no verified provenance attestation", []string{"FAKE_AUDIT=1 package has a verified registry signature\\n"}},
		{"npm installed another tarball", "4.15.0", "npm installed", []string{"FAKE_LOCKED=" + sri([]byte("other"))}},
		{"dependabot", "4.15.0", "never by Dependabot", []string{"GITHUB_ACTOR=dependabot[bot]"}},
		{"not a version", "latest", "usage: scripts/update-axe.sh <version>", nil},
		{"header of another version", "4.16.0", "doesn't start with /*! axe v4.16.0", []string{"FAKE_VERSION=4.16.0"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo, out, err := run(t, c.version, c.env...)
			if err == nil || !strings.Contains(out, c.want) {
				t.Fatalf("err %v, output:\n%s", err, out)
			}
			js, _ := os.ReadFile(filepath.Join(repo, "checks/a11y/vendor/axe.min.js"))
			if string(js) != axeSource {
				t.Error("the vendored file changed")
			}
		})
	}
}
