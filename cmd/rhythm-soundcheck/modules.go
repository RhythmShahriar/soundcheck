package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/checks/speed"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/lighthouse"
)

// speedModule keeps per-run state between its hooks; one per process.
var speedModule = speed.New()

// modules returns the enabled modules in config.Modules order.
func modules(cfg config.Config) []check.Module {
	all := map[string]check.Module{
		"health": health.Module{},
		"speed":  speedModule,
	}
	var out []check.Module
	for _, name := range config.Modules {
		if m, ok := all[name]; ok && cfg.Has(name) {
			out = append(out, m)
		}
	}
	return out
}

// loginFunc returns how to log in, and who as (masked), when
// SOUNDCHECK_USER and SOUNDCHECK_PASSWORD are set.
func loginFunc(_ config.Config, off bool) (func(context.Context, *rod.Browser) error, string) {
	if off || os.Getenv("SOUNDCHECK_USER") == "" || os.Getenv("SOUNDCHECK_PASSWORD") == "" {
		return nil, ""
	}
	return nil, ""
}

// lighthouse hands the site to Unlighthouse for full Lighthouse audits,
// next to the run's own files.
func (s *session) lighthouse(site, dir string) {
	if !lighthouse.Available() {
		fmt.Fprintln(s.stderr, "rhythm soundcheck: --lighthouse needs npx; install Node.js from https://nodejs.org")
		return
	}
	out := filepath.Join(dir, "lighthouse")
	s.view.Step("Lighthouse", "running Unlighthouse (this takes a while)…")
	if err := lighthouse.Run(s.ctx, site, out); err != nil {
		fmt.Fprintln(s.stderr, "rhythm soundcheck: lighthouse:", err)
		return
	}
	s.view.Step("Lighthouse", "results in "+rel(filepath.Join(out, lighthouse.Result)))
}

// cmdFlows handles `flows init`.
func cmdFlows(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "init" {
		fmt.Fprintln(stderr, "rhythm soundcheck: usage: rhythm soundcheck flows init")
		return exitUsage
	}
	cfg, err := config.Load(filepath.Join(config.Dir, "config.yaml"))
	if err != nil {
		fmt.Fprintln(stderr, "rhythm soundcheck:", err)
		return exitUsage
	}
	fmt.Fprintln(stdout, "flows:", cfg.Flows)
	return exitOK
}
