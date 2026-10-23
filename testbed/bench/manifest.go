//go:build testbed

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Manifest is testbed/scenarios.yaml.
type Manifest struct {
	Sites     map[string]string `yaml:"sites"`
	Scenarios []Scenario        `yaml:"scenarios"`
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
	Tools   []string `yaml:"tools"`
}

// Expect is one acceptable finding: a change of Class whose check starts
// with Check and whose title contains Title (case-insensitive).
type Expect struct {
	Class string `yaml:"class"`
	Check string `yaml:"check"`
	Title string `yaml:"title"`
}

// Match reports whether c is the expected finding.
func (e Expect) Match(c model.Change) bool {
	return c.Class == e.Class && e.about(c)
}

// about reports whether c is about the expected problem, whatever its class.
func (e Expect) about(c model.Change) bool {
	return strings.HasPrefix(c.Check, e.Check) && strings.Contains(strings.ToLower(c.Title), strings.ToLower(e.Title))
}

func (e Expect) String() string {
	s := strings.ToUpper(e.Class) + " " + e.Check
	if e.Title != "" {
		s += " “" + e.Title + "”"
	}
	return s
}

// LoadManifest reads and checks scenarios.yaml.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the testbed's own manifest
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, s := range m.Scenarios {
		switch {
		case s.ID == "" || seen[s.ID]:
			return nil, fmt.Errorf("%s: missing or duplicate id %q", path, s.ID)
		case m.Sites[s.Site] == "":
			return nil, fmt.Errorf("%s: %s: unknown site %q", path, s.ID, s.Site)
		case !strings.HasPrefix(s.Path, "/"):
			return nil, fmt.Errorf("%s: %s: path must start with /", path, s.ID)
		case len(s.Expect) == 0:
			return nil, fmt.Errorf("%s: %s: no expect", path, s.ID)
		}
		for _, e := range s.Expect {
			if !slices.Contains(model.Classes, e.Class) {
				return nil, fmt.Errorf("%s: %s: unknown class %q", path, s.ID, e.Class)
			}
		}
		seen[s.ID] = true
	}
	return &m, nil
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

// Evaluate checks a Soundcheck comparison against the scenario.
func Evaluate(s Scenario, cmp *model.Comparison) Result {
	var r Result
	var about *model.Change
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
	switch {
	case r.Outcome == Caught:
	case about != nil:
		r.Outcome, r.Found = OtherClass, describe(*about)
	default:
		r.Outcome = Missed
	}
	return r
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
