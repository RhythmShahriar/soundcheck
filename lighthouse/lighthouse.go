// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package lighthouse hands a site to Unlighthouse (Lighthouse run on
// every page) for people who want Google's full audit next to
// Soundcheck's change report. It needs Node.js; Soundcheck never
// installs Node itself.
//
// Unlighthouse is a third-party npm tool with a large dependency tree,
// so the whole tree is pinned: npm/package.json and npm/package-lock.json
// are embedded here and installed once into Rhythm's cache with
// `npm ci --ignore-scripts`, which takes exactly the locked versions,
// checks every package's integrity hash and runs no install scripts.
// The installed unlighthouse-ci then runs with node as `unlighthouse-ci
// --site <url> --reporter jsonExpanded --output-path <dir>` (checked
// against unlighthouse-ci 0.19.1, whose CLI writes <dir>/ci-result.json
// for json reporters).
//
// To update the tree, change the version in npm/package.json, then in
// that folder run `npm install --package-lock-only --ignore-scripts` and
// `npm audit --omit=dev`, and update Package.
package lighthouse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/config"
)

// Package is the npm package the embedded lockfile pins. It's pinned so
// a run doesn't silently pick up a new version with different flags or
// output.
const Package = "unlighthouse-ci@0.19.1"

// Result is the report file Unlighthouse writes into the output folder.
const Result = "ci-result.json"

//go:embed npm/package.json npm/package-lock.json
var tree embed.FS

// Available reports whether node and npm are on PATH.
func Available() bool {
	_, errNode := exec.LookPath("node")
	_, errNPM := exec.LookPath("npm")
	return errNode == nil && errNPM == nil
}

// Options tune a run. The zero value is fine.
type Options struct {
	Output io.Writer    // Unlighthouse's and npm's progress output; nil discards it
	Log    func(string) // one-line notices; nil writes them to stderr

	// Production reports whether a URL is listed as production, like
	// config.Config.IsProduction. Unlighthouse crawls every page on its
	// own terms, so it never runs on one.
	Production func(string) bool

	// The run's settings Unlighthouse can't take. Each one set is named
	// in a notice before it starts, so nobody assumes it applied.
	Proxy    string
	Insecure bool
	Headers  bool // any -H or --cookie

	// Chrome is the browser for Lighthouse to use (CHROME_PATH), so it
	// needs no download of its own.
	Chrome string
}

// Run audits the site at rawURL with Unlighthouse and writes its reports
// into outDir (created if missing). The JSON summary is at
// filepath.Join(outDir, Result).
func Run(ctx context.Context, rawURL, outDir string) error {
	return RunWith(ctx, rawURL, outDir, Options{})
}

// RunWith is Run with options.
func RunWith(ctx context.Context, rawURL, outDir string, opts Options) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("lighthouse: %q isn't an http(s) URL", rawURL)
	}
	if opts.Production != nil && opts.Production(u.String()) {
		return fmt.Errorf("lighthouse: %s is listed as production; Unlighthouse crawls every page with its own settings, so --lighthouse won't run there", u)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return errors.New("lighthouse: node not found; install Node.js (https://nodejs.org) to use --lighthouse")
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return errors.New("lighthouse: npm not found; install Node.js with npm (https://nodejs.org) to use --lighthouse")
	}
	dir, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if ignored := Ignored(opts); len(ignored) > 0 {
		notice(opts, "Lighthouse: Unlighthouse uses its own settings and ignores "+strings.Join(ignored, ", ")+" for this run.")
	}
	var tail bytes.Buffer
	out := io.Writer(&tail)
	if opts.Output != nil {
		out = io.MultiWriter(opts.Output, &tail)
	}
	home, err := Install(ctx, npm, out)
	if err != nil {
		return fmt.Errorf("lighthouse: installing %s: %w%s", Package, err, lastLines(tail.String(), 5))
	}
	bin, err := binPath(home)
	if err != nil {
		return fmt.Errorf("lighthouse: %w", err)
	}
	tail.Reset()
	// Arguments are passed directly, never through a shell, so the URL
	// can't inject anything.
	cmd := exec.CommandContext(ctx, node, append([]string{bin}, Args(u.String(), dir)...)...) //nolint:gosec // fixed program and flags; URL validated above
	cmd.Dir = dir
	if opts.Chrome != "" {
		cmd.Env = append(os.Environ(), "CHROME_PATH="+opts.Chrome)
	}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("lighthouse: unlighthouse failed: %w%s", err, lastLines(tail.String(), 5))
	}
	if _, err := os.Stat(filepath.Join(dir, Result)); err != nil {
		return fmt.Errorf("lighthouse: unlighthouse finished but wrote no %s", Result)
	}
	return nil
}

