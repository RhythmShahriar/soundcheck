// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// cancelSite stands in for a site-wide module: it sends the process
// Ctrl+C while its CheckSite runs, after every page finished.
type cancelSite struct{ ran *atomic.Bool }

func (cancelSite) Name() string { return "cache" }

func (c cancelSite) CheckSite(ctx context.Context, _ *check.Context) {
	c.ran.Store(true)
	_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	select {
	case <-ctx.Done():
	case <-time.After(20 * time.Second):
	}
}

// Ctrl+C during the site-wide phase leaves a partial result: the
// comparison is interrupted, the history doesn't get it and report of
// it exits 130.
func TestCancelDuringSiteChecks(t *testing.T) {
	needBrowser(t)
	t.Chdir(t.TempDir())
	var broken atomic.Bool
	srv := shop(&broken)
	defer srv.Close()
	common := []string{"--no-download", "--viewport", "desktop", "--modules", "health,cache", "--rate", "50", "--plain"}
	if code, out, errOut := runCLI(t, append([]string{"snapshot", srv.URL, "--name", "good"}, common...)...); code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	history := func() int {
		data, _ := os.ReadFile(".soundcheck/history.jsonl")
		return strings.Count(string(data), "\n")
	}
	before := history()

	var ran atomic.Bool
	saved := builders["cache"]
	builders["cache"] = func(*plan) check.Module { return cancelSite{&ran} }
	defer func() { builders["cache"] = saved }()
	broken.Store(true)
	code, out, errOut := runCLI(t, append([]string{"compare", "--confirm=off", "--json"}, common...)...)
	builders["cache"] = saved
	if !ran.Load() {
		t.Fatalf("CheckSite never ran: exit %d\n%s", code, errOut)
	}
	if code != exitCanceled {
		t.Fatalf("compare: exit %d, want 130\n%s", code, errOut)
	}
	var c model.Comparison
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatal(err, out)
	}
	if !c.Interrupted || c.Summary.Verdict != model.VerdictInterrupted {
		t.Errorf("interrupted %v, summary %+v", c.Interrupted, c.Summary)
	}
	if n := history(); n != before {
		t.Errorf("history grew from %d to %d entries", before, n)
	}
	if code, _, errOut := runCLI(t, "report", "--fail-on", "slower", "--plain"); code != exitCanceled ||
		!strings.Contains(errOut, "this result is partial (interrupted)") {
		t.Errorf("report: exit %d %q", code, errOut)
	}
}
