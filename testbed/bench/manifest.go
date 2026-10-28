// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
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
	Sites map[string]string `yaml:"sites"`
	// Defaults are the sites as scenarios.yaml gives them, before -wp,
	// -php or TESTBED_*_PORT move them (fixtures were taken there).
	Defaults  map[string]string `yaml:"-"`
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

	Args []string `yaml:"args"` // extra flags for snapshot and compare
	// CompareArgs are extra flags for compare only, such as
	// --fail-on broke, which snapshot refuses.
	CompareArgs []string `yaml:"compare_args"`
	Modules     []string `yaml:"modules"` // --modules, when set
	Limit       int      `yaml:"limit"`   // page limit (default 20)
	// PerGroup is pages.per_group: how many pages of one URL pattern
	// are checked (-1 for all).
	PerGroup int       `yaml:"per_group"`
	Echo     bool      `yaml:"echo"`    // needs Echo (-echo)
	Rules    []string  `yaml:"rules"`   // rule files under testbed/, copied to .soundcheck/rules/
	Harness  Harnesses `yaml:"harness"` // see harnesses

	// All makes every expect entry a requirement: the scenario is caught
	// only when each one holds (for example a BROKE change and an exit
	// code). Without it one is enough.
	All bool `yaml:"all"`
	// Same says the broken state is the good state: nothing is switched,
	// the scenario checks that an unchanged page gets no false alarm.
	Same bool `yaml:"same"`
	// Backlog says the page has accessibility findings in its good state
	// on purpose (an old backlog a compare must keep quiet about), so the
	// good-state audit (TestGoodStateAudit) leaves it out.
	Backlog bool `yaml:"backlog"`
	// Fixture is a snapshot folder under testbed/ that is copied into
	// the run's .soundcheck/snapshots/good instead of taking a snapshot,
	// e.g. one written by an older Soundcheck.
	Fixture string `yaml:"fixture"`
	// Flows are flow files under testbed/ copied into the run's
	// .soundcheck/flows/.
	Flows []string `yaml:"flows"`
	// Says are lines the statement draft must contain besides the ones
	// every statement harness checks (harness statement).
	Says []string `yaml:"says"`
	// Control runs the scenario's page in every round of the control
	// pass too.
	Control bool `yaml:"control"`

	// InterruptAfter is when harness interrupt sends Ctrl+C.
	InterruptAfter time.Duration `yaml:"interrupt_after"`
	// Timeout is the time limit for each of the scenario's commands when
	// it needs more than -timeout (a big site).
	Timeout time.Duration `yaml:"timeout"`
	// Within is how long the compare may take at most: one that takes
	// longer (a walk that doesn't stop at its cap) is missed.
	Within time.Duration `yaml:"within"`

	// Pending marks a scenario whose detector isn't merged yet; the
	// scoreboard reports it apart from the score. It is true or the name
	// of the build lane the scenario waits for (see lanes); Note says
	// more.
	Pending Pending `yaml:"pending"`
	Note    string  `yaml:"note"`
}

// Harnesses are what the benchmark can do besides switching the state.
var harnesses = []string{"interrupt", "parallel", "delete-shots", "stress", "wp-update", "statement", "requests", "orders", "canaries"}

// Harnesses is a scenario's harness list. scenarios.yaml writes one as a
// plain value (harness: stress) and several as a list.
type Harnesses []string

// UnmarshalYAML reads a single harness or a list of them.
func (h *Harnesses) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*h = Harnesses{n.Value}
		return nil
	}
	var list []string
	if err := n.Decode(&list); err != nil {
		return err
	}
	*h = list
	return nil
}

// Has reports whether name is one of the harnesses.
func (h Harnesses) Has(name string) bool { return slices.Contains(h, name) }

// lanes are the build lanes of Soundcheck 1.3 a scenario can wait for
// (pending: <lane>).
var lanes = []string{"axe", "keyboard", "flows", "statement", "report", "cli"}

// Pending is a scenario's pending mark: empty (not pending), "true" or a
// lane's name.
type Pending string

// UnmarshalYAML reads true, false or a lane's name.
func (p *Pending) UnmarshalYAML(n *yaml.Node) error {
	var b bool
	if n.Tag == "!!bool" {
		if err := n.Decode(&b); err != nil {
			return err
		}
		*p = ""
		if b {
			*p = "true"
		}
		return nil
	}
	*p = Pending(n.Value)
	return nil
}

// Is reports whether the scenario is pending.
func (p Pending) Is() bool { return p != "" }

// Lane is the lane the scenario waits for, or "" when it names none.
func (p Pending) Lane() string {
	if p == "true" {
		return ""
	}
	return string(p)
}

// ChangesSite reports whether the scenario's state file changes the
// site. The parallel and stress cases only change how Soundcheck runs;
// wp-update breaks the site by updating plugins, not through a state
// file; a scenario marked same changes nothing.
func (s Scenario) ChangesSite() bool {
	return !s.Same && !s.Harness.Has("parallel") && !s.Harness.Has("stress") && !s.Harness.Has("wp-update")
}

