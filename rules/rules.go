// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package rules loads error signatures written as YAML, so a new plugin
// or framework error page needs a rule file, not Go. The health module
// evaluates them on every page (see Set.Check).
//
// A rule is one file:
//
//	id: health/elementor-missing-widget  # must start with health/
//	match: text                          # text, header, console or status
//	pattern: 'Widget \S+ not found'      # RE2, at most 512 characters
//	level: error                         # error, warning or notice
//	title: Elementor widget missing
//	fix: Reinstall the add-on that provides this widget.
//	source: 1                            # optional: a capture group (number or name) to show as the source
//	group: error-screen                  # optional: see below
//	priority: 10                         # optional: order within a group
//
// It matches against the page's visible text, each response header of
// the page as "name: value" (names lowercase), each console error, or
// the HTTP status as digits. A rule reports one issue per page, or one
// per distinct source when it has a source group. Rules sharing a group
// report at most one issue per page: the first that matches, tried by
// priority (highest first). The health module's error screens (WordPress
// "critical error", database errors, framework error pages) are such a
// group, so a page gets the one that explains it best.
//
// Every rule file has a sibling <name>_test.yaml with samples that must
// match and samples that must not:
//
//	positive:
//	  - name: Elementor 3 message
//	    text: 'Widget "price-table" not found.'
//	    source: '"price-table"'  # optional: the source the issue must have
//	negative:
//	  - name: a page that mentions widgets
//	    text: Our widget shop has 40 widgets.
//
// A sample can set text, headers (a map), console (a list) and status
// (200 when left out). Test runs them.
//
// Built-in rules are embedded from builtin/. A project's own rules live
// in .soundcheck/rules/ and load after them; they can't reuse a built-in
// id or one of the health module's own checks, so they can't replace or
// downgrade what Soundcheck already reports. The loader only reads an
// fs.FS and knows nothing of health, so it can move to the shared kit.
//
// A source goes into run.json and reports. One taken from the value of a
// header that carries credentials (cookie, set-cookie, authorization and
// similar) is masked; tokens the kit recognizes in any other source are
// redacted.
package rules

