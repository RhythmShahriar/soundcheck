// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/capture"
	execmod "github.com/RhythmShahriar/soundcheck/checks/exec"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/rules"
)

type options struct {
	json, md, junit, html          bool
	outputs                        cli.RepeatFlag
	open                           bool
	quiet, plain, noColor, verbose bool

	name, snapshot, against string
	strict                  bool

	configPath             string
	limit                  int
	include, exclude       cli.ListFlag
	pages                  cli.RepeatFlag
	ignoreRobots           bool
	modules, skip          cli.ListFlag
	levels, viewports      cli.ListFlag
	noLogin, lighthouse    bool
	quick                  bool
	allowEnv, allowHosts   cli.RepeatFlag
	insecureLogin          bool
	rate                   float64
	tabs                   int
	timeout                secondsFlag
	userAgent              string
	headers                cli.RepeatFlag
	cookie, proxy, browser string
	insecure               bool
	noDownload, show       bool
	noSandbox, followHost  bool
	allowExec              bool

	echo echoFlag

	failOn string

	confirm onOffFlag // compare: re-visit BROKE and NEW pages first
	resume  bool      // snapshot: carry on with a stopped snapshot

	// report
	serve, client bool
	brand         string
}

// onOffFlag is a flag that is on by default: --confirm, --confirm=off.
type onOffFlag struct {
	set, off bool
}

func (f *onOffFlag) String() string {
	if f.off {
		return "off"
	}
	return "on"
}

func (f *onOffFlag) Set(v string) error {
	f.set = true
	switch strings.ToLower(v) {
	case "", "on", "true", "1", "yes":
		f.off = false
	case "off", "false", "0", "no":
		f.off = true
	default:
		return fmt.Errorf("%q isn't on or off", v)
	}
	return nil
}

// IsBoolFlag lets --confirm stand alone.
func (f *onOffFlag) IsBoolFlag() bool { return true }

// echoFlag is --echo with an optional value: a bare --echo means Echo at
// its default address, --echo=<url> another one. Like --color[=when] in
// other tools, the value must be attached with "=": a separate argument
// would be read as the site to check.
type echoFlag struct {
	set bool
	url string
}

func (f *echoFlag) String() string { return f.url }

func (f *echoFlag) Set(v string) error {
	f.set = true
	switch strings.ToLower(v) {
	case "true", "":
		f.url = config.EchoURL
	case "false": // off, even when SOUNDCHECK_ECHO or echo.url is set
		f.url = ""
	default:
		f.url = v
	}
	return nil
}

// IsBoolFlag lets --echo stand alone.
func (f *echoFlag) IsBoolFlag() bool { return true }

// echoSpaced catches `--echo http://127.0.0.1:8025`: the URL would be
// taken as the site to check. Echo's own port gives it away.
func echoSpaced(args []string) error {
	for i, a := range args[:max(len(args)-1, 0)] {
		if a != "--echo" && a != "-echo" {
			continue
		}
		if u, err := url.Parse(args[i+1]); err == nil && u.Host != "" && u.Port() == "8025" {
			return cli.Usagef("--echo takes its address with \"=\": --echo=%s", args[i+1])
		}
	}
	return nil
}

// echoFromEnv reads SOUNDCHECK_ECHO: a URL, or 1/true/on for the default
// address. Empty, 0, false and off leave Echo off.
func echoFromEnv(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "off", "no":
		return ""
	case "1", "true", "on", "yes":
		return config.EchoURL
	}
	return strings.TrimSpace(v)
}

func flagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("soundcheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	both := func(short, long string, f func(name string)) { f(short); f(long) }

	fs.BoolVar(&o.json, "json", false, "")
	fs.BoolVar(&o.md, "md", false, "")
	fs.BoolVar(&o.junit, "junit", false, "")
	fs.BoolVar(&o.html, "html", false, "")
	both("o", "output", func(n string) { fs.Var(&o.outputs, n, "") })
	fs.BoolVar(&o.open, "open", false, "")
	both("q", "quiet", func(n string) { fs.BoolVar(&o.quiet, n, false, "") })
	fs.BoolVar(&o.plain, "plain", false, "")
	fs.BoolVar(&o.noColor, "no-color", false, "")
	both("v", "verbose", func(n string) { fs.BoolVar(&o.verbose, n, false, "") })

	fs.StringVar(&o.name, "name", "", "")
	fs.StringVar(&o.snapshot, "snapshot", "", "")
	fs.StringVar(&o.snapshot, "against-snapshot", "", "")
	fs.StringVar(&o.against, "against", "", "")
	fs.BoolVar(&o.strict, "strict", false, "")

	fs.StringVar(&o.configPath, "config", "", "")
	fs.IntVar(&o.limit, "limit", 0, "")
	fs.Var(&o.include, "include", "")
	fs.Var(&o.exclude, "exclude", "")
	fs.Var(&o.pages, "page", "")
	fs.BoolVar(&o.ignoreRobots, "ignore-robots", false, "")
	fs.Var(&o.modules, "modules", "")
	fs.Var(&o.skip, "skip", "")
	fs.Var(&o.levels, "level", "")
	fs.Var(&o.viewports, "viewport", "")
	fs.BoolVar(&o.quick, "quick", false, "")
	fs.BoolVar(&o.noLogin, "no-login", false, "")
	fs.BoolVar(&o.lighthouse, "lighthouse", false, "")
	fs.Var(&o.allowEnv, "allow-env", "")
	fs.Var(&o.allowHosts, "allow-host", "")
	fs.BoolVar(&o.insecureLogin, "insecure-login", false, "")

	fs.Float64Var(&o.rate, "rate", 0, "")
	fs.IntVar(&o.tabs, "tabs", 0, "")
	fs.Var(&o.timeout, "timeout", "")
	both("A", "user-agent", func(n string) { fs.StringVar(&o.userAgent, n, "", "") })
	both("H", "header", func(n string) { fs.Var(&o.headers, n, "") })
	fs.StringVar(&o.cookie, "cookie", "", "")
	fs.StringVar(&o.proxy, "proxy", "", "")
	both("k", "insecure", func(n string) { fs.BoolVar(&o.insecure, n, false, "") })
	fs.StringVar(&o.browser, "browser", "", "")
	fs.BoolVar(&o.noDownload, "no-download", false, "")
	fs.BoolVar(&o.show, "show-browser", false, "")
	fs.BoolVar(&o.noSandbox, "no-sandbox", false, "")
	fs.BoolVar(&o.followHost, "follow-host", false, "")
	fs.BoolVar(&o.allowExec, "allow-exec", false, "")
	fs.Var(&o.confirm, "confirm", "")
	fs.BoolVar(&o.resume, "resume", false, "")
	fs.BoolVar(&o.serve, "serve", false, "")
	fs.BoolVar(&o.client, "client", false, "")
	fs.StringVar(&o.brand, "brand", "", "")

	fs.Var(&o.echo, "echo", "")

	fs.StringVar(&o.failOn, "fail-on", "", "")
	return fs
}

// plan is a command's validated settings.
type plan struct {
	o        options
	set      map[string]bool // the flags given
	args     []string        // positional arguments
	cfg      config.Config
	format   string   // json, md, junit or html for stdout, or "" for the live view
	outputs  []output // files to write, from -o
	headers  map[string]string
	proxy    *url.URL
	ua       string
	storeDir string

	configPath string   // the config file read, for messages
	failOnFrom string   // the config file, when fail_on gave --fail-on's value
	failOnSet  bool     // --fail-on or fail_on was given, so no tip about it
	envHeaders []string // headers the config file read from the environment
	// siteFrom is the config file when its site: is the site being
	// checked. Such a site is as untrusted as the file, so nothing from
	// the environment is sent to it.
	siteFrom string
	// siteKey is what siteFrom set: "site" (the config file's key) or
	// "target" (a snapshot's run.json).
	siteKey string
	// siteTrusted is set when siteFrom is a snapshot this machine took
	// (not one committed to git): the user's own, so it may get secrets.
	siteTrusted bool
	chosen      chosen // settings the user set, which compare keeps
	quick       string // what --quick set, for the header
	// noReportHint leaves the "full report:" hint out of the summary,
	// for the demo, whose run folder is removed when it ends.
	noReportHint bool
	// echoFrom is what turned Echo on: "flag", "env", "config" or "".
	// A token goes to an Echo on another machine only when the person
	// running Soundcheck named it, never the project's config alone.
	echoFrom string
	// rules are health's error signatures: the built-in ones and the
	// project's own from .soundcheck/rules.
	rules *rules.Set
	// allowExec is --allow-exec or SOUNDCHECK_ALLOW_EXEC=1: the exec
	// module may run the programs the config lists.
	allowExec bool
	// engine is the browser engine --browser named; empty is Chrome.
	engine capture.Engine
	// bin is the browser binary --browser named, if any.
	bin string
}

