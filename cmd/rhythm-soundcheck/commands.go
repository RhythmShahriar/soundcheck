// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	kitconfig "github.com/RhythmShahriar/rhythm/kit/config"
	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/rhythm/kit/update"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/runner"
	"github.com/RhythmShahriar/soundcheck/store"
)

// keepRuns is how many runs and comparisons .soundcheck/runs keeps.
const keepRuns = 20

// command parses flags and runs fn, turning errors into exit codes.
func command(fn func(*session) int, args []string, stdout, stderr io.Writer) int {
	p, err := parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stdout)
			return exitOK
		}
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitUsage
	}
	s := p.begin(stdout, stderr)
	defer s.stop()
	return fn(s)
}

// session is one command's output, signals and store.
type session struct {
	*plan
	stdout, stderr io.Writer
	ctx            context.Context
	stop           func()
	tty, live      bool
	view           *report.Pretty
	st             store.Store
	notice         <-chan string
	echo           *echolink.Link // connected on the first check, when Echo is on
	flowsChecked   bool           // checkFlows ran
}

func (p *plan) begin(stdout, stderr io.Writer) *session {
	s := &session{plan: p, stdout: stdout, stderr: stderr, st: store.Store{Root: p.storeDir}}
	s.ctx, s.stop = signal.NotifyContext(context.Background(), stopSignals...)
	s.tty = cli.IsTerminal(stdout)
	plain := p.o.plain || !s.tty
	if plain || p.o.noColor {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	// Machine output on stdout replaces the live view; with -o both show.
	s.live = p.format == ""
	out := stdout
	if !s.live {
		out = io.Discard
	}
	s.view = report.NewPretty(out, stderr, report.PrettyOptions{
		Plain: plain, Quiet: p.o.quiet, Progress: s.tty && s.live && !p.o.verbose, Verbose: p.o.verbose,
	})
	return s
}

func (s *session) fail(err error) int {
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(s.stderr, prefix(), "canceled")
		return exitCanceled
	}
	fmt.Fprintln(s.stderr, prefix(), named(err.Error()))
	var ue cli.UsageError
	if errors.As(err, &ue) {
		return exitUsage
	}
	return exitError
}

// site returns the one site snapshot or run checks: the argument, or the
// config file's site when there is none.
func (s *session) site(cmd, example string) (string, error) {
	switch {
	case len(s.args) == 1:
		return s.args[0], target(s.args[0])
	case len(s.args) == 0 && s.cfg.Site != "":
		s.siteFrom = s.configPath
		return s.cfg.Site, nil // Validate checked it
	}
	return "", cli.Usagef("%[1]s needs one site\n  try: %[2]s %[1]s %[3]s", cmd, cmdName(), example)
}

// untrusted reports whether target is the config file's site on another
// machine. Nothing from the environment goes there: a pull request could
// point site at its own server and collect the test password, the
// headers' secrets or what flows type.
func (s *session) untrusted(target string) bool {
	if s.siteFrom == "" {
		return false
	}
	u, _, err := ratelimit.ParseTarget(target)
	return err != nil || !config.IsLoopback(u.Hostname())
}

// start prints the header, the effective settings, the production guard
// and the one-time notice, then looks for a new release in the background.
func (s *session) start(target string) {
	if kitconfig.FirstRun("soundcheck-authorised-use") && !s.o.quiet {
		fmt.Fprint(s.stderr, "Note: check only sites you own or are authorised to test. Soundcheck browses\n"+
			"like a visitor at a polite pace, and only submits forms when you turn on the\n"+
			"submit level, never on sites listed as production. This notice is shown once.\n\n")
	}
	s.notice = update.Notice(s.ctx, &update.Updater{Repo: "RhythmShahriar/soundcheck", Binary: "rhythm-soundcheck",
		Current: version, UserAgent: s.ua, API: os.Getenv("RHYTHM_GITHUB_API")})
	s.view.Header(version, target)
	if s.siteFrom != "" {
		s.view.Step("Site", "from "+s.siteFrom+" (site)")
	}
	// What will run, after the config file, flags and (for compare) the
	// snapshot had their say, so nothing is dropped silently.
	if s.quick != "" {
		s.view.Step("Preset", s.quick)
	}
	s.view.Step("Modules", strings.Join(s.cfg.Modules, ", "))
	s.view.Step("Levels", levels(s.cfg))
	s.view.Step("Viewports", strings.Join(s.cfg.Viewports, ", "))
	s.view.Step("Production", production(s.cfg))
}

