// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/capture/drivertest"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
)

// launchFirefox starts Firefox, or skips when none is installed.
func launchFirefox(t *testing.T) *Browser {
	t.Helper()
	if _, err := FindFirefox(""); err != nil {
		t.Skip(err)
	}
	b, err := LaunchFirefox(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func TestFirefoxContract(t *testing.T) {
	drivertest.Run(t, func(t *testing.T) capture.Driver {
		b := launchFirefox(t)
		if b.Engine() != capture.Firefox || !b.BiDi() {
			t.Errorf("Engine = %q, BiDi = %v", b.Engine(), b.BiDi())
		}
		return b
	}, drivertest.Options{})
}

func TestWebKitContract(t *testing.T) {
	if _, err := FindWebKit(""); err != nil {
		t.Skip(err)
	}
	b, err := LaunchWebKit(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	classic := !b.BiDi()
	_ = b.Close()
	drivertest.Run(t, func(t *testing.T) capture.Driver {
		b, err := LaunchWebKit(context.Background(), Options{})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}, drivertest.Options{Classic: classic})
}

// NewTab finds the window a failed create opened among the browser's
// top-level contexts: each tab it opens is one of them, in the run's
// user context.
func TestFirefoxTopContextsListTheRunsTabs(t *testing.T) {
	b := launchFirefox(t)
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(30*time.Second))
	defer cancel()
	before, err := b.topContexts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := b.NewTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	id := opened.(*tab).id
	if before[id] {
		t.Fatalf("the new tab %s was there before: %v", id, before)
	}
	if got := b.adoptNew(ctx, before); got != id {
		t.Errorf("adoptNew = %q, want the new tab %q", got, id)
	}
}
