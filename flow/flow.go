// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

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
// On URLs listed under production in the config, click and submit steps
// are refused unless the flow sets `production: true`; read-only steps
// (goto, fill, select, check, wait, expect, screenshot) always run.
//
// Values may use ${env:NAME} (read from the environment, never stored or
// printed) and ${fake:kind} (test data such as email, name, phone, url,
// message; see Fake.Value).
//
// Flow files live in the project folder, which a pull request can change,
// so a flow can't reach the runner's secrets or take them elsewhere: it
// reads only SOUNDCHECK_* variables and opens only the site being checked,
// unless Options.AllowEnv and Options.AllowHosts (--allow-env and
// --allow-host) name more.
//
// With Echo running (--echo), expect: {message: …} checks what the flow
// made the site send:
//
//	steps:
//	  - expect: {message: {channel: email, to: "${fake:email}", subject: Thanks}}
//
// A key flow (keyboard: true or keyboard: send) is played by keyboard
// only: each click, fill, select, check and submit reaches its target
// with Tab and Shift+Tab, then presses Enter, Space or the arrow keys.
// press steps press a key and expect: {focus: …} and
// expect: {focus_within: …} check where focus is; both work in any flow.
// keyboard: true holds what the flow would send and stops at the first
// step that would send it, so a key flow proves a checkout can be done
// without a mouse and places no order. keyboard: send sends for real.
//
//	keyboard: true
//	steps:
//	  - fill: {selector: "#email", value: "${fake:email}"}
//	  - press: {key: Space, on: "#terms"}
//	  - expect: {focus: "#terms"}
//	  - click: {text: Place order}
//
// The hold covers the flow's own tab only. Neither a popup or
// target=_blank form a key press opens nor messages sent over an open
// WebSocket are held: a site that places orders that way can still get
// one from a keyboard: true flow.
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
	"syscall"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	echomodel "github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/rhythm/kit/secrets"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/internal/focus"
)

// Flow is one scripted journey.
type Flow struct {
	Name  string `yaml:"name"`
	Start string `yaml:"start"` // path or URL opened first; empty starts on the site's home page
	// Login runs the flow as the test user (SOUNDCHECK_USER and
	// SOUNDCHECK_PASSWORD); without credentials the flow is skipped.
	Login bool `yaml:"login"`
	// Production lets the flow click and submit on URLs listed under
	// production in the config. Without it those steps are refused there.
	Production bool `yaml:"production"`
	// Keyboard makes the flow a key flow, played by keyboard only:
	// KeyboardDry (keyboard: true), KeyboardSend (keyboard: send) or ""
	// (keyboard: false or not set) for a flow played by mouse.
	Keyboard string `yaml:"-"`
	Steps    []Step `yaml:"steps"`
	File     string `yaml:"-"` // where it was loaded from
}

// The Keyboard modes of a key flow.
const (
	// KeyboardDry is keyboard: true. What the flow would send (any
	// request but GET and HEAD) is held: the first click, submit or
	// Enter or Space press that would send one passes and ends the flow.
	KeyboardDry = "dry"
	// KeyboardSend is keyboard: send. The flow sends for real, off
	// public addresses or with --staging.
	KeyboardSend = "send"
)

// keyboardMode decodes keyboard: true, false or send.
type keyboardMode string

func (k *keyboardMode) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		switch {
		case n.Tag == "!!null":
			*k = ""
			return nil
		case n.Tag == "!!bool":
			var b bool
			if err := n.Decode(&b); err == nil {
				*k = ""
				if b {
					*k = KeyboardDry
				}
				return nil
			}
		case n.Value == KeyboardSend:
			*k = KeyboardSend
			return nil
		}
	}
	v := n.Value
	if n.Kind != yaml.ScalarNode {
		v = "a list or mapping"
	}
	return fmt.Errorf("keyboard: %q isn't one of true, false, send", v)
}

// document is the flow file as YAML has it: Flow, with keyboard: read
// into Flow.Keyboard by Parse.
type document struct {
	Flow     `yaml:",inline"`
	Keyboard keyboardMode `yaml:"keyboard"`
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
	Press      *Press
}

// Press presses a key Times times: on the element On names, after
// moving focus to it, or on whatever has focus.
type Press struct {
	Key   focus.Key // one of Keys
	On    *Target   // nil presses on the focused element
	Times int       // 1 to MaxTimes; Parse sets 1 when the step doesn't say
}

