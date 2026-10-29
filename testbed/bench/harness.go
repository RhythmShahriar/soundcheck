// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// The EAA harnesses (scenarios.yaml: statement, requests, orders,
// canaries) check what a scenario must never do, whatever the
// comparison says: claim compliance, contact a host outside the
// testbed, place an order or leak a value the page showed. A scenario
// that breaks one is missed, even when its expectation held.

// stateDir is the testbed's state folder, the one toggle.sh and the
// sites use.
func stateDir(testbed string) string {
	if d := os.Getenv("TESTBED_STATE"); d != "" {
		return d
	}
	return filepath.Join(testbed, "state")
}

// counters are the files the sites count side effects in: the shop's
// orders and s88's GET and focus side effects.
var counters = []string{"orders.txt", "sidefx.txt"}

// harnessRun is what the EAA harnesses set up before a scenario and
// check after its compare.
type harnessRun struct {
	state    string
	canaries []canary          // harness canaries
	before   map[string]string // harness orders: each counter's content before
}

// startHarnesses prepares a scenario's harnesses: it writes the
// canaries into the state folder and reads the counters. stop removes
// the canaries again.
func startHarnesses(testbed string, s Scenario) (h *harnessRun, stop func(), err error) {
	h = &harnessRun{state: stateDir(testbed)}
	stop = func() {}
	if s.Harness.Has("orders") {
		h.before = readCounters(h.state)
	}
	if s.Harness.Has("canaries") {
		if h.canaries, err = newCanaries(); err != nil {
			return nil, stop, err
		}
		path := filepath.Join(h.state, "canaries")
		// 0644: the web servers in the containers run as www-data and only
		// read it. The values are fake and made for this run.
		if err := os.WriteFile(path, canaryFile(h.canaries), 0o644); err != nil { //nolint:gosec // see above
			return nil, stop, err
		}
		stop = func() { _ = os.Remove(path) }
	}
	return h, stop, nil
}

// check runs the scenario's harnesses after its compare and returns
// what went wrong, one line each. The statement harness writes its
// draft first, so the canary sweep reads it too.
func (h *harnessRun) check(ctx context.Context, o options, testbed string, s Scenario, wd, bin, cfgPath string) []string {
	var problems []string
	if s.Harness.Has("statement") {
		problems = append(problems, statementHarness(ctx, o, s, wd, bin, cfgPath, h.canaries)...)
	}
	if s.Harness.Has("canaries") {
		problems = append(problems, h.leftOut(ctx, o, s, wd, bin, cfgPath)...)
		leaks, err := sweepCanaries(wd, h.canaries)
		if err != nil {
			problems = append(problems, "canaries: "+err.Error())
		}
		problems = append(problems, leaks...)
	}
	if s.Harness.Has("requests") {
		services, err := composeServices(testbed)
		if err == nil {
			var hosts []string
			hosts, err = outsideHosts(wd, services)
			for _, host := range hosts {
				problems = append(problems, "requests: Chrome asked "+host+", which isn't the testbed")
			}
		}
		if err != nil {
			problems = append(problems, "requests: "+err.Error())
		}
	}
	if s.Harness.Has("orders") {
		problems = append(problems, changedCounters(h.before, readCounters(h.state))...)
	}
	return problems
}

// withHarnesses folds harness problems into a scenario's result: any
// problem makes it missed, except that a false alarm stays one.
func withHarnesses(r Result, problems []string) Result {
	if len(problems) == 0 || r.Outcome == NotRun || r.Outcome == Failed {
		return r
	}
	detail := "harness: " + strings.Join(problems, "; ")
	if r.Outcome == Caught {
		detail = "the expectation held (" + r.Found + ") but " + detail
	} else if r.Detail != "" {
		detail = r.Detail + "; " + detail
	}
	if r.Outcome == FalseAlarm {
		// Keep the false alarm so a good scenario still counts as one; the
		// harness problem goes in the detail.
		return Result{Outcome: FalseAlarm, Found: r.Found, Detail: detail, Others: r.Others}
	}
	return Result{Outcome: Missed, Detail: detail, Others: r.Others}
}

