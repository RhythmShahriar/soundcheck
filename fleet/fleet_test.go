// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

// TestMain makes the test binary stand in for Soundcheck when the fleet
// runs it as a child: the site's host picks a canned result.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && (os.Args[1] == Snapshot || os.Args[1] == Compare) {
		os.Exit(fakeSoundcheck(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeSoundcheck(args []string) int {
	_ = os.WriteFile("env.txt", []byte(strings.Join(os.Environ(), "\n")), 0o600)
	_ = os.WriteFile("args.txt", []byte(strings.Join(args, "\n")), 0o600)
	u, _ := url.Parse(args[len(args)-1])
	emit := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = os.Stdout.Write(b)
	}
	cmpWith := func(classes ...string) *model.Comparison {
		c := &model.Comparison{Schema: model.Schema, Pages: 4, Before: model.RunRef{Name: "good"}}
		for i, cl := range classes {
			c.Changes = append(c.Changes, model.Change{Class: cl, Check: "health/x", Title: fmt.Sprint("change ", i)})
		}
		// Soundcheck writes the HTML report into the run's folder.
		dir := filepath.Join(".soundcheck", "runs", "2026-10-24T10-00-00")
		_ = os.MkdirAll(dir, 0o750)
		_ = os.WriteFile(filepath.Join(dir, "report.html"), []byte("<html>"), 0o600)
		return c
	}
	if args[0] == Snapshot {
		run := &model.Run{Schema: model.Schema, Name: "2026-10-24T10-00-00", Pages: []model.Page{{Path: "/"}, {Path: "/a/"}}}
		if u.Hostname() == "errors.test" {
			run.Pages[0].Issues = []model.Issue{{Check: "health/status", Level: model.Error, Title: "HTTP 500"}}
		}
		dir := filepath.Join(".soundcheck", "snapshots", run.Name)
		_ = os.MkdirAll(dir, 0o750)
		_ = os.WriteFile(filepath.Join(dir, "run.json"), []byte(`{"schema":"`+model.Schema+`"}`), 0o600)
		_ = os.WriteFile(filepath.Join(dir, "report.html"), []byte("<html>"), 0o600)
		emit(run)
		return 0
	}
	switch u.Hostname() {
	case "broke.test":
		emit(cmpWith(model.Broke, model.Broke, model.New))
	case "changed.test":
		emit(cmpWith(model.Changed, model.Fixed))
	case "summary.test": // the newer JSON with kind and summary
		emit(map[string]any{"kind": "comparison", "pages": 3, "changes": []any{},
			"summary": map[string]any{"new": 1, "verdict": "changed"}})
	case "failon.test":
		emit(cmpWith(model.Slower))
		return 1
	case "down.test":
		fmt.Fprintln(os.Stderr, "rhythm soundcheck: couldn't reach down.test")
		return 3
	case "slow.test":
		time.Sleep(20 * time.Second)
	default:
		emit(cmpWith())
	}
	return 0
}

func writeSites(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "sites.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	p := writeSites(t, `
- name: acme-shop
  url: https://staging.acme.test
  args: ["--limit", "5"]
- name: 9lives
  url: brew.test
  env_prefix: BREW
`)
	sites, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 || sites[0].EnvPrefix != "ACME_SHOP" || sites[1].EnvPrefix != "BREW" || sites[0].Args[1] != "5" {
		t.Errorf("sites = %+v", sites)
	}
	if DefaultPrefix("9lives") != "SITE_9LIVES" {
		t.Errorf("DefaultPrefix(9lives) = %s", DefaultPrefix("9lives"))
	}
	p = writeSites(t, "sites:\n  - name: one\n    url: http://one.test\n")
	if sites, err := Load(p); err != nil || len(sites) != 1 {
		t.Errorf("mapping form: %v %v", sites, err)
	}
}

func TestLoadRejects(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"empty":            {"", "no sites"},
		"empty list":       {"[]", "no sites"},
		"duplicate name":   {"- {name: acme, url: a.test}\n- {name: Acme, url: b.test}", "already used"},
		"path name":        {"- {name: ../acme, url: a.test}", "name must be"},
		"missing name":     {"- {url: a.test}", "name must be"},
		"missing url":      {"- {name: acme}", "url is missing"},
		"ftp url":          {"- {name: acme, url: 'ftp://a.test'}", "isn't an http"},
		"unknown key":      {"- {name: acme, url: a.test, env-prefix: X}", "env-prefix"},
		"unknown top key":  {"sitez: []", "sitez"},
		"bad prefix":       {"- {name: acme, url: a.test, env_prefix: 'acme;rm'}", "env_prefix"},
		"duplicate prefix": {"- {name: a, url: a.test, env_prefix: X}\n- {name: b, url: b.test, env_prefix: X}", "already used"},
		"json arg":         {"- {name: acme, url: a.test, args: [--json]}", "can't set --json"},
		"output arg":       {"- {name: acme, url: a.test, args: ['-o=x.html']}", "can't set -o=x.html"},
		"config arg":       {"- {name: acme, url: a.test, args: [--config, x]}", "can't set --config"},
		"missing config":   {"- {name: acme, url: a.test, config: nosuch.yaml}", "nosuch.yaml"},
		"not yaml":         {"- [", "yaml"},
	} {
		_, err := Load(writeSites(t, tc.yaml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

func TestChildEnv(t *testing.T) {
	env := childEnv([]string{
		"PATH=/bin", "HOME=/home/x", "LC_ALL=C",
		"ACME_SOUNDCHECK_USER=alice", "ACME_SOUNDCHECK_PASSWORD=pw", "ACME_RHYTHM_ECHO_TOKEN=tok",
		"BREW_SOUNDCHECK_PASSWORD=brewpw", "ACME_SHOP_SOUNDCHECK_PASSWORD=shoppw",
		"SOUNDCHECK_PASSWORD=global", "AWS_SECRET_ACCESS_KEY=aws", "ACME_OTHER=x", "ACME_SOUNDCHECK_=empty",
	}, "ACME")
	got := strings.Join(env, " ")
	want := "NO_COLOR=1 PATH=/bin HOME=/home/x LC_ALL=C SOUNDCHECK_USER=alice SOUNDCHECK_PASSWORD=pw RHYTHM_ECHO_TOKEN=tok"
	if got != want {
		t.Errorf("childEnv =\n %s\nwant\n %s", got, want)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunCompare(t *testing.T) {
	t.Setenv("ALPHA_SOUNDCHECK_PASSWORD", "alpha-secret")
	t.Setenv("BETA_SOUNDCHECK_PASSWORD", "beta-secret")
	t.Setenv("SOUNDCHECK_PASSWORD", "global-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret")
	p := writeSites(t, `
- {name: alpha, url: "http://clean.test/", config: alpha.yaml, args: ["--limit", "5"]}
- {name: beta, url: "http://broke.test/"}
- {name: gamma, url: "http://down.test/"}
- {name: delta, url: "http://changed.test/"}
- {name: epsilon, url: "http://summary.test/"}
- {name: zeta, url: "http://failon.test/"}
`)
	dir := filepath.Dir(p)
	if err := os.WriteFile(filepath.Join(dir, "alpha.yaml"), []byte("pages: {limit: 5}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := Run(context.Background(), os.Args[0], p, Compare, 3, &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 3 {
		t.Errorf("exit = %d, want 3 (the site that couldn't be checked)", code)
	}
	var page Page
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, Folder, "fleet.json"))), &page); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, r := range page.Sites {
		order = append(order, r.Site+":"+r.Verdict)
	}
	want := "gamma:failed beta:broke epsilon:changed delta:changed zeta:changed alpha:clean"
	if strings.Join(order, " ") != want {
		t.Errorf("ranking = %s\nwant      %s", strings.Join(order, " "), want)
	}
	beta := page.Sites[1]
	if beta.Counts[model.Broke] != 2 || beta.Counts[model.New] != 1 || beta.Pages != 4 || beta.Exit != 0 {
		t.Errorf("beta = %+v", beta)
	}
	if beta.Report != "beta/.soundcheck/runs/2026-10-24T10-00-00/report.html" {
		t.Errorf("beta report = %q", beta.Report)
	}
	if g := page.Sites[0]; g.Exit != 3 || !strings.Contains(g.Error, "soundcheck.log") {
		t.Errorf("gamma = %+v", g)
	}
	if log := readFile(t, filepath.Join(dir, Folder, "gamma", "soundcheck.log")); !strings.Contains(log, "couldn't reach") {
		t.Errorf("gamma's stderr not kept: %q", log)
	}

	args := readFile(t, filepath.Join(dir, Folder, "alpha", "args.txt"))
	wantArgs := strings.Join([]string{"compare", "--json", "--config", filepath.Join(dir, "alpha.yaml"), "--limit", "5", "http://clean.test/"}, "\n")
	if args != wantArgs {
		t.Errorf("alpha args =\n%s\nwant\n%s", args, wantArgs)
	}

	// Each site sees only its own password, and never the fleet's own
	// SOUNDCHECK_* or unrelated secrets.
	alpha := readFile(t, filepath.Join(dir, Folder, "alpha", "env.txt"))
	betaEnv := readFile(t, filepath.Join(dir, Folder, "beta", "env.txt"))
	if !strings.Contains(alpha, "SOUNDCHECK_PASSWORD=alpha-secret") || !strings.Contains(betaEnv, "SOUNDCHECK_PASSWORD=beta-secret") {
		t.Error("a site didn't get its own password")
	}
	for _, leak := range []string{"alpha-secret", "global-secret", "aws-secret", "ALPHA_"} {
		if strings.Contains(betaEnv, leak) {
			t.Errorf("beta's environment has %q", leak)
		}
	}
	if strings.Contains(alpha, "beta-secret") {
		t.Error("alpha's environment has beta's password")
	}

	html := readFile(t, filepath.Join(dir, Folder, "fleet.html"))
	if i, j := strings.Index(html, ">gamma<"), strings.Index(html, ">alpha<"); i < 0 || j < 0 || i > j {
		t.Error("fleet.html doesn't list gamma before alpha")
	}
	if !strings.Contains(html, `href="beta/.soundcheck/runs/2026-10-24T10-00-00/report.html"`) || !strings.Contains(html, "2 broke, 1 new") {
		t.Error("fleet.html lacks beta's report link or counts")
	}
	for _, line := range []string{"Fleet: comparing 6 sites, 3 at a time", "beta: Something broke · 2 broke, 1 new", "Worst: gamma (Couldn't check)"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("output lacks %q:\n%s", line, out.String())
		}
	}
}

func TestRunSnapshot(t *testing.T) {
	p := writeSites(t, "- {name: one, url: http://ok.test/}\n- {name: two, url: http://errors.test/}\n")
	code, err := Run(context.Background(), os.Args[0], p, Snapshot, 0, &bytes.Buffer{})
	if err != nil || code != 0 {
		t.Fatalf("Run = %d, %v", code, err)
	}
	var page Page
	_ = json.Unmarshal([]byte(readFile(t, filepath.Join(filepath.Dir(p), Folder, "fleet.json"))), &page)
	if len(page.Sites) != 2 || page.Sites[0].Site != "two" || page.Sites[0].Verdict != Errors || page.Sites[0].Counts["error"] != 1 ||
		page.Sites[1].Verdict != Clean || page.Sites[1].Pages != 2 || page.Sites[1].Kind != "run" {
		t.Errorf("sites = %+v", page.Sites)
	}
	if page.Sites[1].Report != "one/.soundcheck/snapshots/2026-10-24T10-00-00/report.html" {
		t.Errorf("snapshot report = %q", page.Sites[1].Report)
	}
}

func TestRunCanceled(t *testing.T) {
	p := writeSites(t, "- {name: slow, url: http://slow.test/}\n")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	code, err := Run(ctx, os.Args[0], p, Compare, 1, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if code != exitCanceled || time.Since(start) > 15*time.Second {
		t.Errorf("exit = %d after %s, want 130 soon after the cancel", code, time.Since(start))
	}
}

func TestRunBadInput(t *testing.T) {
	p := writeSites(t, "- {name: one, url: http://ok.test/}\n")
	if _, err := Run(context.Background(), os.Args[0], p, "check", 1, &bytes.Buffer{}); err == nil {
		t.Error("an unknown mode was accepted")
	}
	if _, err := Run(context.Background(), os.Args[0], writeSites(t, "- {name: '..'}"), Compare, 1, &bytes.Buffer{}); err == nil {
		t.Error("a bad sites file was accepted")
	}
	code, err := Run(context.Background(), filepath.Join(t.TempDir(), "nosuch"), p, Compare, 1, &bytes.Buffer{})
	if err != nil || code != exitError {
		t.Errorf("missing binary: %d, %v; want exit 3", code, err)
	}
}
