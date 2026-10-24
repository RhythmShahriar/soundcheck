// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/rhythm/kit/secrets"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/checks/cache"
	echomod "github.com/RhythmShahriar/soundcheck/checks/echo"
	execmod "github.com/RhythmShahriar/soundcheck/checks/exec"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/checks/interact"
	"github.com/RhythmShahriar/soundcheck/checks/seo"
	"github.com/RhythmShahriar/soundcheck/checks/speed"
	"github.com/RhythmShahriar/soundcheck/checks/visual"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/lighthouse"
	"github.com/RhythmShahriar/soundcheck/login"
)

// speedModule keeps per-run state between its hooks; one per process.
var speedModule = speed.New()

// builders make each module for a command, by name. Every module the
// binary registers (check.Registered) has one, in config.Modules order;
// a test keeps the three in step.
var builders = map[string]func(p *plan) check.Module{
	"health":   func(p *plan) check.Module { return health.Module{Rules: p.rules} },
	"visual":   func(*plan) check.Module { return visual.Module{} },
	"interact": func(*plan) check.Module { return interact.New() },
	"cache":    func(*plan) check.Module { return cache.Module{} },
	"speed":    func(*plan) check.Module { return speedModule },
	"a11y":     func(*plan) check.Module { return a11y.Module{} },
	"seo":      func(*plan) check.Module { return seo.Module{} },
	"echo":     func(*plan) check.Module { return echomod.Module{} },
	"exec": func(p *plan) check.Module {
		m, err := execmod.New(execCommands(p.cfg), p.allowExec)
		if err != nil { // parse checked the commands
			return nil
		}
		return m
	},
}

// modules returns the enabled modules in config.Modules order. The runner
// runs interact's hooks last on each page (it declares check.PhaseMutate):
// its clicks and submits change the page. The other modules' Live hooks
// must measure it as loaded.
func (p *plan) modules(cfg config.Config) []check.Module {
	var out []check.Module
	for _, name := range config.Modules {
		if b, ok := builders[name]; ok && cfg.Has(name) {
			if m := b(p); m != nil {
				out = append(out, m)
			}
		}
	}
	return out
}

// execCommands converts the config's exec: list for the exec module.
func execCommands(cfg config.Config) []execmod.Command {
	out := make([]execmod.Command, 0, len(cfg.Exec))
	for _, c := range cfg.Exec {
		out = append(out, execmod.Command(c))
	}
	return out
}

// loginFunc returns how to log in once the site's address is known, and
// who as (masked), when SOUNDCHECK_USER and SOUNDCHECK_PASSWORD are set.
func loginFunc(cfg config.Config, off bool) (func(*url.URL) func(context.Context, *rod.Browser) error, string) {
	user, _, ok := login.Credentials()
	if off || !ok {
		return nil, ""
	}
	return func(base *url.URL) func(context.Context, *rod.Browser) error {
		return login.New(login.Options{Base: base, Config: cfg})
	}, secrets.Mask(user, 2)
}

// lighthouse hands the site to Unlighthouse for full Lighthouse audits,
// next to the run's own files. It never runs on a production site:
// Unlighthouse crawls every page with its own settings.
func (s *session) lighthouse(site, dir string) {
	if s.cfg.IsProduction(site) {
		fmt.Fprintf(s.stderr, "%s --lighthouse: %s is listed as production; Unlighthouse crawls every page with its own settings, so it won't run there\n", prefix(), site)
		return
	}
	if !lighthouse.Available() {
		fmt.Fprintln(s.stderr, prefix(), "--lighthouse needs Node.js and npm; install Node.js from https://nodejs.org")
		return
	}
	out := filepath.Join(dir, "lighthouse")
	s.view.Step("Lighthouse", "running Unlighthouse (this takes a while)…")
	chrome, _, _ := browser.Find(browser.Options{Bin: s.o.browser, NoDownload: true})
	err := lighthouse.RunWith(s.ctx, site, out, lighthouse.Options{
		Log:        func(msg string) { fmt.Fprintln(s.stderr, msg) },
		Production: s.cfg.IsProduction,
		Proxy:      s.o.proxy, Insecure: s.o.insecure, Headers: len(s.headers) > 0,
		Chrome: chrome,
	})
	if err != nil {
		fmt.Fprintln(s.stderr, prefix(), err)
		return
	}
	s.view.Step("Lighthouse", "results in "+rel(filepath.Join(out, lighthouse.Result)))
}

// cmdFlows handles `flows init`.
func cmdFlows(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "init" {
		fmt.Fprintln(stderr, prefix(), "usage:", cmdName(), "flows init")
		return exitUsage
	}
	cfg, err := config.Load(filepath.Join(config.Dir, "config.yaml"))
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitUsage
	}
	if _, err := os.Stat(filepath.Join(cfg.Flows, "example.yaml")); err == nil {
		fmt.Fprintf(stderr, "%s %s already exists\n", prefix(), filepath.Join(cfg.Flows, "example.yaml"))
		return exitUsage
	}
	path, err := flow.Init(cfg.Flows)
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	fmt.Fprintf(stdout, "Wrote %s. Edit it, then run flows with: --level flows (it submits forms too)\n", path)
	return exitOK
}
