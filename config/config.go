// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package config loads Soundcheck's optional project settings from
// .soundcheck/config.yaml. Everything has a sensible default, so most
// sites need no config at all.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/rhythm/kit/httpx"
	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
)

// Dir is the project folder Soundcheck keeps snapshots and reports in.
const Dir = ".soundcheck"

// Modules Soundcheck can run, in order. DefaultModules run unless a flag
// or the file says otherwise. a11y, keyboard and seo are off until asked
// for (+a11y, +keyboard, +seo; --eaa adds a11y and keyboard). echo runs only when Echo is configured (Echo.URL) and
// exec only when the file lists commands under exec: the CLI adds them.
// A test in the CLI keeps this list in step with the registered modules.
var Modules = []string{"health", "visual", "interact", "cache", "speed", "a11y", "keyboard", "seo", "echo", "exec"}

// DefaultModules are the modules a run uses when nothing chooses.
var DefaultModules = []string{"health", "visual", "interact", "cache", "speed"}

// EchoURL is where `rhythm echo up` listens by default.
const EchoURL = "http://127.0.0.1:8025"

// EchoTokenPrefix starts every variable echo.token_env may name.
const EchoTokenPrefix = "RHYTHM_ECHO_"

// Levels are the interaction levels, from safest to most invasive.
var Levels = []string{"inspect", "click", "submit", "flows"}

// Config is every project setting. The file is untrusted (a pull request
// can change it), so nothing in it can pick a secret to send or a host to
// send one to; Trust holds what only command-line flags may allow.
type Config struct {
	// Site is the site snapshot and run check when no URL is given.
	Site         string       `yaml:"site"`
	Pages        Pages        `yaml:"pages"`
	Viewports    []string     `yaml:"viewports"` // desktop, mobile
	Modules      []string     `yaml:"modules"`
	Visual       Visual       `yaml:"visual"`
	Interactions Interactions `yaml:"interactions"`
	Production   []string     `yaml:"production"` // live sites (hosts, *.hosts or URLs with a path): form submits refuse them
	Login        Login        `yaml:"login"`
	Flows        string       `yaml:"flows"` // folder of YAML flows, relative to the project
	Rate         float64      `yaml:"rate"`  // page loads per second per host
	Tabs         int          `yaml:"tabs"`
	Timeout      Seconds      `yaml:"timeout"` // per page
	Echo         Echo         `yaml:"echo"`
	// FailOn is --fail-on's value when the flag isn't given: a class
	// (broke, new, changed, slower) for comparisons, a level (error,
	// warning, notice) for run and snapshot, or none. Validate lowercases
	// it.
	FailOn string `yaml:"fail_on"`
	// Headers are extra request headers, each read from an environment
	// variable (header name -> variable name), so the file holds no
	// secret and the CLI decides which variables it may read.
	Headers map[string]string `yaml:"headers"`
	// Ignore lists changes that are known and shouldn't count, applied
	// after every comparison (compare.ApplyIgnore).
	Ignore []IgnoreRule `yaml:"ignore"`
	// Exec lists programs the exec module runs on every page. They run
	// only with --allow-exec: a committed file can't run programs alone.
	Exec []ExecCommand `yaml:"exec"`
	// EAA is --eaa on every snapshot, run and compare: the accessibility
	// and keyboard checks for an accessibility statement draft.
	EAA bool `yaml:"eaa"`

	// Trust is set by command-line flags only, never by the file.
	Trust Trust `yaml:"-"`

	// prod is Production as Validate parsed it and prodFrom the entries
	// it parsed, so IsProduction can tell when Production changed since.
	prod     []prodSite
	prodFrom []string

	// inFile holds the line of each key the file set, for InFile.
	inFile map[string]int
	// blocks holds the value of each top-level key the file set, for
	// Block.
	blocks map[string]*yaml.Node
}

// Pages chooses which pages are checked.
type Pages struct {
	Limit        int      `yaml:"limit"`
	PerGroup     int      `yaml:"per_group"` // pages per URL pattern (/blog/{slug}/); -1 checks all
	Include      []string `yaml:"include"`   // only pages matching these globs
	Exclude      []string `yaml:"exclude"`   // skip pages matching these globs
	IgnoreRobots bool     `yaml:"ignore_robots"`
	Extra        []string `yaml:"extra"` // paths to always check, e.g. /checkout/
	// Flag lists the paths of Extra that came from --page, not the file,
	// so a note about one names the flag.
	Flag []string `yaml:"-"`
}

