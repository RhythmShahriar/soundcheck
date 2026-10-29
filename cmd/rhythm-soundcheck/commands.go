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
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	kitconfig "github.com/RhythmShahriar/rhythm/kit/config"
	"github.com/RhythmShahriar/rhythm/kit/httpx"
	"github.com/RhythmShahriar/rhythm/kit/update"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/history"
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
	notice         *update.Pending
	echo           *echolink.Link // connected on the first check, when Echo is on
	flowsChecked   bool           // checkFlows ran
	loginShown     bool           // the Login line was printed
	resume         *resumed       // snapshot --resume: the pages kept and the journal
	checked        bool           // the command checked a site, so --eaa writes its files
	discovered     atomic.Int64   // the pages the last check set out to visit, for an interrupted --eaa run
	chrome         string         // the browser's version, from runner.Hooks.Browser, for the evidence pack
	compareNotes   []string       // notes about the snapshot for the comparison's warnings
	widgetPages    []string       // the pages the earlier run tested widgets on, for the next check (runner.Options.WidgetPages)
}

// columns is the width of the terminal w writes to, for the text the
// report wraps; 0 (the report's default of 80) when w isn't a terminal
// or its size can't be read.
func columns(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return 0
	}
	width, _, err := term.GetSize(f.Fd())
	if err != nil || width <= 0 {
		return 0
	}
	return width
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
	accept := cmdName() + " accept"
	if p.noAcceptHint {
		accept = ""
	}
	s.view = report.NewPretty(out, stderr, report.PrettyOptions{
		Plain: plain, Quiet: p.o.quiet, Progress: s.tty && s.live && !p.o.verbose, Verbose: p.o.verbose,
		Columns: columns(stdout),
		// The header names the command the same way the hints do,
		// with cmdName's filter on RHYTHM_INVOKED_AS.
		Name:   cmdName(),
		Accept: accept,
	})
	return s
}

func (s *session) fail(err error) int {
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(s.stderr, prefix(), "canceled")
		return exitCanceled
	}
	fmt.Fprintln(s.stderr, prefix(), named(s.maskURLs(err.Error())))
	var ue cli.UsageError
	if errors.As(err, &ue) {
		return exitUsage
	}
	return exitError
}

// urlInText matches a URL in a message, such as the one net/http quotes
// in a "can't reach" error.
var urlInText = regexp.MustCompile(`https?://[^\s"'<>]+`)

// maskURLs masks every URL in msg the way an --eaa run shows its start
// URL (echolink.RedactTarget), so an error doesn't print the path token
// or query string the header masked. Without --eaa msg is unchanged.
func (s *session) maskURLs(msg string) string {
	if !s.cfg.EAA {
		return msg
	}
	return urlInText.ReplaceAllStringFunc(msg, func(u string) string {
		// Punctuation that ends a sentence stays after the URL.
		trimmed := strings.TrimRight(u, ".,:;)")
		return echolink.RedactTarget(trimmed) + u[len(trimmed):]
	})
}

// shownTarget is a site URL as the terminal shows it: masked under --eaa
// like the header (security M3), as typed otherwise.
func (s *session) shownTarget(u string) string {
	if s.cfg.EAA {
		return echolink.RedactTarget(u)
	}
	return u
}

// site returns the one site snapshot or run checks: the argument, or the
// config file's site when there is none.
func (s *session) site(cmd, example string) (string, error) {
	switch {
	case len(s.args) == 1:
		return s.args[0], target(s.args[0])
	case len(s.args) == 0 && s.cfg.Site != "":
		s.siteFrom, s.siteKey = s.configPath, "site"
		return s.cfg.Site, nil // Validate checked it
	}
	return "", cli.Usagef("%[1]s needs one site\n  try: %[2]s %[1]s %[3]s", cmd, cmdName(), example)
}

// untrusted reports whether target is the config file's site, or the
// target stored in a snapshot's run.json, on another machine: one the
// user didn't name. Nothing from the environment or the command line's
// -H and --cookie goes there: a pull request could point site at its own
// server (or commit a snapshot that does) and collect the test password,
// the headers' secrets or what flows type.
func (s *session) untrusted(target string) bool {
	if s.siteFrom == "" || s.siteTrusted {
		return false
	}
	u, _, err := httpx.ParseTarget(target)
	return err != nil || !config.IsLoopback(u.Hostname())
}

// gitTracked reports whether git tracks path: it was committed, so it
// came with the project rather than from a run on this machine. Without
// git, or outside a repository, nothing is tracked.
func gitTracked(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--error-unmatch", "--", path) //nolint:gosec // fixed program; the path is an argument
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run() == nil
}

