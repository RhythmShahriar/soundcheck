// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/secrets"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/checks/cache"
	echomod "github.com/RhythmShahriar/soundcheck/checks/echo"
	execmod "github.com/RhythmShahriar/soundcheck/checks/exec"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/checks/interact"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard"
	"github.com/RhythmShahriar/soundcheck/checks/seo"
	"github.com/RhythmShahriar/soundcheck/checks/speed"
	"github.com/RhythmShahriar/soundcheck/checks/visual"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/lighthouse"
	"github.com/RhythmShahriar/soundcheck/login"
	// The statement package registers the statement: block of the
	// config file.
	_ "github.com/RhythmShahriar/soundcheck/statement"
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
	"keyboard": func(*plan) check.Module { return keyboard.New() },
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

// configureModules hands each registered module that keeps settings of
// its own (check.Configurable) its block of the config file at path, so
// a typo in it is a usage error before anything runs, enabled or not.
func configureModules(cfg config.Config, path string) error {
	for _, m := range check.Registered() {
		if err := check.Configure(m, cfg); err != nil {
			var se *config.SettingError
			if errors.As(err, &se) && se.File == "" {
				se.File = path
			}
			return cli.UsageError{Msg: err.Error()}
		}
	}
	return nil
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
				// parse checked the block: configureModules
				_ = check.Configure(m, cfg)
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

// loginFunc returns how to log in once the site's address is known
// and who as (masked), when SOUNDCHECK_USER and SOUNDCHECK_PASSWORD are set.
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
	// Machine output on stdout hides the live view, so the steps go to
	// stderr then: a fleet's per-site log is that stderr.
	step := func(msg string) {
		if s.live {
			s.view.Step("Lighthouse", msg)
		} else if !s.o.quiet {
			fmt.Fprintln(s.stderr, "Lighthouse:", msg)
		}
	}
	step("running Unlighthouse (this takes a while)…")
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
	step("results in " + rel(filepath.Join(out, lighthouse.Result)))
}

// cmdFlows handles `flows init`.
func cmdFlows(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("flows", flag.ContinueOnError)
	keys := fs.Bool("keyboard", false, "")
	args, code, ok := subFlags(fs, args, flowsUsage, stdout, stderr)
	if !ok {
		return code
	}
	if len(args) != 1 || args[0] != "init" {
		fmt.Fprintln(stderr, prefix(), "usage:", cmdName(), "flows init [--keyboard]")
		return exitUsage
	}
	cfg, err := config.Load(filepath.Join(config.Dir, "config.yaml"))
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitUsage
	}
	if *keys {
		return keyFlowsInit(cfg.Flows, stdout, stderr)
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

// keyFlowFiles are the key flows flows init --keyboard writes.
var keyFlowFiles = []string{"keyboard-checkout.yaml", "keyboard-contact.yaml", "keyboard-dialog.yaml", "keyboard-menu.yaml"}

// keyFlowsInit writes the four key flow templates into the flows folder.
// It replaces nothing: a file that is there (init --eaa writes
// keyboard-checkout.yaml) is kept and named while the rest are written.
// With all four there it writes nothing and exits 2.
func keyFlowsInit(dir string, stdout, stderr io.Writer) int {
	written, kept, err := writeKeyFlows(dir, keyFlowFiles)
	for _, p := range written {
		fmt.Fprintf(stdout, "Wrote %s\n", p)
	}
	for _, p := range kept {
		fmt.Fprintf(stdout, "Kept %s: it's already there\n", p)
	}
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	if len(written) == 0 {
		fmt.Fprintf(stderr, "%s the four key flows are already in %s; nothing was written\n", prefix(), dir)
		return exitUsage
	}
	fmt.Fprintln(stdout, "Edit them, then run them with --level flows: they press keys only; keyboard: true holds every request that would send data.")
	return exitOK
}
