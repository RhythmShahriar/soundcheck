// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The wp-update scenarios (harness wp-update) run the binary's wp-update
// against the testbed's ten testbed-update-* plugins, of which one 1.1
// breaks the contact page. They score whether it names that plugin and in
// how many compares. WP-CLI runs in the compose stack's cli container,
// since the WordPress image has none.

// Culprit is a plugin wp-update blamed for a break.
type Culprit struct {
	Slug     string `json:"slug"`
	Old      string `json:"old"`
	New      string `json:"new"`
	Evidence string `json:"evidence,omitempty"`
	// Compares is how many compares it took to find: bisect counts from
	// the compare that saw the break, one by one counts every compare up
	// to and including the plugin's own.
	Compares int `json:"compares"`
}

func (c Culprit) String() string {
	s := fmt.Sprintf("%s %s → %s", c.Slug, c.Old, c.New)
	if c.Evidence != "" {
		s += " (" + c.Evidence + ")"
	}
	return s + fmt.Sprintf(", found in %d %s", c.Compares, plural(c.Compares, "compare"))
}

func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// The progress lines wp-update prints (package wpupdate):
//
//	Culprit: p07 1.0 → 1.1 (<evidence>), found in 4 compares   (bisect)
//	[7/10] p07 1.0 → 1.1: broke: <evidence>                    (one by one)
//
// A one-by-one line names a bad update only when it carries evidence; a
// good one ends at the verdict ("[3/10] p03 1.0 → 1.1: clean").
var (
	bisectLine   = regexp.MustCompile(`^Culprit: (\S+) (\S+) → (\S+) \((.*)\), found in (\d+) compares?$`)
	oneByOneLine = regexp.MustCompile(`^\[(\d+)/\d+\] (\S+) (\S+) → (\S+): (broke|new|changed|slower): (.+)$`)
)

// parseWPUpdate reads the culprits from wp-update's output.
func parseWPUpdate(out string) []Culprit {
	var found []Culprit
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		if m := bisectLine.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[5])
			found = append(found, Culprit{Slug: m[1], Old: m[2], New: m[3], Evidence: m[4], Compares: n})
		} else if m := oneByOneLine.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			found = append(found, Culprit{Slug: m[2], Old: m[3], New: m[4], Evidence: m[6], Compares: n})
		}
	}
	return found
}

// EvaluateWPUpdate scores a wp-update run against the scenario: caught
// when it names the expected culprit within the compare budget, a false
// alarm when it blames a plugin the scenario doesn't expect.
func EvaluateWPUpdate(s Scenario, culprits []Culprit, exit int) Result {
	var want []Expect
	for _, e := range s.Expect {
		if e.kind() == kindCulprit {
			want = append(want, e)
		}
	}
	var r Result
	for _, c := range culprits {
		i := slices.IndexFunc(want, func(e Expect) bool { return e.Culprit == c.Slug })
		switch {
		case i < 0:
			return Result{Outcome: FalseAlarm, Found: "blamed " + c.String()}
		case want[i].Compares > 0 && c.Compares > want[i].Compares:
			r = Result{Outcome: Missed, Found: fmt.Sprintf("%s; want at most %d", c, want[i].Compares)}
		case r.Outcome != Caught:
			r = Result{Outcome: Caught, Found: c.String()}
		}
	}
	if r.Outcome == "" {
		r = Result{Outcome: Missed, Detail: fmt.Sprintf("wp-update named no culprit (exit %d)", exit)}
	}
	return r
}

// wpUpdateScenario runs one wp-update scenario: every plugin back at 1.0,
// then wp-update with the scenario's args (--bisect or --one-by-one…),
// then back at 1.0 again for the scenarios after it.
func wpUpdateScenario(ctx context.Context, o options, m *Manifest, s Scenario, wd, bin string, binErr error) Result {
	if binErr != nil {
		return Result{Outcome: NotRun, Detail: binErr.Error()}
	}
	// Before wp-update is wired, the binary answers any unknown command
	// with its general help, which doesn't mention wp-update.
	if help, _, _ := command(ctx, o.timeout, wd, nil, bin, "wp-update", "--help"); !strings.Contains(help, "wp-update") {
		return Result{Outcome: NotRun, Detail: "this rhythm-soundcheck has no wp-update command yet"}
	}
	reset := append(slices.Clone(o.compose), "run", "--rm", "--no-deps", "-T", "--entrypoint", "sh", "cli", "/testbed-updates/reset.sh")
	if _, _, err := command(ctx, o.timeout, "", nil, reset[0], reset[1:]...); err != nil {
		return Result{Outcome: Failed, Detail: "reset: " + err.Error()}
	}
	defer func() { _, _, _ = command(context.WithoutCancel(ctx), o.timeout, "", nil, reset[0], reset[1:]...) }()

	cfgPath := filepath.Join(wd, ".soundcheck", "config.yaml")
	if err := writeConfig(cfgPath, pages{Include: s.Include, Limit: s.Limit, PerGroup: s.PerGroup}, s.Levels, nil); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	wp := append(slices.Clone(o.compose), "run", "--rm", "--no-deps", "-T", "--entrypoint", "wp", "cli")
	args := append([]string{"wp-update", m.URL(s), "--wp", shellJoin(wp), "--config", cfgPath}, scenarioArgs(s, o.echo)...)
	// A loop is a snapshot and up to one compare per plugin.
	out, code, err := command(ctx, 4*o.timeout, wd, env(s), bin, args...)
	if err != nil && code < 0 {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	return EvaluateWPUpdate(s, parseWPUpdate(out), code)
}

// shellJoin quotes argv for wp-update's --wp, which splits it the way a
// POSIX shell would, so a testbed path with spaces stays one argument.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && !strings.ContainsFunc(a, func(r rune) bool {
			return !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@", r)
		}) {
			quoted[i] = a
			continue
		}
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