// start prints the header, the effective settings, the production guard
// and the one-time notice, then looks for a new release in the background.
func (s *session) start(target string) {
	if kitconfig.FirstRun("soundcheck-authorised-use") && !s.o.quiet {
		fmt.Fprint(s.stderr, "Note: check only sites you own or are authorised to test. Soundcheck browses\n"+
			"like a visitor at a polite pace. It only submits forms when you turn on the\n"+
			"submit level, never on sites listed as production. This notice is shown once.\n\n")
	}
	s.notice = update.StartCheck(s.ctx, &update.Updater{Repo: "RhythmShahriar/soundcheck", Binary: "rhythm-soundcheck",
		Current: version, UserAgent: s.ua, API: os.Getenv("RHYTHM_GITHUB_API")})
	// An --eaa run masks the start URL in every output (security M3).
	s.view.Header(shownVersion(), s.shownTarget(target))
	if s.siteFrom != "" {
		s.view.Step("Site", "from "+s.siteFrom+" ("+s.siteKey+")")
	}
	// What will run, after the config file, flags and (for compare) the
	// snapshot had their say, so nothing is dropped silently.
	if s.quick != "" {
		s.view.Step("Preset", s.quick)
	}
	if h := s.eaaHeader(); h != "" {
		s.view.Step("Preset", h)
	}
	s.view.Step("Modules", strings.Join(s.cfg.Modules, ", "))
	s.eaaSteps()
	s.view.Step("Levels", levels(s.cfg))
	s.view.Step("Viewports", strings.Join(s.cfg.Viewports, ", "))
	s.view.Step("Production", production(s.cfg))
}

// levels describes the interaction levels for the run header. With the
// interact module off its levels don't apply; only the keyboard module's
// key flows still read the flows level, so --level flows reads "flows
// (key flows only; ...)" rather than "none".
func levels(cfg config.Config) string {
	switch {
	case !cfg.Has("interact") && cfg.Has("keyboard") && cfg.Level("flows"):
		return "flows (key flows only; the interact module is off)"
	case !cfg.Has("interact"):
		return "none: the interact module is off"
	case len(cfg.Interactions.Levels) == 0:
		return "none"
	}
	return strings.Join(cfg.Interactions.Levels, ", ")
}

// production describes the production guard for the run header, so a
// user can see an entry matched what they meant. With the interact
// module off only a key flow with keyboard: send could submit, only on
// a local or private address or with --staging.
func production(cfg config.Config) string {
	sites := cfg.ProductionSites()
	if len(sites) > 0 {
		return strings.Join(sites, ", ") + ": forms are not submitted there"
	}
	switch {
	case cfg.Has("interact"):
		return "none listed: submit and flows run on any site"
	case cfg.Has("keyboard") && cfg.Level("flows"):
		return "none listed: only key flows with keyboard: send submit, on a local or private address or with --staging"
	}
	return "none listed: nothing in this run submits forms"
}

// connectEcho checks once that Echo is running, when it's on. Echo not
// answering is a usage error: the user asked for it. A run without it
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
		what = "on; forms are only submitted with --level submit (and flows with flows)"
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
			return cli.Usagef("%s: not letting it read %s on %s: the site came from %s (%s); name it on the command line",
				f.File, strings.Join(names, ", "), target, s.siteFrom, s.siteKey)
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

// runSite checks a site; tests stand in for the browser with their own.
var runSite = runner.Run

// check runs the site once.
func (s *session) check(target string, paths []string, snapshot bool, name, dir string) (*model.Run, error) {
	opts, err := s.options(target, paths, snapshot, name, dir)
	if err != nil {
		return nil, err
	}
	if s.resume != nil {
		opts.Done, opts.OnPage = s.resume.done, s.resume.add
	}
	run, err := runSite(s.ctx, opts)
	s.checked = true
	s.recordEAA(run)
	if s.o.verbose && run != nil {
		shown := run
		if run.Options.EAA {
			shown = report.Masked(report.Result{Run: run}).Run // the paths' tokens masked
		}
		s.view.Step("Timing", timing(shown))
	}
	// A snapshot runs Lighthouse once it's saved under its own name.
	if run != nil && s.o.lighthouse && !snapshot && s.ctx.Err() == nil {
		s.lighthouse(run.Target, dir)
	}
	return run, err
}

// options builds the runner's options for one check of target, after
// the trust checks: what may be sent to a site the user didn't name.
func (s *session) options(target string, paths []string, snapshot bool, name, dir string) (runner.Options, error) {
	login, who := loginFunc(s.cfg, s.o.noLogin)
	if s.untrusted(target) {
		if len(s.envHeaders) > 0 {
			return runner.Options{}, cli.Usagef("not sending the headers %s to %s: the site came from %s (%s); name it on the command line to send them",
				strings.Join(s.envHeaders, ", "), target, s.siteFrom, s.siteKey)
		}
		if flags := s.headerFlags(); flags != "" {
			return runner.Options{}, cli.Usagef("not sending %s to %s: the site came from %s (%s); name it on the command line to send them",
				flags, target, s.siteFrom, s.siteKey)
		}
		if login != nil {
			fmt.Fprintf(s.stderr, "%s not logging in to %s: the site came from %s (%s); name it on the command line to log in\n",
				prefix(), target, s.siteFrom, s.siteKey)
			login, who = nil, ""
		}
	}
	if err := s.checkFlows(target, login != nil); err != nil {
		return runner.Options{}, err
	}
	if err := s.connectEcho(); err != nil {
		return runner.Options{}, err
	}
	if s.o.verbose {
		s.view.Step("Browser", s.browserInfo())
	}
	if who != "" && !s.loginShown {
		s.loginShown = true
		s.view.Step("Login", "logging in as "+who+" (from SOUNDCHECK_USER) for the leak test and login flows")
	}
	return runner.Options{
		Config: s.cfg, Target: target, Paths: paths, Snapshot: snapshot, Name: name, Dir: dir, Version: shownVersion(),
		EAA: s.cfg.EAA, WCAG: s.wcag, Keyboard: s.runKeyboard(), WidgetPages: s.widgetPages,
		UserAgent: s.ua, Headers: s.headers, Proxy: s.proxy, Insecure: s.o.insecure, Modules: s.modules(s.cfg), Login: login,
		Echo: s.echo, Engine: s.engine, FollowHost: s.o.followHost,
		Browser: browser.Options{Bin: s.bin, NoDownload: s.o.noDownload, NoSandbox: s.o.noSandbox, Proxy: s.o.proxy, Insecure: s.o.insecure,
			Visible: s.o.show, Log: browserLog(s.view.Log)},
		Hooks: runner.Hooks{Discovered: s.discover, Skipped: s.view.Skipped, Page: s.pageView(), Log: s.view.Log, Progress: s.view.Progress,
			Browser: func(v string) { s.chrome = v }},
	}, nil
}