// levels describes the interaction levels for the run header.
func levels(cfg config.Config) string {
	switch {
	case !cfg.Has("interact"):
		return "none: the interact module is off"
	case len(cfg.Interactions.Levels) == 0:
		return "none"
	}
	return strings.Join(cfg.Interactions.Levels, ", ")
}

// production describes the production guard for the run header, so a
// user can see an entry matched what they meant.
func production(cfg config.Config) string {
	sites := cfg.ProductionSites()
	if len(sites) == 0 {
		return "none listed: submit and flows run on any site"
	}
	return strings.Join(sites, ", ") + ": forms are not submitted there"
}

// connectEcho checks once that Echo is running, when it's on. Echo not
// answering is a usage error: the user asked for it, and a run without it
// would silently record no messages.
func (s *session) connectEcho() error {
	if !s.cfg.Has("echo") || s.echo != nil {
		return nil
	}
	link, err := echolink.Connect(s.ctx, s.cfg.Echo, s.echoToken())
	if err != nil {
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		return cli.UsageError{Msg: err.Error()}
	}
	s.echo = link
	what := "recording what each form submit and flow sends"
	if !s.cfg.Level("submit") && !s.cfg.Level("flows") {
		what = "on, but forms are only submitted with --level submit (and flows with flows)"
	}
	s.view.Step("Echo", fmt.Sprintf("%s, %s mode: %s", link.URL, link.Mode, what))
	return nil
}

// checkFlows names the environment variables each flow will read (once,
// before any runs) and refuses a flow that reads one it may not.
// Unreadable flow files are left to the interact module, which reports
// them as issues.
func (s *session) checkFlows(target string, loggingIn bool) error {
	if s.flowsChecked {
		return nil
	}
	s.flowsChecked = true
	var flows []flow.Flow
	if s.cfg.Has("interact") && s.cfg.Level("flows") && s.cfg.Flows != "" {
		flows, _ = flow.Load(s.cfg.Flows)
	}
	if loggingIn && s.cfg.Login.Flow != "" {
		if f, err := flow.ReadFile(s.cfg.Login.Flow); err == nil {
			flows = append(flows, f)
		}
	}
	for _, f := range flows {
		names := f.EnvNames()
		if len(names) == 0 {
			continue
		}
		s.view.Step("Flows", fmt.Sprintf("%q reads %s", f.Name, strings.Join(names, ", ")))
		if s.untrusted(target) {
			return cli.Usagef("%s: not letting it read %s on %s: the site came from %s (site); name it on the command line",
				f.File, strings.Join(names, ", "), target, s.siteFrom)
		}
		if err := f.CheckEnv(s.cfg.Trust.AllowEnv); err != nil {
			return cli.UsageError{Msg: fmt.Sprintf("%s: %v", f.File, err)}
		}
	}
	return nil
}

// echoToken returns the token to send Echo. When the project's config
// alone turned Echo on, it says so on stderr and keeps the token on this
// machine: a committed echo.url must not be able to collect it.
func (s *session) echoToken() string {
	token := ""
	if s.cfg.Echo.TokenEnv != "" {
		token = os.Getenv(s.cfg.Echo.TokenEnv)
	}
	if s.echoFrom != "config" {
		return token
	}
	fmt.Fprintf(s.stderr, "Echo turned on by %s (echo.url)\n", s.configPath)
	if u, err := url.Parse(s.cfg.Echo.URL); token != "" && (err != nil || !config.IsLoopback(u.Hostname())) {
		fmt.Fprintf(s.stderr, "%s not sending %s to %s: the address came from the config; pass --echo=%s to send it\n",
			prefix(), s.cfg.Echo.TokenEnv, s.cfg.Echo.URL, s.cfg.Echo.URL)
		return ""
	}
	return token
}

