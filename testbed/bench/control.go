// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// ControlRound is one round of the control pass on one site: a snapshot,
// then two compares with nothing changed. Any change at or above NEW in
// those compares is a false alarm, since nothing broke.
type ControlRound struct {
	Site   string   `json:"site"`
	Round  int      `json:"round"`
	Pages  int      `json:"pages"`
	Alarms []string `json:"false_alarms,omitempty"`
	Error  string   `json:"error,omitempty"` // the round didn't finish
}

// controlPass runs o.control rounds on each control site with every
// scenario good, then on the page of every scenario marked control (with
// its own pages and flags). Catches alone say nothing about noise; this
// measures it.
func controlPass(ctx context.Context, o options, testbed string, m *Manifest, bin string) ([]ControlRound, error) {
	if err := Toggle(testbed, "all", "good"); err != nil {
		return nil, err
	}
	var targets []controlTarget
	for _, site := range m.Control.Sites {
		targets = append(targets, controlTarget{name: site, url: m.Sites[site], pages: pages{Limit: m.Control.Limit}})
	}
	for _, s := range m.Scenarios {
		if s.Control {
			targets = append(targets, controlTarget{name: s.ID, url: m.URL(s), pages: pages{Include: s.Include, Limit: s.Limit}, args: scenarioArgs(s, o.echo)})
		}
	}
	var rounds []ControlRound
	for _, t := range targets {
		for round := 1; round <= o.control; round++ {
			if ctx.Err() != nil {
				return rounds, ctx.Err()
			}
			fmt.Fprintf(os.Stderr, "control %s %d/%d\n", t.name, round, o.control)
			r := controlRound(ctx, o, bin, t, round)
			if r.Error != "" {
				fmt.Fprintf(os.Stderr, "    error: %s\n", r.Error)
			}
			for _, a := range r.Alarms {
				fmt.Fprintf(os.Stderr, "    false alarm: %s\n", a)
			}
			rounds = append(rounds, r)
		}
	}
	return rounds, nil
}

// controlTarget is what one series of control rounds checks: a whole
// site, or a scenario's pages.
type controlTarget struct {
	name  string // the site, or the scenario's ID
	url   string
	pages pages
	args  []string // extra flags for snapshot and compare
}

func controlRound(ctx context.Context, o options, bin string, t controlTarget, round int) ControlRound {
	r := ControlRound{Site: t.name, Round: round}
	wd := filepath.Join(o.work, "control", t.name, strconv.Itoa(round))
	if err := os.RemoveAll(wd); err != nil {
		r.Error = err.Error()
		return r
	}
	cfg := filepath.Join(wd, ".soundcheck", "config.yaml")
	if err := writeConfig(cfg, t.pages, nil); err != nil {
		r.Error = err.Error()
		return r
	}
	snap := append([]string{"snapshot", t.url, "--name", "control", "--json", "-o", "snapshot.json", "--config", cfg}, t.args...)
	if _, _, err := command(ctx, o.timeout, wd, os.Environ(), bin, snap...); err != nil {
		r.Error = "snapshot: " + err.Error()
		return r
	}
	for _, out := range []string{"compare-1.json", "compare-2.json"} {
		cmp := append([]string{"compare", "--against-snapshot", "control", "--json", "-o", out, "--config", cfg}, t.args...)
		_, _, err := command(ctx, o.timeout, wd, os.Environ(), bin, cmp...)
		got, _, rerr := readComparison(filepath.Join(wd, out))
		if rerr != nil {
			if err == nil {
				err = rerr
			}
			r.Error = "compare: " + err.Error()
			return r
		}
		r.Pages = max(r.Pages, got.Pages)
		r.Alarms = append(r.Alarms, FalseAlarms(got)...)
	}
	return r
}

// falseAlarmCount is the number of false alarms over all rounds.
func falseAlarmCount(rounds []ControlRound) int {
	n := 0
	for _, r := range rounds {
		n += len(r.Alarms)
	}
	return n
}

// tally is the score: scenarios with a merged detector and the pending
// ones, counted apart. Scenarios Soundcheck wasn't run on (they need Echo
// and -echo wasn't given) count in neither: notRun lists them.
type tally struct {
	caught, total, pendingCaught, pending int
	notRun                                []string
}

func score(outcomes []Outcome) tally {
	var t tally
	for _, o := range outcomes {
		ok := o.Soundcheck.Outcome == Caught
		if o.Soundcheck.Outcome == NotRun {
			t.notRun = append(t.notRun, o.Scenario.ID)
			continue
		}
		if o.Scenario.Pending.Is() {
			t.pending++
			if ok {
				t.pendingCaught++
			}
			continue
		}
		t.total++
		if ok {
			t.caught++
		}
	}
	return t
}