// pageView is the live view's page hook. Under --eaa it shows each
// page with the tokens in its path masked, as every report does.
func (s *session) pageView() func(*model.Page, int, int) {
	if !s.cfg.EAA {
		return s.view.Page
	}
	return func(p *model.Page, done, total int) { s.view.Page(report.MaskedPage(p), done, total) }
}

// discover notes how many pages the check will visit and shows it
// (runner.Hooks.Discovered).
func (s *session) discover(pages int, skipped map[string]int) {
	s.discovered.Store(int64(pages))
	s.view.Discovered(pages, skipped)
}

// browserInfo names the browser a run will use, for -v. It never
// downloads: the run itself does that and says so.
func (s *session) browserInfo() string {
	bin, downloaded, err := browser.Find(browser.Options{Bin: s.o.browser, NoDownload: true})
	switch {
	case err != nil && s.o.noDownload:
		return "none found"
	case err != nil:
		return "none installed; Chrome for Testing is downloaded once, now"
	case downloaded:
		return bin + " (the Chrome for Testing Soundcheck downloaded)"
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
// summary and picks the exit code. next is an optional last hint, such
// as what to run after a snapshot; it follows the "full report:" line.
func (s *session) finish(res report.Result, next string) int {
	res.FailOn = s.o.failOn // JSON's summary.failed
	res.Trend = s.trend(res.Run)
	if report.Partial(res) && res.Planned == 0 {
		res.Planned = int(s.discovered.Load()) // pages the check set out to visit
	}
	draft := s.eaaResult(&res)
	// What the formats show: an --eaa result with its paths' tokens
	// masked. The stored run keeps them; compare visits them again.
	res = report.Masked(res)
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
	var hint string
	if open != "" && !s.noReportHint {
		hint = "full report: " + rel(open)
	}
	if s.noReportHint {
		draft = "" // the demo removes the folder the draft is in
	}
	if s.live {
		s.view.Summary(res, hint, draft, next)
	}
	if !s.o.quiet {
		for _, path := range written {
			fmt.Fprintf(s.stderr, "Report written to %s\n", path)
		}
	}
	if tip := s.failOnTip(res); tip != "" {
		fmt.Fprintln(s.stderr, faint.Render(tip))
	}
	if tip := s.eaaTip(); tip != "" {
		fmt.Fprintln(s.stderr, faint.Render(tip))
	}
	if s.o.open && open != "" {
		if err := openFile(open); err != nil {
			fmt.Fprintln(s.stderr, prefix(), "opening the report:", err, "\n  open it yourself:", open)
		}
	}
	// Every browser is closed by now (runner.Run and Revisit close theirs
	// before they return), so a "y" doesn't race Chrome's shutdown.
	update.PromptAtExit(s.ctx, s.promptOptions())
	if s.ctx.Err() != nil {
		fmt.Fprintln(s.stderr, prefix(), "canceled; results are partial")
		return exitCanceled
	}
	if len(written) < len(s.outputs) {
		return exitError
	}
	// A stored result that was interrupted (report of it, also with
	// --fail-on) exits as the interrupted run did: its findings are
	// partial, so neither 0 nor 1 would be true.
	if report.Partial(res) {
		fmt.Fprintln(s.stderr, prefix(), "this result is partial (interrupted); run it again")
		return exitCanceled
	}
	// Under --eaa a page the a11y or keyboard tests didn't finish leaves
	// the evidence incomplete: 3 wins over 1, as a site that couldn't be
	// checked does.
	if n := notAudited(res.Run); n > 0 {
		fmt.Fprintln(s.stderr, prefix(), errNotAudited(n))
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
// promptOptions is how the end of a run asks about a new release: never
// when stdout carries a report for a program to read, or with -q.
func (s *session) promptOptions() update.PromptOptions {
	return update.PromptOptions{Check: s.notice, Quiet: s.o.quiet || !s.live,
		Stdout: s.stdout, Out: s.stderr}
}

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

// discard removes the folder of a check that saved nothing. It also
// removes the folders above it up to the store that it left empty, so a
// site that couldn't be checked leaves no .soundcheck behind. os.Remove
// takes only empty folders, so stored runs and snapshots stay.
func (s *session) discard(dir string) {
	_ = os.RemoveAll(dir) //nolint:gosec // a folder this command made in the store
	root := filepath.Clean(s.st.Root)
	for p := filepath.Dir(dir); strings.HasPrefix(p, root); p = filepath.Dir(p) {
		if os.Remove(p) != nil || p == root { //nolint:gosec // a parent of that folder, inside the store
			return
		}
	}
}

func cmdSnapshot(s *session) int {
	site, err := s.site("snapshot", "staging.example.com")
	if err != nil {
		return s.fail(err)
	}
	if err := cmp.Or(s.failOn(false), s.notStrict(), s.only("snapshot", "confirm", "serve", "client", "brand"), s.siteChecks(site)); err != nil {
		return s.fail(err)
	}
	if s.o.resume && s.o.name == "" {
		return s.fail(cli.Usagef("--resume carries on with a snapshot by its name: add --name, as in the snapshot that stopped"))
	}
	name := cmp.Or(s.o.name, store.Stamp(time.Now()))
	var dir string
	var done []model.Page
	if s.o.resume {
		if dir, done, err = s.st.Resumable(name); err != nil {
			return s.fail(err)
		}
		if dir != "" {
			if err := s.resumeSettings(name, dir); err != nil {
				return s.fail(err)
			}
		} else if _, err := s.st.Snapshot(name); err == nil {
			// Starting afresh would replace a finished snapshot, with
			// the settings given here rather than its own.
			return s.fail(cli.Usagef("snapshot %q already finished, so there's nothing to carry on with; drop --resume to take it again", name))
		}
	}
	if dir == "" {
		if dir, err = s.st.NewDir(store.Incoming, name); err != nil {
			return s.fail(err)
		}
	}
	// --resume carries on with the settings recorded here.
	engine := string(s.engine)
	if engine == string(capture.Chrome) {
		engine = ""
	}
	if err := store.SaveResumeOptions(dir, model.Options{Viewports: s.cfg.Viewports, Levels: s.cfg.Interactions.Levels,
		Modules: s.cfg.Modules, Engine: engine, EAA: s.cfg.EAA, WCAG: s.wcag}); err != nil {
		s.discard(dir)
		return s.fail(err)
	}
	// The pages journal lets a stopped snapshot carry on with --resume.
	journal, err := store.NewPageWriter(dir)
	if err != nil {
		s.discard(dir)
		return s.fail(err)
	}
	s.resume = &resumed{journal: journal}
	for i := range done {
		s.resume.done = append(s.resume.done, &done[i])
	}
	keep := false
	defer func() {
		_ = journal.Close()
		if !keep {
			s.discard(dir) // gone once promoted; cleans up a failed snapshot
		}
	}()
	s.start(site)
	s.view.Step("Snapshot", fmt.Sprintf("%q", name))
	if s.o.resume {
		if len(done) > 0 {
			s.view.Step("Resume", fmt.Sprintf("keeping %s the stopped snapshot finished", plural(len(done), "page")))
		} else {
			s.view.Step("Resume", fmt.Sprintf("no stopped snapshot %q to carry on with; starting afresh", name))
		}
	}
	run, err := s.check(site, nil, true, name, dir)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			if journal.Len()+len(done) > 0 {
				keep = true
				fmt.Fprintln(s.stderr, s.resumeHint(site, name, journal.Len()+len(done)))
				return exitCanceled
			}
			fmt.Fprintln(s.stderr, prefix(), "snapshot canceled; nothing was saved")
			return exitCanceled
		}
		return s.fail(err)
	}
	// Windows can't rename a folder with a file open in it.
	if err := journal.Close(); err != nil {
		return s.fail(err)
	}
	if err := store.SaveRun(dir, run); err != nil {
		return s.fail(err)
	}
	if err := s.keepReplaced(name); err != nil {
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
	s.record(run, nil)
	return s.finish(report.Result{Run: run, Dir: final},
		"next: make your change (update a plugin, deploy…), then run: "+cmdName()+" compare")
}

// keepReplaced keeps the evidence pack of the snapshot name is about to
// replace, so the chains of later --eaa runs still verify. It says so.
func (s *session) keepReplaced(name string) error {
	old, err := s.st.Snapshot(name)
	if err != nil {
		return nil // no snapshot of that name yet
	}
	kept, err := keepEvidence(old)
	if err != nil || kept == "" {
		return err
	}
	fmt.Fprintln(s.stderr, keptPackNote(name, rel(filepath.Join(s.st.Root, filepath.FromSlash(kept)))))
	return nil
}

// keptPackNote is the note of a retaken or deleted snapshot whose
// evidence pack was kept in dir. Both say it the same way.
func keptPackNote(name, dir string) string {
	return fmt.Sprintf("note: snapshot %q had an evidence pack; it is kept in %s, so evidence verify of later --eaa runs still checks their chain", name, dir)
}

// resumeHint is the line a canceled snapshot leaves on stderr: the
// command that carries on with it. resumeSettings gives that command
// the stopped snapshot's settings, so it needs no more flags.
func (s *session) resumeHint(site, name string, pages int) string {
	what := "modules, levels, viewports and browser"
	if s.cfg.EAA {
		what = "modules, levels, viewports, browser and EAA mode"
	}
	return fmt.Sprintf("%s snapshot canceled; %s kept: to carry on with the same %s, run %s snapshot %s --name %s --resume",
		prefix(), plural(pages, "finished page"), what, cmdName(), site, name)
}

// resumeSettings gives the session the modules, levels, viewports,
// browser, EAA mode and WCAG version of the stopped snapshot in dir
// where the user didn't choose them, so its pages and the new ones are
// checked alike. Settings the user chose that differ are a usage
// error: the snapshot would mix pages checked two ways. A snapshot
// stopped by an older Soundcheck recorded none and carries on with the
// settings given.
func (s *session) resumeSettings(name, dir string) error {
	prev, err := store.ResumeOptions(dir)
	if err != nil || prev == nil {
		return err
	}
	changes := inherit(&s.cfg, *prev, s.chosen, s.o.skip)
	if c := inheritEngine(&s.engine, *prev, s.chosen); c != nil {
		changes = append(changes, *c)
	}
	if err := s.resumeEAA(*prev); err != nil {
		return err
	}
	// A snapshot's pages are audited one way: half its pages without the
	// a11y and keyboard audits, or by other WCAG rules, is no evidence.
	if before, after := eaaSetting(prev.EAA, prev.WCAG), eaaSetting(s.cfg.EAA, s.wcag); before != after {
		changes = append(changes, settingChange{name: "eaa", before: []string{before}, after: []string{after}})
	}
	if len(changes) > 0 {
		return cli.Usagef("--resume: snapshot %q stopped with other settings (%s); carry on with the same ones, or leave out --resume to start afresh",
			name, joinChanges(changes))
	}
	return nil
}

// resumeEAA turns EAA mode on and takes the WCAG version of a stopped
// --eaa snapshot when neither the flags nor the config file chose them,
// with the modules and limits EAA mode brings. A choice that differs
// is left for resumeSettings to refuse.
func (s *session) resumeEAA(prev model.Options) error {
	if !prev.EAA {
		return nil
	}
	changed := false
	if !s.set["eaa"] && !s.cfg.InFile("eaa") && !s.cfg.EAA {
		s.cfg.EAA, changed = true, true
	}
	if !s.set["wcag"] && !s.cfg.InFile("a11y.wcag") && prev.WCAG != "" && s.wcag != prev.WCAG {
		s.wcag, changed = prev.WCAG, true
	}
	if !changed || !s.cfg.EAA {
		return nil
	}
	s.eaaModules()
	return cmp.Or(s.eaaSkipped(), s.eaaSettle())
}

// eaaSetting words EAA mode for a --resume refusal: "off", or "on" with
// its WCAG version.
func eaaSetting(eaa bool, wcag string) string {
	if !eaa {
		return "off"
	}
	return "on (WCAG " + cmpOr(wcag, "2.0") + ")"
}

// resumed is what snapshot --resume carries: the pages the stopped
// snapshot finished and the journal each new page is written to.
type resumed struct {
	done    []*model.Page
	journal *store.PageWriter
}

// add writes a finished page to the journal (runner.Options.OnPage).
// A page that can't be written only costs the resume, never the run.
func (r *resumed) add(p *model.Page) { _ = r.journal.Append(p) }

// record adds a finished run or comparison to the history, for trends.
// A partial result is left out: its numbers would read as a change.
func (s *session) record(run *model.Run, c *model.Comparison) {
	if run == nil || run.Interrupted || (c != nil && c.Interrupted) {
		return
	}
	if err := history.Append(s.st.Root, run, c); err != nil {
		fmt.Fprintln(s.stderr, prefix(), "history:", err)
	}
}

// trendRuns is how many of the latest history entries the HTML report's
// trend shows.
const trendRuns = 12

// trend is the median LCP of the latest runs of run's site, from the
// history, for the HTML report.
func (s *session) trend(run *model.Run) []float64 {
	if run == nil {
		return nil
	}
	entries, err := history.Read(s.st.Root)
	if err != nil {
		return nil
	}
	var out []float64
	for _, e := range entries {
		// An --eaa run's entry keeps its target masked (history.Target).
		if (e.Target == run.Target || e.Target == history.Target(run)) && e.LCP > 0 {
			out = append(out, e.LCP)
		}
	}
	return out[max(0, len(out)-trendRuns):]
}

// siteChecks are the usage errors about each site found before anything
// starts: -H Host and login.url naming another site.
func (s *session) siteChecks(sites ...string) error {
	return cmp.Or(s.hostHeader(sites...), s.loginHost(sites...))
}

// loginHost refuses a login.url on another site than each site the run
// logs in to. The password is only typed on the site being checked, so
// such a login would fail at the end of the run; it's a usage error
// (exit 2) found before the run starts instead.
func (s *session) loginHost(sites ...string) error {
	if login, _ := loginFunc(s.cfg, s.o.noLogin); login == nil {
		return nil
	}
	u, err := url.Parse(s.cfg.Login.URL)
	if err != nil || u.Host == "" {
		return nil // a path, as in the default: always on the site
	}
	for _, site := range sites {
		t, _, err := httpx.ParseTarget(site)
		if err != nil || s.untrusted(site) {
			continue // target() words that error; an untrusted site gets no login
		}
		if config.HostKey(t) != config.HostKey(u) {
			where := "login.url"
			if s.configPath != "" {
				where = s.configPath + ": login.url"
			}
			return cli.Usagef("%s is on %s, not the site being checked (%s); the password is only typed on the site being checked",
				where, config.HostKey(u), config.HostKey(t))
		}
	}
	return nil
}

// hostHeader refuses -H Host naming another host than each site: the
// browser can't send it, so that's a usage error (exit 2) found before
// anything starts, not a site that couldn't be checked.
func (s *session) hostHeader(sites ...string) error {
	for _, site := range sites {
		u, _, err := httpx.ParseTarget(site)
		if err != nil {
			continue // target() words that error
		}
		if err := capture.HostError(s.headers, u); err != nil {
			return cli.UsageError{Msg: err.Error()}
		}
	}
	return nil
}

// only refuses flags that belong to another command.
func (s *session) only(cmd string, flags ...string) error {
	for _, f := range flags {
		if s.set[f] {
			return cli.Usagef("--%s doesn't apply to %s", f, cmd)
		}
	}
	return nil
}

func cmdCompare(s *session) int {
	if s.o.against != "" {
		return cmdAgainst(s)
	}
	if len(s.args) > 1 {
		return s.fail(cli.Usagef("compare takes at most one site (default: the snapshot's)"))
	}
	if err := cmp.Or(s.failOn(true), s.only("compare", "resume", "serve", "client", "brand")); err != nil {
		return s.fail(err)
	}
	snapDir, err := s.st.Snapshot(s.o.snapshot)
	if err != nil {
		return s.fail(cli.UsageError{Msg: err.Error()})
	}
	// The snapshot's pages are read one at a time (store.OpenRun): a big
	// site's snapshot doesn't have to fit in memory next to the run.
	before, pages, err := store.OpenRun(snapDir)
	if err != nil {
		return s.fail(err)
	}
	defer func() { _ = pages.Close() }()
	site := before.Target
	if len(s.args) == 1 {
		if err := target(s.args[0]); err != nil {
			return s.fail(err)
		}
		site = s.args[0]
	} else {
		// The header says where the site came from. A snapshot committed
		// to git came with the project (a pull request can add one), so
		// its target is as untrusted as a config's site:. One this
		// machine took is the user's own.
		runJSON := rel(filepath.Join(snapDir, "run.json"))
		s.siteFrom, s.siteKey, s.siteTrusted = runJSON, "target", !gitTracked(runJSON)
	}
	if err := s.siteChecks(site); err != nil {
		return s.fail(err)
	}
	changes := inherit(&s.cfg, before.Options, s.chosen, s.o.skip)
	if c := inheritEngine(&s.engine, before.Options, s.chosen); c != nil {
		changes = append(changes, *c)
	}
	if changes, err = s.inheritEAA(before, changes); err != nil {
		return s.fail(err)
	}
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
	s.view.Step("Snapshot", fmt.Sprintf("%q, %s, taken %s", before.Name, plural(pages.Len(), "page"), report.Ago(before.Started)))
	if len(before.Options.Modules)+len(before.Options.Levels)+len(before.Options.Viewports)+len(before.Options.Engine) > 0 {
		s.view.Step("Settings", snapshotSettings(before.Name, before.Options))
	}
	if len(changes) > 0 {
		s.view.Step("You changed", joinChanges(changes))
	}
	s.view.Step("Confirm", s.confirmSetting())
	return s.compareWith(before, pages, snapDir, site, dir)
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
	if err := cmp.Or(target(s.args[0]), target(s.o.against), s.failOn(true), s.notStrict(), s.only("--against", "resume", "serve", "client", "brand"),
		s.siteChecks(s.args[0], s.o.against)); err != nil {
		return s.fail(err)
	}
	dir, err := s.st.NewDir("runs", store.Stamp(time.Now()))
	if err != nil {
		return s.fail(err)
	}
	beforeDir := filepath.Join(dir, "against")
	s.start(s.args[0])
	s.view.Step("Against", s.shownTarget(s.o.against))
	s.view.Step("Confirm", s.confirmSetting())
	before, err := s.check(s.o.against, nil, true, "", beforeDir)
	if err != nil {
		s.discard(dir)
		return s.fail(fmt.Errorf("%s: %w", s.shownTarget(s.o.against), err))
	}
	if err := store.SaveRun(beforeDir, before); err != nil {
		return s.fail(err)
	}
	s.view.Step("Now", s.shownTarget(s.args[0]))
	pages := compare.InMemory(before.Pages)
	head := *before
	head.Pages = nil
	return s.compareWith(&head, pages, beforeDir, s.args[0], dir)
}

// compareWith checks site again on the earlier run's pages and compares.
// before is that run without its pages, which come from pages one at a
// time (compare.Stream). BROKE and NEW pages are visited once more to
// confirm them (unless --confirm=off), then the config's ignore rules
// apply.
func (s *session) compareWith(before *model.Run, pages compare.PageSource, beforeDir, site, dir string) int {
	paths := make([]string, 0, pages.Len())
	known := make([]model.Page, 0, pages.Len()) // what confirm needs: which pages the earlier run has
	for i := range pages.Len() {
		paths = append(paths, pages.Path(i))
		known = append(known, model.Page{Path: pages.Path(i)})
	}
	// The keyboard module tests each URL group's widgets on the page the
	// earlier run tested them on. A control broken on every page of a
	// group is then not FIXED on one page and NEW on another. The confirm
	// visits below test them on the pages this run did.
	s.widgetPages = before.WidgetPages
	after, err := s.check(site, paths, false, "", dir)
	if err != nil && after == nil {
		s.discard(dir)
		return s.fail(err)
	}
	if err := store.SaveRun(dir, after); err != nil {
		return s.fail(err)
	}
	c, err := s.compareStored(before, pages, beforeDir, after, dir)
	if err != nil {
		return s.fail(err)
	}
	c.Warnings = append(c.Warnings, s.compareNotes...)
	s.widgetPages = after.WidgetPages
	s.confirm(c, &model.Run{Pages: known}, site, dir)
	compare.ApplyIgnore(c, ignoreRules(s.cfg), time.Now())
	if err := store.SaveComparison(dir, savedComparison(after, c)); err != nil {
		return s.fail(err)
	}
	s.st.ExcludeFromGit()
	if err := s.st.Prune(keepRuns); err != nil {
		fmt.Fprintln(s.stderr, prefix(), "pruning old runs:", err)
	}
	s.record(after, c)
	return s.finish(report.Result{Run: after, Comparison: c, Dir: dir}, "")
}

// savedComparison is c as comparison.json keeps it. A comparison with
// a snapshot keeps its page paths as they are: accept and ignore find
// the pages of a change by them in the snapshot and the run. Nothing
// picks pages from an --against comparison (accept refuses one), so an
// --eaa one is stored the way the reports show it, with the query
// values of its page paths masked (report.Masked). Its change ids are
// set first, so the stored ones are the ids the run printed. One with
// a snapshot from an --eaa run has the free text of its changes masked
// (report.MaskedText): a title change can name a customer.
func savedComparison(after *model.Run, c *model.Comparison) *model.Comparison {
	if c.Before.Name != "" {
		if after != nil && after.Options.EAA {
			c.Complete()
			return report.MaskedText(c)
		}
		return c
	}
	c.Complete()
	return report.Masked(report.Result{Run: after, Comparison: c}).Comparison
}

// compareStored compares after, the run just made and still in memory,
// with the earlier run before, whose pages are read from pages a pair at
// a time: only the changes are kept, so a big site's snapshot is never
// read whole.
func (s *session) compareStored(before *model.Run, pages compare.PageSource, beforeDir string, after *model.Run, dir string) (*model.Comparison, error) {
	head := *after
	head.Pages = nil
	cc := &check.CompareContext{Config: s.cfg, Before: before, After: &head, BeforeDir: beforeDir, AfterDir: dir,
		Dir: dir, Save: store.Saver(dir)}
	return compare.Stream(context.WithoutCancel(s.ctx), cc, pages, compare.InMemory(after.Pages), s.modules(s.cfg), shownVersion())
}

// confirm visits the pages of c's BROKE and NEW changes again and moves
// the ones that don't come back to UNSTABLE: a flaky 500 or a timeout
// under load then doesn't fail a build. A page issue that does come back
// is checked on one more visit, so a server that fails one load in five
// is rarely taken for a broken one.
func (s *session) confirm(c *model.Comparison, before *model.Run, site, dir string) {
	if s.o.confirm.off || s.ctx.Err() != nil || c.Interrupted {
		return
	}
	var paths []string
	for _, ch := range c.Changes {
		if ch.Class != model.Broke && ch.Class != model.New {
			continue
		}
		for _, p := range ch.Pages {
			if !slices.Contains(paths, p) {
				paths = append(paths, p)
			}
		}
	}
	again := filepath.Join(dir, "confirm")
	defer func() { _ = os.RemoveAll(again) }() //nolint:gosec // a folder of this run
	for round := range confirmVisits {
		if len(paths) == 0 || s.ctx.Err() != nil {
			return
		}
		if round == 0 {
			s.view.Step("Confirming", fmt.Sprintf("visiting %s again, to tell a real break from a flaky one", plural(len(paths), "page")))
		} else {
			s.view.Step("Confirming", fmt.Sprintf("visiting %s once more, where the change came back", plural(len(paths), "page")))
		}
		opts, err := s.options(site, paths, false, "", filepath.Join(again, fmt.Sprint(round+1)))
		var run *model.Run
		if err == nil {
			opts.Hooks = runner.Hooks{Log: s.view.Log}
			run, err = runner.Revisit(s.ctx, opts, paths)
		}
		if err != nil {
			// After the first visit, the changes it confirmed stand.
			if round == 0 && s.ctx.Err() == nil {
				c.Warnings = append(c.Warnings, "confirm: couldn't visit the pages again ("+err.Error()+"); BROKE and NEW changes aren't confirmed")
			}
			return
		}
		compare.Confirm(c, before, run)
		paths = compare.Confirmable(c)
	}
}

// confirmVisits is how many times confirm visits a page whose change
// keeps coming back.
const confirmVisits = 2

// confirmSetting describes the confirm pass for the run header.
func (s *session) confirmSetting() string {
	if s.o.confirm.off {
		return "off (--confirm=off): BROKE and NEW pages aren't visited again"
	}
	return "BROKE and NEW pages are visited again before they're reported; --confirm=off skips it"
}

// ignoreRules converts the config's ignore: rules for compare.
func ignoreRules(cfg config.Config) []compare.IgnoreRule {
	out := make([]compare.IgnoreRule, 0, len(cfg.Ignore))
	for _, r := range cfg.Ignore {
		out = append(out, compare.IgnoreRule(r))
	}
	return out
}

func cmdRun(s *session) int {
	site, err := s.site("run", "example.test")
	if err != nil {
		return s.fail(err)
	}
	if err := cmp.Or(s.failOn(false), s.notStrict(), s.only("run", "confirm", "resume", "serve", "client", "brand"), s.siteChecks(site)); err != nil {
		return s.fail(err)
	}
	dir, err := s.st.NewDir("runs", store.Stamp(time.Now()))
	if err != nil {
		return s.fail(err)
	}
	s.start(site)
	run, err := s.check(site, nil, false, "", dir)
	if err != nil && run == nil {
		s.discard(dir)
		return s.fail(err)
	}
	if err := store.SaveRun(dir, run); err != nil {
		return s.fail(err)
	}
	s.st.ExcludeFromGit()
	if err := s.st.Prune(keepRuns); err != nil {
		fmt.Fprintln(s.stderr, prefix(), "pruning old runs:", err)
	}
	s.record(run, nil)
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
		// With no run or comparison yet, the newest snapshot is the last
		// result there is.
		if dir, err = s.st.LatestRun(); err != nil {
			if snap, snapErr := s.st.Snapshot(""); snapErr == nil {
				dir, err = snap, nil
				if !s.o.quiet {
					fmt.Fprintf(s.stderr, "No run or comparison yet; showing snapshot %q (%s)\n", filepath.Base(snap), rel(snap))
				}
			} else {
				err = cli.UsageError{Msg: err.Error()}
			}
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
	if err := cmp.Or(s.failOn(res.Compared()), s.only("report", "confirm", "resume", "eaa", "wcag", "staging")); err != nil {
		return s.fail(err)
	}
	// The accessibility summary isn't stored: the client report and
	// --serve need it as much as finish does.
	eaaSummary(&res)
	switch {
	case s.o.serve && s.o.client:
		return s.fail(cli.Usagef("pick --serve or --client, not both"))
	case s.o.serve:
		return s.serve(res)
	case s.o.client:
		return s.clientReport(res)
	case s.o.brand != "":
		return s.fail(cli.Usagef("--brand goes with --client"))
	}
	s.view.Header(shownVersion(), report.ShownTarget(run))
	return s.finish(res, "")
}

// cmdSnapshots lists snapshots, or deletes one.
func cmdSnapshots(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("snapshots", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	args, code, ok := subFlags(fs, args, snapshotsUsage, stdout, stderr)
	if !ok {
		return code
	}
	st := store.Store{Root: config.Dir}
	if len(args) == 2 && !*asJSON && (args[0] == "delete" || args[0] == "rm") {
		dir, err := st.Snapshot(args[1])
		if err != nil {
			fmt.Fprintln(stderr, prefix(), named(err.Error()))
			return exitUsage
		}
		kept, err := keepEvidence(dir)
		if err != nil {
			fmt.Fprintln(stderr, prefix(), named(err.Error()))
			return exitError
		}
		if err := os.RemoveAll(dir); err != nil { //nolint:gosec // st.Snapshot validated the name
			fmt.Fprintln(stderr, prefix(), named(err.Error()))
			return exitError
		}
		fmt.Fprintln(stdout, "Deleted snapshot", args[1])
		// LATEST mustn't name the snapshot that is gone.
		if err := st.Forget(args[1]); err != nil {
			fmt.Fprintln(stderr, prefix(), "moving the latest snapshot pointer:", err)
		}
		if kept != "" {
			fmt.Fprintln(stderr, keptPackNote(args[1], rel(filepath.Join(st.Root, filepath.FromSlash(kept)))))
		}
		return exitOK
	}
	if len(args) > 0 {
		fmt.Fprintln(stderr, prefix(), "usage:", cmdName(), "snapshots [--json | delete <name>]")
		return exitUsage
	}
	names, err := st.Snapshots()
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	if *asJSON {
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
		fmt.Fprintf(stdout, "%-24s %-6d %-12s %s%s\n", n, len(r.Pages), report.Ago(r.Started), report.ShownTarget(r), mark)
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
			e.Created, e.Site, e.Pages, e.Version = r.Started.UTC().Format(time.RFC3339), report.ShownTarget(r), len(r.Pages), r.Version
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
	clear := fs.Bool("clear", false, "")
	pos, code, ok := subFlags(fs, args, updateUsage, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintln(stderr, prefix(), "usage:", cmdName(), "update [--clear]")
		return exitUsage
	}
	quiet := boolFlag(fs, "quiet")
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
	// Ctrl+C stops a download too.
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	logf := func(m string) {
		if !quiet {
			fmt.Fprintln(stdout, m)
		}
	}
	bin, downloaded, err := browser.FindContext(ctx, browser.Options{Log: logf})
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	removed, err := browser.Prune(!downloaded)
	for _, r := range removed {
		fmt.Fprintln(stdout, "Removed unused Chrome for Testing", r)
	}
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
	}
	if !downloaded {
		fmt.Fprintln(stdout, "Browser: installed Chrome at", bin, "(updated with your system)")
	} else {
		fmt.Fprintln(stdout, "Browser: Chrome for Testing at", bin)
	}
	return exitOK
}

// errSymlink refuses a report path that is a symbolic link: the report
// would land wherever the link points.
var errSymlink = errors.New("refusing to write through a symlink")

// writeFile writes a report atomically enough for CI: the file only
// appears once complete. A symlink at path is refused, never followed
// or replaced.
func writeFile(path string, write func(io.Writer) error) error {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return errSymlink
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
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
	// Reports hold what the pages showed, so only their owner reads them
	// (0600), like the rest of a run folder; CI uploads still work, as
	// the job runs as that owner.
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
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

// browserLog passes the browser's messages to log: the line naming the
// browser used is a status line, hidden with -q; the rest (a download,
// a failure) always shows.
func browserLog(log func(string)) func(string) {
	return func(msg string) {
		if strings.HasPrefix(msg, "Browser: ") {
			log("~" + msg)
			return
		}
		log("!" + msg)
	}
}