// readCounters reads each counter file; a missing one reads as empty.
func readCounters(state string) map[string]string {
	out := map[string]string{}
	for _, f := range counters {
		data, _ := os.ReadFile(filepath.Join(state, f)) //nolint:gosec // the testbed's own state folder
		out[f] = strings.TrimSpace(string(data))
	}
	return out
}

// changedCounters lists the counters that changed between two reads.
func changedCounters(before, after map[string]string) []string {
	var out []string
	for _, f := range counters {
		if before[f] != after[f] {
			out = append(out, fmt.Sprintf("orders: state/%s went from %s to %s during the scenario", f, orZero(before[f]), orZero(after[f])))
		}
	}
	return out
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// canary is one value the testbed shows on a page and Soundcheck must
// never write down. Its name is what the pages read from
// state/canaries; needle is the random part searched for, which no
// escaping or prefix masking changes.
type canary struct {
	name, value, needle string
	// start marks the canary on the start URL's query (startCanary): the
	// pages don't read it. The stored run (run.json and pages.jsonl)
	// keeps the URL as typed, so compare and resume visit it again.
	start bool
}

// startCanary is the query parameter the canaries harness adds to the
// start URL: a short value under a name that sounds like no credential,
// the kind only a rule that masks every query value of an --eaa result
// keeps out of its outputs (security M3).
const startCanary = "sid"

// storedRun lists the files that hold the run as stored (store's
// run.json and its pages files) and a snapshot comparison's
// comparison.json, where a start canary may stay: compare and resume
// visit a page again by its path, accept and ignore find a change's
// pages by theirs.
var storedRun = []string{"run.json", "pages.jsonl", "pages.1.jsonl", "comparison.json"}

// canaryNames are the canaries the pages read: s68's account form
// (email, phone, token), s84's payment frame (data, field, aria,
// query, text) and s91's controls, which carry them in an id (idtoken,
// idemail, trapemail), a name (namejwt) and a class (classtoken).
var canaryNames = []string{"email", "phone", "token", "data", "field", "aria", "query", "text",
	"idtoken", "namejwt", "idemail", "classtoken", "trapemail"}

// letters is the alphabet of the canaries an id carries: no digits and
// no hex letters, so Soundcheck doesn't take the id for a generated one
// and leave it out of a selector, which would hide a leak.
const letters = "ghijklmnopqrstuvwxyzGHIJKLMNOPQRSTUVWXYZ"

// newCanaries makes this run's canaries. Each is built from parts at
// run time, so no secret-looking value is ever committed (push
// protection blocks literal ones).
func newCanaries() ([]canary, error) {
	var out []canary
	for _, name := range canaryNames {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		core := hex.EncodeToString(b)
		word, err := randomWord(36)
		if err != nil {
			return nil, err
		}
		var value string
		switch name {
		case "idtoken", "classtoken":
			// A GitHub token: gh + p_ and 36 characters.
			core = word
			value = "gh" + "p_" + core
		case "namejwt":
			// A JWT: header, payload and signature, base64url.
			enc := base64.RawURLEncoding.EncodeToString
			core = enc([]byte(`{"sub":"` + word[:20] + `"}`))
			value = enc([]byte(`{"alg":"HS256"}`)) + "." + core + "." + word[20:]
		case "idemail", "trapemail":
			core = word[:20]
			value = "c-" + core + "@" + "example" + ".test"
		case "email", "aria":
			value = "canary." + core + "@" + "example" + ".test"
		case "phone":
			// Digits only, so the needle is the number itself.
			digits := make([]byte, 0, 12)
			for _, c := range b {
				digits = append(digits, '0'+c%10)
			}
			core = "447700" + string(digits[:6])
			value = "+" + core
		case "token", "query":
			value = "sk_" + "live_" + core
		default:
			value = "cnry_" + core
		}
		out = append(out, canary{name: name, value: value, needle: core})
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	sid := hex.EncodeToString(b)
	return append(out, canary{name: startCanary, value: sid, needle: sid, start: true}), nil
}

// randomWord is n random characters of letters.
func randomWord(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b), nil
}

// startURL is u with the start canary added to its query, or u when
// the scenario has no canaries.
func (h *harnessRun) startURL(u string) string {
	for _, c := range h.canaries {
		if c.start {
			sep := "?"
			if strings.Contains(u, "?") {
				sep = "&"
			}
			return u + sep + c.name + "=" + c.value
		}
	}
	return u
}

// Terminal files: what the canaries harness's runs printed, kept in the
// scenario's folder so the sweep reads the terminal too.
const (
	snapshotTerminal = "snapshot.terminal.txt"
	leftOutTerminal  = "left-out.terminal.txt"
)

// snapshotArgs are the flags the canaries harness adds to a scenario's
// snapshot: a --page under the scenario's path with the token canary in
// a reset_token query, which the default exclude *?*token=* drops. The
// note the run prints about it must mask the value.
func (h *harnessRun) snapshotArgs(path string) []string {
	for _, c := range h.canaries {
		if c.name == "token" {
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			return []string{"--page", path + sep + "reset_token=" + c.value}
		}
	}
	return nil
}

// leftOutArgs are the flags of the extra compare the canaries harness
// runs: one that excludes the start page the snapshot stored with the
// start canary in its query, so the run prints a note about a snapshot
// page it didn't check. That note must mask the value too.
func (h *harnessRun) leftOutArgs(cfgPath string, s Scenario, echo string) []string {
	for _, c := range h.canaries {
		if c.start {
			args := []string{"compare", "--against-snapshot", "good", "--json", "-o", "left-out.json", "--config", cfgPath,
				"--exclude", "*" + c.name + "=*"}
			return append(args, scenarioArgs(s, echo)...)
		}
	}
	return nil
}

// leftOut runs the extra compare (leftOutArgs) and keeps what it
// printed for the sweep. Both terminals must show their note about a
// page left out: without one the harness tested nothing.
func (h *harnessRun) leftOut(ctx context.Context, o options, s Scenario, wd, bin, cfgPath string) []string {
	args := h.leftOutArgs(cfgPath, s, o.echo)
	if args == nil {
		return nil
	}
	// compare exits non-zero when something broke: only the output
	// matters here.
	out, _, _ := command(ctx, o.timeout, wd, env(s), bin, args...)
	if err := os.WriteFile(filepath.Join(wd, leftOutTerminal), []byte(out), 0o600); err != nil {
		return []string{"canaries: " + err.Error()}
	}
	// A snapshot copied from a fixture printed nothing.
	snap, err := os.ReadFile(filepath.Join(wd, snapshotTerminal)) //nolint:gosec // our own file
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return []string{"canaries: " + err.Error()}
	}
	return leftOutNotes(string(snap), err == nil, out)
}

