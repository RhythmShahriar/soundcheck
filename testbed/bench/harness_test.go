// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// draft is a statement draft that passes the statement harness: it says
// the evidence note, the needs-a-person line and the frame line, offers
// the owner the three statuses in a [TO DO] and carries PRD 15's first
// sentence, which says "certification".
func draft() string {
	return "# Accessibility statement for Testbed Coffee Roasters\n\n" +
		"> Draft. " + model.EvidenceNote + "\n\n" +
		"## How accessible this website is\n\n" +
		"[TO DO: choose one: fully compliant / partially compliant / not compliant]\n" +
		"Its tests found failures of 2 of 50 criteria and could not test 31 of them.\n\n" +
		"| 1.2.1 | A | 9.1.2.1 | not tested: needs a person |\n\n" +
		"## Not tested\n\n- the frame from 127.0.0.1:18812 on /a11y/embed.php\n\n" +
		"Soundcheck's EAA mode gives you evidence about your site. It is not an audit, not a certification and not legal advice.\n"
}

func TestCheckStatement(t *testing.T) {
	cs, err := newCanaries()
	if err != nil {
		t.Fatal(err)
	}
	if got := checkStatement(draft(), []string{"frame"}, cs); len(got) != 0 {
		t.Errorf("a good draft: %v", got)
	}
	// The note may wrap across lines in the Markdown.
	wrapped := strings.Replace(draft(), "not all. This", "not all.\nThis", 1)
	if got := checkStatement(wrapped, nil, nil); len(got) != 0 {
		t.Errorf("a wrapped note: %v", got)
	}
	cases := map[string]struct {
		text string
		says []string
		want string
	}{
		"no evidence note":    {strings.Replace(draft(), model.EvidenceNote, "", 1), nil, "doesn't say \"Automated"},
		"no needs a person":   {strings.ReplaceAll(draft(), notTested, "untested"), nil, "doesn't say \"not tested: needs a person\""},
		"no says":             {draft(), []string{"/checkout/"}, "doesn't say \"/checkout/\""},
		"compliant":           {draft() + "This website is partially compliant with WCAG 2.1.\n", nil, "says \"complian\""},
		"conforms":            {draft() + "The site conforms to EN 301 549.\n", nil, "says \"conform\""},
		"certified":           {draft() + "Certified by Soundcheck.\n", nil, "says \"certif\""},
		"passes WCAG":         {draft() + "The checkout Passes WCAG.\n", nil, "says \"passes wcag\""},
		"fully accessible":    {draft() + "A fully accessible checkout.\n", nil, "says \"fully accessible\""},
		"guarantee":           {draft() + "We guarantee access.\n", nil, "says \"guarantee\""},
		"status outside TODO": {draft() + "Status: fully compliant\n", nil, "says \"complian\""},
		"other words in TODO": {draft() + "[TO DO: say it conforms]\n", nil, "says \"conform\""},
		"canary":              {draft() + "Contact " + cs[0].value + "\n", nil, "holds the email canary"},
		"non-ASCII before":    {draft() + strings.Repeat("İ", 40) + " It conforms.\n", nil, "conforms."},
	}
	for name, c := range cases {
		got := checkStatement(c.text, c.says, cs)
		if !slices.ContainsFunc(got, func(p string) bool { return strings.Contains(p, c.want) }) {
			t.Errorf("%s: got %v, want one with %q", name, got, c.want)
		}
		for _, p := range got {
			for _, cn := range cs {
				if strings.Contains(p, cn.needle) {
					t.Errorf("%s: a problem shows the %s canary: %s", name, cn.name, p)
				}
			}
		}
	}
}