// EAA reports whether the scenario belongs to EAA mode (s49 to s91):
// the ones -only eaa picks.
func (s Scenario) EAA() bool { return slices.Contains(s.Args, "--eaa") }

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
// A change matcher may also ask for Source (the change's source, such as
// the element's selector, contains it) and Count (at least that many
// changes match, e.g. one per field). Forbid entries use the change form
// only.
type Expect struct {
	Class       string `yaml:"class"`
	Check       string `yaml:"check"`
	Title       string `yaml:"title"`
	Source      string `yaml:"source"`
	Count       int    `yaml:"count"`
	Warning     string `yaml:"warning"`
	Interrupted bool   `yaml:"interrupted"`
	Clean       bool   `yaml:"clean"`
	Exit        *int   `yaml:"exit"`
	Culprit     string `yaml:"culprit"`
	Compares    int    `yaml:"compares"`
	// Widgets is the least number of menus, dialogs and disclosures
	// the --eaa compare's keyboard walk must have tested, so a hard-good
	// scenario can't pass by never testing its widget.
	Widgets int `yaml:"widgets"`
}

// Kinds of Expect.
const (
	kindChange      = "change"
	kindWarning     = "warning"
	kindInterrupted = "interrupted"
	kindClean       = "clean"
	kindExit        = "exit"
	kindCulprit     = "culprit"
	kindWidgets     = "widgets"
)