// leftOutNotes checks that the snapshot (when one was taken) printed
// its note about the --page it dropped and the extra compare its note
// about the snapshot page it excluded.
func leftOutNotes(snapshot string, took bool, leftOut string) []string {
	var problems []string
	if took && (!strings.Contains(snapshot, "--page ") || !strings.Contains(snapshot, "not checked")) {
		problems = append(problems, "canaries: the snapshot printed no note about the --page it left out")
	}
	if !strings.Contains(leftOut, "page ") || !strings.Contains(leftOut, "not checked") {
		problems = append(problems, "canaries: the compare printed no note about the snapshot page it left out")
	}
	return problems
}

// canaryFile is state/canaries: name=value, one per line.
func canaryFile(cs []canary) []byte {
	var b bytes.Buffer
	for _, c := range cs {
		if c.start {
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", c.name, c.value)
	}
	return b.Bytes()
}

// sweepCanaries reads every file the run wrote (the run's .soundcheck
// folder and the -o files next to it, not the third-party tools'
// folders) and lists each canary found, by name (a start canary may
// stay in the stored run): the value itself stays
// out of the scoreboard.
func sweepCanaries(wd string, cs []canary) ([]string, error) {
	if len(cs) == 0 {
		return nil, nil
	}
	var leaks []string
	seen := map[string]bool{}
	look := func(path string) error {
		data, err := os.ReadFile(path) //nolint:gosec // files the run wrote
		if err != nil {
			return err
		}
		stored := slices.Contains(storedRun, filepath.Base(path))
		for _, c := range cs {
			if c.start && stored {
				continue
			}
			if bytes.Contains(data, []byte(c.needle)) {
				rel, _ := filepath.Rel(wd, path)
				if key := c.name + rel; !seen[key] {
					seen[key] = true
					leaks = append(leaks, fmt.Sprintf("canaries: the %s canary is in %s", c.name, filepath.ToSlash(rel)))
				}
			}
		}
		return nil
	}
	entries, err := os.ReadDir(wd)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			if err := look(filepath.Join(wd, e.Name())); err != nil {
				return leaks, err
			}
		}
	}
	err = filepath.WalkDir(filepath.Join(wd, ".soundcheck"), func(path string, d fs.DirEntry, err error) error {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		case !d.Type().IsRegular():
			return nil
		}
		return look(path)
	})
	return leaks, err
}

