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
// scenario good. Catches alone say nothing about noise; this measures it.
func controlPass(ctx context.Context, o options, testbed string, m *Manifest, bin string) ([]ControlRound, error) {
	if err := Toggle(testbed, "all", "good"); err != nil {
		return nil, err
	}
	var rounds []ControlRound
	for _, site := range m.Control.Sites {
		for round := 1; round <= o.control; round++ {
			if ctx.Err() != nil {
				return rounds, ctx.Err()
			}
			fmt.Fprintf(os.Stderr, "control %s %d/%d\n", site, round, o.control)
			r := controlRound(ctx, o, m, bin, site, round)
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

func controlRound(ctx context.Context, o options, m *Manifest, bin, site string, round int) ControlRound {
	r := ControlRound{Site: site, Round: round}
	wd := filepath.Join(o.work, "control", site, strconv.Itoa(round))
	if err := os.RemoveAll(wd); err != nil {
		r.Error = err.Error()
		return r
	}
	cfg := filepath.Join(wd, ".soundcheck", "config.yaml")
	if err := writeConfig(cfg, pages{Limit: m.Control.Limit}, nil); err != nil {
		r.Error = err.Error()
		return r
	}
	url := m.Sites[site]
	if _, _, err := command(ctx, o.timeout, wd, os.Environ(), bin, "snapshot", url, "--name", "control", "--json", "-o", "snapshot.json", "--config", cfg); err != nil {
		r.Error = "snapshot: " + err.Error()
		return r
	}
	for _, out := range []string{"compare-1.json", "compare-2.json"} {
		_, _, err := command(ctx, o.timeout, wd, os.Environ(), bin, "compare", "--against-snapshot", "control", "--json", "-o", out, "--config", cfg)
		cmp, _, rerr := readComparison(filepath.Join(wd, out))
		if rerr != nil {
			if err == nil {
				err = rerr
			}
			r.Error = "compare: " + err.Error()
			return r
		}
		r.Pages = max(r.Pages, cmp.Pages)
		r.Alarms = append(r.Alarms, FalseAlarms(cmp)...)
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
// ones, counted apart.
type tally struct{ caught, total, pendingCaught, pending int }

func score(outcomes []Outcome) tally {
	var t tally
	for _, o := range outcomes {
		ok := o.Soundcheck.Outcome == Caught
		if o.Scenario.Pending {
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