// check runs the site once.
func (s *session) check(target string, paths []string, snapshot bool, name, dir string) (*model.Run, error) {
	login, who := loginFunc(s.cfg, s.o.noLogin)
	if s.untrusted(target) {
		if len(s.envHeaders) > 0 {
			return nil, cli.Usagef("not sending the headers %s to %s: the site came from %s (site); name it on the command line to send them",
				strings.Join(s.envHeaders, ", "), target, s.siteFrom)
		}
		if login != nil {
			fmt.Fprintf(s.stderr, "%s not logging in to %s: the site came from %s (site); name it on the command line to log in\n",
				prefix(), target, s.siteFrom)
			login, who = nil, ""
		}
	}
	if err := s.checkFlows(target, login != nil); err != nil {
		return nil, err
	}
	if err := s.connectEcho(); err != nil {
		return nil, err
	}
	if s.o.verbose {
		s.view.Step("Browser", s.browserInfo())
	}
	if who != "" {
		s.view.Step("Login", "logging in as "+who+" (from SOUNDCHECK_USER) for the leak test and login flows")
	}
	opts := runner.Options{
		Config: s.cfg, Target: target, Paths: paths, Snapshot: snapshot, Name: name, Dir: dir, Version: version,
		UserAgent: s.ua, Headers: s.headers, Proxy: s.proxy, Insecure: s.o.insecure, Modules: modules(s.cfg), Login: login,
		Echo: s.echo,
		Browser: browser.Options{Bin: s.o.browser, NoDownload: s.o.noDownload, Proxy: s.o.proxy, Insecure: s.o.insecure,
			Visible: s.o.show, Log: func(msg string) { s.view.Log("!" + msg) }},
		Hooks: runner.Hooks{Discovered: s.view.Discovered, Page: s.view.Page, Log: s.view.Log},
	}
	run, err := runner.Run(s.ctx, opts)
	if s.o.verbose && run != nil {
		s.view.Step("Timing", timing(run))
	}
	// A snapshot runs Lighthouse once it's saved under its own name.
	if run != nil && s.o.lighthouse && !snapshot && s.ctx.Err() == nil {
		s.lighthouse(run.Target, dir)
	}
	return run, err
}

// browserInfo names the browser a run will use, for -v. It never
// downloads: the run itself does that and says so.
func (s *session) browserInfo() string {
	bin, downloaded, err := browser.Find(browser.Options{Bin: s.o.browser, NoDownload: true})
	switch {
	case err != nil && s.o.noDownload:
		return "none found"
	case err != nil:
		return "none installed; Chromium is downloaded once, now"
	case downloaded:
		return bin + " (the Chromium Soundcheck downloaded)"
	}
	return bin
}

// timing sums up how long a run took and its slowest pages, for -v.
func timing(run *model.Run) string {
	ms := func(v float64) string {
		return time.Duration(v * float64(time.Millisecond)).Round(10 * time.Millisecond).String()
	}
	line := fmt.Sprintf("%s in %s", plural(len(run.Pages), "page"), ms(run.Duration))
	pages := slices.Clone(run.Pages)
	slices.SortStableFunc(pages, func(a, b model.Page) int { return cmp.Compare(b.Duration, a.Duration) })
	var slow []string
	for _, p := range pages[:min(3, len(pages))] {
		slow = append(slow, p.Path+" "+ms(p.Duration))
	}
	if len(slow) > 0 {
		line += "; slowest " + strings.Join(slow, ", ")
	}
	return line
}

// render writes res in one output format.
func (s *session) render(w io.Writer, res report.Result, format string) error {
	switch format {
	case "json":
		return report.JSON(w, res)
	case "md":
		return report.Markdown(w, res)
	case "junit":
		return report.JUnit(w, res, s.o.failOn)
	}
	return report.HTML(w, res)
}