// MaxTimes is the most presses one press step makes.
const MaxTimes = 50

// Keys are the keys a press step can press, as flows write them.
var Keys = []focus.Key{focus.Tab, focus.ShiftTab, focus.Enter, focus.Space, focus.Escape,
	focus.ArrowUp, focus.ArrowDown, focus.ArrowLeft, focus.ArrowRight, focus.Home, focus.End}

func (p Press) String() string {
	out := "press " + string(p.Key)
	if p.On != nil {
		out += " on " + p.On.String()
	}
	if p.Times > 1 {
		out += fmt.Sprintf(" %d times", p.Times)
	}
	return out
}

// decodePress reads `press: Tab` or `press: {key: Space, on: "#terms",
// times: 2}`, where on is a selector or a {selector, text} target.
func decodePress(val *yaml.Node) (*Press, error) {
	if val.Kind == yaml.ScalarNode {
		return &Press{Key: focus.Key(val.Value), Times: 1}, nil
	}
	var raw struct {
		Key   string    `yaml:"key"`
		On    yaml.Node `yaml:"on"`
		Times *int      `yaml:"times"`
	}
	if err := decodeStrict(val, &raw); err != nil {
		return nil, err
	}
	p := &Press{Key: focus.Key(raw.Key), Times: 1}
	if raw.Times != nil {
		p.Times = *raw.Times
		if p.Times < 1 || p.Times > MaxTimes {
			return nil, fmt.Errorf("line %d: press: times is 1 to %d, not %d", val.Line, MaxTimes, p.Times)
		}
	}
	switch raw.On.Kind {
	case 0:
	case yaml.ScalarNode:
		if raw.On.Tag != "!!null" {
			p.On = &Target{Selector: raw.On.Value}
		}
	default:
		p.On = &Target{}
		if err := decodeStrict(&raw.On, p.On); err != nil {
			return nil, fmt.Errorf("press: on: %w", err)
		}
	}
	return p, nil
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
	Text     string   `yaml:"text"`     // visible somewhere on the page
	Selector string   `yaml:"selector"` // a visible element
	URL      string   `yaml:"url"`      // the current URL contains this
	Status   int      `yaml:"status"`   // the last page load's HTTP status
	Absent   bool     `yaml:"absent"`   // text, selector, focus and message must NOT be present
	Message  *Message `yaml:"message"`  // the flow made the site send this (needs Echo)
	// Focus is a selector the focused element matches and FocusWithin
	// one it is inside. Focus is read through open shadow roots.
	Focus       string `yaml:"focus"`
	FocusWithin string `yaml:"focus_within"`
}

// Message describes a message the flow should have made the site send, as
// Echo captured it. Text fields match substrings, ignoring case; every
// field set must hold.
type Message struct {
	Channel string `yaml:"channel"` // email, sms, whatsapp, chat, webhook, analytics, payment, push, http
	To      string `yaml:"to"`      // a recipient (an address, a number, or a webhook's URL)
	Subject string `yaml:"subject"`
	Text    string `yaml:"text"`  // in the message's text, HTML or payload
	Count   int    `yaml:"count"` // at least this many such messages; default 1
}

func (m Message) validate(absent bool) error {
	if m.Channel != "" && !slices.Contains(echomodel.Channels, echomodel.Channel(m.Channel)) {
		names := make([]string, len(echomodel.Channels))
		for i, c := range echomodel.Channels {
			names[i] = string(c)
		}
		return fmt.Errorf("expect message: channel %q isn't one of %s", m.Channel, strings.Join(names, ", "))
	}
	switch {
	case m.Count < 0:
		return errors.New("expect message: count can't be negative")
	case m.Count > 0 && absent:
		return errors.New("expect message: count and absent don't mix; absent means no such message")
	}
	return nil
}

func (m Message) String() string {
	parts := []string{"message"}
	if m.Channel != "" {
		parts = append(parts, m.Channel)
	}
	if m.To != "" {
		parts = append(parts, "to "+strconv.Quote(m.To))
	}
	if m.Subject != "" {
		parts = append(parts, "subject "+strconv.Quote(m.Subject))
	}
	if m.Text != "" {
		parts = append(parts, "text "+strconv.Quote(m.Text))
	}
	if m.Count > 1 {
		parts = append(parts, fmt.Sprintf("count %d", m.Count))
	}
	return strings.Join(parts, " ")
}

