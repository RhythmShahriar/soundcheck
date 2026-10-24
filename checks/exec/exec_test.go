// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package exec

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// TestMain makes the test binary stand in for a check command:
// "exec-helper <mode>" reads the page document and answers by mode.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "exec-helper" {
		os.Exit(helper(os.Args[2], os.Args[3:]))
	}
	os.Exit(m.Run())
}

func helper(mode string, args []string) int {
	in, _ := io.ReadAll(os.Stdin)
	var doc Page
	_ = json.Unmarshal(in, &doc)
	switch mode {
	case "issues":
		_ = os.WriteFile(args[0], in, 0o600)
		fmt.Printf(`{"issues":[
			{"check":"exec/laravel-log/error","level":"error","title":"Laravel logged an error","detail":%q,"source":"app/Http/Kernel.php:12"},
			{"check":"exec/laravel-log/slow-query","level":"notice","title":"Slow query","fix":"Add an index"}]}`, "on "+doc.Path)
	case "none":
		fmt.Print(`{"issues": []}`)
	case "garbage":
		fmt.Print(`Fatal error: not JSON`)
	case "two":
		fmt.Print(`{"issues": []} {"issues": []}`)
	case "missing":
		fmt.Print(`{"problems": []}`)
	case "prefix":
		fmt.Print(`{"issues":[{"check":"health/status","level":"error","title":"Pretending to be health"}]}`)
	case "bare-prefix":
		fmt.Print(`{"issues":[{"check":"exec/laravel-log/","level":"error","title":"No rule"}]}`)
	case "level":
		fmt.Print(`{"issues":[{"check":"exec/laravel-log/x","level":"fatal","title":"Bad level"}]}`)
	case "title":
		fmt.Print(`{"issues":[{"check":"exec/laravel-log/x","level":"error","title":"  "}]}`)
	case "many":
		var b strings.Builder
		b.WriteString(`{"issues":[`)
		for i := range MaxIssues + 1 {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"check":"exec/laravel-log/x","level":"notice","title":"n%d"}`, i)
		}
		b.WriteString("]}")
		fmt.Print(b.String())
	case "big":
		chunk := strings.Repeat("x", 64<<10)
		for range 40 {
			fmt.Print(chunk)
		}
	case "fail":
		fmt.Fprintln(os.Stderr, "loading app\nboom: no database")
		return 2
	case "env":
		fmt.Printf(`{"issues":[{"check":"exec/laravel-log/env","level":"notice","title":%q}]}`, os.Getenv("SOUNDCHECK_PASSWORD")+"|"+os.Getenv("EXEC_TEST_VISIBLE"))
	case "sleep":
		time.Sleep(30 * time.Second)
	}
	return 0
}

func ctx() *check.Context { return &check.Context{Run: &model.Run{}} }

func page() *model.Page {
	return &model.Page{
		Path: "/contact/", URL: "https://shop.test/contact", FinalURL: "https://shop.test/contact/", Status: 200,
		Title: "Contact", Text: "Write to us", Console: []string{"TypeError: x is undefined"},
		Headers:   map[string]string{"content-type": "text/html", "set-cookie": "session=secret", "x-powered-by": "PHP/8.3", "authorization": "Bearer t"},
		Resources: []model.Resource{{URL: "https://shop.test/app.js", Type: "Script", Status: 200}},
		Failed:    []model.Request{{URL: "https://shop.test/missing.css", Type: "Stylesheet", Error: "net::ERR_FAILED"}},
	}
}

func helperCmd(mode string, args ...string) Command {
	return Command{Name: "laravel-log", Run: append([]string{os.Args[0], "exec-helper", mode}, args...), Timeout: 20 * time.Second}
}

func module(t *testing.T, cmds ...Command) *Module {
	t.Helper()
	// Under -race every child would wait a second before exiting.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	m, err := New(cmds, true)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestIssues(t *testing.T) {
	docFile := filepath.Join(t.TempDir(), "doc.json")
	m := module(t, helperCmd("issues", docFile))
	cc, p := ctx(), page()
	m.CheckPage(cc, p)
	if len(cc.Run.Warnings) != 0 || len(p.Issues) != 2 {
		t.Fatalf("warnings %v issues %+v", cc.Run.Warnings, p.Issues)
	}
	i := p.Issues[0]
	if i.Check != "exec/laravel-log/error" || i.Level != model.Error || i.Detail != "on /contact/" || i.Source != "app/Http/Kernel.php:12" || i.Key == "" {
		t.Errorf("issue %+v", i)
	}
	if p.Issues[1].Level != model.Notice || p.Issues[1].Fix != "Add an index" {
		t.Errorf("issue %+v", p.Issues[1])
	}

	// The document the command read.
	data, err := os.ReadFile(docFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["schema"] != PageSchema || doc["path"] != "/contact/" || doc["status"] != 200.0 || doc["text"] != "Write to us" || doc["title"] != "Contact" {
		t.Errorf("doc %v", doc)
	}
	headers := doc["headers"].(map[string]any)
	if headers["content-type"] != "text/html" || headers["x-powered-by"] != "PHP/8.3" || headers["set-cookie"] != nil || headers["authorization"] != nil {
		t.Errorf("headers %v", headers)
	}
	reqs := doc["requests"].([]any)
	if len(reqs) != 2 || reqs[1].(map[string]any)["error"] != "net::ERR_FAILED" {
		t.Errorf("requests %v", reqs)
	}
	// Every key is in the published schema and every required one is there.
	var schema struct {
		Required   []string
		Properties map[string]any
	}
	if err := json.Unmarshal(PageSchemaJSON, &schema); err != nil {
		t.Fatal(err)
	}
	for k := range doc {
		if _, ok := schema.Properties[k]; !ok {
			t.Errorf("key %q isn't in page.v1.json", k)
		}
	}
	for _, k := range schema.Required {
		if _, ok := doc[k]; !ok {
			t.Errorf("required key %q is missing", k)
		}
	}
	if !json.Valid(IssuesSchemaJSON) {
		t.Error("issues.v1.json isn't JSON")
	}
}

func TestNoIssues(t *testing.T) {
	m := module(t, helperCmd("none"))
	cc, p := ctx(), page()
	m.CheckPage(cc, p)
	if len(cc.Run.Warnings) != 0 || len(p.Issues) != 0 {
		t.Errorf("warnings %v issues %v", cc.Run.Warnings, p.Issues)
	}
}

func TestBadAnswersWarn(t *testing.T) {
	for mode, want := range map[string]string{
		"garbage":     "answered with something that isn't JSON",
		"two":         "more than one JSON value",
		"missing":     `answered without "issues"`,
		"prefix":      `issue 1: check "health/status" must start with "exec/laravel-log/"`,
		"bare-prefix": `must start with "exec/laravel-log/"`,
		"level":       `issue 1: level "fatal": want error, warning or notice`,
		"title":       "issue 1: the title is missing",
		"many":        "reported 201 issues on one page; the most is 200",
		"big":         "printed more than 1 MB",
		"fail":        "exited with code 2: boom: no database",
	} {
		t.Run(mode, func(t *testing.T) {
			m := module(t, helperCmd(mode))
			cc, p := ctx(), page()
			m.CheckPage(cc, p)
			if len(p.Issues) != 0 || len(cc.Run.Warnings) != 1 || !strings.Contains(cc.Run.Warnings[0], want) ||
				!strings.HasPrefix(cc.Run.Warnings[0], "exec/laravel-log: ") || !strings.HasSuffix(cc.Run.Warnings[0], "(first on /contact/)") {
				t.Errorf("issues %v warnings %q", p.Issues, cc.Run.Warnings)
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	c := helperCmd("sleep")
	c.Timeout = 300 * time.Millisecond
	m := module(t, c)
	cc, p := ctx(), page()
	start := time.Now()
	m.CheckPage(cc, p)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %s", d)
	}
	if len(cc.Run.Warnings) != 1 || !strings.Contains(cc.Run.Warnings[0], "took longer than 300ms and was stopped") {
		t.Errorf("warnings %q", cc.Run.Warnings)
	}
}

func TestWarnsOncePerProblem(t *testing.T) {
	m := module(t, helperCmd("garbage"))
	cc := ctx()
	for _, path := range []string{"/a/", "/b/", "/c/"} {
		p := page()
		p.Path = path
		m.CheckPage(cc, p)
	}
	if len(cc.Run.Warnings) != 1 || !strings.HasSuffix(cc.Run.Warnings[0], "(first on /a/)") {
		t.Errorf("warnings %q", cc.Run.Warnings)
	}
}

func TestCantStart(t *testing.T) {
	m := module(t, Command{Name: "gone", Run: []string{filepath.Join(t.TempDir(), "no-such-program")}})
	cc, p := ctx(), page()
	m.CheckPage(cc, p)
	if len(cc.Run.Warnings) != 1 || !strings.Contains(cc.Run.Warnings[0], "exec/gone: couldn't run") {
		t.Errorf("warnings %q", cc.Run.Warnings)
	}
}

func TestNotAllowedRefuses(t *testing.T) {
	docFile := filepath.Join(t.TempDir(), "doc.json")
	m, err := New([]Command{helperCmd("issues", docFile)}, false)
	if err != nil {
		t.Fatal(err)
	}
	cc := ctx()
	for range 3 {
		p := page()
		m.CheckPage(cc, p)
		if len(p.Issues) != 0 {
			t.Errorf("issues %v", p.Issues)
		}
	}
	if _, err := os.Stat(docFile); err == nil {
		t.Error("the command ran without --allow-exec")
	}
	want := "exec: the config lists commands to run on every page; they run only with --allow-exec"
	if !slices.Equal(cc.Run.Warnings, []string{want}) {
		t.Errorf("warnings %q", cc.Run.Warnings)
	}
}

func TestSkipsPagesThatDidntLoad(t *testing.T) {
	docFile := filepath.Join(t.TempDir(), "doc.json")
	m := module(t, helperCmd("issues", docFile))
	cc, p := ctx(), page()
	p.Error = "net::ERR_CONNECTION_REFUSED"
	m.CheckPage(cc, p)
	if _, err := os.Stat(docFile); err == nil || len(p.Issues) != 0 {
		t.Error("ran on a page that didn't load")
	}
}

func TestCredentialsStayOut(t *testing.T) {
	t.Setenv("SOUNDCHECK_PASSWORD", "hunter2")
	t.Setenv("EXEC_TEST_VISIBLE", "yes")
	m := module(t, helperCmd("env"))
	cc, p := ctx(), page()
	m.CheckPage(cc, p)
	if len(p.Issues) != 1 || p.Issues[0].Title != "|yes" {
		t.Errorf("issues %+v warnings %v", p.Issues, cc.Run.Warnings)
	}
}

func TestNewRejects(t *testing.T) {
	ok := []string{"true"}
	for _, c := range []struct {
		cmds []Command
		want string
	}{
		{[]Command{{Name: "Laravel Log", Run: ok}}, `name "Laravel Log": use lower-case letters`},
		{[]Command{{Name: "", Run: ok}}, `name ""`},
		{[]Command{{Name: "a", Run: ok}, {Name: "a", Run: ok}}, `two commands are named "a"`},
		{[]Command{{Name: "a"}}, `exec "a": run is missing`},
		{[]Command{{Name: "a", Run: []string{""}}}, "run is missing"},
		{[]Command{{Name: "a", Run: ok, Timeout: time.Hour}}, "timeout 1h0m0s: want up to 5m0s"},
		{[]Command{{Name: "a", Run: ok, Timeout: -time.Second}}, "timeout -1s"},
	} {
		if _, err := New(c.cmds, true); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
	if m, err := New(nil, false); err != nil || m.Name() != "exec" {
		t.Errorf("empty: %v", err)
	}
}

func TestDocumentCapsText(t *testing.T) {
	p := page()
	p.Text = strings.Repeat("é", maxText) // two bytes each
	d := Document(p)
	if len(d.Text) > maxText || !strings.HasPrefix(p.Text, d.Text) || strings.ContainsRune(d.Text, '�') {
		t.Errorf("text of %d bytes", len(d.Text))
	}
	p = &model.Page{Path: "/"}
	if d := Document(p); d.Console == nil || d.Requests == nil || d.Headers == nil {
		t.Errorf("nil lists in %+v", d)
	}
}

// The module plugs into the runner as a page checker.
var _ check.PageChecker = (*Module)(nil)
