// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/clientreport"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/fleet"
	"github.com/RhythmShahriar/soundcheck/history"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/review"
	"github.com/RhythmShahriar/soundcheck/rules"
	"github.com/RhythmShahriar/soundcheck/wcag"
	"github.com/RhythmShahriar/soundcheck/wpupdate"
)

// subFlags parses a subcommand's own flags, anywhere among its arguments.
// It returns ok false with the exit code when the command should stop:
// after --help (0) or a bad flag (2).
func subFlags(fs *flag.FlagSet, args []string, usage string, stdout, stderr io.Writer) (pos []string, code int, ok bool) {
	fs.SetOutput(io.Discard)
	apply := colorFlags(fs)
	pos, err := cli.ParseInterspersed(fs, args)
	apply()
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, named(usage))
		return nil, exitOK, false
	case err != nil:
		fmt.Fprintln(stderr, prefix(), flagError(fs, err))
		return nil, exitUsage, false
	}
	return pos, 0, true
}

// colorFlags adds --plain, --no-color and -q (--quiet) to a subcommand's
// flags, as every command takes them. The returned func, called after
// parsing, turns color off when either color flag is set. A subcommand
// prints only results anyway; one with progress lines reads -q with
// boolFlag(fs, "quiet").
func colorFlags(fs *flag.FlagSet) (apply func()) {
	plain := fs.Bool("plain", false, "")
	noColor := fs.Bool("no-color", false, "")
	quiet := new(bool)
	fs.BoolVar(quiet, "q", false, "")
	fs.BoolVar(quiet, "quiet", false, "")
	return func() {
		if *plain || *noColor {
			lipgloss.SetColorProfile(termenv.Ascii)
		}
	}
}

// boolFlag reports whether the bool flag name of fs is set.
func boolFlag(fs *flag.FlagSet, name string) bool {
	if f := fs.Lookup(name); f != nil {
		if g, ok := f.Value.(flag.Getter); ok {
			on, _ := g.Get().(bool)
			return on
		}
	}
	return false
}

// listChecks prints every check of every module, in config.Modules
// order, for --list-checks. The a11y and keyboard checks also name the
// WCAG criteria they fail, or say they are advisory.
func listChecks(w io.Writer) {
	mods := check.Registered()
	slices.SortStableFunc(mods, func(a, b check.Module) int {
		return slices.Index(config.Modules, a.Name()) - slices.Index(config.Modules, b.Name())
	})
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, m := range mods {
		if i > 0 {
			fmt.Fprintln(tw)
		}
		state := "off unless asked for (+" + m.Name() + ")"
		switch {
		case slices.Contains(config.DefaultModules, m.Name()):
			state = "on by default"
		case slices.Contains(eaaNeeds, m.Name()):
			state += "; on with --eaa"
		case m.Name() == "echo":
			state = "on with --echo"
		case m.Name() == "exec":
			state = "on when the config lists exec: commands, which run with --allow-exec"
		}
		fmt.Fprintf(tw, "%s: %s\n", m.Name(), state)
		d, ok := m.(check.Describer)
		if !ok {
			continue
		}
		for _, c := range d.Checks() {
			if listedCriteria(m.Name()) {
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", c.ID, c.Level, c.Title, criteriaText(c.ID))
				continue
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", c.ID, c.Level, c.Title)
		}
	}
	_ = tw.Flush()
}

const historyUsage = `Usage:
  rhythm soundcheck history [--page <path>] [--metric <name>] [--backfill]

Shows every snapshot, run and comparison in .soundcheck/history.jsonl,
oldest first, with the site's median LCP (or --metric) and a sparkline.
--metric takes lcp, tbt, ttfb, a11y (open accessibility issues) or keyboard
(open keyboard issues). --page shows that page's figures instead. --backfill adds the runs and
snapshots still in .soundcheck that the history doesn't have.
`

// cmdHistory prints the history of runs with a trend.
func cmdHistory(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	page := fs.String("page", "", "")
	metric := fs.String("metric", "", "")
	backfill := fs.Bool("backfill", false, "")
	pos, code, ok := subFlags(fs, args, historyUsage, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprint(stderr, named(historyUsage))
		return exitUsage
	}
	if *metric != "" && !slices.Contains(history.Metrics, *metric) {
		return failed(stderr, cli.Usagef("--metric: %v", config.OneOf(*metric, history.Metrics)))
	}
	root := config.Dir
	if _, err := os.Stat(filepath.Join(root, history.File)); *backfill || errors.Is(err, os.ErrNotExist) {
		n, err := history.Backfill(root)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return failed(stderr, err)
		}
		if n > 0 {
			fmt.Fprintf(stderr, "Added %s still in %s to the history\n", plural(n, "past result"), root)
		}
	}
	entries, err := history.Read(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return failed(stderr, err)
	}
	if err := history.Render(stdout, entries, *page, *metric, boolFlag(fs, "plain")); err != nil {
		return failed(stderr, cli.UsageError{Msg: err.Error()})
	}
	return exitOK
}

