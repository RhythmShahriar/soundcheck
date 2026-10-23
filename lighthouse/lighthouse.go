// Package lighthouse hands a site to Unlighthouse (Lighthouse run on
// every page) for people who want Google's full audit next to
// Soundcheck's change report. It runs through npx, so it needs Node.js;
// Soundcheck never installs Node itself.
//
// The command is `npx --yes unlighthouse-ci --site <url> --reporter
// jsonExpanded --output-path <dir>` (checked against unlighthouse-ci
// 0.19.1, whose CLI writes <dir>/ci-result.json for json reporters).
package lighthouse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Package is the npm package npx runs. It's pinned so a run doesn't
// silently pick up a new major version with different flags or output.
const Package = "unlighthouse-ci@0.19.1"

// Result is the report file Unlighthouse writes into the output folder.
const Result = "ci-result.json"

// Available reports whether npx is on PATH.
func Available() bool {
	_, err := exec.LookPath("npx")
	return err == nil
}

// Options tune a run. The zero value is fine.
type Options struct {
	Output io.Writer // Unlighthouse's progress output; nil discards it
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
	npx, err := exec.LookPath("npx")
	if err != nil {
		return errors.New("lighthouse: npx not found; install Node.js (https://nodejs.org) to use --lighthouse")
	}
	dir, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	// --yes: install the pinned package without asking (npx would
	// otherwise wait for a prompt). Arguments are passed directly, never
	// through a shell, so the URL can't inject anything.
	cmd := exec.CommandContext(ctx, npx, Args(u.String(), dir)...) //nolint:gosec // fixed program and flags; URL validated above
	cmd.Dir = dir
	var tail bytes.Buffer
	out := io.Writer(&tail)
	if opts.Output != nil {
		out = io.MultiWriter(opts.Output, &tail)
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

// Args returns the npx arguments for auditing site into outDir.
func Args(site, outDir string) []string {
	return []string{"--yes", Package, "--site", site, "--reporter", "jsonExpanded", "--output-path", outDir}
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