// Visual tunes the screenshot comparison.
type Visual struct {
	Threshold float64  `yaml:"threshold"` // % of pixels that must change to report a page
	Ignore    []string `yaml:"ignore"`    // CSS selectors hidden before screenshots
}

// Interactions tunes what the interact module clicks and submits.
type Interactions struct {
	Levels    []string `yaml:"levels"`     // which of inspect, click, submit, flows run; each brings the ones before it
	MaxClicks int      `yaml:"max_clicks"` // per page
	Allow     []string `yaml:"allow"`      // selectors clickable even if they look destructive
	Forms     []string `yaml:"forms"`      // forms that may be submitted on production, by CSS selector
}

// Trust holds what only the person running Soundcheck may allow. The
// project folder and the site are untrusted (a pull request can change
// .soundcheck/), so secrets, logins and flow navigation stay on the target
// site unless a command-line flag says otherwise.
type Trust struct {
	AllowEnv      []string // --allow-env: variables flows may read besides SOUNDCHECK_*
	AllowHosts    []string // --allow-host: hosts flows may open besides the target, as HostKey gives them
	InsecureLogin bool     // --insecure-login: type the password over plain http to a public host
	// Staging (--staging) says a public target is a staging site, so a
	// key flow with keyboard: send may really submit there.
	Staging bool
}

// IgnoreRule names changes that shouldn't count. It has the fields of
// compare.IgnoreRule, which the CLI converts it to: Check, Title and
// Source are globs, Pages page globs, Until the last day it applies
// (YYYY-MM-DD). Every glob is a model.Glob, where only * is special, so
// a selector's brackets in Source match themselves.
type IgnoreRule struct {
	Check  string   `yaml:"check" json:"check,omitempty"`
	Pages  []string `yaml:"pages" json:"pages,omitempty"`
	Title  string   `yaml:"title" json:"title,omitempty"`
	Source string   `yaml:"source" json:"source,omitempty"`
	Until  string   `yaml:"until" json:"until,omitempty"`
	Reason string   `yaml:"reason" json:"reason,omitempty"`
}

// ExecCommand is one program the exec module runs on every page. It has
// the fields of the exec module's Command.
type ExecCommand struct {
	Name    string        `yaml:"name" json:"name"`
	Run     []string      `yaml:"run" json:"run"`         // the program and its arguments, without a shell
	Timeout time.Duration `yaml:"timeout" json:"timeout"` // per page; 0 is 10s
}

// Login says how to log in with SOUNDCHECK_USER and SOUNDCHECK_PASSWORD.
type Login struct {
	URL  string `yaml:"url"`  // login page; default /wp-login.php for WordPress
	Flow string `yaml:"flow"` // a YAML flow that logs in, for non-WordPress sites
}

// Echo ties form submits and flows to the emails, SMS and webhooks they
// make the site send, through a running Echo (`rhythm echo up`).
type Echo struct {
	URL      string        `yaml:"url"`       // empty: off
	TokenEnv string        `yaml:"token_env"` // environment variable holding Echo's token, if it needs one
	Quiet    time.Duration `yaml:"quiet"`     // after a submit, wait until nothing new arrives for this long…
	Timeout  time.Duration `yaml:"timeout"`   // …or at most this long
}

// Default returns the settings used when there's no config file.
func Default() Config {
	return Config{
		Pages:        Pages{Limit: 300, PerGroup: 3},
		Viewports:    slices.Clone(Viewports),
		Modules:      slices.Clone(DefaultModules),
		Visual:       Visual{Threshold: 0.5},
		Interactions: Interactions{Levels: []string{"inspect", "click"}, MaxClicks: 15},
		Flows:        filepath.Join(Dir, "flows"),
		Rate:         5,
		Tabs:         8,
		Timeout:      30,
		Echo:         Echo{TokenEnv: "RHYTHM_ECHO_TOKEN", Quiet: 1500 * time.Millisecond, Timeout: 10 * time.Second}, //nolint:gosec // the name of a variable, not a token
	}
}