// localHosts are the hosts Chrome may ask besides the compose services.
var localHosts = []string{"localhost", "127.0.0.1", "::1"}

// composeServices lists the services in the testbed's
// docker-compose.yml: their names are hosts inside the stack.
func composeServices(testbed string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(testbed, "docker-compose.yml")) //nolint:gosec // the testbed's own file
	if err != nil {
		return nil, err
	}
	var c struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("docker-compose.yml: %w", err)
	}
	var out []string
	for name := range c.Services {
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

// outsideHosts reads every run under the scenario's .soundcheck folder
// (snapshot and compare) and lists the hosts Chrome asked that aren't
// the testbed's: every resource, failed request, redirect and final URL
// of every page.
func outsideHosts(wd string, services []string) ([]string, error) {
	allowed := append(slices.Clone(localHosts), services...)
	var out []string
	add := func(raw string) {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return // data:, blob:, about: and the like ask no host
		}
		switch u.Scheme {
		case "http", "https", "ws", "wss":
		default:
			return
		}
		h := strings.ToLower(u.Hostname())
		if !slices.Contains(allowed, h) && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	err := filepath.WalkDir(filepath.Join(wd, ".soundcheck"), func(path string, d fs.DirEntry, err error) error {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		case d.IsDir() || d.Name() != "run.json":
			return nil
		}
		run, err := store.LoadRun(filepath.Dir(path))
		if err != nil {
			return err
		}
		for _, p := range run.Pages {
			add(p.URL)
			add(p.FinalURL)
			for _, r := range p.Redirects {
				add(r)
			}
			for _, r := range p.Resources {
				add(r.URL)
			}
			for _, r := range p.Failed {
				add(r.URL)
			}
		}
		return nil
	})
	slices.Sort(out)
	return out, err
}

// statementOwner is the statement: block the bench writes into the
// config of a scenario with harness statement: the draft refuses to
// run without an owner, a website and a contact.
func statementOwner(site string) map[string]any {
	return map[string]any{
		"owner":   "Testbed Coffee Roasters",
		"website": site,
		"contact": map[string]string{"email": "access@" + "example" + ".test"},
	}
}

// statementHarness writes the statement draft of the scenario's compare
// (`statement -o statement.md`) and checks its wording.
func statementHarness(ctx context.Context, o options, s Scenario, wd, bin, cfgPath string, cs []canary) []string {
	out := filepath.Join(wd, "statement.md")
	if _, _, err := command(ctx, o.timeout, wd, env(s), bin, "statement", "-o", out, "--config", cfgPath); err != nil {
		return []string{"statement: " + err.Error()}
	}
	data, err := os.ReadFile(out) //nolint:gosec // the draft just written
	if err != nil {
		return []string{"statement: " + err.Error()}
	}
	return checkStatement(string(data), s.Says, cs)
}

