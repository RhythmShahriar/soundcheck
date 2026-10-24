// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Manifest is testbed/scenarios.yaml.
type Manifest struct {
	Sites     map[string]string `yaml:"sites"`
	Control   Control           `yaml:"control"`
	Scenarios []Scenario        `yaml:"scenarios"`
}

// Control is the control pass: the sites it checks with nothing broken,
// and how many pages of each.
type Control struct {
	Sites []string `yaml:"sites"`
	Limit int      `yaml:"limit"`
}

// Scenario is one broken-site case.
type Scenario struct {
	ID      string   `yaml:"id"`
	Name    string   `yaml:"name"`
	Site    string   `yaml:"site"`
	Path    string   `yaml:"path"`
	Include []string `yaml:"include"`
	Module  string   `yaml:"module"`
	Levels  []string `yaml:"levels"`
	Login   bool     `yaml:"login"`
	Toggle  string   `yaml:"toggle"`
	Expect  []Expect `yaml:"expect"`
	Forbid  []Expect `yaml:"forbid"`
	Tools   []string `yaml:"tools"`

	Args    []string `yaml:"args"`    // extra flags for snapshot and compare
	Modules []string `yaml:"modules"` // --modules, when set
	Limit   int      `yaml:"limit"`   // page limit (default 20)
	// PerGroup is pages.per_group: how many pages of one URL pattern
	// are checked (-1 for all).
	PerGroup int      `yaml:"per_group"`
	Echo     bool     `yaml:"echo"`    // needs Echo (-echo)
	Rules    []string `yaml:"rules"`   // rule files under testbed/, copied to .soundcheck/rules/
	Harness  string   `yaml:"harness"` // see harnesses

	// InterruptAfter is when harness interrupt sends Ctrl+C.
	InterruptAfter time.Duration `yaml:"interrupt_after"`
	// Timeout is the time limit for each of the scenario's commands when
	// it needs more than -timeout (a big site).
	Timeout time.Duration `yaml:"timeout"`

	// Pending marks a scenario whose detector isn't merged yet; the
	// scoreboard reports it apart from the score. Note says what it waits for.
	Pending bool   `yaml:"pending"`
	Note    string `yaml:"note"`
}

// Harnesses are what the benchmark can do besides switching the state.
var harnesses = []string{"", "interrupt", "parallel", "delete-shots", "stress", "wp-update"}

// ChangesSite reports whether the scenario's state file changes the
// site. The parallel and stress cases only change how Soundcheck runs;
// wp-update breaks the site by updating plugins, not through a state file.
func (s Scenario) ChangesSite() bool {
	return s.Harness != "parallel" && s.Harness != "stress" && s.Harness != "wp-update"
}

// Expect is one acceptable result. Exactly one kind is set:
//
//   - a change of Class (any class when empty) whose check starts with
//     Check and whose title contains Title (case-insensitive)
//   - Warning: a comparison warning containing the text
//   - Interrupted: the comparison is marked interrupted
//   - Clean: nothing at or above NEW
//   - Exit: compare's exit code
//   - Culprit: wp-update names this plugin as the one that broke the
//     site, in at most Compares compares when that is set (harness
//     wp-update only)
//
// Forbid entries use the change form only.
type Expect struct {
	Class       string `yaml:"class"`
	Check       string `yaml:"check"`
	Title       string `yaml:"title"`
	Warning     string `yaml:"warning"`
	Interrupted bool   `yaml:"interrupted"`
	Clean       bool   `yaml:"clean"`
	Exit        *int   `yaml:"exit"`
	Culprit     string `yaml:"culprit"`
	Compares    int    `yaml:"compares"`
}

// Kinds of Expect.
const (
	kindChange      = "change"
	kindWarning     = "warning"
	kindInterrupted = "interrupted"
	kindClean       = "clean"
	kindExit        = "exit"
	kindCulprit     = "culprit"
)

// kind says which kind of expectation e is; "" when it sets none or more
// than one.
func (e Expect) kind() string {
	var kinds []string
	if e.Class != "" || e.Check != "" || e.Title != "" {
		kinds = append(kinds, kindChange)
	}
	if e.Warning != "" {
		kinds = append(kinds, kindWarning)
	}
	if e.Interrupted {
		kinds = append(kinds, kindInterrupted)
	}
	if e.Clean {
		kinds = append(kinds, kindClean)
	}
	if e.Exit != nil {
		kinds = append(kinds, kindExit)
	}
	if e.Culprit != "" || e.Compares != 0 {
		kinds = append(kinds, kindCulprit)
	}
	if len(kinds) != 1 {
		return ""
	}
	return kinds[0]
}

// Match reports whether c is the expected change.
func (e Expect) Match(c model.Change) bool {
	return e.kind() == kindChange && (e.Class == "" || c.Class == e.Class) && e.about(c)
}

