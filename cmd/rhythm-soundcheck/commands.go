package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	kitconfig "github.com/RhythmShahriar/rhythm/kit/config"
	"github.com/RhythmShahriar/rhythm/kit/update"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
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
		fmt.Fprintln(stderr, "rhythm soundcheck:", err)
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
}

func (p *plan) begin(stdout, stderr io.Writer) *session {
	s := &session{plan: p, stdout: stdout, stderr: stderr, st: store.Store{Root: p.storeDir}}
	s.ctx, s.stop = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	s.tty = cli.IsTerminal(stdout)
	plain := p.o.plain || !s.tty
	if plain || p.o.noColor {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	// Machine output on stdout replaces the live view; with -o both show.
	s.live = p.format == "" || p.o.output != ""
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
		fmt.Fprintln(s.stderr, "rhythm soundcheck: canceled")
		return exitCanceled
	}
	fmt.Fprintln(s.stderr, "rhythm soundcheck:", err)
	var ue cli.UsageError
	if errors.As(err, &ue) {
		return exitUsage
	}
	return exitError
}

// start prints the header and the one-time notice, and looks for a new
// release in the background.
func (s *session) start(target string) {
	if kitconfig.FirstRun("soundcheck-authorised-use") && !s.o.quiet {
		fmt.Fprint(s.stderr, "Note: check only sites you own or are authorised to test. Soundcheck browses\n"+
			"like a visitor at a polite pace, and only submits forms when you turn on the\n"+
			"submit level, never on sites listed as production. This notice is shown once.\n\n")
	}
	s.notice = update.Notice(s.ctx, &update.Updater{Repo: "RhythmShahriar/soundcheck", Binary: "rhythm-soundcheck",
		Current: version, UserAgent: s.ua, API: os.Getenv("RHYTHM_GITHUB_API")})
	s.view.Header(version, target)
}

// check runs the site once.
func (s *session) check(target string, paths []string, snapshot bool, name, dir string) (*model.Run, error) {
	login, who := loginFunc(s.cfg, s.o.noLogin)
	if who != "" {
		s.view.Step("Login", "as "+who+" (from SOUNDCHECK_USER)")
	}
	opts := runner.Options{
		Config: s.cfg, Target: target, Paths: paths, Snapshot: snapshot, Name: name, Dir: dir, Version: version,
		UserAgent: s.ua, Headers: s.headers, Proxy: s.proxy, Insecure: s.o.insecure, Modules: modules(s.cfg), Login: login,
		Browser: browser.Options{Bin: s.o.browser, NoDownload: s.o.noDownload, Proxy: s.o.proxy, Insecure: s.o.insecure,
			Visible: s.o.show, Log: func(msg string) { s.view.Log("!" + msg) }},
		Hooks: runner.Hooks{Discovered: s.view.Discovered, Page: s.view.Page, Log: s.view.Log},
	}
	run, err := runner.Run(s.ctx, opts)
	// A snapshot runs Lighthouse once it's saved under its own name.
	if run != nil && s.o.lighthouse && !snapshot && s.ctx.Err() == nil {
		s.lighthouse(run.Target, dir)
	}
	return run, err
}

// finish saves the HTML report, writes the chosen format, prints the
// summary and picks the exit code.
func (s *session) finish(res report.Result, hint string) int {
	html := filepath.Join(res.Dir, "report.html")
	if err := writeFile(html, func(w io.Writer) error { return report.HTML(w, res) }); err != nil {
		fmt.Fprintln(s.stderr, "rhythm soundcheck: writing the HTML report:", err)
		html = ""
	}
	if s.format != "" {
		write := func(w io.Writer) error {
			switch s.format {
			case "json":
				return report.JSON(w, res)
			case "md":
				return report.Markdown(w, res)
			case "junit":
				return report.JUnit(w, res, s.o.failOn)
			}
			return report.HTML(w, res)
		}
		var err error
		if s.o.output != "" {
			err = writeFile(s.o.output, write)
		} else {
			err = write(s.stdout)
		}
		if err != nil {
			fmt.Fprintln(s.stderr, "rhythm soundcheck: writing the report:", err)
			return exitError
		}
	}
	open := html
	if s.format == "html" && s.o.output != "" {
		open = s.o.output
	}
	if hint == "" && open != "" {
		hint = "full report: " + rel(open)
	}
	if s.live {
		s.view.Summary(res, hint)
	}
	if s.o.output != "" && !s.o.quiet {
		fmt.Fprintf(s.stderr, "Report written to %s\n", s.o.output)
	}
	if s.o.open && open != "" {
		if err := openFile(open); err != nil {
			fmt.Fprintln(s.stderr, "rhythm soundcheck: opening the report:", err, "\n  open it yourself:", open)
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
		fmt.Fprintln(s.stderr, "rhythm soundcheck: canceled; results are partial")
		return exitCanceled
	}
	if report.FailOn(res, s.o.failOn) {
		return exitFindings
	}
	return exitOK
}

func cmdSnapshot(s *session) int {
	if len(s.args) != 1 {
		return s.fail(cli.Usagef("snapshot needs one site\n  try: rhythm soundcheck snapshot staging.example.com"))
	}
	if err := cmp.Or(target(s.args[0]), s.failOn(false)); err != nil {
		return s.fail(err)
	}
	name := cmp.Or(s.o.name, store.Stamp(time.Now()))
	dir, err := s.st.NewDir(store.Incoming, name)
	if err != nil {
		return s.fail(err)
	}
	defer func() { _ = os.RemoveAll(dir) }() //nolint:gosec // our own incoming folder; gone once promoted, cleans up a failed snapshot
	s.start(s.args[0])
	s.view.Step("Snapshot", fmt.Sprintf("%q", name))
	run, err := s.check(s.args[0], nil, true, name, dir)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(s.stderr, "rhythm soundcheck: snapshot canceled; nothing was saved")
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
		"next: make your change (update a plugin, deploy…), then run: rhythm soundcheck compare")
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
	dir, err := s.st.NewDir("runs", store.Stamp(time.Now()))
	if err != nil {
		return s.fail(err)
	}
	s.start(site)
	s.view.Step("Snapshot", fmt.Sprintf("%q, %s, taken %s", before.Name, plural(len(before.Pages), "page"), report.Ago(before.Started)))
	return s.compareWith(before, snapDir, site, dir)
}