const fleetUsage = `Usage:
  rhythm soundcheck fleet <sites.yaml> snapshot|compare [--tabs <n>] [--open]

Checks every site listed in sites.yaml, --tabs at a time (default 3), each
in its own folder under .soundcheck-fleet next to the file, then ranks
them worst first in .soundcheck-fleet/fleet.html. A site's variables come
from <PREFIX>_SOUNDCHECK_* (env_prefix, default from its name). A site's
args can't run programs: set <PREFIX>_SOUNDCHECK_ALLOW_EXEC=1 or
<PREFIX>_SOUNDCHECK_LIGHTHOUSE=1 for --allow-exec or --lighthouse.
--eaa and --wcag <version> go to every site's command.
`

// cmdFleet runs snapshot or compare for every site in a sites file.
func cmdFleet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fleet", flag.ContinueOnError)
	tabs := fs.Int("tabs", 3, "")
	open := fs.Bool("open", false, "")
	eaa := fs.Bool("eaa", false, "")
	version := fs.String("wcag", "", "")
	pos, code, ok := subFlags(fs, args, fleetUsage, stdout, stderr)
	if !ok {
		return code
	}
	// --eaa and --wcag go to every site, after the site's own args.
	var extra []string
	if *eaa {
		extra = append(extra, "--eaa")
	}
	if *version != "" {
		if !slices.Contains(wcag.Versions, *version) {
			return failed(stderr, cli.Usagef("--wcag (a11y.wcag): %q isn't one of %s", *version, strings.Join(wcag.Versions, ", ")))
		}
		extra = append(extra, "--wcag", *version)
	}
	if len(pos) != 2 || (pos[1] != fleet.Snapshot && pos[1] != fleet.Compare) {
		fmt.Fprint(stderr, named(fleetUsage))
		return exitUsage
	}
	if *tabs < 1 || *tabs > 16 {
		fmt.Fprintln(stderr, prefix(), "--tabs: must be between 1 and 16")
		return exitUsage
	}
	bin, err := os.Executable()
	if err != nil {
		return failed(stderr, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	code, err = fleet.Run(ctx, bin, pos[0], pos[1], *tabs, extra, stdout)
	if err != nil {
		if code == 0 { // the file was refused before any site ran
			return failed(stderr, cli.UsageError{Msg: err.Error()})
		}
		fmt.Fprintln(stderr, prefix(), err)
	}
	if *open {
		page := filepath.Join(filepath.Dir(pos[0]), fleet.Folder, "fleet.html")
		if err := openFile(page); err != nil {
			fmt.Fprintln(stderr, prefix(), "opening the fleet report:", err, "\n  open it yourself:", page)
		}
	}
	return code
}

const wpUpdateUsage = `Usage:
  rhythm soundcheck wp-update <url> --wp "<wp-cli command>" [flags] [-- <soundcheck flags>]

Updates a staging site's WordPress plugins with WP-CLI and compares the
site after each update (or, with --bisect, after all of them), so a plugin
that breaks the site is found and named. It never runs on a site listed
under production: in the config.

      --wp <command>         how to run WP-CLI, e.g. "wp --path=/srv/www" or
                             "ssh deploy@staging -- wp --path=/srv/www"
      --plugins <list>       plugins to update (default all with an update)
      --one-by-one           update and compare one plugin at a time (default)
      --bisect               update all, then halve the suspects on a break
      --stop-on <class>      broke (default), new or changed
      --rollback             put a bad update back to its old version and go on
      --from-tuner <file>    update the plugins a rhythm-tuner JSON report
                             lists as vulnerable first
      --config <file>        settings file (default .soundcheck/config.yaml)

Flags after -- go to every snapshot and compare, e.g. -- --limit 50.
Logs go to .soundcheck/wp-update/. Exit codes: 0 every update worked, 1 an
update broke the site (also when --rollback put it back and the site works
now), 2 usage error, 3 an update or check failed, 130 interrupted.
`

// cmdWPUpdate runs the WordPress update loop.
func cmdWPUpdate(args []string, stdout, stderr io.Writer) int {
	var extra []string
	if i := slices.Index(args, "--"); i >= 0 {
		args, extra = args[:i], args[i+1:]
	}
	fs := flag.NewFlagSet("wp-update", flag.ContinueOnError)
	wp := fs.String("wp", "", "")
	var plugins cli.ListFlag
	fs.Var(&plugins, "plugins", "")
	oneByOne := fs.Bool("one-by-one", false, "")
	bisect := fs.Bool("bisect", false, "")
	stopOn := fs.String("stop-on", "", "")
	rollback := fs.Bool("rollback", false, "")
	fromTuner := fs.String("from-tuner", "", "")
	configPath := fs.String("config", "", "")
	pos, code, ok := subFlags(fs, args, wpUpdateUsage, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		fmt.Fprint(stderr, named(wpUpdateUsage))
		return exitUsage
	}
	if err := target(pos[0]); err != nil {
		return failed(stderr, err)
	}
	argv, err := wpupdate.SplitCommand(*wp)
	if *wp != "" && err != nil {
		return failed(stderr, cli.Usagef("--wp: %v", err))
	}
	path := cmpOr(*configPath, filepath.Join(config.Dir, "config.yaml"))
	cfg, err := config.Load(path)
	if err != nil {
		return failed(stderr, cli.UsageError{Msg: err.Error()})
	}
	bin, err := os.Executable()
	if err != nil {
		return failed(stderr, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	log, err := wpupdate.Run(ctx, wpupdate.Options{
		Target: pos[0], WP: argv, Plugins: plugins, OneByOne: *oneByOne, Bisect: *bisect, StopOn: *stopOn,
		Rollback: *rollback, FromTuner: *fromTuner, Bin: bin, Config: cfg, ConfigPath: *configPath, Args: extra, Out: stderr,
	})
	if log != nil {
		for _, line := range log.Summary() {
			fmt.Fprintln(stdout, line)
		}
		for _, f := range log.Files {
			fmt.Fprintln(stderr, "Log written to", rel(f))
		}
	}
	switch {
	case ctx.Err() != nil:
		return exitCanceled
	case err != nil && log == nil: // refused before anything ran
		return failed(stderr, cli.UsageError{Msg: err.Error()})
	case err != nil:
		return failed(stderr, err)
	}
	return wpUpdateExit(log)
}

// wpUpdateExit is the exit code of an update night that ran to the end:
// 1 when an update broke the site, even one --rollback put back, so CI
// learns of it.
func wpUpdateExit(l *wpupdate.Log) int {
	if !l.Works || l.Broke() {
		return exitFindings
	}
	return exitOK
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

const rulesUsage = `Usage:
  rhythm soundcheck rules list           the error rules health uses
  rhythm soundcheck rules test [dir]     run the rules' samples

Your own rules are YAML files in .soundcheck/rules, next to their samples
(<name>_test.yaml). rules test runs the samples of a folder (default
.soundcheck/rules, or the built-in rules when there is none) and exits 1
when one doesn't behave.
`

// cmdRules lists the error rules or tests a folder of them.
func cmdRules(args []string, stdout, stderr io.Writer) int {
	dir := filepath.Join(config.Dir, "rules")
	args, code, ok := subFlags(flag.NewFlagSet("rules", flag.ContinueOnError), args, rulesUsage, stdout, stderr)
	if !ok {
		return code
	}
	switch {
	case len(args) == 1 && args[0] == "list":
		set, err := health.LoadRules(dir)
		if err != nil {
			return failed(stderr, cli.Usagef("%s: %v", dir, err))
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tLEVEL\tTITLE\tFROM")
		for _, r := range set.Rules() {
			from := "built in"
			if !r.Builtin {
				from = filepath.Join(dir, r.File)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ID, r.Level, report.Term(r.Title), report.Term(from))
		}
		_ = tw.Flush()
		return exitOK
	case len(args) >= 1 && len(args) <= 2 && args[0] == "test":
		var rep rules.Report
		var err error
		switch {
		case len(args) == 2:
			dir = args[1]
			rep, err = rules.Test(dir, health.Checks...)
		case fileExists(dir):
			rep, err = rules.Test(dir, health.Checks...)
		default:
			dir = "the built-in rules"
			rep, err = rules.TestBuiltin()
		}
		if err != nil {
			return failed(stderr, cli.Usagef("%s: %v", dir, err))
		}
		for _, f := range rep.Failures {
			fmt.Fprintln(stdout, "FAIL", report.Term(f))
		}
		fmt.Fprintf(stdout, "%s: %s, %s, %s\n", dir, plural(rep.Rules, "rule"), plural(rep.Samples, "sample"),
			plural(len(rep.Failures), "failure"))
		if len(rep.Failures) > 0 {
			return exitFindings
		}
		return exitOK
	}
	fmt.Fprint(stderr, named(rulesUsage))
	return exitUsage
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// serve opens a stored comparison for review in the browser, for
// report --serve.
func (s *session) serve(res report.Result) int {
	if !res.Compared() {
		return s.fail(cli.Usagef("nothing to review: this result isn't a comparison"))
	}
	sc, err := comparisonIn(s.st, res.Dir, "report --serve")
	if err != nil {
		return s.fail(err)
	}
	if err := review.Serve(s.ctx, res, s.stderr, decider(sc, s.configPath)); err != nil && s.ctx.Err() == nil {
		return s.fail(err)
	}
	return exitOK
}

// clientReport writes the client-ready report, for report --client: to
// each -o file, or to stdout.
func (s *session) clientReport(res report.Result) int {
	if s.o.brand == "" {
		return s.fail(cli.Usagef("--client needs --brand <agency.yaml>: the agency's name, logo and color"))
	}
	brand, err := clientreport.LoadBrand(s.o.brand)
	if err != nil {
		return s.fail(cli.Usagef("--brand: %v", err))
	}
	if len(s.outputs) == 0 {
		if err := clientreport.Write(s.stdout, res, brand); err != nil {
			return s.fail(err)
		}
		return exitOK
	}
	code := exitOK
	for _, out := range s.outputs {
		if out.format != "html" {
			return s.fail(cli.Usagef("--client writes HTML; name the file .html, not %s", out.path))
		}
	}
	for _, out := range s.outputs {
		if err := writeFile(out.path, func(w io.Writer) error { return clientreport.Write(w, res, brand) }); err != nil {
			fmt.Fprintf(s.stderr, "%s writing %s: %v\n", prefix(), out.path, err)
			code = exitError
			continue
		}
		if !s.o.quiet {
			fmt.Fprintf(s.stderr, "Client report written to %s\n", out.path)
		}
	}
	return code
}

// helpTopics are the commands with their own --help plus the main ones
// that `help <command>` describes before the full list of flags.
var helpTopics = map[string]string{"accept": acceptUsage, "review": reviewUsage, "history": historyUsage,
	"fleet": fleetUsage, "wp-update": wpUpdateUsage, "rules": rulesUsage, "statement": statementUsage, "evidence": evidenceUsage,
	"snapshots": snapshotsUsage, "flows": flowsUsage, "update": updateUsage, "init": initUsage,
	"snapshot": snapshotUsage, "compare": compareUsage, "report": reportUsage, "import": importTopic}

const snapshotsUsage = `Usage:
  rhythm soundcheck snapshots [--json]
  rhythm soundcheck snapshots delete <name>

Lists the snapshots in .soundcheck/snapshots with their pages, age and site,
marking the one compare uses by default. --json prints them as a JSON array
for scripts. delete removes one.
`

const initUsage = `Usage:
  rhythm soundcheck init [--eaa] [--force]

Writes .soundcheck/config.yaml with every setting described and asks git to
ignore the snapshot folders. --force replaces a config that's already there.
--eaa turns EAA mode on (eaa: true), adds the a11y, keyboard and statement:
blocks as comments to fill in and writes the key flow
.soundcheck/flows/keyboard-checkout.yaml.
`

const flowsUsage = `Usage:
  rhythm soundcheck flows init [--keyboard]

Writes an example flow to the flows folder (.soundcheck/flows unless the
config sets flows:). Edit it, then run flows with --level flows, which
submits forms too. --keyboard writes four key flows instead, which the
keyboard module runs by keyboard alone: keyboard-checkout.yaml,
keyboard-contact.yaml, keyboard-menu.yaml and keyboard-dialog.yaml. A file
that's already there is never replaced.
`

const updateUsage = `Usage:
  rhythm soundcheck update [--clear]

Installs the browser if there's none (Chrome for Testing, when no Chrome is
installed) and removes older downloaded revisions. --clear deletes the
downloaded browser instead.
`

const snapshotUsage = `Usage:
  rhythm soundcheck snapshot <url> [flags]

Records how the site works now: each page's errors, screenshots, forms,
cache and timing, kept in .soundcheck/snapshots/<name>. compare checks the
site again against it. --name names it (default: date and time). --resume
carries on with a stopped snapshot of the same --name and keeps the pages it
finished, with the modules, levels, viewports and browser it ran with; a flag
that sets them differently is refused. Every flag in rhythm soundcheck help
applies.
`

const compareUsage = `Usage:
  rhythm soundcheck compare [url] [flags]
  rhythm soundcheck <url> --against <url>

Checks the site again and shows what changed since the snapshot (the latest
one unless --snapshot <name> picks another) as BROKE, NEW, CHANGED, SLOWER,
FIXED and UNSTABLE. With no URL it checks the snapshot's site. It runs with
the snapshot's modules, levels, viewports and browser unless a flag or the
config file sets them; --strict stops (exit 2) when they would differ.
BROKE and NEW pages are visited once more to confirm them (--confirm=off
skips that). --against compares two sites now instead of a snapshot. Every
flag in rhythm soundcheck help applies.
`

const reportUsage = `Usage:
  rhythm soundcheck report [run folder|snapshot] [flags]
  rhythm soundcheck report --serve
  rhythm soundcheck report --client --brand <agency.yaml> [-o report.html]

Shows the last result again (or the run folder or snapshot named) in any
output format: --md, --json, --junit, --html or -o <file>. --serve opens the
comparison in the browser, where each change can be accepted or ignored.
--client writes a client-ready HTML report with the agency's name, logo and
color from --brand, to -o or stdout.

agency.yaml takes four keys:
  name: Acme Web              the agency's name (required)
  logo: logo.svg              PNG, JPEG or SVG, inside the folder of agency.yaml
  accent: "#0F766E"           hex color for headings and badges (color:
                              works too)
  contact: hello@acme.test    how the client reaches you, shown as text
`

const importTopic = `Usage:
  rhythm soundcheck import <backstop.json> [--site <url>] [--force]
  rhythm soundcheck import <lostpixel.config.ts> --pages-only [--site <url>] [--force]

Writes .soundcheck/config.yaml from a BackstopJS config, or from the page
paths of a Lost Pixel config with --pages-only (that file is code, so it is
read as text and never run). --site sets the site to check (default: the
first BackstopJS scenario's). --force replaces a config that's already
there.
`

// topic returns a command's own usage, for `help <command>`.
func topic(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if gen, ok := generatedTopics[name]; ok {
		return gen(), true
	}
	u, ok := helpTopics[name]
	return u, ok
}
