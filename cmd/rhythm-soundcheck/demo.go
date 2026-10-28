// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/soundcheck/demo"
)

// demoFlags are the flags demo takes: how it looks and which browser it
// uses. Everything else is fixed, so the demo shows the same result
// everywhere.
var demoFlags = []string{"plain", "no-color", "q", "quiet", "v", "verbose", "open", "keep",
	"browser", "no-download", "no-sandbox", "show-browser", "eaa"}

// demoArgs are the settings demo runs with. The levels include submit,
// so the broken contact form is caught; Echo and the login stay off
// whatever the environment says.
var demoArgs = []string{"--level", "submit", "--viewport", "desktop", "--rate", "20", "--echo=false", "--no-login"}

// demoEAAArgs are added for demo --eaa: health and interact keep the
// usual breaks in view and --eaa adds a11y and keyboard, which catch the
// keyboard breaks.
var demoEAAArgs = []string{"--modules", "health,interact"}

// cmdDemo serves the built-in demo site on this machine, snapshots it,
// breaks it and compares. Its snapshot and run go to a temporary folder,
// never the project's .soundcheck, which is removed afterwards unless
// --keep or --open asks for the report.
func cmdDemo(args []string, stdout, stderr io.Writer) int {
	keep, eaa, rest, err := demoOptions(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stdout)
			return exitOK
		}
		fmt.Fprintln(stderr, prefix(), err)
		return exitUsage
	}
	tmp, err := os.MkdirTemp("", "soundcheck-demo-")
	if err != nil {
		fmt.Fprintln(stderr, prefix(), err)
		return exitError
	}
	if !keep {
		defer func() { _ = os.RemoveAll(tmp) }()
	}
	// An empty config file of its own, so the project's settings
	// (production sites, headers, flows) don't change the demo.
	cfgPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("# rhythm-soundcheck demo\n"), 0o600); err != nil {
		fmt.Fprintln(stderr, prefix(), err)
		return exitError
	}
	var site demo.Site
	site.SetKeyboard(eaa)
	srv, err := demo.Start(&site)
	if err != nil {
		fmt.Fprintln(stderr, prefix(), "starting the demo site:", err)
		return exitError
	}
	defer func() { _ = srv.Close() }()
	quiet := slices.ContainsFunc(rest, func(a string) bool { return a == "-q" || a == "--quiet" || a == "-quiet" })
	say := func(format string, a ...any) {
		if !quiet {
			fmt.Fprintf(stdout, format+"\n", a...)
		}
	}

	say("Soundcheck demo: a small bakery site at %s, served by this binary.", srv.URL)
	say("1. Taking a snapshot of it while it works (the whole demo takes under a minute)...")
	// The snapshot's own result isn't the point: its output is left out
	// and only errors show.
	snap, code := demoSession(append([]string{srv.URL, "--name", "working", "-q"}, rest...), cfgPath, tmp, keep, eaa, io.Discard, stderr)
	if snap == nil {
		return code
	}
	code = cmdSnapshot(snap)
	snap.stop()
	if code != exitOK {
		return code
	}
	site.SetBroken(true)
	say("2. Breaking it the way an update can: %s.", demo.Describe(eaa))
	say("3. Comparing with the snapshot:")
	say("")
	cmpS, code := demoSession(rest, cfgPath, tmp, keep, eaa, stdout, stderr)
	if cmpS == nil {
		return code
	}
	code = cmdCompare(cmpS)
	cmpS.stop()
	if keep {
		say("The demo's snapshot and report are kept in %s.", tmp)
		if eaa {
			say("Its statement draft and evidence pack are in the run folder's eaa/ (statement: above).")
		}
	}
	say("")
	// The demo's changes show no ids: accept works on the project's
	// snapshots, not the demo's, so the hint says where they appear.
	say("On your own site, compare shows an id with each change; %s accept <id> makes a change you expected part of the snapshot.", cmdName())
	switch {
	case eaa && keep:
		say("With --eaa on your own site, every run also writes a statement draft and an evidence pack (%s help eaa).", cmdName())
	case eaa:
		say("With --eaa on your own site, every run also writes a statement draft and an evidence pack; %s demo --eaa --keep keeps this one's to look at.", cmdName())
	}
	say("Now try it on your staging site: %s snapshot staging.example.com", cmdName())
	return code
}

// demoOptions checks demo's arguments: only the flags in demoFlags and
// no site. It returns --keep (or --open, which needs the report kept),
// --eaa and the other flags to pass on.
func demoOptions(args []string) (keep, eaa bool, rest []string, err error) {
	var o options
	fs := flagSet(&o)
	fs.BoolVar(&keep, "keep", false, "")
	pos, err := cli.ParseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, false, nil, err
		}
		return false, false, nil, flagError(fs, err)
	}
	if len(pos) > 0 {
		return false, false, nil, fmt.Errorf("demo checks its own site, so it takes no URL: %s demo", cmdName())
	}
	var bad []string
	fs.Visit(func(f *flag.Flag) {
		if !slices.Contains(demoFlags, f.Name) {
			bad = append(bad, "--"+f.Name)
		}
	})
	if len(bad) > 0 {
		return false, false, nil, fmt.Errorf("demo doesn't take %s; it takes --plain, --no-color, -q, -v, --keep, --open, --eaa, --browser, --no-download, --no-sandbox and --show-browser",
			strings.Join(bad, ", "))
	}
	for _, a := range args {
		if name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); name != "keep" || !strings.HasPrefix(a, "-") {
			rest = append(rest, a)
		}
	}
	return keep || o.open, o.eaa, rest, nil
}

// demoSession parses one demo step's flags into a session that keeps its
// files in tmp. Without keep the run folder goes away with tmp, so the
// summary doesn't point at its report.
func demoSession(args []string, cfgPath, tmp string, keep, eaa bool, stdout, stderr io.Writer) (*session, int) {
	fixed := slices.Clone(demoArgs)
	if eaa {
		fixed = append(fixed, demoEAAArgs...)
	}
	p, err := parse(append(append([]string{"--config", cfgPath}, fixed...), args...))
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return nil, exitUsage
	}
	p.storeDir = filepath.Join(tmp, ".soundcheck")
	p.cfg.Flows = filepath.Join(tmp, "flows")
	p.failOnSet = true // no --fail-on tip: the demo isn't a script
	p.noReportHint = !keep
	p.noAcceptHint = true // accept works on the project's snapshots, not the demo's
	return p.begin(stdout, stderr), exitOK
}
