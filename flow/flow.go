// Package flow runs scripted journeys through a site, written as small
// YAML files: open a page, click, fill fields, submit, wait and check
// what the visitor should see. Soundcheck uses flows for interaction
// level 4 ("flows") and for logging in to sites that aren't WordPress.
//
// A flow looks like this:
//
//	name: Contact form sends
//	start: /contact/
//	steps:
//	  - fill: {selector: "#email", value: "${fake:email}"}
//	  - fill: {selector: "#message", value: "${fake:message}"}
//	  - click: {text: Send}
//	  - wait: {selector: .sent}
//	  - expect: {text: Thank you}
//
// Values may use ${env:NAME} (read from the environment, never stored or
// printed) and ${fake:kind} (test data such as email, name, phone, url,
// message; see Fake.Value).
package flow

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Flow is one scripted journey.
type Flow struct {
	Name  string `yaml:"name"`
	Start string `yaml:"start"` // path or URL opened first; empty starts on the site's home page
	// Login runs the flow as the test user (SOUNDCHECK_USER and
	// SOUNDCHECK_PASSWORD); without credentials the flow is skipped.
	Login bool   `yaml:"login"`
	Steps []Step `yaml:"steps"`
	File  string `yaml:"-"` // where it was loaded from
}

// Step is one action. Exactly one field is set.
type Step struct {
	Goto       string
	Click      *Target
	Fill       *Input
	Select     *Input
	Check      *Toggle
	Submit     *Target // a form or a submit button; empty submits the first form
	Wait       *Wait
	Expect     *Expect
	Screenshot string
}

// Target finds an element by CSS selector, or by its visible text.
type Target struct {
	Selector string `yaml:"selector"`
	Text     string `yaml:"text"`
}

// Input sets a field's value (fill) or picks an option by value or label
// (select).
type Input struct {
	Selector string `yaml:"selector"`
	Value    string `yaml:"value"`
}

// Toggle checks or unchecks a checkbox or radio button.
type Toggle struct {
	Selector string `yaml:"selector"`
	Checked  *bool  `yaml:"checked"` // default true
}

// Wait pauses until a selector is visible, or for a number of
// milliseconds.
type Wait struct {
	Selector string `yaml:"selector"`
	MS       int    `yaml:"ms"`
}

// Expect asserts what the page shows. Every field set must hold.
type Expect struct {
	Text     string `yaml:"text"`     // visible somewhere on the page
	Selector string `yaml:"selector"` // a visible element
	URL      string `yaml:"url"`      // the current URL contains this
	Status   int    `yaml:"status"`   // the last page load's HTTP status
	Absent   bool   `yaml:"absent"`   // text and selector must NOT be present
}

var actions = []string{"goto", "click", "fill", "select", "check", "submit", "wait", "expect", "screenshot"}

// UnmarshalYAML reads a step written as a one-key mapping, with scalar
// shortcuts: `click: "#send"`, `wait: 500`, `wait: .done`, `check: "#terms"`.
func (s *Step) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode || len(n.Content) != 2 {
		return fmt.Errorf("line %d: a step is one action, e.g. `- click: \"#send\"` (actions: %s)", n.Line, strings.Join(actions, ", "))
	}
	key, val := n.Content[0].Value, n.Content[1]
	scalar := val.Kind == yaml.ScalarNode
	switch key {
	case "goto":
		return val.Decode(&s.Goto)
	case "screenshot":
		return val.Decode(&s.Screenshot)
	case "click":
		s.Click = &Target{}
		if scalar {
			s.Click.Selector = val.Value
			return nil
		}
		return decodeStrict(val, s.Click)
	case "submit":
		s.Submit = &Target{}
		if scalar {
			s.Submit.Selector = val.Value
			return nil
		}
		return decodeStrict(val, s.Submit)
	case "fill", "select":
		in := &Input{}
		if err := decodeStrict(val, in); err != nil {
			return err
		}
		if key == "fill" {
			s.Fill = in
		} else {
			s.Select = in
		}
		return nil
	case "check":
		s.Check = &Toggle{}
		if scalar {
			s.Check.Selector = val.Value
			return nil
		}
		return decodeStrict(val, s.Check)
	case "wait":
		s.Wait = &Wait{}
		if scalar {
			if ms, err := strconv.Atoi(val.Value); err == nil {
				s.Wait.MS = ms
			} else {
				s.Wait.Selector = val.Value
			}
			return nil
		}
		return decodeStrict(val, s.Wait)
	case "expect":
		s.Expect = &Expect{}
		if scalar {
			s.Expect.Text = val.Value
			return nil
		}
		return decodeStrict(val, s.Expect)
	}
	return fmt.Errorf("line %d: unknown step %q (actions: %s)", n.Line, key, strings.Join(actions, ", "))
}