// about reports whether c is about the expected problem, whatever its class.
func (e Expect) about(c model.Change) bool {
	return e.kind() == kindChange && strings.HasPrefix(c.Check, e.Check) &&
		strings.Contains(strings.ToLower(c.Title), strings.ToLower(e.Title))
}

func (e Expect) String() string {
	switch e.kind() {
	case kindWarning:
		return "warning “" + e.Warning + "”"
	case kindInterrupted:
		return "marked interrupted"
	case kindClean:
		return "nothing at or above NEW"
	case kindExit:
		return fmt.Sprintf("exit %d", *e.Exit)
	case kindCulprit:
		if e.Compares > 0 {
			return fmt.Sprintf("culprit %s in at most %d %s", e.Culprit, e.Compares, plural(e.Compares, "compare"))
		}
		return "culprit " + e.Culprit
	}
	s := strings.ToUpper(e.Class)
	if s == "" {
		s = "any"
	}
	if e.Check != "" {
		s += " " + e.Check
	}
	if e.Title != "" {
		s += " “" + e.Title + "”"
	}
	return s
}

// scenarioID is the form toggle.sh accepts.
var scenarioID = regexp.MustCompile(`^s[0-9]{2}$`)

// LoadManifest reads and checks scenarios.yaml.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the testbed's own manifest
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelled field would silently drop an expectation
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := m.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// check validates the manifest: unique ids toggle.sh accepts, known
// sites, a toggle and at least one well-formed expectation each.
func (m *Manifest) check() error {
	for _, site := range m.Control.Sites {
		if m.Sites[site] == "" {
			return fmt.Errorf("control: unknown site %q", site)
		}
	}
	seen := map[string]bool{}
	for _, s := range m.Scenarios {
		switch {
		case !scenarioID.MatchString(s.ID) || seen[s.ID]:
			return fmt.Errorf("missing, malformed or duplicate id %q (want s01 to s99, once each)", s.ID)
		case m.Sites[s.Site] == "":
			return fmt.Errorf("%s: unknown site %q", s.ID, s.Site)
		case !strings.HasPrefix(s.Path, "/"):
			return fmt.Errorf("%s: path must start with /", s.ID)
		case strings.TrimSpace(s.Name) == "":
			return fmt.Errorf("%s: no name", s.ID)
		case strings.TrimSpace(s.Toggle) == "":
			return fmt.Errorf("%s: no toggle (say what the broken state changes)", s.ID)
		case len(s.Expect) == 0:
			return fmt.Errorf("%s: no expect", s.ID)
		case !slices.Contains(harnesses, s.Harness):
			return fmt.Errorf("%s: unknown harness %q", s.ID, s.Harness)
		case s.Harness == "interrupt" && s.InterruptAfter <= 0:
			return fmt.Errorf("%s: harness interrupt needs interrupt_after", s.ID)
		case s.Limit < 0:
			return fmt.Errorf("%s: negative limit", s.ID)
		case s.Harness == "wp-update" && len(s.Forbid) > 0:
			return fmt.Errorf("%s: harness wp-update scores culprits; forbid doesn't apply", s.ID)
		}
		for _, e := range s.Expect {
			if err := checkExpect(s, e, false); err != nil {
				return err
			}
		}
		for _, e := range s.Forbid {
			if err := checkExpect(s, e, true); err != nil {
				return err
			}
		}
		seen[s.ID] = true
	}
	return nil
}

func checkExpect(s Scenario, e Expect, forbid bool) error {
	kind := e.kind()
	switch {
	case kind == "":
		return fmt.Errorf("%s: expect %+v must set exactly one of class/check/title, warning, interrupted, clean, exit or culprit", s.ID, e)
	case (kind == kindCulprit) != (s.Harness == "wp-update"):
		return fmt.Errorf("%s: only harness wp-update expects culprits and it expects nothing else", s.ID)
	case kind == kindCulprit && (e.Culprit == "" || e.Compares < 0):
		return fmt.Errorf("%s: culprit needs a plugin slug and compares can't be negative", s.ID)
	case forbid && kind != kindChange:
		return fmt.Errorf("%s: forbid entries are changes (class, check, title)", s.ID)
	case kind != kindChange || e.Class == "" || slices.Contains(model.Classes, e.Class):
		return nil
	case s.Pending && e.Class == strings.ToLower(e.Class):
		return nil // a class that is being added, e.g. unstable
	}
	return fmt.Errorf("%s: unknown class %q", s.ID, e.Class)
}

// URL is where the scenario's run starts.
func (m *Manifest) URL(s Scenario) string {
	return strings.TrimRight(m.Sites[s.Site], "/") + s.Path
}

