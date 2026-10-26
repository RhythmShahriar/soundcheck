// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"context"
	"errors"
	"testing"

	"github.com/go-rod/rod"
)

func TestGuardTab(t *testing.T) {
	// Without a guard nothing happens.
	release, err := GuardTab(context.Background(), nil)
	if err != nil || release == nil {
		t.Fatalf("no guard: %v", err)
	}
	release()

	var guarded, released int
	ctx := WithTabGuard(context.Background(), func(*rod.Page) (func(), error) {
		guarded++
		return func() { released++ }, nil
	})
	release, err = GuardTab(ctx, &rod.Page{})
	if err != nil {
		t.Fatal(err)
	}
	release()
	if guarded != 1 || released != 1 {
		t.Errorf("guarded %d, released %d", guarded, released)
	}

	// A guard that fails says so.
	ctx = WithTabGuard(context.Background(), func(*rod.Page) (func(), error) { return nil, errors.New("no Fetch") })
	if _, err := GuardTab(ctx, &rod.Page{}); err == nil {
		t.Error("a failed guard returned no error")
	}
}
