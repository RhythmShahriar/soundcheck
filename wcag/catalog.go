// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import (
	"bytes"
	_ "embed" // criteria.yaml
	"fmt"
	"maps"
	"slices"
	"sync"

	"gopkg.in/yaml.v3"
)

// criteriaYAML is the catalog: every A and AA criterion of WCAG 2.0, 2.1
// and 2.2 with how Soundcheck tests it.
//
//go:embed criteria.yaml
var criteriaYAML []byte

// entry is one criterion of criteria.yaml.
type entry struct {
	ID        string   `yaml:"id"`
	Title     string   `yaml:"title"`
	Slug      string   `yaml:"slug"`
	Level     string   `yaml:"level"`
	Principle string   `yaml:"principle"`
	Since     string   `yaml:"since"`
	Removed   string   `yaml:"removed"`
	EN        string   `yaml:"en"`
	Coverage  string   `yaml:"coverage"`
	TestedBy  []string `yaml:"tested_by"`
	OnlyFails bool     `yaml:"only_failures"`
	Note      string   `yaml:"note"`
}

// parseCatalog reads criteria.yaml strictly: an unknown key is an error,
// so a typo can't drop a mapping without a test noticing.
func parseCatalog(data []byte) ([]entry, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var out []entry
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("criteria.yaml: %w", err)
	}
	return out, nil
}

// catalog is criteria.yaml, read once. The file is embedded and checked
// by the tests, so a bad one is a build mistake.
var catalog = sync.OnceValue(func() []entry {
	e, err := parseCatalog(criteriaYAML)
	if err != nil {
		panic(err)
	}
	return e
})

// table is the criteria of one WCAG version and, by Soundcheck check id,
// the criteria it tests.
type table struct {
	criteria []Criterion
	byCheck  map[string][]string
}

// tables builds the table of every version once: ForCheck runs for
// every issue of a run.
var tables = sync.OnceValue(func() map[string]table {
	rules := slices.Sorted(maps.Keys(AxeRules))
	out := map[string]table{}
	for _, v := range Versions {
		t := table{byCheck: map[string][]string{}}
		for _, e := range catalog() {
			if older(v, e.Since) || (e.Removed != "" && !older(v, e.Removed)) {
				continue
			}
			c := Criterion{ID: e.ID, Title: e.Title, Slug: e.Slug, Level: e.Level, Principle: e.Principle,
				Since: e.Since, EN: e.EN, Note: e.Note, Coverage: CoverageNone, OnlyFails: e.OnlyFails}
			// coverage: none is a promise that nothing tests the criterion,
			// so axe rules naming it (4.1.1's obsolete ones) don't count.
			if e.Coverage == CoveragePartly {
				c.TestedBy = slices.Clone(e.TestedBy)
				for _, id := range e.TestedBy {
					t.byCheck[id] = append(t.byCheck[id], e.ID)
				}
				for _, rule := range rules {
					// A rule that can only ask for review never finds
					// a failure: it doesn't test the criterion.
					if r := AxeRules[rule]; r.Runs(v) && CanFail(rule) && slices.Contains(r.WCAG, e.ID) {
						c.TestedBy = append(c.TestedBy, "axe:"+rule)
					}
				}
				if len(c.TestedBy) > 0 {
					c.Coverage = CoveragePartly
				}
			}
			t.criteria = append(t.criteria, c)
		}
		slices.SortFunc(t.criteria, func(a, b Criterion) int { return Compare(a.ID, b.ID) })
		for _, ids := range t.byCheck {
			slices.SortFunc(ids, Compare)
		}
		out[v] = t
	}
	return out
})

// orDefault is version with empty read as 2.0, the rules of a run made
// before 1.3.
func orDefault(version string) string {
	if version == "" {
		return "2.0"
	}
	return version
}
