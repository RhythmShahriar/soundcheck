// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errBusy = errors.New("busy")

// A step that fails a few times and then works is tried until it works,
// with each pause twice the last.
func TestDoTriesAgainWithBackoff(t *testing.T) {
	calls, at := 0, []time.Time{}
	var notes []int
	err := Do(context.Background(), Policy{Tries: 4, Pause: 20 * time.Millisecond,
		Note: func(try int, err error) {
			if !errors.Is(err, errBusy) {
				t.Errorf("note for %v", err)
			}
			notes = append(notes, try)
		}}, func() error {
		calls++
		at = append(at, time.Now())
		if calls < 3 {
			return errBusy
		}
		return nil
	})
	if err != nil || calls != 3 || len(notes) != 2 || notes[0] != 1 || notes[1] != 2 {
		t.Fatalf("err %v, %d calls, notes %v", err, calls, notes)
	}
	first, second := at[1].Sub(at[0]), at[2].Sub(at[1])
	if first < 20*time.Millisecond || second < 40*time.Millisecond {
		t.Errorf("pauses %s and %s, want at least 20 ms and then 40 ms", first, second)
	}
}

// A step that keeps failing runs Tries times and its last error is
// returned; an error Again rejects isn't tried again at all.
func TestDoGivesUp(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Policy{Tries: 3, Pause: time.Millisecond}, func() error { calls++; return errBusy })
	if !errors.Is(err, errBusy) || calls != 3 {
		t.Errorf("err %v after %d calls, want busy after 3", err, calls)
	}

	fatal := errors.New("no such browser")
	calls = 0
	err = Do(context.Background(), Policy{Tries: 3, Pause: time.Millisecond, Again: func(err error) bool { return errors.Is(err, errBusy) }},
		func() error { calls++; return fatal })
	if !errors.Is(err, fatal) || calls != 1 {
		t.Errorf("err %v after %d calls, want the fatal error after 1", err, calls)
	}

	calls = 0
	if err := Do(context.Background(), Policy{}, func() error { calls++; return errBusy }); calls != 1 || err == nil {
		t.Errorf("zero policy: %d calls, %v", calls, err)
	}
}

// A context that ends during a pause stops the tries.
func TestDoStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	err := Do(ctx, Policy{Tries: 5, Pause: time.Minute}, func() error { calls++; return errBusy })
	if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(start) > 10*time.Second {
		t.Errorf("err %v after %d calls in %s", err, calls, time.Since(start))
	}
}
