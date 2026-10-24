// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// rhythm-soundcheck is the Soundcheck binary. It runs on its own, or as
// `rhythm soundcheck` through the rhythm CLI.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/go-rod/rod/lib/defaults"

	"github.com/RhythmShahriar/rhythm/kit"
	// Turns off rod's -rod flag before rod's init reads os.Args; the
	// package doc says why its init runs first.
	_ "github.com/RhythmShahriar/soundcheck/internal/norodflag"
)

var version = "dev"

// Exit codes, the same as Tuner's.
const (
	exitOK       = 0
	exitFindings = 1 // changes (or issues) at or above --fail-on
	exitUsage    = 2
	exitError    = 3 // the site couldn't be checked
	exitCanceled = 130
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	// A second guard against rod's -rod flag: should a change in init
	// order ever let rod read os.Args first, this puts its defaults back.
	// norodflag's init has run by now, so ResetWith doesn't read os.Args
	// again.
	defaults.ResetWith("")
	if len(args) == 0 {
		if term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) {
			return pick(stdout, stderr)
		}
		usage(stdout)
		return exitOK
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(stdout)
		return exitOK
	case "-v", "--version", "version":
		fmt.Fprintln(stdout, "rhythm-soundcheck", version)
		return exitOK
	case "snapshot", "snap":
		return command(cmdSnapshot, args[1:], stdout, stderr)
	case "compare", "cmp":
		return command(cmdCompare, args[1:], stdout, stderr)
	case "run", "check":
		return command(cmdRun, args[1:], stdout, stderr)
	case "report":
		return command(cmdReport, args[1:], stdout, stderr)
	case "snapshots", "ls":
		return cmdSnapshots(args[1:], stdout, stderr)
	case "init":
		return cmdInit(args[1:], stdout, stderr)
	case "flows":
		return cmdFlows(args[1:], stdout, stderr)
	case "update":
		return cmdUpdate(args[1:], stdout, stderr)
	}
	// `rhythm soundcheck staging.example.com --against example.com`
	if len(args) > 1 || looksLikeTarget(args[0]) {
		return command(cmdAgainst, args, stdout, stderr)
	}
	fmt.Fprintf(stderr, "%s unknown command %q\n\n", prefix(), args[0])
	usage(stderr)
	return exitUsage
}

func usage(w io.Writer) {
	name := cmdName()
	fmt.Fprintln(w, kit.Banner(name, version))
	// help says "rhythm soundcheck" where the command goes; every such
	// line starts with it, so the columns stay lined up for any name.
	fmt.Fprint(w, strings.ReplaceAll(help, "rhythm soundcheck", name))
}

