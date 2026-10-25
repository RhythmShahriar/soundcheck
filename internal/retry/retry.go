// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package retry runs a browser's start-up step again when it failed for
// a reason a later try can get past: a browser that was slow to start on
// a busy machine, or a driver that dropped the request while the last
// session's browser was still shutting down.
package retry

import (
	"context"
	"time"
)

// Policy says how often a step is tried and how long to wait in between.
type Policy struct {
	// Tries is how many times the step runs at most; below 1 means 1.
	Tries int
	// Pause is the wait after the first failure. It doubles after each
	// further one, so a machine that is busy for a while gets more room.
	Pause time.Duration
	// Again reports whether err may go away on a later try. Nil means
	// every error does.
	Again func(err error) bool
	// Note, when set, hears about each failure that is tried again, with
	// the number of the try that failed.
	Note func(try int, err error)
}

// Do runs fn until it succeeds, fails with an error p.Again rejects or
// uses up p.Tries runs. It returns fn's last error. It stops early
// with ctx's error when ctx ends during a pause; fn should watch ctx
// itself.
func Do(ctx context.Context, p Policy, fn func() error) error {
	pause := p.Pause
	for try := 1; ; try++ {
		err := fn()
		if err == nil || try >= p.Tries || ctx.Err() != nil || (p.Again != nil && !p.Again(err)) {
			return err
		}
		if p.Note != nil {
			p.Note(try, err)
		}
		t := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		pause *= 2
	}
}
