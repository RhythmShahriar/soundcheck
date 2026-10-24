// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package config loads Soundcheck's optional project settings from
// .soundcheck/config.yaml. Everything has a sensible default, so most
// sites need no config at all.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Dir is the project folder Soundcheck keeps snapshots and reports in.
const Dir = ".soundcheck"

// Modules Soundcheck can run, in order. echo runs only when Echo is
// configured (Echo.URL), so it isn't in the default list: the CLI adds it
// when --echo, SOUNDCHECK_ECHO or echo.url is set.
var Modules = []string{"health", "visual", "interact", "cache", "speed", "echo"}

// EchoURL is where `rhythm echo up` listens by default.
const EchoURL = "http://127.0.0.1:8025"

// EchoTokenPrefix starts every variable echo.token_env may name.
const EchoTokenPrefix = "RHYTHM_ECHO_"

// Interaction levels, from safest to most invasive.
var Levels = []string{"inspect", "click", "submit", "flows"}

type Config struct {
	Pages        Pages        `yaml:"pages"`
	Viewports    []string     `yaml:"viewports"` // desktop, mobile
	Modules      []string     `yaml:"modules"`
	Visual       Visual       `yaml:"visual"`
	Interactions Interactions `yaml:"interactions"`
	Production   []string     `yaml:"production"` // live sites (hosts, *.hosts or URLs with a path): form submits refuse them
	Login        Login        `yaml:"login"`
	Flows        string       `yaml:"flows"` // folder of YAML flows, relative to the project
	Speed        Speed        `yaml:"speed"`
	Rate         float64      `yaml:"rate"` // page loads per second per host
	Tabs         int          `yaml:"tabs"`
	Timeout      int          `yaml:"timeout"` // seconds per page
	Echo         Echo         `yaml:"echo"`

	// Trust is set by command-line flags only, never by the file.
	Trust Trust `yaml:"-"`

	// prod is Production as Validate parsed it and prodFrom the entries
	// it parsed, so IsProduction can tell when Production changed since.
	prod     []prodSite
	prodFrom []string

	// inFile holds the keys the file set, for InFile.
	inFile map[string]bool
}

type Pages struct {
	Limit        int      `yaml:"limit"`
	PerGroup     int      `yaml:"per_group"`
	Include      []string `yaml:"include"`
	Exclude      []string `yaml:"exclude"`
	IgnoreRobots bool     `yaml:"ignore_robots"`
	Extra        []string `yaml:"extra"` // paths to always check, e.g. /checkout/
}

type Visual struct {
	Threshold float64  `yaml:"threshold"` // % of pixels that must change to report a page
	Ignore    []string `yaml:"ignore"`    // CSS selectors hidden before screenshots
}

type Interactions struct {
	Levels    []string `yaml:"levels"`     // which of inspect, click, submit, flows run
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
}

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

type Speed struct {
	Slower float64 `yaml:"slower"` // % slower before a page is reported SLOWER
}

// Default returns the settings used when there's no config file.
func Default() Config {
	return Config{
		Pages:        Pages{Limit: 300, PerGroup: 3},
		Viewports:    []string{"desktop", "mobile"},
		Modules:      slices.Clone(Modules[:len(Modules)-1]), // not echo: it needs Echo.URL
		Visual:       Visual{Threshold: 0.5},
		Interactions: Interactions{Levels: []string{"inspect", "click"}, MaxClicks: 15},
		Flows:        filepath.Join(Dir, "flows"),
		Speed:        Speed{Slower: 25},
		Rate:         5,
		Tabs:         8,
		Timeout:      30,
		Echo:         Echo{TokenEnv: "RHYTHM_ECHO_TOKEN", Quiet: 1500 * time.Millisecond, Timeout: 10 * time.Second}, //nolint:gosec // the name of a variable, not a token
	}
}

// Load reads path over the defaults. A missing file is not an error.
// Unknown keys are, so a typo fails loudly instead of being ignored.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path) //nolint:gosec // the user's own project config
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) { // EOF: an empty file
		return c, fmt.Errorf("%s: %w", path, err)
	}
	c.inFile = fileKeys(data)
	// Validate first: it stores the parsed production entries on c.
	err = c.Validate()
	return c, err
}

// InFile reports whether the config file set key, written with dots for
// nested keys ("modules", "interactions.levels"), even to its default
// value. compare uses it to tell a setting the user chose from one it
// may take from the snapshot.
func (c Config) InFile(key string) bool { return c.inFile[key] }