// cmdAgainst compares two sites now: the --against site is checked first
// and plays the snapshot's part.
func cmdAgainst(s *session) int {
	if s.o.against == "" {
		return s.fail(cli.Usagef("to check a site once: rhythm soundcheck run %s\n  to compare two sites: rhythm soundcheck <url> --against <url>", strings.Join(s.args, " ")))
	}
	if len(s.args) != 1 {
		return s.fail(cli.Usagef("--against compares one site with another: rhythm soundcheck staging.example.com --against example.com"))
	}
	if err := cmp.Or(target(s.args[0]), target(s.o.against), s.failOn(true)); err != nil {
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
		fmt.Fprintln(s.stderr, "rhythm soundcheck: pruning old runs:", err)
	}
	return s.finish(report.Result{Run: after, Comparison: c, Dir: dir}, "")
}

func cmdRun(s *session) int {
	if len(s.args) != 1 {
		return s.fail(cli.Usagef("run needs one site\n  try: rhythm soundcheck run example.test"))
	}
	if err := cmp.Or(target(s.args[0]), s.failOn(false)); err != nil {
		return s.fail(err)
	}
	dir, err := s.st.NewDir("runs", store.Stamp(time.Now()))
	if err != nil {
		return s.fail(err)
	}
	s.start(s.args[0])
	run, err := s.check(s.args[0], nil, false, "", dir)
	if err != nil && run == nil {
		return s.fail(err)
	}
	if err := store.SaveRun(dir, run); err != nil {
		return s.fail(err)
	}
	s.st.ExcludeFromGit()
	if err := s.st.Prune(keepRuns); err != nil {
		fmt.Fprintln(s.stderr, "rhythm soundcheck: pruning old runs:", err)
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
		if fi, statErr := os.Stat(s.args[0]); statErr == nil && fi.IsDir() { //nolint:gosec // the user names a run folder to show
			dir = s.args[0]
		} else {
			dir, err = s.st.Snapshot(s.args[0])
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
			fmt.Fprintln(stderr, "rhythm soundcheck:", err)
			return exitUsage
		}
		if err := os.RemoveAll(dir); err != nil { //nolint:gosec // st.Snapshot validated the name
			fmt.Fprintln(stderr, "rhythm soundcheck:", err)
			return exitError
		}
		fmt.Fprintln(stdout, "Deleted snapshot", args[1])
		return exitOK
	}
	if len(args) > 0 {
		fmt.Fprintln(stderr, "rhythm soundcheck: usage: rhythm soundcheck snapshots [delete <name>]")
		return exitUsage
	}
	names, err := st.Snapshots()
	if err != nil {
		fmt.Fprintln(stderr, "rhythm soundcheck:", err)
		return exitError
	}
	if len(names) == 0 {
		fmt.Fprintln(stdout, "No snapshots yet. Take one: rhythm soundcheck snapshot <url>")
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
		fmt.Fprintln(stderr, "rhythm soundcheck:", err)
		return exitError
	}
	if *clear {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintln(stderr, "rhythm soundcheck:", err)
			return exitError
		}
		fmt.Fprintln(stdout, "Cleared", dir)
		return exitOK
	}
	bin, downloaded, err := browser.Find(browser.Options{Log: func(m string) { fmt.Fprintln(stdout, m) }})
	if err != nil {
		fmt.Fprintln(stderr, "rhythm soundcheck:", err)
		return exitError
	}
	removed, err := browser.Prune(!downloaded)
	for _, r := range removed {
		fmt.Fprintln(stdout, "Removed unused Chromium", r)
	}
	if err != nil {
		fmt.Fprintln(stderr, "rhythm soundcheck:", err)
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
