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
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Dir is the project folder Soundcheck keeps snapshots and reports in.
const Dir = ".soundcheck"

// Modules Soundcheck can run, in order.
var Modules = []string{"health", "visual", "interact", "cache", "speed"}

// Interaction levels, from safest to most invasive.
var Levels = []string{"inspect", "click", "submit", "flows"}

type Config struct {
	Pages        Pages        `yaml:"pages"`
	Viewports    []string     `yaml:"viewports"` // desktop, mobile
	Modules      []string     `yaml:"modules"`
	Visual       Visual       `yaml:"visual"`
	Interactions Interactions `yaml:"interactions"`
	Production   []string     `yaml:"production"` // URLs (prefixes) that are live sites: form submits refuse them
	Login        Login        `yaml:"login"`
	Flows        string       `yaml:"flows"` // folder of YAML flows, relative to the project
	Speed        Speed        `yaml:"speed"`
	Rate         float64      `yaml:"rate"` // page loads per second per host
	Tabs         int          `yaml:"tabs"`
	Timeout      int          `yaml:"timeout"` // seconds per page
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

type Login struct {
	URL  string `yaml:"url"`  // login page; default /wp-login.php for WordPress
	Flow string `yaml:"flow"` // a YAML flow that logs in, for non-WordPress sites
}

type Speed struct {
	Slower float64 `yaml:"slower"` // % slower before a page is reported SLOWER
}

// Default returns the settings used when there's no config file.
func Default() Config {
	return Config{
		Pages:        Pages{Limit: 300, PerGroup: 3},
		Viewports:    []string{"desktop", "mobile"},
		Modules:      slices.Clone(Modules),
		Visual:       Visual{Threshold: 0.5},
		Interactions: Interactions{Levels: []string{"inspect", "click"}, MaxClicks: 15},
		Flows:        filepath.Join(Dir, "flows"),
		Speed:        Speed{Slower: 25},
		Rate:         5,
		Tabs:         8,
		Timeout:      30,
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
	return c, c.Validate()
}

// Validate checks values a user could get wrong.
func (c Config) Validate() error {
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
	return nil
}

// Has reports whether a module is enabled.
func (c Config) Has(module string) bool { return slices.Contains(c.Modules, module) }

// Level reports whether an interaction level is enabled.
func (c Config) Level(level string) bool { return slices.Contains(c.Interactions.Levels, level) }

// IsProduction reports whether url is under one of the production URLs.
func (c Config) IsProduction(url string) bool {
	u := strings.TrimRight(strings.ToLower(url), "/")
	return slices.ContainsFunc(c.Production, func(p string) bool {
		p = strings.TrimRight(strings.ToLower(p), "/")
		return p != "" && (u == p || strings.HasPrefix(u, p+"/"))
	})
}