// Load reads path over the defaults. A missing file is not an error.
// Unknown keys are, so a typo fails loudly instead of being ignored.
// Every error names the file and, when it can, the line.
func Load(path string) (Config, error) {
	c := Default()
	data, err := readLimited(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	inFile := func(err error) error {
		var se *SettingError
		if !errors.As(err, &se) {
			return fmt.Errorf("%s: %w", path, err)
		}
		se.File = path
		if se.Line == 0 {
			se.Line = c.inFile[se.Key]
		}
		return se
	}
	if c.inFile, err = decode(data, &c); err != nil {
		return c, inFile(err)
	}
	// The file takes the same list syntax as --modules and --level.
	def := Default()
	if c.Modules, err = AddList(def.Modules, c.Modules); err != nil {
		return c, inFile(&SettingError{Key: "modules", Msg: err.Error()})
	}
	levels, err := AddList(def.Interactions.Levels, c.Interactions.Levels)
	if err != nil {
		return c, inFile(&SettingError{Key: "interactions.levels", Msg: err.Error()})
	}
	c.Interactions.Levels = Cumulative(levels)
	// Validate first: it stores the parsed production entries on c.
	if err := c.Validate(); err != nil {
		return c, inFile(err)
	}
	return c, nil
}

// MaxSize is the largest config file Load reads. A real one is a few
// kilobytes; the limit keeps a hostile file (the config comes with a
// repository) from tying up the parser.
const MaxSize = 1 << 20

// readLimited reads the config file, refusing one over MaxSize without
// reading past it.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // the user's own project config
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSize {
		return nil, fmt.Errorf("%s is larger than 1 MB", path)
	}
	return data, nil
}

// InFile reports whether the config file set key, written with dots for
// nested keys ("modules", "interactions.levels"), even to its default
// value. compare uses it to tell a setting the user chose from one it
// may take from the snapshot. Keys are known two levels deep.
func (c Config) InFile(key string) bool { _, ok := c.inFile[key]; return ok }

// FailOnValues are the values fail_on and --fail-on take: the comparison
// classes, then the issue levels.
var FailOnValues = []string{"broke", "new", "changed", "slower", "error", "warning", "notice"}

// FailOnNone is the fail_on and --fail-on value that never fails, so a
// command line or CI input can turn off a fail_on from the config file.
const FailOnNone = "none"

// ParseFailOn reads a fail_on or --fail-on value in any case: one of
// FailOnValues or FailOnNone, lowercased.
func ParseFailOn(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	return v, OneOf(v, append(slices.Clone(FailOnValues), FailOnNone))
}

// Validate checks values a user could get wrong and parses the
// production entries for IsProduction. Its errors are *SettingError.
func (c *Config) Validate() error {
	if c.Site != "" {
		if _, _, err := httpx.ParseTarget(c.Site); err != nil {
			return settingErr("site", "%q isn't a site like staging.example.com", c.Site)
		}
	}
	for _, v := range c.Viewports {
		if err := OneOf(v, Viewports); err != nil {
			return &SettingError{Key: "viewports", Msg: err.Error()}
		}
	}
	for _, m := range c.Modules {
		if err := OneOf(m, Modules); err != nil {
			return &SettingError{Key: "modules", Msg: err.Error()}
		}
	}
	for _, l := range c.Interactions.Levels {
		if err := OneOf(l, Levels); err != nil {
			return &SettingError{Key: "interactions.levels", Msg: err.Error()}
		}
	}
	if c.FailOn != "" {
		v, err := ParseFailOn(c.FailOn)
		if err != nil {
			return &SettingError{Key: "fail_on", Msg: err.Error()}
		}
		c.FailOn = v
	}
	prod, err := parseProductions(c.Production)
	if err != nil {
		return err
	}
	c.prod, c.prodFrom = prod, slices.Clone(c.Production)
	switch {
	case c.Pages.Limit < 1:
		return settingErr("pages.limit", "must be at least 1")
	case c.Tabs < 1 || c.Tabs > 16:
		return settingErr("tabs", "must be between 1 and 16")
	case c.Visual.Threshold < 0 || c.Visual.Threshold > 100:
		return settingErr("visual.threshold", "is a percentage, 0 to 100")
	case c.Timeout < 5:
		return settingErr("timeout", "must be at least 5 seconds")
	}
	if err := CheckRate(c.Rate); err != nil {
		return settingErr("rate", "%v", err)
	}
	if err := c.validateHeaders(); err != nil {
		return err
	}
	if err := c.Login.validate(); err != nil {
		return err
	}
	for n, r := range c.Ignore {
		if r.Check == "" && r.Title == "" && len(r.Pages) == 0 && r.Source == "" {
			return settingErr("ignore", "rule %d has nothing to match: give it a check, a title, pages or a source", n+1)
		}
		if r.Until != "" {
			if _, err := time.Parse("2006-01-02", r.Until); err != nil {
				return settingErr("ignore", "rule %d: until %q isn't a date like 2026-12-31", n+1, r.Until)
			}
		}
	}
	if c.Has("exec") && len(c.Exec) == 0 {
		return settingErr("modules", "exec runs the commands listed under exec: in the config; there are none")
	}
	return c.Echo.validate(c.Has("echo"))
}

