// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
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
	// EAA says the round ran with --eaa (the EAA mode section).
	EAA bool `json:"eaa,omitempty"`
	// Keys are the issue keys of the round's snapshot, page path first:
	// two snapshots of an unchanged site must have the same ones.
	Keys []string `json:"issue_keys,omitempty"`
}

// controlPass runs o.control rounds on each control site with every
// scenario good. plain runs them as the default run does; eaa runs them
// again with --eaa and adds the page of every scenario marked control
// (with its own pages and flags). Catches alone say nothing about
// noise; this measures it.
func controlPass(ctx context.Context, o options, testbed string, m *Manifest, bin string, plain, eaa bool) ([]ControlRound, error) {
	if err := Toggle(testbed, "all", "good"); err != nil {
		return nil, err
	}
	var targets []controlTarget
	for _, site := range m.Control.Sites {
		if plain {
			targets = append(targets, controlTarget{name: site, dir: site, url: m.Sites[site], pages: pages{Limit: m.Control.Limit}})
		}
	}
	if eaa {
		for _, site := range m.Control.Sites {
			targets = append(targets, controlTarget{name: site, dir: "eaa-" + site, url: m.Sites[site], pages: pages{Limit: m.Control.Limit},
				args: []string{"--eaa"}, eaa: true})
		}
		for _, s := range m.Scenarios {
			if s.Control {
				targets = append(targets, controlTarget{name: s.ID, dir: s.ID, url: m.URL(s), pages: pages{Include: s.Include, Limit: s.Limit},
					args: scenarioArgs(s, o.echo), eaa: true})
			}
		}
	}
	var rounds []ControlRound
	for _, t := range targets {
		for round := 1; round <= o.control; round++ {
			if ctx.Err() != nil {
				return rounds, ctx.Err()
			}
			fmt.Fprintf(os.Stderr, "control %s %d/%d\n", t.dir, round, o.control)
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
	dir   string // its folder under work/control
	url   string
	pages pages
	args  []string // extra flags for snapshot and compare
	eaa   bool     // the rounds run with --eaa
}

func controlRound(ctx context.Context, o options, bin string, t controlTarget, round int) ControlRound {
	r := ControlRound{Site: t.name, Round: round, EAA: t.eaa}
	wd := filepath.Join(o.work, "control", t.dir, strconv.Itoa(round))
	if err := os.RemoveAll(wd); err != nil {
		r.Error = err.Error()
		return r
	}
	cfg := filepath.Join(wd, ".soundcheck", "config.yaml")
	if err := writeConfig(cfg, t.pages, nil, nil); err != nil {
		r.Error = err.Error()
		return r
	}
	snap := append([]string{"snapshot", t.url, "--name", "control", "--json", "-o", "snapshot.json", "--config", cfg}, t.args...)
	if _, _, err := command(ctx, o.timeout, wd, os.Environ(), bin, snap...); err != nil {
		r.Error = "snapshot: " + err.Error()
		return r
	}
	if run, err := store.LoadRun(filepath.Join(wd, ".soundcheck", "snapshots", "control")); err == nil {
		r.Keys = issueKeys(run)
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

// issueKeys lists a run's issues by page path and issue key, sorted.
func issueKeys(run *model.Run) []string {
	key := func(i model.Issue) string {
		if i.Key != "" {
			return i.Key
		}
		return model.IssueKey(i.Check, i.Title, i.Source)
	}
	var keys []string
	for _, i := range run.Issues {
		keys = append(keys, "site "+key(i))
	}
	for _, p := range run.Pages {
		for _, i := range p.Issues {
			keys = append(keys, p.Path+" "+key(i))
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
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