import (
	"bytes"
	"cmp"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Rule is one error signature.
type Rule struct {
	ID       string      `yaml:"id"`
	Match    string      `yaml:"match"`
	Pattern  string      `yaml:"pattern"`
	Level    model.Level `yaml:"level"`
	Title    string      `yaml:"title"`
	Fix      string      `yaml:"fix,omitempty"`
	Source   string      `yaml:"source,omitempty"`
	Group    string      `yaml:"group,omitempty"`
	Priority int         `yaml:"priority,omitempty"`

	// File is where the rule was read from, relative to its folder.
	File string `yaml:"-"`
	// Builtin is set on rules embedded in Soundcheck.
	Builtin bool `yaml:"-"`

	re  *regexp.Regexp
	src int // capture group for Source; 0 for none
}

// Kinds of input a rule can match.
const (
	Text    = "text"
	Header  = "header"
	Console = "console"
	Status  = "status"
)

// MaxPattern is the longest pattern a rule may have, in characters.
const MaxPattern = 512

// maxHits caps the issues one rule reports on one page.
const maxHits = 20

var idPattern = regexp.MustCompile(`^health/[a-z0-9]+(?:-[a-z0-9]+)*(?:/[a-z0-9]+(?:-[a-z0-9]+)*)*$`)

// Set is a loaded list of rules, built-in ones first.
type Set struct {
	rules []*Rule
}

// Rules returns the rules in the order Check tries them.
func (s *Set) Rules() []*Rule {
	if s == nil {
		return nil
	}
	return slices.Clone(s.rules)
}

// Rule returns the rule with the given id, or nil.
func (s *Set) Rule(id string) *Rule {
	if s == nil {
		return nil
	}
	i := slices.IndexFunc(s.rules, func(r *Rule) bool { return r.ID == id })
	if i < 0 {
		return nil
	}
	return s.rules[i]
}

//go:embed builtin/*.yaml
var builtinFS embed.FS

var builtin = sync.OnceValues(func() (*Set, error) {
	sub, err := fs.Sub(builtinFS, "builtin")
	if err != nil {
		return nil, err
	}
	rs, err := parseFS(sub, true)
	if err != nil {
		return nil, err
	}
	return newSet(rs), nil
})

// Builtin returns the rules embedded in Soundcheck. They're tested at
// build time, so an error here is a bug in Soundcheck.
func Builtin() *Set {
	s, err := builtin()
	if err != nil {
		panic("rules: built-in rules: " + err.Error())
	}
	return s
}

// Load returns the built-in rules followed by the rules in dir (a
// missing dir has none). reserved lists ids that rules in dir can't use
// besides the built-in ones: the checks a module reports from Go. Every
// problem in dir is reported, each with its file.
func Load(dir string, reserved ...string) (*Set, error) {
	base := Builtin()
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return base, nil
	}
	user, err := parseFS(os.DirFS(dir), false)
	if err != nil {
		return nil, err
	}
	var errs []error
	taken := map[string]bool{}
	for _, id := range reserved {
		taken[id] = true
	}
	for _, r := range base.rules {
		taken[r.ID] = true
	}
	for _, r := range user {
		if taken[r.ID] {
			errs = append(errs, fmt.Errorf("%s: id %s belongs to a built-in check; rules can add checks, not replace or change them", r.File, r.ID))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return newSet(append(slices.Clone(base.rules), user...)), nil
}

// newSet orders rules for Check: built-in ones first, then by priority
// (which only decides anything within a group) and file name.
func newSet(rs []*Rule) *Set {
	slices.SortStableFunc(rs, func(a, b *Rule) int {
		if a.Builtin != b.Builtin {
			if a.Builtin {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(b.Priority, a.Priority), strings.Compare(a.File, b.File))
	})
	return &Set{rules: rs}
}

// IsFixture reports whether a file name is a rule's samples.
func IsFixture(name string) bool { return strings.HasSuffix(name, "_test.yaml") }

// parseFS reads every rule in the top folder of fsys and checks each has
// samples. Ids must be unique.
func parseFS(fsys fs.FS, isBuiltin bool) ([]*Rule, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var errs []error
	var out []*Rule
	files := map[string]bool{}
	for _, e := range entries {
		files[e.Name()] = true
	}
	ids := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || path.Ext(name) != ".yaml" {
			continue
		}
		if IsFixture(name) {
			if !files[strings.TrimSuffix(name, "_test.yaml")+".yaml"] {
				errs = append(errs, fmt.Errorf("%s: samples without a rule (%s is missing)", name, strings.TrimSuffix(name, "_test.yaml")+".yaml"))
			}
			continue
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		r, err := Parse(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		r.File, r.Builtin = name, isBuiltin
		if other, ok := ids[r.ID]; ok {
			errs = append(errs, fmt.Errorf("%s: id %s is already used by %s", name, r.ID, other))
			continue
		}
		ids[r.ID] = name
		if _, err := samplesFor(fsys, r); err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, r)
	}
	return out, errors.Join(errs...)
}

// Parse reads and checks one rule.
func Parse(data []byte) (*Rule, error) {
	var r Rule
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("not a rule: %w", err)
	}
	if err := r.compile(); err != nil {
		return nil, err
	}
	return &r, nil
}

func (r *Rule) compile() error {
	switch {
	case !idPattern.MatchString(r.ID):
		return fmt.Errorf("id %q must start with health/ and use lowercase letters, digits and dashes (health/elementor-missing-widget)", r.ID)
	case !slices.Contains([]string{Text, Header, Console, Status}, r.Match):
		return fmt.Errorf("%s: match %q must be text, header, console or status", r.ID, r.Match)
	case r.Pattern == "":
		return fmt.Errorf("%s: pattern is empty", r.ID)
	case utf8.RuneCountInString(r.Pattern) > MaxPattern:
		return fmt.Errorf("%s: pattern is %d characters; the most is %d", r.ID, utf8.RuneCountInString(r.Pattern), MaxPattern)
	case !slices.Contains([]model.Level{model.Error, model.Warning, model.Notice}, r.Level):
		return fmt.Errorf("%s: level %q must be error, warning or notice", r.ID, r.Level)
	case strings.TrimSpace(r.Title) == "":
		return fmt.Errorf("%s: title is empty", r.ID)
	}
	re, err := regexp.Compile(r.Pattern)
	if err != nil {
		return fmt.Errorf("%s: pattern: %w", r.ID, err)
	}
	r.re = re
	if r.Source != "" {
		if n, err := strconv.Atoi(r.Source); err == nil {
			r.src = n
		} else {
			r.src = re.SubexpIndex(r.Source)
		}
		if r.src < 1 || r.src > re.NumSubexp() {
			return fmt.Errorf("%s: source %q isn't a capture group of the pattern", r.ID, r.Source)
		}
	}
	return nil
}

// Hit is an issue a rule found.
type Hit struct {
	Rule  *Rule
	Issue model.Issue
}

// Check evaluates every rule on a page and returns what matched, in rule
// order. The issues have no key yet (check.Page sets it).
func (s *Set) Check(p *model.Page) []Hit {
	if s == nil {
		return nil
	}
	var out []Hit
	done := map[string]bool{}
	for _, r := range s.rules {
		if r.Group != "" && done[r.Group] {
			continue
		}
		hits := r.find(p)
		if len(hits) > 0 && r.Group != "" {
			done[r.Group] = true
		}
		out = append(out, hits...)
	}
	return out
}

// find returns the rule's issues on a page: one, or one per distinct
// source.
func (r *Rule) find(p *model.Page) []Hit {
	var out []Hit
	seen := map[string]bool{}
	for _, in := range r.inputs(p) {
		for _, m := range r.re.FindAllStringSubmatchIndex(in, maxHits) {
			src := ""
			if r.src > 0 && m[2*r.src] >= 0 {
				src = r.source(in, m[2*r.src], m[2*r.src+1])
			}
			if seen[src] {
				continue
			}
			seen[src] = true
			out = append(out, Hit{Rule: r, Issue: model.Issue{Check: r.ID, Level: r.Level, Title: r.Title, Fix: r.Fix, Source: src}})
			if r.src == 0 || len(out) == maxHits {
				return out
			}
		}
	}
	return out
}

// secretHeaders are the response headers whose values are credentials.
// A project rule may capture one as its source; the value is masked the
// way Soundcheck shows secrets everywhere else.
var secretHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"proxy-authorization": true,
	"set-cookie":          true,
	"www-authenticate":    true,
	"x-api-key":           true,
	"x-auth-token":        true,
	"x-csrf-token":        true,
}

// source is in[start:end] on one line. The part that lies in the value
// of a secret header is masked; elsewhere tokens kit/secrets knows are
// redacted.
func (r *Rule) source(in string, start, end int) string {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if r.Match == Header {
		if name, _, ok := strings.Cut(in, ": "); ok && secretHeaders[name] && end > len(name)+2 {
			at := max(start, len(name)+2)
			return norm(in[start:at] + secrets.Mask(strings.TrimSpace(in[at:end]), 2))
		}
	}
	return secrets.Redact(norm(in[start:end]))
}

func (r *Rule) inputs(p *model.Page) []string {
	switch r.Match {
	case Text:
		return []string{p.Text}
	case Console:
		return p.Console
	case Status:
		return []string{strconv.Itoa(p.Status)}
	}
	names := make([]string, 0, len(p.Headers))
	for k := range p.Headers {
		names = append(names, k)
	}
	slices.Sort(names)
	out := make([]string, 0, len(names))
	for _, k := range names {
		out = append(out, strings.ToLower(k)+": "+p.Headers[k])
	}
	return out
}