// finish saves the HTML report, writes the chosen formats, prints the
// summary and picks the exit code.
func (s *session) finish(res report.Result, hint string) int {
	html := filepath.Join(res.Dir, "report.html")
	if err := writeFile(html, func(w io.Writer) error { return report.HTML(w, res) }); err != nil {
		fmt.Fprintln(s.stderr, prefix(), "writing the HTML report:", err)
		html = ""
	}
	if s.format != "" {
		if err := s.render(s.stdout, res, s.format); err != nil {
			fmt.Fprintln(s.stderr, prefix(), "writing the report:", err)
			return exitError
		}
	}
	// Every file is attempted, so one bad path doesn't lose the others.
	var written []string
	for _, out := range s.outputs {
		if err := writeFile(out.path, func(w io.Writer) error { return s.render(w, res, out.format) }); err != nil {
			fmt.Fprintf(s.stderr, "%s writing %s: %v\n", prefix(), out.path, err)
			continue
		}
		written = append(written, out.path)
	}
	open := html
	for _, out := range s.outputs {
		if out.format == "html" && slices.Contains(written, out.path) {
			open = out.path
			break
		}
	}
	if hint == "" && open != "" && !s.noReportHint {
		hint = "full report: " + rel(open)
	}
	if s.live {
		s.view.Summary(res, hint)
	}
	if !s.o.quiet {
		for _, path := range written {
			fmt.Fprintf(s.stderr, "Report written to %s\n", path)
		}
	}
	if tip := s.failOnTip(res); tip != "" {
		fmt.Fprintln(s.stderr, faint.Render(tip))
	}
	if s.o.open && open != "" {
		if err := openFile(open); err != nil {
			fmt.Fprintln(s.stderr, prefix(), "opening the report:", err, "\n  open it yourself:", open)
		}
	}
	if s.notice != nil && s.live && !s.o.quiet {
		select {
		case msg, ok := <-s.notice:
			if ok {
				fmt.Fprintln(s.stderr, msg)
			}
		case <-time.After(200 * time.Millisecond): // don't hold up the exit for it
		}
	}
	if s.ctx.Err() != nil {
		fmt.Fprintln(s.stderr, prefix(), "canceled; results are partial")
		return exitCanceled
	}
	if len(written) < len(s.outputs) {
		return exitError
	}
	if report.FailOn(res, s.o.failOn) {
		return exitFindings
	}
	return exitOK
}

var faint = lipgloss.NewStyle().Faint(true)

// failOnTip teaches the CI path after a comparison that found something
// to fix: without --fail-on it still exits 0, which a script reads as
// fine. It stays quiet with -q, when machine output owns stdout and when
// --fail-on or fail_on was given, even as none.
func (s *session) failOnTip(res report.Result) string {
	if !res.Compared() || s.failOnSet || s.o.quiet || !s.live {
		return ""
	}
	class := model.Broke
	switch {
	case res.Comparison.Count(model.Broke) > 0:
	case res.Comparison.Count(model.New) > 0:
		class = model.New // --fail-on broke wouldn't catch it
	default:
		return ""
	}
	return fmt.Sprintf("tip: add --fail-on %s to make this exit 1 in scripts", class)
}

func cmdSnapshot(s *session) int {
	site, err := s.site("snapshot", "staging.example.com")
	if err != nil {
		return s.fail(err)
	}
	if err := cmp.Or(s.failOn(false), s.notStrict()); err != nil {
		return s.fail(err)
	}
	name := cmp.Or(s.o.name, store.Stamp(time.Now()))
	dir, err := s.st.NewDir(store.Incoming, name)
	if err != nil {
		return s.fail(err)
	}
	defer func() { _ = os.RemoveAll(dir) }() //nolint:gosec // our own incoming folder; gone once promoted, cleans up a failed snapshot
	s.start(site)
	s.view.Step("Snapshot", fmt.Sprintf("%q", name))
	run, err := s.check(site, nil, true, name, dir)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(s.stderr, prefix(), "snapshot canceled; nothing was saved")
			return exitCanceled
		}
		return s.fail(err)
	}
	if err := store.SaveRun(dir, run); err != nil {
		return s.fail(err)
	}
	final, err := s.st.Promote(dir, name)
	if err == nil {
		err = s.st.SetLatest(name)
	}
	if err != nil {
		return s.fail(err)
	}
	_ = os.Remove(filepath.Dir(dir)) //nolint:gosec // our own .incoming folder, removed only when empty
	s.st.ExcludeFromGit()
	if s.o.lighthouse && s.ctx.Err() == nil {
		s.lighthouse(run.Target, final)
	}
	return s.finish(report.Result{Run: run, Dir: final},
		"next: make your change (update a plugin, deploy…), then run: "+cmdName()+" compare")
}

