// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// readmeCommands returns every rhythm-soundcheck command line README.md
// shows, in code blocks and inline code, without trailing comments.
// Templates ("<url>", "snapshot|compare") are left out.
func readmeCommands(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	add := func(line string) {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if (line == "rhythm-soundcheck" || strings.HasPrefix(line, "rhythm-soundcheck ")) && !strings.ContainsAny(line, "<|") && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	fenced := false
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			add(line)
			continue
		}
		for _, m := range inlineCode.FindAllStringSubmatch(line, -1) {
			add(m[1])
		}
	}
	return out
}

var inlineCode = regexp.MustCompile("`([^`]+)`")

// shellWords splits a command line the way a shell would for these
// examples: on spaces, with '...' and "..." kept as one word.
func shellWords(line string) []string {
	var words []string
	var cur strings.Builder
	quote, in := rune(0), false
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == ' ':
			if in || cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if in || cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

// Every command README.md shows names a real command with flags it
// takes: with --help added it parses and prints that command's help
// (exit 0), where a mistyped flag or command exits 2.
func TestREADMECommandsParse(t *testing.T) {
	cmds := readmeCommands(t)
	t.Chdir(t.TempDir())
	if len(cmds) < 20 {
		t.Fatalf("found only %d commands in README.md: %q", len(cmds), cmds)
	}
	for _, line := range cmds {
		args := shellWords(line)[1:]
		if i := slices.Index(args, "--"); i >= 0 {
			args = slices.Insert(args, i, "--help")
		} else {
			args = append(args, "--help")
		}
		if err := commandShape(args); err != "" {
			t.Errorf("%s: %s", line, err)
			continue
		}
		code, out, errOut := runCLI(t, args...)
		// --help prints the command's usage; --version and --list-checks
		// print what they're for.
		if low := strings.ToLower(out); code != exitOK || !strings.Contains(low, "usage") && !strings.Contains(out, version) && !strings.HasPrefix(out, "health:") {
			t.Errorf("%s: exit %d\n%s\n%s", line, code, out, errOut)
		}
	}
}

// commands are the words run takes as a command; the rest is a site.
var commands = []string{"help", "list-checks", "version", "snapshot", "snap", "compare", "cmp", "run", "check", "report",
	"snapshots", "ls", "init", "flows", "update", "demo", "import", "accept", "review", "history", "fleet", "wp-update", "rules",
	"statement", "evidence"}

// commandShape is what --help can't tell about args: that the first word
// is a command (or a site, or a flag) and that a command's own words are
// ones it takes. It returns "" when args are fine.
func commandShape(args []string) string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return ""
	}
	if !slices.Contains(commands, args[0]) {
		if looksLikeTarget(args[0]) && strings.Contains(args[0], ".") {
			return ""
		}
		return fmt.Sprintf("%q is neither a command nor a site", args[0])
	}
	pos := slices.DeleteFunc(slices.Clone(args[1:]), func(a string) bool { return strings.HasPrefix(a, "-") })
	takes := map[string][]string{"rules": {"list", "test"}, "flows": {"init"}, "evidence": {"verify", "sign", "keygen"}}
	if words, ok := takes[args[0]]; ok && (len(pos) == 0 || !slices.Contains(words, pos[0])) {
		return fmt.Sprintf("%s takes %s", args[0], strings.Join(words, " or "))
	}
	if args[0] == "fleet" && (len(pos) < 2 || !slices.Contains([]string{"snapshot", "compare"}, pos[1])) {
		return "fleet takes <sites.yaml> snapshot or compare"
	}
	return ""
}

// The checks above catch a README line that went stale.
func TestStaleREADMECommandFails(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, out, _ := runCLI(t, "compare", "--modulez", "+a11y", "--help"); code == exitOK {
		t.Errorf("a mistyped flag passed:\n%s", out)
	}
	for _, args := range [][]string{{"rules", "lst"}, {"acept", "3f2a9c01"}, {"flows"}, {"fleet", "sites.yaml", "snap"}, {"evidence", "check"}} {
		if commandShape(args) == "" {
			t.Errorf("%v passed", args)
		}
	}
	for _, args := range [][]string{{"rules", "list"}, {"staging.site.com", "--against", "site.com"}, {"fleet", "sites.yaml", "compare"}, {"--version"}, {"evidence", "sign", "--key", "k"}} {
		if err := commandShape(args); err != "" {
			t.Errorf("%v: %s", args, err)
		}
	}
}