// Lines every statement draft says (PRD 15 and 9).
const notTested = "not tested: needs a person"

// disclaimer is the fixed text a draft may carry although it uses a word
// the never list holds (PRD 15 items 1 to 9 and the evidence note).
var disclaimer = []string{
	model.EvidenceNote,
	"Soundcheck's EAA mode gives you evidence about your site. It is not an audit, not a certification and not legal advice.",
	"Many success criteria need a person: meaningful alt text, captions, reading order, plain language. The draft marks them not tested: needs a person, never passed.",
	"Only the pages and flows Soundcheck visited, in the states it saw, in Chrome, with the keyboard walk at desktop width. Content in iframes from other sites, PDFs, videos, apps and emails was not tested.",
	"The keyboard walkthrough uses Chrome's keyboard events. It is not a screen reader test, not a voice control test and not a test with disabled users.",
	"The site decides what Soundcheck sees. A site that serves headless browsers a different page can pass while visitors fail. Overlay widgets don't make a site accessible.",
	"EAA mode presses keys on your pages. It holds requests that would change data, blocks downloads and follows no links except skip links. A site that changes data on a GET or on focus can still change. Run it on staging and list your live site under production.",
	"Reports contain what your pages showed. Soundcheck masks form values, credentials and tokens it recognises and emails, phone numbers and card numbers in text it writes. Masking is pattern-based: it is pseudonymised, not anonymised. Screenshots are not masked.",
	"A signed evidence pack proves who produced it and that it wasn't changed afterwards. It doesn't prove the site was in that state for every visitor.",
	"Nothing leaves your machine except requests to your own site and hosts you allow. No telemetry, no hosted service, no AI.",
}

// never are the words a draft may not use outside the disclaimer
// (PRD 9: it claims nothing).
var never = []string{"complian", "conform", "certif", "passes wcag", "fully accessible", "guarantee"}

var (
	spaces = regexp.MustCompile(`\s+`)
	// toDo is a [TO DO: …] marker for the owner. The one asking for the
	// status may offer the three statuses of the EU model statement.
	toDo     = regexp.MustCompile(`(?i)\[TO DO\b[^\]]*\]`)
	statuses = regexp.MustCompile(`(?i)\b(fully|partially|not) compliant\b`)
)

// checkStatement checks a draft's wording: it says the evidence note,
// "not tested: needs a person" and says, never uses a word of never
// outside the disclaimer and the owner's status choice and holds no
// canary.
func checkStatement(text string, says []string, cs []canary) []string {
	flat := spaces.ReplaceAllString(text, " ")
	var problems []string
	for _, want := range append([]string{model.EvidenceNote, notTested}, says...) {
		if !strings.Contains(flat, spaces.ReplaceAllString(want, " ")) {
			problems = append(problems, fmt.Sprintf("statement: doesn't say %q", short(want)))
		}
	}
	rest := flat
	for _, d := range disclaimer {
		rest = strings.ReplaceAll(rest, d, " ")
	}
	rest = toDo.ReplaceAllStringFunc(rest, func(m string) string { return statuses.ReplaceAllString(m, "") })
	// The snippet comes from lower, not rest: lowercasing can change the
	// byte length of non-ASCII text, so an index into one is not one into the other.
	lower := strings.ToLower(rest)
	for _, w := range never {
		if i := strings.Index(lower, w); i >= 0 {
			problems = append(problems, fmt.Sprintf("statement: says %q (…%s…)", w, around(lower, i, len(w))))
		}
	}
	for _, c := range cs {
		if strings.Contains(text, c.needle) {
			problems = append(problems, "statement: holds the "+c.name+" canary")
		}
	}
	return problems
}

// short cuts a long line for a message.
func short(s string) string {
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}

// around is the text near s[i:i+n], for a message.
func around(s string, i, n int) string {
	from, to := max(0, i-30), min(len(s), i+n+30)
	for from > 0 && !utf8Start(s[from]) {
		from--
	}
	for to < len(s) && !utf8Start(s[to]) {
		to++
	}
	return strings.TrimSpace(s[from:to])
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
