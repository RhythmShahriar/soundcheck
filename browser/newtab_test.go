// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
)

// A window that never answers is given up after newTabWait and a second
// one is opened. Other errors come back at once.
func TestNewTabGivesUpOnAStuckWindow(t *testing.T) {
	oldOpen, oldWait := openTab, newTabWait
	t.Cleanup(func() { openTab, newTabWait = oldOpen, oldWait })
	newTabWait = 50 * time.Millisecond

	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	openTab = func(*rod.Browser) (*rod.Page, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			<-release // the stuck first window
			return nil, errors.New("closed")
		}
		return nil, nil
	}
	if _, err := NewTab(nil); err != nil {
		t.Errorf("second try: %v", err)
	}
	mu.Lock()
	if calls != 2 {
		t.Errorf("%d tries, want 2", calls)
	}
	mu.Unlock()

	// Both stuck: the error says so.
	openTab = func(*rod.Browser) (*rod.Page, error) {
		<-release
		return nil, nil
	}
	if _, err := NewTab(nil); !errors.Is(err, errTabStuck) {
		t.Errorf("both stuck: %v", err)
	}

	// Any other error isn't tried again.
	mu.Lock()
	calls = 0
	mu.Unlock()
	boom := errors.New("boom")
	openTab = func(*rod.Browser) (*rod.Page, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, boom
	}
	_, err := NewTab(nil)
	mu.Lock()
	defer mu.Unlock()
	if !errors.Is(err, boom) || calls != 1 {
		t.Errorf("error %v after %d tries", err, calls)
	}
}