// Viewports Soundcheck can check at.
var Viewports = []string{"desktop", "mobile"}

var (
	headerName = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")
	envName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// validateHeaders checks the shape of headers. Whether a variable may be
// read and is set is the CLI's to check: --allow-env is a flag.
func (c Config) validateHeaders() error {
	for _, name := range slices.Sorted(maps.Keys(c.Headers)) {
		switch env := c.Headers[name]; {
		case !headerName.MatchString(name):
			return settingErr("headers", "%q isn't a header name like X-Staging-Auth", name)
		case !envName.MatchString(env):
			return settingErr("headers", "%s: %q isn't the name of an environment variable; header values come only from the environment, e.g. %s: SOUNDCHECK_STAGING_AUTH", name, env, name)
		}
	}
	return nil
}

// validate checks login.url is a path or an http(s) URL. Whether a URL is
// on the target site is checked when logging in, once the site is known.
func (l Login) validate() error {
	if l.URL == "" {
		return nil
	}
	u, err := url.Parse(l.URL)
	if err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") || (u.Scheme != "" && u.Host == "") {
		return settingErr("login.url", "%q isn't a path like /wp-login.php", l.URL)
	}
	return nil
}

func (e Echo) validate(on bool) error {
	if on && e.URL == "" {
		return settingErr("modules", "echo needs Echo's address: --echo, SOUNDCHECK_ECHO or echo.url")
	}
	if e.URL != "" {
		u, err := url.Parse(e.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return settingErr("echo.url", "%q isn't an http(s) URL like %s", e.URL, EchoURL)
		}
	}
	switch {
	case e.Quiet < 100*time.Millisecond || e.Quiet > 30*time.Second:
		return settingErr("echo.quiet", "must be between 100ms and 30s")
	case e.Timeout < e.Quiet || e.Timeout > time.Minute:
		return settingErr("echo.timeout", "must be at least echo.quiet and at most 1m")
	case strings.ContainsAny(e.TokenEnv, " =$"):
		return settingErr("echo.token_env", "%q isn't an environment variable name", e.TokenEnv)
	case e.TokenEnv != "" && !strings.HasPrefix(e.TokenEnv, EchoTokenPrefix):
		// A committed config must not pick which secret is sent where.
		return settingErr("echo.token_env", "%q must start with %s", e.TokenEnv, EchoTokenPrefix)
	}
	return nil
}

// The rates Soundcheck accepts, in page loads per second per host.
const (
	MinRate = 0.01
	MaxRate = 1000
)

// CheckRate reports a rate outside MinRate to MaxRate. NaN, infinities and
// rates so small they overflow all make the limiter wait zero, so a typo
// would hammer the site; the comparison is written to catch NaN too. The
// kit's ratelimit.Validate is asked as well, so a rate the limiter itself
// would refuse never gets through. Soundcheck is stricter: 0 (no limit)
// isn't a rate here.
func CheckRate(rate float64) error {
	if ratelimit.Validate(rate) != nil || !(rate >= MinRate && rate <= MaxRate) {
		return fmt.Errorf("%v isn't between %v and %v page loads a second", rate, MinRate, MaxRate)
	}
	return nil
}

// Has reports whether a module is enabled.
func (c Config) Has(module string) bool { return slices.Contains(c.Modules, module) }

// Level reports whether an interaction level is enabled.
func (c Config) Level(level string) bool { return slices.Contains(c.Interactions.Levels, level) }