// help is the usage text.
const help = `Did my change break anything? Check your whole site before and after a
plugin update, theme change, PHP upgrade or deploy.

Usage:
  rhythm soundcheck                          interactive picker
  rhythm soundcheck snapshot <url> [flags]   record how the site works now
  rhythm soundcheck compare [url] [flags]    check again and show what changed
  rhythm soundcheck <url> --against <url>    compare two sites now (staging vs live)
  rhythm soundcheck run <url> [flags]        check once, no comparison
  rhythm soundcheck report [run] [flags]     show the last result again (--open, -o)
  rhythm soundcheck snapshots                list snapshots (delete <name> removes one)
  rhythm soundcheck init                     write .soundcheck/config.yaml to edit
  rhythm soundcheck flows init               write an example flow to edit
  rhythm soundcheck update [--clear]         install or refresh the browser it uses

Snapshots:
      --name <name>          snapshot name (default: date and time); compare
                             uses the latest snapshot unless --snapshot is given
      --snapshot <name>      compare with this snapshot (alias --against-snapshot)
      --against <url>        compare with another site instead of a snapshot
      --strict               stop (exit 2) when compare wouldn't run with the
                             snapshot's modules, levels and viewports

compare runs with the snapshot's modules, levels and viewports unless a flag
or the config file sets them, and says what you changed.

Output:
      --json                 JSON (schema rhythm-soundcheck/v1)
      --md                   Markdown for pull requests and CI summaries
      --junit                JUnit XML for CI test reports
      --html                 standalone HTML report with before/after sliders
  -o, --output <file>        write the report to a file (format from the
                             extension: .json .md .xml .html), keep the live view
      --open                 open the HTML report when done
  -q, --quiet                results only
      --plain                no color, boxes or glyphs (screen readers, logs)
      --no-color             no color
  -v, --verbose              a line per page, and the browser's log

What to check:
      --config <file>        settings file (default .soundcheck/config.yaml)
      --limit <n>            at most this many pages (default 300)
      --include <globs>      only pages matching, e.g. /shop/*
      --exclude <globs>      skip pages matching
      --page <path>          always check this page too (repeatable)
      --ignore-robots        crawl pages robots.txt disallows (your own site)
      --modules <list>       health, visual, interact, cache, speed (default
                             all), echo (on with --echo)
      --skip <list>          leave these modules out
      --level <list>         interactions: inspect, click, submit, flows
                             (default inspect,click; submit sends test data)
      --viewport <list>      desktop, mobile (default both)
      --no-login             skip the login (leak test, login flows) even if
                             SOUNDCHECK_USER is set
      --allow-env <name>     let flows read this variable too (repeatable);
                             otherwise ${env:...} reads only SOUNDCHECK_* ones
      --allow-host <host>    let flows open this host too, e.g. a payment
                             page (repeatable); otherwise only the site
      --insecure-login       type the test password over plain http to a
                             site that isn't this machine
      --lighthouse           also run Unlighthouse (needs Node.js and npx)
      --echo[=<url>]         record the emails, SMS and webhooks each form
                             submit and flow sends, and compare them, through
                             Echo (default http://127.0.0.1:8025; start it with
                             rhythm echo up). Also SOUNDCHECK_ECHO=<url>

Requests and browser:
      --rate <n>             page loads per second per site, 0.01 to 1000
                             (default 5)
      --tabs <n>             pages checked at once (default 8)
      --timeout <d>          per page (default 30s)
  -A, --user-agent <ua>      User-Agent header
  -H, --header <h>           extra header, "Name: value" (repeatable)
      --cookie <c>           Cookie header, e.g. "staging_auth=abc"
      --proxy <url>          http, https or socks5 proxy
  -k, --insecure             don't verify TLS certificates (local sites)
      --browser <path>       Chrome or Chromium to use
      --no-download          never download Chromium; fail if none is installed
      --show-browser         show the browser window (debugging flows)

CI:
      --fail-on <level>      exit 1 when a comparison has changes at or above
                             broke, new, changed or slower; for run and
                             snapshot: issues at or above error, warning, notice

Logging in: set SOUNDCHECK_USER and SOUNDCHECK_PASSWORD to a test account to
test that caches don't leak logged-in pages to visitors, and to run flows that
set login: true. Pages themselves are checked as a visitor. Credentials are
read only from the environment and never written to reports. Uses
/wp-login.php, or the login flow in .soundcheck/config.yaml. The password is
typed only on the site being checked, over https unless it's this machine.

Messages: with Echo running (rhythm echo up) and its connector on the site,
--echo ties each form submit and flow to what it sent, so a form that says
"sent" but emails nobody is caught, and a lost or changed email, SMS or
webhook shows up in the comparison. Flows can check them with
expect: {message: ...}. A token comes from RHYTHM_ECHO_TOKEN (echo.token_env,
which must start with RHYTHM_ECHO_). It goes to an Echo on another machine
only when --echo or SOUNDCHECK_ECHO names it, not echo.url alone.

Files: snapshots and reports are kept in ./.soundcheck (excluded from git).
Exit codes: 0 ok · 1 --fail-on hit · 2 usage error · 3 couldn't check · 130 interrupted

Examples:
  rhythm soundcheck snapshot staging.example.com --name before-update
  rhythm soundcheck compare --open
  rhythm soundcheck staging.example.com --against example.com --fail-on broke
  rhythm soundcheck run example.test -k --level inspect,click,submit --md -o check.md
  rhythm soundcheck snapshot staging.shop.test --level inspect,click,submit --echo
`
