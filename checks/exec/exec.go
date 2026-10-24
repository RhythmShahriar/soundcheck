// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package exec runs checks written in any language as separate
// programs. For every page that loaded, each command gets a page
// document as JSON on stdin (schema rhythm-soundcheck/page/v1, published
// as page.v1.json) and answers with {"issues": [...]} on stdout
// (issues.v1.json). Its issues are compared between runs like any
// other module's.
//
// A command runs from its argv, without a shell, under a timeout that
// kills its whole process group. Anything that goes wrong (it can't
// start, times out, exits non-zero, prints more than 1 MB or answers
// with something that isn't the expected JSON) is a warning on the run,
// never a failed run.
//
// Commands come from a config file and a committed config must not run
// programs on its own, so the module only runs when it was built with
// Allowed set, from an explicit --allow-exec.
package exec

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// PageSchema names the page document's layout.
const PageSchema = "rhythm-soundcheck/page/v1"

// PageSchemaJSON is the JSON Schema of the page document.
//
//go:embed page.v1.json
var PageSchemaJSON []byte

// IssuesSchemaJSON is the JSON Schema of a command's answer.
//
//go:embed issues.v1.json
var IssuesSchemaJSON []byte

// Limits.
const (
	// DefaultTimeout is how long a command gets per page when its
	// config doesn't say.
	DefaultTimeout = 10 * time.Second
	// MaxTimeout caps a command's timeout per page.
	MaxTimeout = 5 * time.Minute
	// MaxOutput caps what a command may print on stdout for one page.
	MaxOutput = 1 << 20
	// MaxIssues caps the issues one command reports for one page.
	MaxIssues = 200
	// maxText caps the page text in the document.
	maxText = 512 << 10
	// waitDelay is how long a killed command's pipes may stay open, e.g.
	// held by a child that left the process group.
	waitDelay = 2 * time.Second
)

// Command is one out-of-process check.
type Command struct {
	// Name names the check: its issues' ids start with exec/<name>/.
	Name string `yaml:"name" json:"name"`
	// Run is the program and its arguments, run without a shell.
	Run []string `yaml:"run" json:"run"`
	// Timeout is how long it gets per page; zero is DefaultTimeout.
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
}