func cmdCompare(s *session) int {
	if s.o.against != "" {
		return cmdAgainst(s)
	}
	if len(s.args) > 1 {
		return s.fail(cli.Usagef("compare takes at most one site (default: the snapshot's)"))
	}
	if err := s.failOn(true); err != nil {
		return s.fail(err)
	}
	snapDir, err := s.st.Snapshot(s.o.snapshot)
	if err != nil {
		return s.fail(cli.UsageError{Msg: err.Error()})
	}
	before, err := store.LoadRun(snapDir)
	if err != nil {
		return s.fail(err)
	}
	site := before.Target
	if len(s.args) == 1 {
		if err := target(s.args[0]); err != nil {
			return s.fail(err)
		}
		site = s.args[0]
	}
	changes := inherit(&s.cfg, before.Options, s.chosen, s.o.skip)
	if len(s.cfg.Modules) == 0 {
		return s.fail(cli.Usagef("no modules left to run: the snapshot ran %s", list(before.Options.Modules)))
	}
	if s.o.strict && len(changes) > 0 {
		return s.fail(cli.Usagef("--strict: this comparison wouldn't use snapshot %q's settings: %s", before.Name, joinChanges(changes)))
	}
	dir, err := s.st.NewDir("runs", store.Stamp(time.Now()))
	if err != nil {
		return s.fail(err)
	}
	s.start(site)
	s.view.Step("Snapshot", fmt.Sprintf("%q, %s, taken %s", before.Name, plural(len(before.Pages), "page"), report.Ago(before.Started)))
	if len(before.Options.Modules)+len(before.Options.Levels)+len(before.Options.Viewports) > 0 {
		s.view.Step("Settings", snapshotSettings(before.Name, before.Options))
	}
	if len(changes) > 0 {
		s.view.Step("You changed", joinChanges(changes))
	}
	return s.compareWith(before, snapDir, site, dir)
}

// cmdAgainst compares two sites now: the --against site is checked first
// and plays the snapshot's part.
func cmdAgainst(s *session) int {
	if s.o.against == "" {
		return s.fail(cli.Usagef("to check a site once: %[1]s run %[2]s\n  to compare two sites: %[1]s <url> --against <url>", cmdName(), strings.Join(s.args, " ")))
	}
	if len(s.args) != 1 {
		return s.fail(cli.Usagef("--against compares one site with another: %s staging.example.com --against example.com", cmdName()))
	}
	if err := cmp.Or(target(s.args[0]), target(s.o.against), s.failOn(true), s.notStrict()); err != nil {
		return s.fail(err)
	}
	dir, err := s.st.NewDir("runs", store.Stamp(time.Now()))
	if err != nil {
		return s.fail(err)
	}
	beforeDir := filepath.Join(dir, "against")
	s.start(s.args[0])
	s.view.Step("Against", s.o.against)
	before, err := s.check(s.o.against, nil, true, "", beforeDir)
	if err != nil {
		return s.fail(fmt.Errorf("%s: %w", s.o.against, err))
	}
	if err := store.SaveRun(beforeDir, before); err != nil {
		return s.fail(err)
	}
	s.view.Step("Now", s.args[0])
	return s.compareWith(before, beforeDir, s.args[0], dir)
}

