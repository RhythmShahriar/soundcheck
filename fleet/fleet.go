// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package fleet checks many sites in one go, for agencies: a sites.yaml
// lists each client site, Run takes a snapshot of every one or compares
// every one with its snapshot, and fleet.html ranks them worst first.
//
// Each site runs as its own Soundcheck process in its own folder
// (.soundcheck-fleet/<name>/, next to sites.yaml), with only its own
// credentials in its environment.
package fleet

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/store"
)

// Folder holds every site's store and the fleet page, next to sites.yaml.
const Folder = ".soundcheck-fleet"

// Site is one entry of sites.yaml.
type Site struct {
	// Name names the site's folder and its row on the fleet page.
	Name string `yaml:"name"`
	// URL is the site to check.
	URL string `yaml:"url"`
	// Config is the site's Soundcheck config file, relative to sites.yaml.
	// Empty uses Soundcheck's defaults.
	Config string `yaml:"config"`
	// EnvPrefix picks the site's own variables: ACME_SOUNDCHECK_PASSWORD
	// reaches this site's run as SOUNDCHECK_PASSWORD. Empty derives it
	// from Name ("acme-shop" gives ACME_SHOP).
	EnvPrefix string `yaml:"env_prefix"`
	// Args are extra Soundcheck flags for this site, e.g. ["--limit", "50"].
	Args []string `yaml:"args"`
}

// Modes Run accepts.
const (
	Snapshot = "snapshot"
	Compare  = "compare"
)

var envPrefix = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// reserved are flags the fleet sets itself: a site's args can't change
// where results go or which config is read.
var reserved = []string{"json", "md", "junit", "html", "o", "output", "config", "open", "name"}

// Load reads and checks a sites file. It accepts a list of sites, or a
// mapping with the list under "sites".
func Load(path string) ([]Site, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the user's own sites file
	if err != nil {
		return nil, err
	}
	sites, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := validate(sites, filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return sites, nil
}

func parse(data []byte) ([]Site, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 {
		return nil, errors.New("no sites listed")
	}
	node := root.Content[0]
	if node.Kind == yaml.MappingNode {
		var top struct {
			Sites yaml.Node `yaml:"sites"`
		}
		if err := decodeStrict(node, &top); err != nil {
			return nil, err
		}
		node = &top.Sites
	}
	var sites []Site
	if err := decodeStrict(node, &sites); err != nil {
		return nil, err
	}
	return sites, nil
}

// decodeStrict decodes a node, rejecting unknown keys so a typo like
// "env-prefix" isn't silently ignored.
func decodeStrict(n *yaml.Node, v any) error {
	b, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	return dec.Decode(v)
}

func validate(sites []Site, dir string) error {
	if len(sites) == 0 {
		return errors.New("no sites listed")
	}
	names, prefixes := map[string]string{}, map[string]string{}
	for i := range sites {
		s := &sites[i]
		where := fmt.Sprintf("site %d", i+1)
		if s.Name != "" {
			where = fmt.Sprintf("site %q", s.Name)
		}
		if !store.ValidName(s.Name) {
			return fmt.Errorf("%s: name must be letters, digits, dots, dashes and underscores (up to 64), starting with a letter or digit", where)
		}
		// Folders on macOS and Windows ignore case, so "Acme" and "acme"
		// would share one store.
		if other, ok := names[strings.ToLower(s.Name)]; ok {
			return fmt.Errorf("%s: the name is already used by site %q", where, other)
		}
		names[strings.ToLower(s.Name)] = s.Name
		if err := checkURL(s.URL); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if s.Config != "" {
			if _, err := os.Stat(filepath.Join(dir, s.Config)); err != nil { //nolint:gosec // the user's own config path
				return fmt.Errorf("%s: config %s: %w", where, s.Config, err)
			}
		}
		if s.EnvPrefix == "" {
			s.EnvPrefix = DefaultPrefix(s.Name)
		}
		if !envPrefix.MatchString(s.EnvPrefix) {
			return fmt.Errorf("%s: env_prefix %q must be upper-case letters, digits and underscores, starting with a letter", where, s.EnvPrefix)
		}
		if other, ok := prefixes[s.EnvPrefix]; ok {
			return fmt.Errorf("%s: env_prefix %s is already used by site %q; give each site its own", where, s.EnvPrefix, other)
		}
		prefixes[s.EnvPrefix] = s.Name
		for _, a := range s.Args {
			name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if strings.HasPrefix(a, "-") && slices.Contains(reserved, name) {
				return fmt.Errorf("%s: args can't set %s; the fleet sets it for every site", where, a)
			}
		}
	}
	return nil
}

func checkURL(raw string) error {
	if raw == "" {
		return errors.New("url is missing")
	}
	s := raw
	if !strings.Contains(s, "://") {
		s = "https://" + s // Soundcheck accepts a bare host
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("url %q isn't an http or https address", raw)
	}
	return nil
}

// DefaultPrefix is the env prefix for a site name: upper case, with
// anything but letters and digits turned into "_", and SITE_ in front
// when the name starts with a digit.
func DefaultPrefix(name string) string {
	p := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 'a' + 'A'
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, name)
	if p == "" || p[0] < 'A' || p[0] > 'Z' {
		p = "SITE_" + p
	}
	return p
}