func TestShellWords(t *testing.T) {
	got := shellWords(`a --page '/team/*' --wp "wp --path=/srv/www" ''`)
	want := []string{"a", "--page", "/team/*", "--wp", "wp --path=/srv/www", ""}
	if !slices.Equal(got, want) {
		t.Errorf("%q", got)
	}
}

// readmeEAA are the sentences README.md's EAA section must keep word for
// word: the evidence note, the honest limits of PRD 15 (1 to 9) and the
// first command. A rewrite that drops or softens one fails here.
var readmeEAA = []string{
	model.EvidenceNote,
	"rhythm soundcheck snapshot <url> --eaa",
	"Soundcheck's EAA mode gives you evidence about your site. It is not an audit, not a certification and not legal advice.",
	"Many success criteria need a person: meaningful alt text, captions, reading order, plain language. The draft marks them not tested: needs a person, never passed.",
	"Only the pages and flows Soundcheck visited, in the states it saw, in Chrome, with the keyboard walk at desktop width. Content in iframes from other sites, PDFs, videos, apps and emails was not tested.",
	"The keyboard walkthrough uses Chrome's keyboard events. It is not a screen reader test, not a voice control test and not a test with disabled users.",
	"The site decides what Soundcheck sees. A site that serves headless browsers a different page can pass while visitors fail. Overlay widgets don't make a site accessible.",
	"EAA mode presses keys on your pages. It holds requests that would change data, blocks downloads and follows no links except skip links. A site that changes data on a GET or on focus can still change. Run it on staging and list your live site under production.",
	"Masking is pattern-based: it is pseudonymised, not anonymised. Screenshots are not masked.",
	"pseudonymised, not anonymised",
	"A signed evidence pack proves who produced it and that it wasn't changed afterwards.",
	"It doesn't prove the site was in that state for every visitor.",
	"Nothing leaves your machine except requests to your own site and hosts you allow. No telemetry, no hosted service, no AI.",
	"docs/benchmark.md#eaa-mode",
}

func TestREADMEEAA(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if missing := readmeMissing(string(data), readmeEAA); len(missing) > 0 {
		t.Errorf("README.md lost:\n%s", strings.Join(missing, "\n"))
	}
	// The table no longer sends people to --modules +a11y for the EAA.
	if strings.Contains(string(data), "--modules +a11y,seo") {
		t.Error("README.md still suggests --modules +a11y,seo")
	}
}

// readmeMissing returns the sentences of want that text doesn't hold.
func readmeMissing(text string, want []string) []string {
	var out []string
	for _, w := range want {
		if !strings.Contains(text, w) {
			out = append(out, w)
		}
	}
	return out
}

// The check above notices a sentence that went missing or was changed.
func TestREADMEEAAMissing(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), "pseudonymised, not anonymised", "anonymised", 1)
	text = strings.Replace(text, model.EvidenceNote, "Soundcheck makes your site compliant.", 1)
	got := readmeMissing(text, readmeEAA)
	if !slices.Contains(got, model.EvidenceNote) || !slices.Contains(got, "pseudonymised, not anonymised") {
		t.Errorf("missing %q", got)
	}
	if got := readmeMissing(string(data), readmeEAA); len(got) != 0 {
		t.Errorf("README.md: missing %q", got)
	}
}

// The browser tests of this package run one after another (they change
// the working directory) and take about 5 minutes under -race, so
// CONTRIBUTING.md and `make test` give go test a timeout past Go's 10
// minute default, which -count=2 would pass.
func TestTestTimeoutDocumented(t *testing.T) {
	timeout := regexp.MustCompile(`go test -race -timeout (\d+)m \./\.\.\.`)
	for _, name := range []string{"../../CONTRIBUTING.md", "../../Makefile"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		m := timeout.FindSubmatch(data)
		if m == nil {
			t.Errorf("%s gives no go test -race -timeout", name)
			continue
		}
		var mins int
		if _, err := fmt.Sscan(string(m[1]), &mins); err != nil || mins < 20 {
			t.Errorf("%s gives go test %sm, want at least 20m", name, m[1])
		}
	}
	if data, _ := os.ReadFile("../../CONTRIBUTING.md"); !strings.Contains(string(data), "-count=2") {
		t.Error("CONTRIBUTING.md doesn't say what -count does to the timeout")
	}
}