var actions = []string{"goto", "click", "fill", "select", "check", "submit", "press", "wait", "expect", "screenshot"}

// UnmarshalYAML reads a step written as a one-key mapping, with scalar
// shortcuts: `click: "#send"`, `wait: 500`, `wait: .done`, `check: "#terms"`,
// `press: Escape`.
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
	case "press":
		p, err := decodePress(val)
		s.Press = p
		return err
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
	case s.Press != nil && s.Press.Key == "":
		return errors.New("press needs a key, such as Tab, Enter or Escape")
	case s.Press != nil && !slices.Contains(Keys, s.Press.Key):
		names := make([]string, len(Keys))
		for i, k := range Keys {
			names[i] = string(k)
		}
		return fmt.Errorf("press: key %w", config.OneOf(string(s.Press.Key), names))
	case s.Press != nil && s.Press.On != nil && s.Press.On.Selector == "" && s.Press.On.Text == "":
		return errors.New("press: on needs a selector or text")
	case s.Expect != nil && *s.Expect == (Expect{}):
		return errors.New("expect needs text, selector, url, status, focus, focus_within or message")
	case s.Expect != nil && s.Expect.Message != nil:
		return s.Expect.Message.validate(s.Expect.Absent)
	case s.Goto == "" && s.Click == nil && s.Fill == nil && s.Select == nil && s.Check == nil &&
		s.Submit == nil && s.Wait == nil && s.Expect == nil && s.Screenshot == "" && s.Press == nil:
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
	case s.Press != nil:
		return s.Press.String()
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
	if e.Focus != "" {
		parts = append(parts, not+"focus "+e.Focus)
	}
	if e.FocusWithin != "" {
		parts = append(parts, not+"focus within "+e.FocusWithin)
	}
	if e.Message != nil {
		parts = append(parts, not+e.Message.String())
	}
	return strings.Join(parts, ", ")
}