// decodeStrict decodes a mapping, failing on unknown keys so typos show.
func decodeStrict(n *yaml.Node, v any) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: expected a mapping", n.Line)
	}
	out, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(out)))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	return nil
}

// Validate reports a step that's missing what it needs.
func (s Step) Validate() error {
	switch {
	case s.Click != nil && s.Click.Selector == "" && s.Click.Text == "":
		return errors.New("click needs a selector or text")
	case s.Fill != nil && s.Fill.Selector == "":
		return errors.New("fill needs a selector")
	case s.Select != nil && (s.Select.Selector == "" || s.Select.Value == ""):
		return errors.New("select needs a selector and a value")
	case s.Check != nil && s.Check.Selector == "":
		return errors.New("check needs a selector")
	case s.Wait != nil && s.Wait.Selector == "" && s.Wait.MS <= 0:
		return errors.New("wait needs a selector or a number of milliseconds")
	case s.Wait != nil && s.Wait.MS > 60000:
		return errors.New("wait is limited to 60000 ms")
	case s.Expect != nil && *s.Expect == (Expect{}):
		return errors.New("expect needs text, selector, url or status")
	case s.Goto == "" && s.Click == nil && s.Fill == nil && s.Select == nil && s.Check == nil &&
		s.Submit == nil && s.Wait == nil && s.Expect == nil && s.Screenshot == "":
		return errors.New("empty step")
	}
	return nil
}

// String describes the step from its definition (never from the values
// typed), for stable issue titles.
func (s Step) String() string {
	switch {
	case s.Goto != "":
		return "goto " + s.Goto
	case s.Click != nil:
		return "click " + s.Click.String()
	case s.Fill != nil:
		return "fill " + s.Fill.Selector
	case s.Select != nil:
		return "select " + s.Select.Selector
	case s.Check != nil:
		return "check " + s.Check.Selector
	case s.Submit != nil:
		if t := s.Submit.String(); t != "" {
			return "submit " + t
		}
		return "submit"
	case s.Wait != nil:
		if s.Wait.Selector != "" {
			return "wait for " + s.Wait.Selector
		}
		return fmt.Sprintf("wait %d ms", s.Wait.MS)
	case s.Expect != nil:
		return "expect " + s.Expect.String()
	case s.Screenshot != "":
		return "screenshot " + s.Screenshot
	}
	return "step"
}

func (t Target) String() string {
	if t.Text != "" {
		return strconv.Quote(t.Text)
	}
	return t.Selector
}

func (e Expect) String() string {
	var parts []string
	not := ""
	if e.Absent {
		not = "no "
	}
	if e.Text != "" {
		parts = append(parts, not+"text "+strconv.Quote(e.Text))
	}
	if e.Selector != "" {
		parts = append(parts, not+e.Selector)
	}
	if e.URL != "" {
		parts = append(parts, "url "+e.URL)
	}
	if e.Status != 0 {
		parts = append(parts, "status "+strconv.Itoa(e.Status))
	}
	return strings.Join(parts, ", ")
}

