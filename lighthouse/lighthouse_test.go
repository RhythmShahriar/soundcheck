// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package lighthouse

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// fakeNode puts an npm and a node on PATH. npm records its arguments and
// folder and "installs" an unlighthouse-ci package; node records its
// arguments and, unless fail is set, writes the result file where
// --output-path points. The cache goes to a temporary folder.
func fakeNode(t *testing.T, fail bool) (logDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake node and npm are shell scripts")
	}
	bin := t.TempDir()
	logDir = t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	npm := `#!/bin/sh
printf '%s\n' "$@" >> "` + logDir + `/npm-args"
pwd >> "` + logDir + `/npm-dir"
mkdir -p node_modules/unlighthouse-ci/bin
echo '{"name":"unlighthouse-ci","bin":{"unlighthouse":"bin/unlighthouse.mjs","unlighthouse-ci":"bin/unlighthouse-ci.mjs"}}' > node_modules/unlighthouse-ci/package.json
echo "added 675 packages"
`
	node := `#!/bin/sh
printf '%s\n' "$@" > "` + logDir + `/node-args"
echo "CHROME_PATH=$CHROME_PATH" > "` + logDir + `/node-env"
echo "scanning..."
`
	if fail {
		node += "echo 'Error: site unreachable' >&2\nexit 1\n"
	} else {
		node += `out=""
while [ $# -gt 0 ]; do if [ "$1" = "--output-path" ]; then out="$2"; fi; shift; done
echo '{"summary":{"score":0.9}}' > "$out/ci-result.json"
`
	}
	for name, script := range map[string]string{"npm": npm, "node": node} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	return logDir
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestAvailable(t *testing.T) {
	fakeNode(t, false)
	if !Available() {
		t.Error("node and npm on PATH not found")
	}
	t.Setenv("PATH", t.TempDir())
	if Available() {
		t.Error("found node on an empty PATH")
	}
}

func TestRun(t *testing.T) {
	logDir := fakeNode(t, false)
	out := filepath.Join(t.TempDir(), "lighthouse")
	var progress strings.Builder
	var notices []string
	opts := Options{Output: &progress, Log: func(m string) { notices = append(notices, m) }, Chrome: "/opt/chrome"}
	if err := RunWith(context.Background(), "https://shop.example/", out, opts); err != nil {
		t.Fatal(err)
	}
	home, _ := Dir()
	if got := lines(t, filepath.Join(logDir, "npm-args")); !slices.Equal(got, []string{"ci", "--ignore-scripts", "--no-audit", "--no-fund"}) {
		t.Errorf("npm args = %q", got)
	}
	if got := lines(t, filepath.Join(logDir, "npm-dir")); len(got) != 1 || got[0] != home || !strings.Contains(home, filepath.Join("rhythm", "soundcheck", "lighthouse")) {
		t.Errorf("npm ran in %q, want the cache folder %s", got, home)
	}
	for _, f := range []string{"package.json", "package-lock.json"} {
		if _, err := os.Stat(filepath.Join(home, f)); err != nil {
			t.Error(err)
		}
	}
	got := lines(t, filepath.Join(logDir, "node-args"))
	want := []string{filepath.Join(home, "node_modules", "unlighthouse-ci", "bin", "unlighthouse-ci.mjs"), "--site", "https://shop.example/", "--reporter", "jsonExpanded", "--output-path", out}
	if !slices.Equal(got, want) {
		t.Errorf("node args = %q, want %q", got, want)
	}
	if env := lines(t, filepath.Join(logDir, "node-env")); env[0] != "CHROME_PATH=/opt/chrome" {
		t.Errorf("env = %q", env)
	}
	if _, err := os.Stat(filepath.Join(out, Result)); err != nil {
		t.Error(err)
	}
	if !strings.Contains(progress.String(), "scanning") || !strings.Contains(progress.String(), "added 675 packages") {
		t.Errorf("progress = %q", progress.String())
	}
	if len(notices) != 0 {
		t.Errorf("notices with nothing ignored: %q", notices)
	}

	// A second run reuses the install.
	if err := RunWith(context.Background(), "https://shop.example/", out, opts); err != nil {
		t.Fatal(err)
	}
	if got := lines(t, filepath.Join(logDir, "npm-dir")); len(got) != 1 {
		t.Errorf("npm ran %d times", len(got))
	}
}