// Ignored names the run settings in opts that Unlighthouse won't use.
func Ignored(opts Options) []string {
	var s []string
	if opts.Proxy != "" {
		s = append(s, "--proxy")
	}
	if opts.Insecure {
		s = append(s, "-k")
	}
	if opts.Headers {
		s = append(s, "-H and --cookie")
	}
	return s
}

func notice(opts Options, msg string) {
	if opts.Log != nil {
		opts.Log(msg)
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}

// Dir is the cache folder the pinned tree is installed into. Its name
// carries the lockfile's hash, so a Soundcheck with a different tree
// installs next to it instead of reusing a stale one.
func Dir() (string, error) {
	cache, err := config.CacheDir("soundcheck")
	if err != nil {
		return "", err
	}
	lock, err := tree.ReadFile("npm/package-lock.json")
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(lock)
	return filepath.Join(cache, "lighthouse", hex.EncodeToString(h[:6])), nil
}

// installed marks a finished npm ci, so a half-done install is redone.
const installed = ".soundcheck-installed"

// Install puts the pinned tree into Dir with npm (once) and returns the
// folder. npm's output goes to out.
func Install(ctx context.Context, npm string, out io.Writer) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, installed)); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	for _, name := range []string{"package.json", "package-lock.json"} {
		data, err := tree.ReadFile("npm/" + name)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return "", err
		}
	}
	// npm ci installs exactly the lockfile and fails if anything differs;
	// --ignore-scripts keeps the tree's install scripts from running.
	cmd := exec.CommandContext(ctx, npm, CIArgs()...) //nolint:gosec // fixed program and flags
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("npm ci: %w", err)
	}
	return dir, os.WriteFile(filepath.Join(dir, installed), nil, 0o600)
}

// CIArgs are the npm arguments that install the pinned tree.
func CIArgs() []string {
	return []string{"ci", "--ignore-scripts", "--no-audit", "--no-fund"}
}

// binPath finds unlighthouse-ci's entry script from its package.json,
// so node runs it directly on every platform (no .cmd shims).
func binPath(home string) (string, error) {
	pkgDir := filepath.Join(home, "node_modules", "unlighthouse-ci")
	data, err := os.ReadFile(filepath.Join(pkgDir, "package.json")) //nolint:gosec // our own cache folder
	if err != nil {
		return "", fmt.Errorf("unlighthouse-ci isn't installed in %s: %w", home, err)
	}
	var pkg struct {
		Bin json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", err
	}
	var rel string
	var bins map[string]string
	if json.Unmarshal(pkg.Bin, &bins) == nil {
		rel = bins["unlighthouse-ci"]
	} else {
		_ = json.Unmarshal(pkg.Bin, &rel)
	}
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(filepath.Clean(rel), "..") {
		return "", fmt.Errorf("unlighthouse-ci in %s has no usable bin entry", pkgDir)
	}
	return filepath.Join(pkgDir, rel), nil
}

// Args returns unlighthouse-ci's arguments for auditing site into outDir.
func Args(site, outDir string) []string {
	return []string{"--site", site, "--reporter", "jsonExpanded", "--output-path", outDir}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return ":\n" + strings.Join(lines, "\n")
}