// Parse reads one flow.
func Parse(data []byte) (Flow, error) {
	var f Flow
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return f, err
	}
	if strings.TrimSpace(f.Name) == "" {
		return f, errors.New("a flow needs a name")
	}
	if len(f.Steps) == 0 {
		return f, errors.New("a flow needs at least one step")
	}
	for i, s := range f.Steps {
		if err := s.Validate(); err != nil {
			return f, fmt.Errorf("step %d: %w", i+1, err)
		}
		for _, v := range s.values() {
			if err := checkRefs(v); err != nil {
				return f, fmt.Errorf("step %d: %w", i+1, err)
			}
		}
	}
	return f, nil
}

func (s Step) values() []string {
	var out []string
	if s.Fill != nil {
		out = append(out, s.Fill.Value)
	}
	if s.Select != nil {
		out = append(out, s.Select.Value)
	}
	return append(out, s.Goto)
}

// ReadFile reads one flow from a file.
func ReadFile(path string) (Flow, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the user's own flow file
	if err != nil {
		return Flow{}, err
	}
	f, err := Parse(data)
	if err != nil {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	f.File = path
	return f, nil
}

// Load reads every .yaml/.yml flow in dir, in name order. A missing
// folder means no flows.
func Load(dir string) ([]Flow, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Flow
	var names []string
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		names = append(names, e.Name())
	}
	slices.Sort(names)
	for _, n := range names {
		f, err := ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// Example is the flow `flows init` writes.
const Example = `# A Soundcheck flow: a scripted journey checked on every run when
# interactions.levels includes "flows". Each failed step is reported.
#
# Steps: goto, click, fill, select, check, submit, wait, expect, screenshot.
# Values can use ${env:NAME} (read from the environment, never stored) and
# ${fake:email}, ${fake:name}, ${fake:phone}, ${fake:message}, ...
name: Contact form sends
start: /contact/
# login: true   # run as SOUNDCHECK_USER / SOUNDCHECK_PASSWORD
steps:
  - fill: {selector: "input[type=email]", value: "${fake:email}"}
  - fill: {selector: textarea, value: "${fake:message}"}
  - click: {text: Send}
  - wait: 1500
  - expect: {text: Thank you}
  - screenshot: contact-sent
`

// Init writes an example flow into dir, creating it. It never overwrites
// an existing file.
func Init(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "example.yaml")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path built from the user's flows folder
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return path, fmt.Errorf("%s already exists", path)
		}
		return "", err
	}
	if _, err := f.WriteString(Example); err != nil {
		_ = f.Close()
		return "", err
	}
	return path, f.Close()
}

var ref = regexp.MustCompile(`\$\{(env|fake):([A-Za-z0-9_\-]+)\}`)

func checkRefs(s string) error {
	for _, m := range ref.FindAllStringSubmatch(s, -1) {
		if m[1] == "fake" {
			if _, ok := (Fake{}).Value(m[2]); !ok {
				return fmt.Errorf("unknown ${fake:%s}", m[2])
			}
		}
	}
	return nil
}

// Expand replaces ${env:NAME} and ${fake:kind} in s. It returns the
// environment values it used, so callers can mask them.
func Expand(s string, fake Fake, env func(string) string) (string, []string, error) {
	if env == nil {
		env = os.Getenv
	}
	var secrets []string
	var err error
	out := ref.ReplaceAllStringFunc(s, func(m string) string {
		p := ref.FindStringSubmatch(m)
		if p[1] == "fake" {
			v, ok := fake.Value(p[2])
			if !ok && err == nil {
				err = fmt.Errorf("unknown ${fake:%s}", p[2])
			}
			return v
		}
		v := env(p[2])
		if v == "" && err == nil {
			err = fmt.Errorf("environment variable %s isn't set", p[2])
		}
		if v != "" {
			secrets = append(secrets, v)
		}
		return v
	})
	return out, secrets, err
}

// Mask replaces every secret in s with asterisks.
func Mask(s string, secrets ...string) string {
	for _, v := range secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, "****")
		}
	}
	return s
}