// kind says which kind of expectation e is; "" when it sets none or more
// than one.
func (e Expect) kind() string {
	var kinds []string
	if e.Class != "" || e.Check != "" || e.Title != "" || e.Source != "" {
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
	if e.Widgets != 0 {
		kinds = append(kinds, kindWidgets)
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
		strings.Contains(strings.ToLower(c.Title), strings.ToLower(e.Title)) &&
		strings.Contains(c.Source, e.Source)
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
	case kindWidgets:
		return fmt.Sprintf("at least %d %s tested", e.Widgets, plural(e.Widgets, "widget"))
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
	if e.Source != "" {
		s += " in " + e.Source
	}
	if e.Count > 1 {
		s += fmt.Sprintf(" (%d×)", e.Count)
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
	m.Defaults = maps.Clone(m.Sites)
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
		case slices.ContainsFunc(s.Harness, func(h string) bool { return !slices.Contains(harnesses, h) }):
			return fmt.Errorf("%s: unknown harness in %v (want %s)", s.ID, []string(s.Harness), strings.Join(harnesses, ", "))
		case s.Harness.Has("interrupt") && s.InterruptAfter <= 0:
			return fmt.Errorf("%s: harness interrupt needs interrupt_after", s.ID)
		case s.Limit < 0 || s.Within < 0:
			return fmt.Errorf("%s: negative limit or within", s.ID)
		case s.Harness.Has("wp-update") && len(s.Forbid) > 0:
			return fmt.Errorf("%s: harness wp-update scores culprits; forbid doesn't apply", s.ID)
		case s.Harness.Has("wp-update") && len(s.Harness) > 1:
			return fmt.Errorf("%s: harness wp-update runs alone", s.ID)
		case s.Pending.Lane() != "" && !slices.Contains(lanes, s.Pending.Lane()):
			return fmt.Errorf("%s: pending %q isn't true or a lane (%s)", s.ID, s.Pending, strings.Join(lanes, ", "))
		case len(s.Says) > 0 && !s.Harness.Has("statement"):
			return fmt.Errorf("%s: says needs harness statement", s.ID)
		case s.Same && s.Harness.Has("wp-update"):
			return fmt.Errorf("%s: a wp-update scenario changes the site", s.ID)
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
		return fmt.Errorf("%s: expect %+v must set exactly one of class/check/title, warning, interrupted, clean, exit, culprit or widgets", s.ID, e)
	case kind == kindWidgets && e.Widgets < 0:
		return fmt.Errorf("%s: widgets is a positive number", s.ID)
	case (kind == kindCulprit) != s.Harness.Has("wp-update"):
		return fmt.Errorf("%s: only harness wp-update expects culprits and it expects nothing else", s.ID)
	case kind == kindCulprit && (e.Culprit == "" || e.Compares < 0):
		return fmt.Errorf("%s: culprit needs a plugin slug and compares can't be negative", s.ID)
	case forbid && kind != kindChange:
		return fmt.Errorf("%s: forbid entries are changes (class, check, title, source)", s.ID)
	case e.Count < 0 || e.Count > 0 && kind != kindChange:
		return fmt.Errorf("%s: count is a positive number of changes", s.ID)
	case kind != kindChange || e.Class == "" || slices.Contains(model.Classes, e.Class):
		return nil
	case s.Pending.Is() && e.Class == strings.ToLower(e.Class):
		return nil // a class that is being added, e.g. unstable
	}
	return fmt.Errorf("%s: unknown class %q", s.ID, e.Class)
}

// URL is where the scenario's run starts.
func (m *Manifest) URL(s Scenario) string {
	return strings.TrimRight(m.Sites[s.Site], "/") + s.Path
}

// Pick returns the scenarios whose IDs are in only (all when empty).
// The name eaa picks every EAA mode scenario (s49 to s91). Each scenario
// comes back once, however often only names it.
func (m *Manifest) Pick(only []string) ([]Scenario, error) {
	if len(only) == 0 {
		return m.Scenarios, nil
	}
	var out []Scenario
	for _, id := range only {
		if id == "eaa" {
			for _, s := range m.Scenarios {
				if s.EAA() && !slices.ContainsFunc(out, func(o Scenario) bool { return o.ID == s.ID }) {
					out = append(out, s)
				}
			}
			continue
		}
		i := slices.IndexFunc(m.Scenarios, func(s Scenario) bool { return s.ID == id })
		if i < 0 {
			return nil, fmt.Errorf("unknown scenario %q", id)
		}
		if !slices.ContainsFunc(out, func(o Scenario) bool { return o.ID == id }) {
			out = append(out, m.Scenarios[i])
		}
	}
	return out, nil
}

// Outcomes of a scenario for one tool.
const (
	Caught     = "caught"
	OtherClass = "other class" // found the problem yet classed it differently
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
	// Good counts the violations an accessibility tool listed in the
	// good state (EAA scenarios only).
	Good int `json:"good_violations,omitempty"`
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
	held := make([]string, len(s.Expect)) // what each expectation found, "" while unmet
	matches := make([]int, len(s.Expect))
	for i, c := range cmp.Changes {
		matched := false
		for j, e := range s.Expect {
			if e.Match(c) {
				matched = true
				if matches[j]++; matches[j] >= max(e.Count, 1) && held[j] == "" {
					held[j] = describe(c)
				}
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
	for j, e := range s.Expect {
		switch e.kind() {
		case kindWarning:
			for _, w := range cmp.Warnings {
				if strings.Contains(strings.ToLower(w), strings.ToLower(e.Warning)) {
					held[j] = "warning: " + w
					break
				}
			}
		case kindInterrupted:
			if obs.Interrupted {
				held[j] = "the comparison is marked interrupted"
			}
		case kindClean:
			if len(FalseAlarms(cmp)) == 0 {
				held[j] = "nothing at or above NEW"
			}
		case kindExit:
			if obs.Exit == *e.Exit {
				held[j] = fmt.Sprintf("exit %d", obs.Exit)
			}
		case kindWidgets:
			if cmp.EAA != nil && cmp.EAA.Keyboard.WidgetsTested >= e.Widgets {
				held[j] = fmt.Sprintf("%d %s tested", cmp.EAA.Keyboard.WidgetsTested, plural(cmp.EAA.Keyboard.WidgetsTested, "widget"))
			}
		}
	}
	var found, missing []string
	for j, h := range held {
		if h != "" {
			found = append(found, h)
		} else {
			missing = append(missing, s.Expect[j].String())
		}
	}
	switch {
	case s.All && len(missing) == 0, !s.All && len(found) > 0:
		r.Outcome = Caught
		if s.All {
			r.Found = strings.Join(found, "; ")
		} else {
			r.Found = found[0]
		}
	case s.All && len(found) > 0:
		r.Detail = "still missing: " + strings.Join(missing, ", ")
	}
	alarms := FalseAlarms(cmp)
	for _, c := range cmp.Changes {
		for _, f := range s.Forbid {
			if f.Match(c) {
				return Result{Outcome: FalseAlarm, Found: describe(c), Others: r.Others}
			}
		}
	}
	switch {
	case r.Outcome == Caught:
	case len(alarms) > 0 && slices.ContainsFunc(s.Expect, func(e Expect) bool { return e.kind() == kindClean }):
		// A clean expectation failed: whatever reached NEW is a false
		// alarm, even when the forbid list doesn't name it.
		r.Outcome, r.Found = FalseAlarm, strings.Join(alarms, "; ")
	case about != nil:
		r.Outcome, r.Found = OtherClass, describe(*about)
	default:
		r.Outcome = Missed
		if obs.Comparison == nil {
			r.Detail = strings.TrimSpace(r.Detail + fmt.Sprintf(" compare wrote no comparison (exit %d)", obs.Exit))
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
	if c.Source != "" && (strings.HasPrefix(c.Check, "a11y/") || strings.HasPrefix(c.Check, "keyboard/")) {
		s += " [" + c.Source + "]"
	}
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

// sitePorts are the environment variables that move each site, as
// docker-compose.yml and the Makefile read them.
var sitePorts = map[string]struct{ env, scheme string }{
	"wp":        {"TESTBED_WP_PORT", "http"},
	"php":       {"TESTBED_PHP_PORT", "http"},
	"php-https": {"TESTBED_PHP_HTTPS_PORT", "https"},
}

// sitesFromEnv points the sites at the ports in TESTBED_WP_PORT,
// TESTBED_PHP_PORT and TESTBED_PHP_HTTPS_PORT when they are set, so the
// tests follow a stack started on other ports.
func sitesFromEnv(m *Manifest) {
	for site, p := range sitePorts {
		if port := os.Getenv(p.env); port != "" && m.Sites[site] != "" {
			m.Sites[site] = p.scheme + "://localhost:" + port
		}
	}
}