// Module runs the commands on every page.
type Module struct {
	Commands []Command
	// Allowed is set from --allow-exec. Without it the module runs
	// nothing and warns once.
	Allowed bool

	mu     sync.Mutex
	warned map[string]bool
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// New checks the commands and builds the module.
func New(cmds []Command, allowed bool) (*Module, error) {
	seen := map[string]bool{}
	for i, c := range cmds {
		switch {
		case !validName.MatchString(c.Name):
			return nil, fmt.Errorf("modules.exec[%d]: name %q: use lower-case letters, digits and dashes, up to 40", i, c.Name)
		case seen[c.Name]:
			return nil, fmt.Errorf("modules.exec: two commands are named %q", c.Name)
		case len(c.Run) == 0 || c.Run[0] == "":
			return nil, fmt.Errorf("modules.exec %q: run is missing: the program and its arguments, as a list", c.Name)
		case c.Timeout < 0 || c.Timeout > MaxTimeout:
			return nil, fmt.Errorf("modules.exec %q: timeout %s: want up to %s", c.Name, c.Timeout, MaxTimeout)
		}
		seen[c.Name] = true
	}
	return &Module{Commands: cmds, Allowed: allowed}, nil
}

// Name is the module's name.
func (*Module) Name() string { return "exec" }

// Page is the document a command reads on stdin.
type Page struct {
	Schema   string            `json:"schema"`
	Path     string            `json:"path"`
	URL      string            `json:"url"`
	FinalURL string            `json:"final_url,omitempty"`
	Status   int               `json:"status"`
	Title    string            `json:"title"`
	Text     string            `json:"text"`
	Console  []string          `json:"console"`
	Requests []Request         `json:"requests"`
	Headers  map[string]string `json:"headers"`
}

// Request is one request the page made.
type Request struct {
	URL    string `json:"url"`
	Type   string `json:"type,omitempty"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"` // net::ERR_... when it never got a response
}

// passHeaders are the response headers a command sees. Cookies and
// anything that may carry a credential stay out.
var passHeaders = []string{
	"content-type", "content-language", "cache-control", "expires", "last-modified", "etag", "age", "vary",
	"x-cache", "cf-cache-status", "x-powered-by", "server", "x-robots-tag", "link",
	"content-security-policy", "strict-transport-security", "x-frame-options", "x-content-type-options",
	"referrer-policy", "permissions-policy",
}

// Document builds the page document for p.
func Document(p *model.Page) Page {
	doc := Page{
		Schema: PageSchema, Path: p.Path, URL: p.URL, FinalURL: p.FinalURL, Status: p.Status, Title: p.Title,
		Text: p.Text, Console: p.Console, Requests: []Request{}, Headers: map[string]string{},
	}
	if len(doc.Text) > maxText {
		doc.Text = strings.ToValidUTF8(doc.Text[:maxText], "")
	}
	if doc.Console == nil {
		doc.Console = []string{}
	}
	for _, r := range p.Resources {
		doc.Requests = append(doc.Requests, Request{URL: r.URL, Type: r.Type, Status: r.Status})
	}
	for _, r := range p.Failed {
		doc.Requests = append(doc.Requests, Request{URL: r.URL, Type: r.Type, Status: r.Status, Error: r.Error})
	}
	for k, v := range p.Headers {
		if slices.Contains(passHeaders, strings.ToLower(k)) {
			doc.Headers[strings.ToLower(k)] = v
		}
	}
	return doc
}

// CheckPage runs every command on a page that loaded.
func (m *Module) CheckPage(cc *check.Context, p *model.Page) {
	if len(m.Commands) == 0 {
		return
	}
	if !m.Allowed {
		m.warnOnce(cc, "refused", "exec: the config lists commands to run on every page; they run only with --allow-exec")
		return
	}
	if p.Error != "" {
		return
	}
	in, err := json.Marshal(Document(p))
	if err != nil {
		m.warnOnce(cc, "doc", "exec: "+err.Error())
		return
	}
	for _, c := range m.Commands {
		issues, err := run(c, in)
		if err != nil {
			m.warnOnce(cc, c.Name+"\x00"+err.Error(), fmt.Sprintf("exec/%s: %s (first on %s)", c.Name, err, p.Path))
			continue
		}
		for _, i := range issues {
			check.Page(p, i)
		}
	}
}

// warnOnce adds a run warning the first time key comes up, so a command
// failing on every page warns once.
func (m *Module) warnOnce(cc *check.Context, key, msg string) {
	m.mu.Lock()
	if m.warned == nil {
		m.warned = map[string]bool{}
	}
	seen := m.warned[key]
	m.warned[key] = true
	m.mu.Unlock()
	if !seen {
		cc.Warn(msg)
	}
}

// answer is what a command prints.
type answer struct {
	Issues *[]struct {
		Check  string `json:"check"`
		Level  string `json:"level"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Source string `json:"source"`
		Fix    string `json:"fix"`
	} `json:"issues"`
}

var errTooMuch = fmt.Errorf("printed more than %d MB", MaxOutput>>20)

// run runs one command on one page document and returns its issues.
func run(c Command, in []byte) ([]model.Issue, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := osexec.CommandContext(ctx, c.Run[0], c.Run[1:]...) //nolint:gosec // argv from a config the user allowed with --allow-exec, no shell
	cmd.Env = childEnv(os.Environ())
	cmd.Stdin = bytes.NewReader(in)
	out := &limited{max: MaxOutput, over: cancel}
	stderr := &limited{max: 4 << 10}
	cmd.Stdout, cmd.Stderr = out, stderr
	inGroup(cmd)
	cmd.WaitDelay = waitDelay
	err := cmd.Run()
	switch {
	case out.exceeded:
		return nil, errTooMuch
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, fmt.Errorf("took longer than %s and was stopped", timeout)
	case err != nil:
		var exitErr *osexec.ExitError
		if errors.As(err, &exitErr) {
			msg := fmt.Sprintf("exited with code %d", exitErr.ExitCode())
			if line := lastLine(stderr.buf.String()); line != "" {
				msg += ": " + line
			}
			return nil, errors.New(msg)
		}
		return nil, fmt.Errorf("couldn't run %s: %w", c.Run[0], err)
	}
	return parse(c.Name, out.buf.Bytes())
}

// parse reads a command's answer and checks every issue.
func parse(name string, data []byte) ([]model.Issue, error) {
	var a answer
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("answered with something that isn't JSON: %w", err)
	}
	if dec.More() {
		return nil, errors.New("answered with more than one JSON value")
	}
	if a.Issues == nil {
		return nil, errors.New(`answered without "issues": want {"issues": [...]}`)
	}
	if len(*a.Issues) > MaxIssues {
		return nil, fmt.Errorf("reported %d issues on one page; the most is %d", len(*a.Issues), MaxIssues)
	}
	prefix := "exec/" + name + "/"
	out := make([]model.Issue, 0, len(*a.Issues))
	for n, i := range *a.Issues {
		switch {
		case !strings.HasPrefix(i.Check, prefix) || len(i.Check) == len(prefix):
			return nil, fmt.Errorf("issue %d: check %q must start with %q", n+1, i.Check, prefix)
		case !slices.Contains([]model.Level{model.Error, model.Warning, model.Notice}, model.Level(i.Level)):
			return nil, fmt.Errorf("issue %d: level %q: want error, warning or notice", n+1, i.Level)
		case strings.TrimSpace(i.Title) == "":
			return nil, fmt.Errorf("issue %d: the title is missing", n+1)
		}
		out = append(out, model.Issue{Check: i.Check, Level: model.Level(i.Level), Title: i.Title,
			Detail: i.Detail, Source: i.Source, Fix: i.Fix})
	}
	return out, nil
}

// hiddenEnv are the variable families a command never sees: the test
// account's password and Echo's credentials.
var hiddenEnv = []string{"SOUNDCHECK_", "RHYTHM_ECHO_"}

func childEnv(environ []string) []string {
	return slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		return slices.ContainsFunc(hiddenEnv, func(p string) bool { return strings.HasPrefix(strings.ToUpper(kv), p) })
	})
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// limited keeps the first max bytes written to it. Past that it drops
// the rest and calls over, which stops the command.
type limited struct {
	max      int
	over     func()
	buf      bytes.Buffer
	exceeded bool
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); len(p) <= room {
		l.buf.Write(p)
		return len(p), nil
	}
	if l.over != nil && !l.exceeded {
		l.exceeded = true
		l.over()
	}
	return len(p), nil
}
