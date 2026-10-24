// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package rules

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Sample is a page a rule must match (positive) or must not (negative).
type Sample struct {
	Name    string            `yaml:"name"`
	Text    string            `yaml:"text,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Console []string          `yaml:"console,omitempty"`
	Status  int               `yaml:"status,omitempty"` // 200 when left out
	// Source, on a positive sample, is the source the issue must have.
	Source string `yaml:"source,omitempty"`
}

// Samples are a rule's fixtures, from its _test.yaml.
type Samples struct {
	Positive []Sample `yaml:"positive"`
	Negative []Sample `yaml:"negative"`
}

func samplesFor(fsys fs.FS, r *Rule) (*Samples, error) {
	name := strings.TrimSuffix(r.File, ".yaml") + "_test.yaml"
	data, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: no samples; add %s with positive and negative samples", r.File, name)
	}
	if err != nil {
		return nil, err
	}
	var s Samples
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(s.Positive) == 0 || len(s.Negative) == 0 {
		return nil, fmt.Errorf("%s: needs at least one positive and one negative sample", name)
	}
	return &s, nil
}

// page builds the page a sample describes.
func (s Sample) page() *model.Page {
	p := &model.Page{Path: "/", Text: s.Text, Console: s.Console, Status: s.Status, TextLength: len([]rune(s.Text))}
	if p.Status == 0 {
		p.Status = 200
	}
	if len(s.Headers) > 0 {
		p.Headers = map[string]string{}
		for k, v := range s.Headers {
			p.Headers[strings.ToLower(k)] = v
		}
	}
	return p
}

// Report is what Test found: how much it ran and every sample that
// didn't behave, as "file: sample: what happened".
type Report struct {
	Rules    int
	Samples  int
	Failures []string
}

// Test loads the rules in dir and runs each one's samples. Rules that
// don't load (a bad pattern, missing samples, a reserved id) are the
// error; samples that fail are in the report.
func Test(dir string, reserved ...string) (Report, error) {
	if _, err := os.Stat(dir); err != nil {
		return Report{}, err
	}
	set, err := Load(dir, reserved...)
	if err != nil {
		return Report{}, err
	}
	var own []*Rule
	for _, r := range set.rules {
		if !r.Builtin {
			own = append(own, r)
		}
	}
	return run(os.DirFS(dir), own)
}

// TestBuiltin runs the built-in rules' samples.
func TestBuiltin() (Report, error) {
	sub, err := fs.Sub(builtinFS, "builtin")
	if err != nil {
		return Report{}, err
	}
	return run(sub, Builtin().rules)
}

func run(fsys fs.FS, rs []*Rule) (Report, error) {
	var rep Report
	for _, r := range rs {
		s, err := samplesFor(fsys, r)
		if err != nil {
			return rep, err
		}
		rep.Rules++
		file := strings.TrimSuffix(r.File, ".yaml") + "_test.yaml"
		for i, sm := range s.Positive {
			rep.Samples++
			hits := r.find(sm.page())
			name := cmpName(sm.Name, "positive", i)
			switch {
			case len(hits) == 0:
				rep.Failures = append(rep.Failures, fmt.Sprintf("%s: %s: %s didn't match", file, name, r.ID))
			case sm.Source != "" && !slices.ContainsFunc(hits, func(h Hit) bool { return h.Issue.Source == sm.Source }):
				rep.Failures = append(rep.Failures, fmt.Sprintf("%s: %s: source is %q, want %q", file, name, hits[0].Issue.Source, sm.Source))
			}
		}
		for i, sm := range s.Negative {
			rep.Samples++
			if hits := r.find(sm.page()); len(hits) > 0 {
				rep.Failures = append(rep.Failures, fmt.Sprintf("%s: %s: %s matched; it mustn't", file, cmpName(sm.Name, "negative", i), r.ID))
			}
		}
	}
	return rep, nil
}

func cmpName(name, kind string, i int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("%s sample %d", kind, i+1)
}