// fileKeys lists the keys in a YAML document two levels deep: "pages",
// "pages.limit" and so on. The document has already decoded once, so a
// parse error here can only mean an empty file.
func fileKeys(data []byte) map[string]bool {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	keys := map[string]bool{}
	var walk func(n *yaml.Node, prefix string, depth int)
	walk = func(n *yaml.Node, prefix string, depth int) {
		if n.Kind != yaml.MappingNode || depth == 0 {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := prefix + n.Content[i].Value
			keys[key] = true
			walk(n.Content[i+1], key+".", depth-1)
		}
	}
	walk(doc.Content[0], "", 2)
	return keys
}

// Validate checks values a user could get wrong and parses the
// production entries for IsProduction.
func (c *Config) Validate() error {
	for _, v := range c.Viewports {
		if v != "desktop" && v != "mobile" {
			return fmt.Errorf("viewports: %q isn't desktop or mobile", v)
		}
	}
	for _, m := range c.Modules {
		if !slices.Contains(Modules, m) {
			return fmt.Errorf("modules: %q isn't one of %s", m, strings.Join(Modules, ", "))
		}
	}
	for _, l := range c.Interactions.Levels {
		if !slices.Contains(Levels, l) {
			return fmt.Errorf("interactions.levels: %q isn't one of %s", l, strings.Join(Levels, ", "))
		}
	}
	prod, err := parseProductions(c.Production)
	if err != nil {
		return err
	}
	c.prod, c.prodFrom = prod, slices.Clone(c.Production)
	switch {
	case c.Pages.Limit < 1:
		return errors.New("pages.limit must be at least 1")
	case c.Tabs < 1 || c.Tabs > 16:
		return errors.New("tabs must be between 1 and 16")
	case c.Visual.Threshold < 0 || c.Visual.Threshold > 100:
		return errors.New("visual.threshold is a percentage, 0 to 100")
	case c.Timeout < 5:
		return errors.New("timeout must be at least 5 seconds")
	}
	if err := CheckRate(c.Rate); err != nil {
		return fmt.Errorf("rate: %w", err)
	}
	if err := c.Login.validate(); err != nil {
		return err
	}
	return c.Echo.validate(c.Has("echo"))
}

// validate checks login.url is a path or an http(s) URL. Whether a URL is
// on the target site is checked when logging in, once the site is known.
func (l Login) validate() error {
	if l.URL == "" {
		return nil
	}
	u, err := url.Parse(l.URL)
	if err != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") || (u.Scheme != "" && u.Host == "") {
		return fmt.Errorf("login.url: %q isn't a path like /wp-login.php", l.URL)
	}
	return nil
}

func (e Echo) validate(on bool) error {
	if on && e.URL == "" {
		return errors.New("modules: echo needs Echo's address: --echo, SOUNDCHECK_ECHO or echo.url")
	}
	if e.URL != "" {
		u, err := url.Parse(e.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("echo.url: %q isn't an http(s) URL like %s", e.URL, EchoURL)
		}
	}
	switch {
	case e.Quiet < 100*time.Millisecond || e.Quiet > 30*time.Second:
		return errors.New("echo.quiet must be between 100ms and 30s")
	case e.Timeout < e.Quiet || e.Timeout > time.Minute:
		return errors.New("echo.timeout must be at least echo.quiet and at most 1m")
	case strings.ContainsAny(e.TokenEnv, " =$"):
		return fmt.Errorf("echo.token_env: %q isn't an environment variable name", e.TokenEnv)
	case e.TokenEnv != "" && !strings.HasPrefix(e.TokenEnv, EchoTokenPrefix):
		// A committed config must not pick which secret is sent where.
		return fmt.Errorf("echo.token_env: %q must start with %s", e.TokenEnv, EchoTokenPrefix)
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
// would hammer the site; the comparison is written to catch NaN too.
func CheckRate(rate float64) error {
	if !(rate >= MinRate && rate <= MaxRate) {
		return fmt.Errorf("%v isn't between %v and %v page loads a second", rate, MinRate, MaxRate)
	}
	return nil
}

// Has reports whether a module is enabled.
func (c Config) Has(module string) bool { return slices.Contains(c.Modules, module) }

// Level reports whether an interaction level is enabled.
func (c Config) Level(level string) bool { return slices.Contains(c.Interactions.Levels, level) }