func parse(args []string) (*plan, error) {
	p := &plan{}
	o := &p.o
	if err := echoSpaced(args); err != nil {
		return nil, err
	}
	fs := flagSet(o)
	pos, err := cli.ParseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, err
		}
		return nil, flagError(fs, err)
	}
	p.args = pos
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	p.set = set

	// Settings: the config file, then flags on top.
	path := o.configPath
	if path == "" {
		path = filepath.Join(config.Dir, "config.yaml")
	} else if _, err := os.Stat(path); err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err // the path is in the message already
		}
		return nil, cli.Usagef("--config: %s: %v", path, err)
	}
	if p.cfg, err = config.Load(path); err != nil {
		return nil, cli.UsageError{Msg: err.Error()}
	}
	if err := configureModules(p.cfg, path); err != nil {
		return nil, err
	}
	p.configPath = path
	p.chosen = chosenSettings(set, p.cfg, path)
	cfg := &p.cfg
	if set["limit"] {
		cfg.Pages.Limit = o.limit
	}
	cfg.Pages.Include = append(cfg.Pages.Include, o.include...)
	cfg.Pages.Exclude = append(cfg.Pages.Exclude, o.exclude...)
	cfg.Pages.Extra = append(cfg.Pages.Extra, o.pages...)
	if o.ignoreRobots {
		cfg.Pages.IgnoreRobots = true
	}
	p.quickPreset(set)
	if len(o.modules) > 0 {
		if cfg.Modules, err = config.AddList(cfg.Modules, o.modules); err != nil {
			return nil, cli.Usagef("--modules (modules): %v", err)
		}
	}
	for _, m := range o.skip {
		if !slices.Contains(config.Modules, m) {
			return nil, cli.Usagef("--skip: %v", config.OneOf(m, config.Modules))
		}
		cfg.Modules = slices.DeleteFunc(cfg.Modules, func(x string) bool { return x == m })
	}
	// exec is on whenever the config lists commands, unless --skip exec.
	// Without --allow-exec it runs nothing and says so.
	if len(cfg.Exec) > 0 && !slices.Contains(o.skip, "exec") && !cfg.Has("exec") {
		cfg.Modules = append(cfg.Modules, "exec")
	}
	p.allowExec = o.allowExec || os.Getenv("SOUNDCHECK_ALLOW_EXEC") == "1"
	// A fleet's sites.yaml can't set --lighthouse (it installs and runs
	// Unlighthouse); the person running the fleet turns it on per site
	// with <PREFIX>_SOUNDCHECK_LIGHTHOUSE=1, which reaches the site's run
	// as this.
	o.lighthouse = o.lighthouse || os.Getenv("SOUNDCHECK_LIGHTHOUSE") == "1"
	if len(cfg.Exec) > 0 {
		if _, err := execmod.New(execCommands(*cfg), false); err != nil {
			return nil, cli.Usagef("%s: %v", path, err)
		}
	}
	if p.rules, err = health.LoadRules(filepath.Join(config.Dir, "rules")); err != nil {
		return nil, cli.Usagef("%s: %v", filepath.Join(config.Dir, "rules"), err)
	}
	if err := p.browserFlag(); err != nil {
		return nil, err
	}
	// Echo: --echo, then SOUNDCHECK_ECHO, then echo.url. It's on whenever
	// it's configured, unless --skip echo.
	if cfg.Echo.URL != "" {
		p.echoFrom = "config"
	}
	if env := echoFromEnv(os.Getenv("SOUNDCHECK_ECHO")); env != "" {
		cfg.Echo.URL, p.echoFrom = env, "env"
	}
	if o.echo.set {
		cfg.Echo.URL, p.echoFrom = o.echo.url, "flag"
	}
	switch {
	case slices.Contains(o.skip, "echo"):
		cfg.Echo.URL = ""
	case cfg.Echo.URL != "" && !cfg.Has("echo"):
		cfg.Modules = append(cfg.Modules, "echo")
	}
	if len(o.levels) > 0 {
		levels, err := config.AddList(cfg.Interactions.Levels, o.levels)
		if err != nil {
			return nil, cli.Usagef("--level (interactions.levels): %v", err)
		}
		cfg.Interactions.Levels = config.Cumulative(levels)
	}
	if len(o.viewports) > 0 {
		cfg.Viewports = o.viewports
	}
	if set["rate"] {
		if err := config.CheckRate(o.rate); err != nil {
			return nil, cli.Usagef("--rate (rate): %v", err)
		}
		cfg.Rate = o.rate
	}
	if set["tabs"] {
		cfg.Tabs = o.tabs
	}
	if set["timeout"] {
		cfg.Timeout = config.Seconds(o.timeout)
	}
	p.failOnSet = set["fail-on"] || cfg.FailOn != ""
	if !set["fail-on"] && cfg.FailOn != "" {
		o.failOn, p.failOnFrom = cfg.FailOn, path
	}
	if o.failOn != "" {
		v, err := config.ParseFailOn(o.failOn)
		if err != nil {
			return nil, cli.Usagef("--fail-on (fail_on): %v", err)
		}
		if v == config.FailOnNone {
			v, p.failOnFrom = "", ""
		}
		o.failOn = v
	}
	if cfg.Echo.URL == "" {
		p.echoFrom = ""
	}
	if err := p.trust(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, cli.UsageError{Msg: flagName(err)}
	}
	if len(cfg.Modules) == 0 {
		return nil, cli.Usagef("no modules left to run")
	}

	if err := p.outputFormats(); err != nil {
		return nil, err
	}

	// Requests.
	p.headers = map[string]string{}
	for _, raw := range o.headers {
		name, value, ok := strings.Cut(raw, ":")
		if name = strings.TrimSpace(name); !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, cli.Usagef("--header: %q isn't a header; use \"Name: value\"", raw)
		}
		p.headers[name] = strings.TrimSpace(value)
	}
	if o.cookie != "" {
		p.headers["Cookie"] = o.cookie
	}
	if err := p.configHeaders(); err != nil {
		return nil, err
	}
	if o.proxy != "" {
		u, err := url.Parse(o.proxy)
		if err != nil || u.Host == "" || !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, u.Scheme) {
			return nil, cli.Usagef("--proxy: %q isn't an http, https or socks5 proxy, e.g. http://127.0.0.1:8080 or socks5://127.0.0.1:1080", o.proxy)
		}
		p.proxy = u
	}
	p.ua = o.userAgent
	if p.ua == "" {
		p.ua = fmt.Sprintf("rhythm-soundcheck/%s (+https://rythm.sh/soundcheck)", version)
	}
	if o.name != "" && !validName(o.name) {
		return nil, cli.Usagef("--name: %q isn't a snapshot name; use letters, digits, dots, dashes and underscores", o.name)
	}
	p.storeDir = config.Dir
	return p, nil
}