// compareWith checks site again on before's pages and compares.
func (s *session) compareWith(before *model.Run, beforeDir, site, dir string) int {
	var paths []string
	for _, p := range before.Pages {
		paths = append(paths, p.Path)
	}
	after, err := s.check(site, paths, false, "", dir)
	if err != nil && after == nil {
		return s.fail(err)
	}
	if err := store.SaveRun(dir, after); err != nil {
		return s.fail(err)
	}
	cc := &check.CompareContext{Config: s.cfg, Before: before, After: after, BeforeDir: beforeDir, AfterDir: dir,
		Dir: dir, Save: store.Saver(dir)}
	c := compare.Compare(context.WithoutCancel(s.ctx), cc, modules(s.cfg), version)
	if err := store.SaveComparison(dir, c); err != nil {
		return s.fail(err)
	}
	s.st.ExcludeFromGit()
	if err := s.st.Prune(keepRuns); err != nil {
		fmt.Fprintln(s.stderr, prefix(), "pruning old runs:", err)
	}
	return s.finish(report.Result{Run: after, Comparison: c, Dir: dir}, "")
}

func cmdRun(s *session) int {
	site, err := s.site("run", "example.test")
	if err != nil {
		return s.fail(err)
	}
	if err := cmp.Or(s.failOn(false), s.notStrict()); err != nil {
		return s.fail(err)
	}
	dir, err := s.st.NewDir("runs", store.Stamp(time.Now()))
	if err != nil {
		return s.fail(err)
	}
	s.start(site)
	run, err := s.check(site, nil, false, "", dir)
	if err != nil && run == nil {
		return s.fail(err)
	}
	if err := store.SaveRun(dir, run); err != nil {
		return s.fail(err)
	}
	s.st.ExcludeFromGit()
	if err := s.st.Prune(keepRuns); err != nil {
		fmt.Fprintln(s.stderr, prefix(), "pruning old runs:", err)
	}
	return s.finish(report.Result{Run: run, Dir: dir}, "")
}

// cmdReport shows a stored result again: the latest run, a run folder,
// or a snapshot by name.
func cmdReport(s *session) int {
	if len(s.args) > 1 {
		return s.fail(cli.Usagef("report takes at most one run folder or snapshot name"))
	}
	var dir string
	var err error
	switch {
	case len(s.args) == 0:
		if dir, err = s.st.LatestRun(); err != nil {
			err = cli.UsageError{Msg: err.Error()}
		}
	default:
		// Naming something that isn't there is a usage error (2), like no
		// argument with nothing stored: CI reads 3 as "the site is down".
		// A run.json that can't be read stays 3.
		if fi, statErr := os.Stat(s.args[0]); statErr == nil && fi.IsDir() { //nolint:gosec // the user names a run folder to show
			dir = s.args[0]
			if _, statErr := os.Stat(filepath.Join(dir, "run.json")); errors.Is(statErr, fs.ErrNotExist) { //nolint:gosec // as above
				err = cli.Usagef("no run in %s; name a run folder or a snapshot (see %s snapshots)", dir, cmdName())
			}
		} else if dir, err = s.st.Snapshot(s.args[0]); err != nil {
			err = cli.UsageError{Msg: err.Error()} // not found, or not a valid name
		}
	}
	if err != nil {
		return s.fail(err)
	}
	run, err := store.LoadRun(dir)
	if err != nil {
		return s.fail(err)
	}
	res := report.Result{Run: run, Dir: dir}
	if c, err := store.LoadComparison(dir); err == nil {
		res.Comparison = c
	}
	if err := s.failOn(res.Compared()); err != nil {
		return s.fail(err)
	}
	s.view.Header(version, run.Target)
	return s.finish(res, "")
}