// Parse reads one flow.
func Parse(data []byte) (Flow, error) {
	var doc document
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	err := dec.Decode(&doc)
	f := doc.Flow
	f.Keyboard = string(doc.Keyboard)
	if err != nil {
		return f, err
	}
	if strings.TrimSpace(f.Name) == "" {
		return f, errors.New("a flow needs a name")
	}
	if len(f.Steps) == 0 {
		return f, errors.New("a flow needs at least one step")
	}
	if err := checkRefs(f.Start); err != nil {
		return f, fmt.Errorf("start: %w", err)
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
	if s.Expect != nil {
		out = append(out, s.Expect.Text)
		if m := s.Expect.Message; m != nil {
			out = append(out, m.To, m.Subject, m.Text)
		}
	}
	return append(out, s.Goto)
}

// EnvNames lists the environment variables the flow reads with
// ${env:NAME}, sorted, each once.
func (f Flow) EnvNames() []string {
	vals := []string{f.Start}
	for _, s := range f.Steps {
		vals = append(vals, s.values()...)
	}
	var names []string
	for _, v := range vals {
		names = append(names, envRefs(v)...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// envRefs returns the names in every ${env:NAME} in s.
func envRefs(s string) []string {
	var out []string
	for _, m := range ref.FindAllStringSubmatch(s, -1) {
		if m[1] == "env" {
			out = append(out, m[2])
		}
	}
	return out
}

// EnvPrefix starts the variables every flow may read.
const EnvPrefix = "SOUNDCHECK_"

// EnvAllowed reports whether a flow may read the variable name: a
// SOUNDCHECK_* one, or one in allow (--allow-env).
func EnvAllowed(name string, allow []string) bool {
	return strings.HasPrefix(name, EnvPrefix) || slices.Contains(allow, name)
}

// CheckEnv returns an error naming every variable the flow reads that
// isn't allowed, so it fails before any step runs.
func (f Flow) CheckEnv(allow []string) error {
	return envError(f.EnvNames(), allow)
}

func envError(names, allow []string) error {
	var bad []string
	for _, n := range names {
		if !EnvAllowed(n, allow) {
			bad = append(bad, n)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("flows read only %s* variables; to let this one read %s, pass --allow-env %s",
		EnvPrefix, strings.Join(bad, ", "), strings.Join(bad, " --allow-env "))
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

// FileError is a flow file that couldn't be read or parsed.
type FileError struct {
	File string
	Err  error
}

func (e *FileError) Error() string { return e.File + ": " + e.Err.Error() }
func (e *FileError) Unwrap() error { return e.Err }

// Load reads every .yaml/.yml flow in dir, in name order. A missing
// folder means no flows. A file in the folder's place is an error: it is
// checked up front, as Windows reports reading a file as a folder in a
// way that can pass for a missing folder or an empty one. A file that
// can't be read or parsed doesn't stop the others: it's returned as a
// *FileError, joined in err, next to the flows that loaded.
func Load(dir string) ([]Flow, error) {
	fi, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: syscall.ENOTDIR}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		names = append(names, e.Name())
	}
	slices.Sort(names)
	var out []Flow
	var errs []error
	for _, n := range names {
		path := filepath.Join(dir, n)
		data, err := os.ReadFile(path) //nolint:gosec // the user's own flow file
		if err == nil {
			var f Flow
			if f, err = Parse(data); err == nil {
				f.File = path
				out = append(out, f)
				continue
			}
		}
		errs = append(errs, &FileError{File: path, Err: err})
	}
	return out, errors.Join(errs...)
}

// Example is the flow `flows init` writes.
const Example = `# A Soundcheck flow: a scripted journey checked on every run when
# interactions.levels includes "flows". Each failed step is reported.
#
# Steps: goto, click, fill, select, check, submit, press, wait, expect,
# screenshot.
# Values can use ${env:NAME} (read from the environment, never stored; only
# SOUNDCHECK_* names unless you pass --allow-env NAME) and ${fake:email},
# ${fake:name}, ${fake:phone}, ${fake:message}, ... goto and start stay on
# the site being checked unless you pass --allow-host.
name: Contact form sends
start: /contact/
# login: true        # run as SOUNDCHECK_USER / SOUNDCHECK_PASSWORD
# production: true   # allow clicks and submits on production URLs (config: production)
# keyboard: true     # play it by keyboard only and hold what it would send; send sends it
steps:
  - fill: {selector: "input[type=email]", value: "${fake:email}"}
  - fill: {selector: textarea, value: "${fake:message}"}
  - click: {text: Send}
  - wait: 1500
  - expect: {text: Thank you}
  - screenshot: contact-sent
  # With Echo running (--echo), check what the form sent. Fields match
  # substrings; channel is email, sms, webhook, chat, ...; count: n wants at
  # least n; absent: true wants none.
  # - expect: {message: {channel: email, to: "${fake:email}", subject: Thank}}
  # - expect: {message: {channel: email, to: admin@, count: 1}}
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

// Mask replaces every secret in s with the kit's mask, which hides the
// whole value and its length. A value of 8 characters or more is masked
// wherever it is. A shorter one is masked where it stands alone, not
// inside a longer word: with a one-letter user "a", "login.url" keeps its
// letters while "user a" becomes "user •••••".
func Mask(s string, values ...string) string {
	for _, v := range values {
		switch {
		case v == "":
		case utf8.RuneCountInString(v) >= 8:
			s = strings.ReplaceAll(s, v, secrets.Mask(v, 0))
		default:
			s = maskWord(s, v, secrets.Mask(v, 0))
		}
	}
	return s
}

// maskWord replaces v in s with mask wherever v isn't joined to a letter
// or digit on a side where v itself starts or ends with one.
func maskWord(s, v, mask string) string {
	first, _ := utf8.DecodeRuneInString(v)
	last, _ := utf8.DecodeLastRuneInString(v)
	var b strings.Builder
	done := 0 // s[:done] is written
	for from := 0; ; {
		j := strings.Index(s[from:], v)
		if j < 0 {
			b.WriteString(s[done:])
			return b.String()
		}
		i, end := from+j, from+j+len(v)
		before, _ := utf8.DecodeLastRuneInString(s[:i])
		after, _ := utf8.DecodeRuneInString(s[end:])
		if (wordRune(first) && i > 0 && wordRune(before)) || (wordRune(last) && end < len(s) && wordRune(after)) {
			_, size := utf8.DecodeRuneInString(s[i:])
			from = i + size
			continue
		}
		b.WriteString(s[done:i])
		b.WriteString(mask)
		done, from = end, end
	}
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