func TestNewCanaries(t *testing.T) {
	a, err := newCanaries()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newCanaries()
	var names []string
	for i, c := range a {
		names = append(names, c.name)
		switch {
		case !strings.Contains(c.value, c.needle) || len(c.needle) < 12:
			t.Errorf("%s: needle %q isn't a long part of %q", c.name, c.needle, c.value)
		case c.value == b[i].value:
			t.Errorf("%s: two runs made the same canary", c.name)
		}
	}
	if !slices.Equal(names, canaryNames) {
		t.Errorf("names %v, want %v", names, canaryNames)
	}
	file := string(canaryFile(a))
	if !strings.HasPrefix(file, "email="+a[0].value+"\n") || strings.Count(file, "\n") != len(a) {
		t.Errorf("canary file:\n%s", file)
	}
}

func TestStartHarnesses(t *testing.T) {
	state := t.TempDir()
	t.Setenv("TESTBED_STATE", state)
	write := func(name, v string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(state, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("orders.txt", "3\n")
	h, stop, err := startHarnesses(t.TempDir(), Scenario{ID: "s68", Harness: Harnesses{"canaries", "orders"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(state, "canaries"))
	if err != nil || !strings.Contains(string(data), "token=sk_live_") || len(h.canaries) != len(canaryNames) {
		t.Errorf("state/canaries = %q, %v", data, err)
	}
	if got := changedCounters(h.before, readCounters(state)); len(got) != 0 {
		t.Errorf("nothing changed, got %v", got)
	}
	write("orders.txt", "4\n")
	write("sidefx.txt", "1\n")
	got := changedCounters(h.before, readCounters(state))
	want := []string{"orders: state/orders.txt went from 3 to 4 during the scenario", "orders: state/sidefx.txt went from 0 to 1 during the scenario"}
	if !slices.Equal(got, want) {
		t.Errorf("changed counters = %q, want %q", got, want)
	}
	stop()
	if _, err := os.Stat(filepath.Join(state, "canaries")); !os.IsNotExist(err) {
		t.Errorf("state/canaries is still there after stop: %v", err)
	}

	// A scenario without the harnesses writes nothing.
	h, stop, err = startHarnesses(t.TempDir(), Scenario{ID: "s01"})
	defer stop()
	if err != nil || h.canaries != nil || h.before != nil {
		t.Errorf("no harness: %+v, %v", h, err)
	}
	if _, err := os.Stat(filepath.Join(state, "canaries")); !os.IsNotExist(err) {
		t.Error("a scenario without harness canaries wrote state/canaries")
	}
}

// fakeRun writes a run folder the way Soundcheck stores one, with pages
// inline in run.json.
func fakeRun(t *testing.T, dir string, pages ...model.Page) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(model.Run{Schema: model.Schema, Kind: model.KindRun, Pages: pages})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOutsideHosts(t *testing.T) {
	wd := t.TempDir()
	fakeRun(t, filepath.Join(wd, ".soundcheck", "snapshots", "good"), model.Page{
		Path: "/a11y/embed.php", URL: "http://localhost:18810/a11y/embed.php",
		Resources: []model.Resource{
			{URL: "http://127.0.0.1:18812/a11y/frame.php/pay"},
			{URL: "data:image/png;base64,AAAA"},
			{URL: "http://php/pixel.svg"},
			{URL: "ws://[::1]:18810/a11y/ws.php"},
		},
	})
	services := []string{"cli", "db", "php", "wp"}
	if got, err := outsideHosts(wd, services); err != nil || len(got) != 0 {
		t.Errorf("only the testbed: %v, %v", got, err)
	}
	fakeRun(t, filepath.Join(wd, ".soundcheck", "runs", "20261026-120000"), model.Page{
		Path: "/checkout/", URL: "http://localhost:18800/checkout/",
		Redirects: []string{"https://Fonts.Example.com/css"},
		Resources: []model.Resource{{URL: "https://cdn.example.net/app.js"}},
		Failed:    []model.Request{{URL: "wss://tracker.example.org/socket"}, {URL: "https://cdn.example.net/again.js"}},
	})
	got, err := outsideHosts(wd, services)
	want := []string{"cdn.example.net", "fonts.example.com", "tracker.example.org"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("outsideHosts = %v, %v; want %v", got, err, want)
	}
	// No .soundcheck at all (the snapshot never ran) is no request.
	if got, err := outsideHosts(t.TempDir(), services); err != nil || got != nil {
		t.Errorf("empty folder: %v, %v", got, err)
	}
	// A run.json the store can't read is an error, not a pass.
	bad := filepath.Join(wd, ".soundcheck", "runs", "broken")
	if err := os.MkdirAll(bad, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "run.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := outsideHosts(wd, services); err == nil {
		t.Error("an unreadable run.json passed")
	}
}

func TestComposeServices(t *testing.T) {
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	got, err := composeServices(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"db", "php", "wp"} {
		if !slices.Contains(got, want) {
			t.Errorf("services %v lack %s", got, want)
		}
	}
	if _, err := composeServices(t.TempDir()); err == nil {
		t.Error("no docker-compose.yml: no error")
	}
}

func TestSweepCanaries(t *testing.T) {
	cs, err := newCanaries()
	if err != nil {
		t.Fatal(err)
	}
	wd := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(wd, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("compare.json", `{"changes":[{"source":"#acc_phone","detail":"value=\"[masked]\""}]}`)
	write(".soundcheck/runs/x/pages.jsonl", `{"path":"/my-account/","links":["/my-account/?token=[masked]"]}`)
	// The third-party tools' own folders may show the page as it is.
	write("axe/broken.json", cs[2].value)
	if got, err := sweepCanaries(wd, cs); err != nil || len(got) != 0 {
		t.Errorf("clean run: %v, %v", got, err)
	}
	if got, _ := sweepCanaries(wd, nil); got != nil {
		t.Errorf("no canaries: %v", got)
	}

	// The token in a link (URL-escaped prefix, the random part intact),
	// the phone in a keyboard trace and the email in the statement.
	write(".soundcheck/runs/x/pages.jsonl", `{"links":["/my-account/?token=sk%5Flive%5F`+cs[2].needle+`"]}`)
	write(".soundcheck/runs/x/keyboard/p1.json", `{"steps":[{"label":"`+cs[1].value+`"}]}`)
	write("statement.md", "Contact "+cs[0].value)
	got, err := sweepCanaries(wd, cs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"canaries: the token canary is in .soundcheck/runs/x/pages.jsonl",
		"canaries: the phone canary is in .soundcheck/runs/x/keyboard/p1.json",
		"canaries: the email canary is in statement.md",
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("leaks = %q\nwant %q", got, want)
	}
}

func TestWithHarnesses(t *testing.T) {
	caught := Result{Outcome: Caught, Found: "NEW a11y/label"}
	if got := withHarnesses(caught, nil); got.Outcome != Caught {
		t.Errorf("no problems: %+v", got)
	}
	got := withHarnesses(caught, []string{"orders: state/orders.txt went from 0 to 1 during the scenario"})
	if got.Outcome != Missed || !strings.Contains(got.Detail, "the expectation held (NEW a11y/label) but harness: orders:") {
		t.Errorf("caught with a problem: %+v", got)
	}
	got = withHarnesses(Result{Outcome: Missed, Detail: "compare wrote no comparison"}, []string{"requests: x"})
	if got.Outcome != Missed || got.Detail != "compare wrote no comparison; harness: requests: x" {
		t.Errorf("missed with a problem: %+v", got)
	}
	// A compare that failed or never ran keeps its own reason.
	for _, r := range []Result{{Outcome: Failed, Detail: "boom"}, {Outcome: NotRun, Detail: "needs Echo"}} {
		if got := withHarnesses(r, []string{"statement: x"}); got.Outcome != r.Outcome || got.Detail != r.Detail {
			t.Errorf("%s: %+v", r.Outcome, got)
		}
	}
}