// browserFlag reads --browser: an engine (chrome, firefox, webkit), a
// path to a Chrome binary, or engine=path for another engine's binary.
func (p *plan) browserFlag() error {
	v := p.o.browser
	if v == "" {
		return nil
	}
	name, bin, hasBin := strings.Cut(v, "=")
	if e, err := capture.ParseEngine(name); err == nil && !strings.ContainsAny(name, `/\`) {
		if hasBin && bin == "" {
			return cli.Usagef("--browser: %s= needs the path to its binary, e.g. --browser %s=/usr/bin/%s", name, name, name)
		}
		p.engine, p.bin = e, bin
		return nil
	}
	if hasBin {
		return cli.Usagef("--browser: %q isn't chrome, firefox or webkit%s", name, engineHint(name))
	}
	// A bare word that names no program is a mistyped engine, not a
	// Chrome binary: say so now rather than after the run has started.
	if !strings.ContainsAny(v, `/\`) && !isProgram(v) {
		return cli.Usagef("--browser: %q isn't chrome, firefox or webkit%s", v, engineHint(v))
	}
	p.bin = v // a path to Chrome, as before
	return nil
}

// engineNames are the names --browser takes for an engine.
var engineNames = []string{"chrome", "firefox", "webkit", "chromium", "safari"}

// engineHint is " (did you mean firefox?)" for a near miss of an engine
// name, else "".
func engineHint(name string) string {
	if s := config.Suggest(strings.ToLower(name), engineNames); s != "" {
		return fmt.Sprintf(" (did you mean %s?)", s)
	}
	return ""
}

// isProgram reports whether name is a file here or a program on PATH.
func isProgram(name string) bool {
	if info, err := os.Stat(name); err == nil && !info.IsDir() {
		return true
	}
	_, err := exec.LookPath(name)
	return err == nil
}

// Quick is what --quick checks: one viewport and the modules that find
// what breaks, leaving out the screenshots, cache and timing passes.
var (
	quickModules   = []string{"health", "interact"}
	quickViewports = []string{"desktop"}
)

// quickPreset applies --quick over the config file. --modules and
// --viewport still win, so --quick --viewport mobile checks mobile only.
// compare then keeps --quick's settings over the snapshot's.
func (p *plan) quickPreset(set map[string]bool) {
	if !p.o.quick {
		return
	}
	var applied, kept []string
	if set["viewport"] {
		kept = append(kept, "--viewport")
	} else {
		p.cfg.Viewports = slices.Clone(quickViewports)
		p.chosen.viewports = "--quick"
		applied = append(applied, "viewport "+strings.Join(quickViewports, ", "))
	}
	if set["modules"] {
		kept = append(kept, "--modules")
	} else {
		p.cfg.Modules = slices.Clone(quickModules)
		p.chosen.modules = "--quick"
		applied = append(applied, "modules "+strings.Join(quickModules, ", "))
	}
	switch {
	case len(applied) == 0:
		p.quick = "--quick: nothing, " + strings.Join(kept, " and ") + " set it all"
	case len(kept) == 0:
		p.quick = "--quick: " + strings.Join(applied, "; ")
	default:
		p.quick = "--quick: " + strings.Join(applied, "; ") + " (" + strings.Join(kept, " and ") + " kept)"
	}
}

// output is one report file -o asked for.
type output struct {
	path, format string
}

// outputFormats reads the format flags and -o. A format flag alone writes
// to stdout in place of the live view. Each -o file takes its format from
// its extension, so one run can write JSON, Markdown, JUnit and HTML at
// once; with a single -o a format flag may name the format instead, as
// in --md -o check.md.
func (p *plan) outputFormats() error {
	o := &p.o
	var chosen []string
	for name, on := range map[string]bool{"json": o.json, "md": o.md, "junit": o.junit, "html": o.html} {
		if on {
			chosen = append(chosen, name)
		}
	}
	slices.Sort(chosen)
	switch {
	case len(chosen) > 1:
		return cli.Usagef("pick one output format, not --%s", strings.Join(chosen, " and --"))
	case len(o.outputs) == 0:
		if len(chosen) == 1 {
			p.format = chosen[0]
		}
		return nil
	case len(o.outputs) == 1 && len(chosen) == 1:
		p.outputs = []output{{o.outputs[0], chosen[0]}}
		return nil
	case len(chosen) == 1:
		return cli.Usagef("--%s with several -o: each file's format comes from its extension, so leave out --%[1]s", chosen[0])
	}
	seen := map[string]string{}
	for _, path := range o.outputs {
		f := formatFromExt(path)
		switch {
		case f == "" && len(o.outputs) == 1:
			return cli.Usagef("can't tell the format of %s; add --json, --md, --junit or --html", path)
		case f == "":
			return cli.Usagef("can't tell the format of %s from its extension; use .json, .md, .xml or .html", path)
		}
		// Two -o naming one file would leave only the last format there.
		key := filepath.Clean(path)
		if abs, err := filepath.Abs(path); err == nil {
			key = abs
		}
		if first, ok := seen[key]; ok {
			return cli.Usagef("-o %s and -o %s are the same file", first, path)
		}
		seen[key] = path
		p.outputs = append(p.outputs, output{path, f})
	}
	return nil
}

// trust reads --allow-env, --allow-host and --insecure-login into the
// config: flags only, since the project folder is untrusted.
func (p *plan) trust() error {
	t := &p.cfg.Trust
	for _, name := range p.o.allowEnv {
		if name == "" || strings.ContainsAny(name, " =${}") {
			return cli.Usagef("--allow-env: %q isn't an environment variable name", name)
		}
		t.AllowEnv = append(t.AllowEnv, name)
	}
	for _, h := range p.o.allowHosts {
		key, err := config.ParseHost(h)
		if err != nil {
			return cli.Usagef("--allow-host: %v", err)
		}
		t.AllowHosts = append(t.AllowHosts, key)
	}
	t.InsecureLogin = p.o.insecureLogin
	return nil
}

// notStrict refuses --strict outside compare with a snapshot.
func (p *plan) notStrict() error {
	if p.o.strict {
		return cli.Usagef("--strict works with compare against a snapshot: it stops when the comparison wouldn't use the snapshot's settings")
	}
	return nil
}

// configHeaders adds the config file's headers, each read from the
// environment variable it names. -H and --cookie win over them. The file
// is untrusted, so it may name only variables a flow could read: SOUNDCHECK_*
// ones, or ones --allow-env lets through.
func (p *plan) configHeaders() error {
	for _, name := range slices.Sorted(maps.Keys(p.cfg.Headers)) {
		if hasHeader(p.headers, name) {
			continue
		}
		env := p.cfg.Headers[name]
		if !flow.EnvAllowed(env, p.cfg.Trust.AllowEnv) {
			return cli.Usagef("%s: headers: %s reads %s; headers read only %s* variables unless you pass --allow-env %s",
				p.configPath, name, env, flow.EnvPrefix, env)
		}
		// Empty counts as missing: CI fills in an unset secret with "".
		v := os.Getenv(env)
		if v == "" {
			return cli.Usagef("%s: headers: %s reads %s, which isn't set", p.configPath, name, env)
		}
		p.headers[name] = v
		p.envHeaders = append(p.envHeaders, name)
	}
	return nil
}

// headerFlags names the command line's -H and --cookie when given, as
// "-H and --cookie", for refusing to send them to an untrusted site.
func (p *plan) headerFlags() string {
	var f []string
	if len(p.o.headers) > 0 {
		f = append(f, "-H")
	}
	if p.o.cookie != "" {
		f = append(f, "--cookie")
	}
	return strings.Join(f, " and ")
}

func hasHeader(h map[string]string, name string) bool {
	for k := range h {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// failOn validates --fail-on now that the command knows whether it
// compares. A fail_on from the config file that fits the other kind of
// command (broke for a run, error for a compare) is left out with a note,
// so one file can serve both.
func (s *session) failOn(compared bool) error {
	v := s.o.failOn
	if v == "" {
		return nil
	}
	if report.ValidFailOn(v, compared) == nil { // parse lowercased it
		return nil
	}
	if s.failOnFrom == "" {
		// parse checked it's a known value, so it's for the other kind
		// of command.
		if compared {
			return cli.Usagef("--fail-on (fail_on): %q is for run and snapshot; a comparison takes broke, new, changed or slower", v)
		}
		return cli.Usagef("--fail-on (fail_on): %q is for comparisons; run and snapshot take error, warning or notice", v)
	}
	s.o.failOn = ""
	if !s.o.quiet {
		kind := "comparisons"
		if compared {
			kind = "run and snapshot"
		}
		fmt.Fprintf(s.stderr, "Note: fail_on: %s in %s is for %s, so it doesn't apply here\n", v, s.failOnFrom, kind)
	}
	return nil
}

// target validates a site argument.
func target(s string) error {
	if _, _, err := ratelimit.ParseTarget(s); err != nil {
		return cli.UsageError{Msg: err.Error()}
	}
	return nil
}

// looksLikeTarget tells a site ("example.com", "http://…") from a
// mistyped command.
func looksLikeTarget(s string) bool {
	return strings.Contains(s, ".") || strings.Contains(s, "://") || strings.HasPrefix(s, "localhost")
}

// settingFlags are the flags that set the same value as a config key.
var settingFlags = map[string]string{
	"modules": "--modules", "interactions.levels": "--level", "viewports": "--viewport", "pages.limit": "--limit",
	"tabs": "--tabs", "timeout": "--timeout", "rate": "--rate", "echo.url": "--echo", "fail_on": "--fail-on",
}

// Go's flag package words its errors for its own single-dash style.
var (
	flagBadValue  = regexp.MustCompile(`^invalid (?:boolean )?value "(.*)" for (?:flag )?-([^:]+): (.*)$`)
	flagUndefined = regexp.MustCompile(`^flag provided but not defined: -+(.+)$`)
	flagNoValue   = regexp.MustCompile(`^flag needs an argument: -+(.+)$`)
)

// flagError rewords a flag parse error the way every other value error
// reads: "--tabs (tabs): "abc" isn't a whole number" or "unknown flag
// --modlues (did you mean --modules?)".
func flagError(fs *flag.FlagSet, err error) error {
	msg := err.Error()
	if m := flagUndefined.FindStringSubmatch(msg); m != nil {
		var names []string
		fs.VisitAll(func(f *flag.Flag) {
			if len(f.Name) > 1 {
				names = append(names, f.Name)
			}
		})
		hint := ""
		if s := config.Suggest(m[1], names); s != "" {
			hint = fmt.Sprintf(" (did you mean --%s?)", s)
		}
		return cli.Usagef("unknown flag %s%s", dashed(m[1]), hint)
	}
	if m := flagNoValue.FindStringSubmatch(msg); m != nil {
		return cli.Usagef("%s needs a value", dashed(m[1]))
	}
	if m := flagBadValue.FindStringSubmatch(msg); m != nil {
		value, name, why := m[1], m[2], m[3]
		if why == "parse error" || strings.HasPrefix(why, "strconv.") {
			why = fmt.Sprintf("%q isn't %s", value, valueKind(fs.Lookup(name)))
		}
		return cli.Usagef("%s: %s", flagLabel(name), why)
	}
	return cli.UsageError{Msg: msg}
}

// dashed writes a flag as it's typed: -o, --fail-on.
func dashed(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// flagLabel names a flag for an error: its long name plus the config key
// that sets the same value when there is one, "--tabs (tabs)".
func flagLabel(name string) string {
	if long, ok := shortFlags[name]; ok {
		name = long
	}
	label := "--" + name
	for key, f := range settingFlags {
		if f == label {
			return label + " (" + key + ")"
		}
	}
	return label
}

// shortFlags are the one-letter flags and the long names they stand for.
var shortFlags = map[string]string{"o": "output", "q": "quiet", "v": "verbose", "A": "user-agent", "H": "header", "k": "insecure"}

// valueKind says what a flag takes, for "isn't a whole number".
func valueKind(f *flag.Flag) string {
	if f == nil {
		return "a valid value"
	}
	g, ok := f.Value.(flag.Getter)
	if !ok {
		return "a valid value"
	}
	switch g.Get().(type) {
	case int:
		return "a whole number"
	case float64:
		return "a number"
	case bool:
		return "true or false"
	}
	return "a valid value"
}

// flagName points a config error at the flag that sets the same value,
// "--level (interactions.levels): ...", when a flag could have caused it:
// errors from the file itself already name the file and line.
func flagName(err error) string {
	var se *config.SettingError
	if errors.As(err, &se) && se.File == "" {
		if f, ok := settingFlags[se.Key]; ok {
			return fmt.Sprintf("%s (%s): %s", f, se.Key, se.Msg)
		}
	}
	return err.Error()
}

// secondsFlag is --timeout: a duration like 30s or 1m, or a number of
// seconds as in the config file.
type secondsFlag int

func (f *secondsFlag) String() string { return strconv.Itoa(int(*f)) + "s" }

func (f *secondsFlag) Set(v string) error {
	n, err := config.ParseSeconds(v)
	*f = secondsFlag(n)
	return err
}

func formatFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "json"
	case ".md", ".markdown":
		return "md"
	case ".xml":
		return "junit"
	case ".html", ".htm":
		return "html"
	}
	return ""
}
