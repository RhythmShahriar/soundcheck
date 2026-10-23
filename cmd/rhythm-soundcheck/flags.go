package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/report"
)

type options struct {
	json, md, junit, html          bool
	output                         string
	open                           bool
	quiet, plain, noColor, verbose bool

	name, snapshot, against string

	configPath             string
	limit                  int
	include, exclude       cli.ListFlag
	pages                  cli.RepeatFlag
	ignoreRobots           bool
	modules, skip          cli.ListFlag
	levels, viewports      cli.ListFlag
	noLogin, lighthouse    bool
	rate                   float64
	tabs                   int
	timeout                time.Duration
	userAgent              string
	headers                cli.RepeatFlag
	cookie, proxy, browser string
	insecure               bool
	noDownload, show       bool

	failOn string
}

func flagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet("soundcheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	both := func(short, long string, f func(name string)) { f(short); f(long) }

	fs.BoolVar(&o.json, "json", false, "")
	fs.BoolVar(&o.md, "md", false, "")
	fs.BoolVar(&o.junit, "junit", false, "")
	fs.BoolVar(&o.html, "html", false, "")
	both("o", "output", func(n string) { fs.StringVar(&o.output, n, "", "") })
	fs.BoolVar(&o.open, "open", false, "")
	both("q", "quiet", func(n string) { fs.BoolVar(&o.quiet, n, false, "") })
	fs.BoolVar(&o.plain, "plain", false, "")
	fs.BoolVar(&o.noColor, "no-color", false, "")
	both("v", "verbose", func(n string) { fs.BoolVar(&o.verbose, n, false, "") })

	fs.StringVar(&o.name, "name", "", "")
	fs.StringVar(&o.snapshot, "snapshot", "", "")
	fs.StringVar(&o.snapshot, "against-snapshot", "", "")
	fs.StringVar(&o.against, "against", "", "")

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
	fs.BoolVar(&o.noLogin, "no-login", false, "")
	fs.BoolVar(&o.lighthouse, "lighthouse", false, "")

	fs.Float64Var(&o.rate, "rate", 0, "")
	fs.IntVar(&o.tabs, "tabs", 0, "")
	fs.DurationVar(&o.timeout, "timeout", 0, "")
	both("A", "user-agent", func(n string) { fs.StringVar(&o.userAgent, n, "", "") })
	both("H", "header", func(n string) { fs.Var(&o.headers, n, "") })
	fs.StringVar(&o.cookie, "cookie", "", "")
	fs.StringVar(&o.proxy, "proxy", "", "")
	both("k", "insecure", func(n string) { fs.BoolVar(&o.insecure, n, false, "") })
	fs.StringVar(&o.browser, "browser", "", "")
	fs.BoolVar(&o.noDownload, "no-download", false, "")
	fs.BoolVar(&o.show, "show-browser", false, "")

	fs.StringVar(&o.failOn, "fail-on", "", "")
	return fs
}

// plan is a command's validated settings.
type plan struct {
	o        options
	args     []string // positional arguments
	cfg      config.Config
	format   string // json, md, junit, html or "" for the terminal view only
	headers  map[string]string
	proxy    *url.URL
	ua       string
	storeDir string
}

func parse(args []string) (*plan, error) {
	p := &plan{}
	o := &p.o
	fs := flagSet(o)
	pos, err := cli.ParseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, err
		}
		return nil, cli.UsageError{Msg: err.Error()}
	}
	p.args = pos
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// Settings: the config file, then flags on top.
	path := o.configPath
	if path == "" {
		path = filepath.Join(config.Dir, "config.yaml")
	} else if _, err := os.Stat(path); err != nil {
		return nil, cli.Usagef("--config %s: %v", path, err)
	}
	if p.cfg, err = config.Load(path); err != nil {
		return nil, cli.UsageError{Msg: err.Error()}
	}
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
	if len(o.modules) > 0 {
		cfg.Modules = o.modules
	}
	for _, m := range o.skip {
		if !slices.Contains(config.Modules, m) {
			return nil, cli.Usagef("--skip %q isn't one of %s", m, strings.Join(config.Modules, ", "))
		}
		cfg.Modules = slices.DeleteFunc(cfg.Modules, func(x string) bool { return x == m })
	}
	if len(o.levels) > 0 {
		cfg.Interactions.Levels = o.levels
	}
	if len(o.viewports) > 0 {
		cfg.Viewports = o.viewports
	}
	if set["rate"] {
		cfg.Rate = o.rate
	}
	if set["tabs"] {
		cfg.Tabs = o.tabs
	}
	if set["timeout"] {
		cfg.Timeout = int(o.timeout.Round(time.Second) / time.Second)
	}
	if err := cfg.Validate(); err != nil {
		return nil, cli.UsageError{Msg: flagName(err.Error())}
	}
	if cfg.Rate <= 0 {
		return nil, cli.Usagef("--rate must be more than 0")
	}
	if len(cfg.Modules) == 0 {
		return nil, cli.Usagef("no modules left to run")
	}

	// Output.
	var chosen []string
	for name, on := range map[string]bool{"json": o.json, "md": o.md, "junit": o.junit, "html": o.html} {
		if on {
			chosen = append(chosen, name)
		}
	}
	switch {
	case len(chosen) > 1:
		slices.Sort(chosen)
		return nil, cli.Usagef("pick one output format, not --%s", strings.Join(chosen, " and --"))
	case len(chosen) == 1:
		p.format = chosen[0]
	case o.output != "":
		if p.format = formatFromExt(o.output); p.format == "" {
			return nil, cli.Usagef("can't tell the format of %s; add --json, --md, --junit or --html", o.output)
		}
	}

	// Requests.
	p.headers = map[string]string{}
	for _, raw := range o.headers {
		name, value, ok := strings.Cut(raw, ":")
		if name = strings.TrimSpace(name); !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, cli.Usagef("bad header %q, use \"Name: value\"", raw)
		}
		p.headers[name] = strings.TrimSpace(value)
	}
	if o.cookie != "" {
		p.headers["Cookie"] = o.cookie
	}
	if o.proxy != "" {
		u, err := url.Parse(o.proxy)
		if err != nil || u.Host == "" || !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, u.Scheme) {
			return nil, cli.Usagef("bad --proxy %q, use e.g. http://127.0.0.1:8080 or socks5://127.0.0.1:1080", o.proxy)
		}
		p.proxy = u
	}
	p.ua = o.userAgent
	if p.ua == "" {
		p.ua = fmt.Sprintf("rhythm-soundcheck/%s (+https://rythm.sh/soundcheck)", version)
	}
	if o.name != "" && !validName(o.name) {
		return nil, cli.Usagef("--name %q: use letters, digits, dots, dashes and underscores", o.name)
	}
	p.storeDir = config.Dir
	return p, nil
}

// failOn validates --fail-on now that the command knows whether it
// compares.
func (p *plan) failOn(compared bool) error {
	if p.o.failOn == "" {
		return nil
	}
	if err := report.ValidFailOn(p.o.failOn, compared); err != nil {
		return cli.UsageError{Msg: err.Error()}
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

// flagName points config errors at the flag that sets the same value.
func flagName(msg string) string {
	r := strings.NewReplacer("interactions.levels", "--level (interactions.levels)", "viewports:", "--viewport (viewports):",
		"pages.limit", "--limit (pages.limit)", "tabs must", "--tabs must", "timeout must", "--timeout must")
	return r.Replace(msg)
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
