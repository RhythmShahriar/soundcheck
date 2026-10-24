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
	"snapshots", "ls", "init", "flows", "update", "demo", "import", "accept", "review", "history", "fleet", "wp-update", "rules"}

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
	takes := map[string][]string{"rules": {"list", "test"}, "flows": {"init"}}
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
	for _, args := range [][]string{{"rules", "lst"}, {"acept", "3f2a9c01"}, {"flows"}, {"fleet", "sites.yaml", "snap"}} {
		if commandShape(args) == "" {
			t.Errorf("%v passed", args)
		}
	}
	for _, args := range [][]string{{"rules", "list"}, {"staging.site.com", "--against", "site.com"}, {"fleet", "sites.yaml", "compare"}, {"--version"}} {
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