func TestRunNamesIgnoredSettings(t *testing.T) {
	fakeNode(t, false)
	var notices []string
	opts := Options{Log: func(m string) { notices = append(notices, m) }, Proxy: "http://127.0.0.1:8080", Insecure: true, Headers: true}
	if err := RunWith(context.Background(), "https://shop.example/", t.TempDir(), opts); err != nil {
		t.Fatal(err)
	}
	want := "Lighthouse: Unlighthouse uses its own settings and ignores --proxy, -k, -H and --cookie for this run."
	if len(notices) != 1 || notices[0] != want {
		t.Errorf("notices = %q", notices)
	}
}

func TestRunRefusesProduction(t *testing.T) {
	logDir := fakeNode(t, false)
	prod := func(u string) bool { return strings.HasPrefix(u, "https://shop.example") }
	err := RunWith(context.Background(), "https://shop.example/", t.TempDir(), Options{Production: prod})
	if err == nil || !strings.Contains(err.Error(), "listed as production") {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(logDir, "npm-args")); err == nil {
		t.Error("installed Unlighthouse for a production URL")
	}
	if err := RunWith(context.Background(), "https://staging.example/", t.TempDir(), Options{Production: prod}); err != nil {
		t.Errorf("refused staging: %v", err)
	}
}

func TestRunFailures(t *testing.T) {
	fakeNode(t, true)
	err := Run(context.Background(), "https://shop.example/", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "site unreachable") {
		t.Errorf("err = %v", err)
	}
	if err := Run(context.Background(), "javascript:alert(1)", t.TempDir()); err == nil {
		t.Error("accepted a non-http URL")
	}
	t.Setenv("PATH", t.TempDir())
	if err := Run(context.Background(), "https://shop.example/", t.TempDir()); err == nil || !strings.Contains(err.Error(), "node not found; install Node.js") {
		t.Errorf("err = %v", err)
	}
}

// TestLockfile checks the embedded tree pins Package and that npm ci
// can check every package it downloads.
func TestLockfile(t *testing.T) {
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	data, _ := tree.ReadFile("npm/package.json")
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	name, version, _ := strings.Cut(Package, "@")
	if pkg.Dependencies[name] != version || len(pkg.Dependencies) != 1 {
		t.Errorf("package.json dependencies = %v, want only %s", pkg.Dependencies, Package)
	}
	var lock struct {
		LockfileVersion int `json:"lockfileVersion"`
		Packages        map[string]struct {
			Version   string `json:"version"`
			Resolved  string `json:"resolved"`
			Integrity string `json:"integrity"`
			InBundle  bool   `json:"inBundle"`
		} `json:"packages"`
	}
	data, _ = tree.ReadFile("npm/package-lock.json")
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	if lock.LockfileVersion < 3 {
		t.Errorf("lockfileVersion = %d", lock.LockfileVersion)
	}
	if got := lock.Packages["node_modules/"+name].Version; got != version {
		t.Errorf("lockfile has %s %s, want %s", name, got, version)
	}
	if len(lock.Packages) < 100 {
		t.Errorf("lockfile has only %d packages", len(lock.Packages))
	}
	for path, p := range lock.Packages {
		// Bundled packages come inside their parent's tarball, which is
		// itself checked.
		if path == "" || p.InBundle {
			continue
		}
		if !strings.HasPrefix(p.Integrity, "sha512-") {
			t.Errorf("%s has no sha512 integrity hash", path)
		}
		if !strings.HasPrefix(p.Resolved, "https://registry.npmjs.org/") {
			t.Errorf("%s resolves to %s, not the npm registry", path, p.Resolved)
		}
	}
}