// cmdSnapshots lists snapshots, or deletes one.
func cmdSnapshots(args []string, stdout, stderr io.Writer) int {
	st := store.Store{Root: config.Dir}
	if len(args) == 2 && (args[0] == "delete" || args[0] == "rm") {
		dir, err := st.Snapshot(args[1])
		if err != nil {
			fmt.Fprintln(stderr, prefix(), named(err.Error()))
			return exitUsage
		}
		if err := os.RemoveAll(dir); err != nil { //nolint:gosec // st.Snapshot validated the name
			fmt.Fprintln(stderr, prefix(), named(err.Error()))
			return exitError
		}
		fmt.Fprintln(stdout, "Deleted snapshot", args[1])
		return exitOK
	}
	asJSON := len(args) == 1 && (args[0] == "--json" || args[0] == "-json")
	if len(args) > 0 && !asJSON {
		fmt.Fprintln(stderr, prefix(), "usage:", cmdName(), "snapshots [--json | delete <name>]")
		return exitUsage
	}
	names, err := st.Snapshots()
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	if asJSON {
		if err := snapshotsJSON(stdout, st, names); err != nil {
			fmt.Fprintln(stderr, prefix(), err)
			return exitError
		}
		return exitOK
	}
	if len(names) == 0 {
		fmt.Fprintln(stdout, "No snapshots yet. Take one:", cmdName(), "snapshot <url>")
		return exitOK
	}
	latest, _ := st.Snapshot("")
	fmt.Fprintf(stdout, "%-24s %-6s %-12s %s\n", "NAME", "PAGES", "TAKEN", "SITE")
	for _, n := range names {
		dir := filepath.Join(st.Root, "snapshots", n)
		r, err := store.LoadRun(dir)
		if err != nil {
			fmt.Fprintf(stdout, "%-24s %s\n", n, err)
			continue
		}
		mark := ""
		if dir == latest {
			mark = "  (latest)"
		}
		fmt.Fprintf(stdout, "%-24s %-6d %-12s %s%s\n", n, len(r.Pages), report.Ago(r.Started), r.Target, mark)
	}
	return exitOK
}

// snapshotEntry is one snapshot in `snapshots --json`.
type snapshotEntry struct {
	Name    string `json:"name"`
	Created string `json:"created,omitempty"` // UTC, RFC 3339
	Site    string `json:"site,omitempty"`
	Pages   int    `json:"pages"`
	Version string `json:"version,omitempty"` // the Soundcheck that took it
	Latest  bool   `json:"latest"`            // what compare uses by default
	Error   string `json:"error,omitempty"`   // run.json couldn't be read
}

// snapshotsJSON prints the snapshots as a JSON array, oldest first, for
// scripts: [] when there are none, never null.
func snapshotsJSON(w io.Writer, st store.Store, names []string) error {
	latest, _ := st.Snapshot("")
	out := make([]snapshotEntry, 0, len(names))
	for _, n := range names {
		dir := filepath.Join(st.Root, "snapshots", n)
		e := snapshotEntry{Name: n, Latest: dir == latest}
		if r, err := store.LoadRun(dir); err != nil {
			e.Error = err.Error()
		} else {
			e.Created, e.Site, e.Pages, e.Version = r.Started.UTC().Format(time.RFC3339), r.Target, len(r.Pages), r.Version
		}
		out = append(out, e)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// cmdUpdate installs the browser if needed, removes older downloaded
// revisions, or with --clear deletes the download. `rhythm update` runs
// it after updating the binary.
func cmdUpdate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	clear := fs.Bool("clear", false, "delete the downloaded Chromium")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	dir, err := browser.DownloadDir()
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	if *clear {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintln(stderr, prefix(), named(err.Error()))
			return exitError
		}
		fmt.Fprintln(stdout, "Cleared", dir)
		return exitOK
	}
	bin, downloaded, err := browser.Find(browser.Options{Log: func(m string) { fmt.Fprintln(stdout, m) }})
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	removed, err := browser.Prune(!downloaded)
	for _, r := range removed {
		fmt.Fprintln(stdout, "Removed unused Chromium", r)
	}
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
	}
	if !downloaded {
		fmt.Fprintln(stdout, "Browser: installed Chrome at", bin, "(updated with your system)")
	} else {
		fmt.Fprintln(stdout, "Browser: Chromium at", bin)
	}
	return exitOK
}

// writeFile writes a report atomically enough for CI: the file only
// appears once complete.
func writeFile(path string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".report-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // a report meant to be read, attached and served
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func openFile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// The commands are fixed; the argument is the report we just wrote.
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", abs) //nolint:gosec // see above
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", abs) //nolint:gosec // see above
	default:
		cmd = exec.Command("xdg-open", abs) //nolint:gosec // see above
	}
	return cmd.Start()
}

// rel shows a path relative to the working directory when it's inside it.
func rel(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if r, err := filepath.Rel(wd, abs); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func validName(s string) bool { return store.ValidName(s) }