// Pick returns the scenarios whose IDs are in only (all when empty).
func (m *Manifest) Pick(only []string) ([]Scenario, error) {
	if len(only) == 0 {
		return m.Scenarios, nil
	}
	var out []Scenario
	for _, id := range only {
		i := slices.IndexFunc(m.Scenarios, func(s Scenario) bool { return s.ID == id })
		if i < 0 {
			return nil, fmt.Errorf("unknown scenario %q", id)
		}
		out = append(out, m.Scenarios[i])
	}
	return out, nil
}

// Outcomes of a scenario for one tool.
const (
	Caught     = "caught"
	OtherClass = "other class" // found the problem, but classed differently
	FalseAlarm = "false alarm" // reported a change the scenario forbids
	Missed     = "missed"
	Failed     = "error" // the tool didn't produce a result
	NotRun     = "not run"
)

// Result is how one tool did on one scenario.
type Result struct {
	Outcome string   `json:"outcome"`
	Found   string   `json:"found,omitempty"`  // the change that matched, or the tool's evidence
	Detail  string   `json:"detail,omitempty"` // why it errored or wasn't run
	Others  []string `json:"others,omitempty"` // other changes the comparison reported
}

// Observed is what one Soundcheck compare produced.
type Observed struct {
	Comparison  *model.Comparison // nil when compare wrote none
	Interrupted bool              // the comparison says it was interrupted
	Exit        int               // compare's exit code
}

// Evaluate checks a Soundcheck compare against the scenario.
func Evaluate(s Scenario, obs Observed) Result {
	var r Result
	var about *model.Change
	cmp := obs.Comparison
	if cmp == nil {
		cmp = &model.Comparison{}
	}
	for i, c := range cmp.Changes {
		matched := false
		for _, e := range s.Expect {
			if e.Match(c) && r.Outcome != Caught {
				r.Outcome, r.Found, matched = Caught, describe(c), true
			}
			if e.about(c) {
				matched = true
				if about == nil {
					about = &cmp.Changes[i]
				}
			}
		}
		if !matched {
			r.Others = append(r.Others, describe(c))
		}
	}
	for _, e := range s.Expect {
		if r.Outcome == Caught {
			break
		}
		switch e.kind() {
		case kindWarning:
			for _, w := range cmp.Warnings {
				if strings.Contains(strings.ToLower(w), strings.ToLower(e.Warning)) {
					r.Outcome, r.Found = Caught, "warning: "+w
					break
				}
			}
		case kindInterrupted:
			if obs.Interrupted {
				r.Outcome, r.Found = Caught, "the comparison is marked interrupted"
			}
		case kindClean:
			if alarms := FalseAlarms(cmp); len(alarms) == 0 {
				r.Outcome, r.Found = Caught, "nothing at or above NEW"
			}
		case kindExit:
			if obs.Exit == *e.Exit {
				r.Outcome, r.Found = Caught, fmt.Sprintf("exit %d", obs.Exit)
			}
		}
	}
	for _, c := range cmp.Changes {
		for _, f := range s.Forbid {
			if f.Match(c) {
				return Result{Outcome: FalseAlarm, Found: describe(c), Others: r.Others}
			}
		}
	}
	switch {
	case r.Outcome == Caught:
	case about != nil:
		r.Outcome, r.Found = OtherClass, describe(*about)
	default:
		r.Outcome = Missed
		if obs.Comparison == nil {
			r.Detail = fmt.Sprintf("compare wrote no comparison (exit %d)", obs.Exit)
		}
	}
	return r
}

// FalseAlarms lists the changes at or above NEW, which an unchanged site
// should never get.
func FalseAlarms(cmp *model.Comparison) []string {
	var out []string
	for _, c := range cmp.Changes {
		if c.Class == model.Broke || c.Class == model.New {
			out = append(out, describe(c))
		}
	}
	return out
}

func describe(c model.Change) string {
	s := strings.ToUpper(c.Class) + " " + c.Check + ": " + c.Title
	if len(c.Pages) > 0 {
		s += " (" + strings.Join(c.Pages, ", ") + ")"
	}
	return s
}

// Testbed locates the testbed directory (holding scenarios.yaml and
// toggle.sh) from the working directory: the module root, testbed/ or
// testbed/bench.
func Testbed() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for _, d := range []string{filepath.Join(wd, "testbed"), wd, filepath.Dir(wd)} {
		if _, err := os.Stat(filepath.Join(d, "scenarios.yaml")); err == nil {
			if _, err := os.Stat(filepath.Join(d, "toggle.sh")); err == nil {
				return d, nil
			}
		}
	}
	return "", errors.New("can't find testbed/scenarios.yaml; run from the soundcheck module root")
}

// Toggle switches a scenario (or "all") to good or broken.
func Toggle(testbed, id, state string) error {
	out, err := exec.Command(filepath.Join(testbed, "toggle.sh"), id, state).CombinedOutput() //nolint:gosec // our own script
	if err != nil {
		return fmt.Errorf("toggle.sh %s %s: %w: %s", id, state, err, strings.TrimSpace(string(out)))
	}
	return nil
}
