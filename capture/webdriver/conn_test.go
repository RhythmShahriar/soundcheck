// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// noAnswer is what a fakeBiDi table returns for a command it leaves
// unanswered.
var noAnswer = &struct{}{}

// fakeBiDi answers commands from a table and sends an event after each
// command named "emit".
func fakeBiDi(t *testing.T, answer func(method string, params json.RawMessage) (any, *Error)) string {
	t.Helper()
	srv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		var mu sync.Mutex
		send := func(v any) {
			mu.Lock()
			defer mu.Unlock()
			_ = websocket.JSON.Send(ws, v)
		}
		for {
			var cmd struct {
				ID     int64           `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := websocket.JSON.Receive(ws, &cmd); err != nil {
				return
			}
			if cmd.Method == "emit" {
				send(map[string]any{"type": "event", "method": "log.entryAdded", "params": map[string]any{"text": "hi"}})
			}
			res, e := answer(cmd.Method, cmd.Params)
			if res == any(noAnswer) {
				continue
			}
			if e != nil {
				send(map[string]any{"type": "error", "id": cmd.ID, "error": e.Code, "message": e.Message})
				continue
			}
			send(map[string]any{"type": "success", "id": cmd.ID, "result": res})
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestConn(t *testing.T) {
	url := fakeBiDi(t, func(method string, params json.RawMessage) (any, *Error) {
		switch method {
		case "echo", "emit":
			return map[string]json.RawMessage{"got": params}, nil
		case "missing":
			return nil, &Error{Code: "unknown command", Message: "missing isn't a command"}
		case "slow":
			time.Sleep(time.Second)
			return struct{}{}, nil
		}
		return nil, &Error{Code: "invalid argument"}
	})
	ctx := context.Background()
	c, err := Dial(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	var mu sync.Mutex
	stop := c.On(func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	// Replies reach the command that sent them, in parallel.
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			var out struct {
				Got struct{ N int } `json:"got"`
			}
			if err := c.Call(ctx, "echo", map[string]int{"N": i}, &out); err != nil || out.Got.N != i {
				t.Errorf("echo %d = %d, %v", i, out.Got.N, err)
			}
		})
	}
	wg.Wait()

	err = c.Call(ctx, "missing", nil, nil)
	var e *Error
	if !errors.As(err, &e) || e.Code != "unknown command" || !Unsupported(err) {
		t.Errorf("missing = %v", err)
	}
	if err := c.Call(ctx, "other", nil, nil); err == nil || Unsupported(err) {
		t.Errorf("other = %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := c.Call(short, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("slow = %v", err)
	}

	// An event handler may send commands: the reader never waits on it.
	if err := c.Call(ctx, "emit", nil, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	if len(events) != 1 || events[0].Method != "log.entryAdded" {
		t.Errorf("events = %+v", events)
	}
	mu.Unlock()
	stop()

	_ = c.Close()
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Error("Done isn't closed after Close")
	}
	if err := c.Call(ctx, "echo", nil, nil); err == nil {
		t.Error("Call after Close succeeded")
	}
}

// A handler that sends a command from inside an event gets its reply.
func TestConnCallFromEvent(t *testing.T) {
	url := fakeBiDi(t, func(string, json.RawMessage) (any, *Error) { return struct{}{}, nil })
	c, err := Dial(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	done := make(chan error, 1)
	c.On(func(Event) { done <- c.Call(context.Background(), "inner", nil, nil) })
	if err := c.Call(context.Background(), "emit", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the command sent from an event never got its reply")
	}
}

// A Firefox that has just started may answer browsingContext.create with
// "no such frame" while it swaps out its first window: NewTab tries again.
func TestNewTabRetriesADiscardedContext(t *testing.T) {
	pause := newTabPause
	newTabPause = time.Millisecond
	t.Cleanup(func() { newTabPause = pause })
	for _, tc := range []struct {
		name  string
		fails int
		ok    bool
	}{{"recovers", 2, true}, {"gives up", 10, false}} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			creates := 0
			var lastType string
			url := fakeBiDi(t, func(method string, params json.RawMessage) (any, *Error) {
				if method != "browsingContext.create" {
					return map[string]string{"subscription": "s"}, nil
				}
				mu.Lock()
				defer mu.Unlock()
				creates++
				var p struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(params, &p)
				lastType = p.Type
				if creates <= tc.fails {
					return nil, &Error{Code: "no such frame", Message: "DiscardedBrowsingContextError: BrowsingContext does no longer exist"}
				}
				return map[string]string{"context": "ctx-1"}, nil
			})
			c, err := Dial(context.Background(), url)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			b := &Browser{conn: c}
			_, err = b.NewTab(context.Background())
			mu.Lock()
			defer mu.Unlock()
			if tc.ok && (err != nil || creates != 3) {
				t.Errorf("NewTab: %v after %d tries", err, creates)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), "no such frame") || creates != newTabTries || lastType != "tab") {
				t.Errorf("NewTab: %v after %d tries (last a %q), want the error after %d, the last a tab", err, creates, lastType, newTabTries)
			}
			if tc.ok && lastType != "window" {
				t.Errorf("a window that opened in time is a %q", lastType)
			}
		})
	}
}

// A create that fails with "no such frame" can still open its window.
// NewTab takes the one that wasn't there before instead of opening
// more. It closes any other a failed try left behind.
func TestNewTabTakesTheWindowAFailedCreateOpened(t *testing.T) {
	pause := newTabPause
	newTabPause = time.Millisecond
	t.Cleanup(func() { newTabPause = pause })
	for _, tc := range []struct {
		name    string
		opened  []string // the windows the failed create leaves open
		want    string
		creates int
		closed  string
	}{
		{"its window", []string{"a"}, "a", 1, ""},
		{"two windows", []string{"b", "a"}, "a", 1, "b"},
		{"no window", nil, "made", 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			type info struct {
				Context     string `json:"context"`
				UserContext string `json:"userContext"`
			}
			// The first window, another user context's and one in the
			// run's that was open before.
			tree := []info{{"first", "default"}, {"other", "uc-other"}, {"old", "uc-1"}}
			creates := 0
			var closed []string
			url := fakeBiDi(t, func(method string, params json.RawMessage) (any, *Error) {
				mu.Lock()
				defer mu.Unlock()
				switch method {
				case "browsingContext.getTree":
					return map[string]any{"contexts": tree}, nil
				case "browsingContext.close":
					var p struct {
						Context string `json:"context"`
					}
					_ = json.Unmarshal(params, &p)
					closed = append(closed, p.Context)
					return map[string]any{}, nil
				case "browsingContext.create":
					creates++
					if creates == 1 {
						for _, id := range tc.opened {
							tree = append(tree, info{id, "uc-1"})
						}
						return nil, &Error{Code: "no such frame", Message: "DiscardedBrowsingContextError: BrowsingContext does no longer exist"}
					}
					tree = append(tree, info{"made", "uc-1"})
					return map[string]string{"context": "made"}, nil
				}
				return map[string]string{"subscription": "s"}, nil
			})
			c, err := Dial(context.Background(), url)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			b := &Browser{conn: c, userContext: "uc-1"}
			got, err := b.NewTab(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if id := got.(*tab).id; id != tc.want || creates != tc.creates || strings.Join(closed, ",") != tc.closed {
				t.Errorf("NewTab = %q after %d creates, closed %v; want %q after %d, closed %q", id, creates, closed, tc.want, tc.creates, tc.closed)
			}
		})
	}
}

// NewTab opens its window without the focus, so Firefox doesn't wait
// 100 ms at most for the page to show, then waits itself: through "no
// such frame" and a hidden page, until the page is visible.
func TestNewTabWaitsForThePageToShow(t *testing.T) {
	pause := shownPause
	shownPause = time.Millisecond
	t.Cleanup(func() { shownPause = pause })
	var mu sync.Mutex
	var background any
	evals := 0
	url := fakeBiDi(t, func(method string, params json.RawMessage) (any, *Error) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "browsingContext.create":
			var p map[string]any
			_ = json.Unmarshal(params, &p)
			background = p["background"]
			return map[string]string{"context": "ctx-1"}, nil
		case "browsingContext.getTree":
			return map[string]any{"contexts": []map[string]string{{"context": "ctx-1", "userContext": "default"}}}, nil
		case "script.evaluate":
			evals++
			if evals <= 2 {
				return nil, &Error{Code: "no such frame", Message: "DiscardedBrowsingContextError: BrowsingContext does no longer exist"}
			}
			state := "hidden"
			if evals > 3 {
				state = "visible"
			}
			return map[string]any{"type": "success", "result": map[string]any{"type": "string", "value": state}}, nil
		}
		return map[string]string{"subscription": "s"}, nil
	})
	c, err := Dial(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	b := &Browser{conn: c}
	if _, err := b.NewTab(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if background != true || evals != 4 {
		t.Errorf("background = %v, %d evaluations; want true and 4", background, evals)
	}
}

// A page that never shows is used after shownWait. A context whose page
// doesn't answer is an error after shownWait while the browser still
// lists it, at once when it doesn't.
func TestWaitShownGivesUp(t *testing.T) {
	wait, pause := shownWait, shownPause
	shownPause = time.Millisecond
	t.Cleanup(func() { shownWait, shownPause = wait, pause })
	for _, tc := range []struct {
		name         string
		answers      bool          // the page answers, hidden
		listed       bool          // getTree lists the context
		wait, before time.Duration // shownWait; waitShown returns before
	}{
		{"hidden", true, true, 20 * time.Millisecond, 10 * time.Second},
		{"no page yet", false, true, 20 * time.Millisecond, 10 * time.Second},
		{"gone", false, false, time.Minute, 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shownWait = tc.wait
			url := fakeBiDi(t, func(method string, _ json.RawMessage) (any, *Error) {
				switch {
				case method == "browsingContext.getTree":
					tree := []map[string]string{{"context": "other", "userContext": "default"}}
					if tc.listed {
						tree = append(tree, map[string]string{"context": "ctx-1", "userContext": "default"})
					}
					return map[string]any{"contexts": tree}, nil
				case !tc.answers:
					return nil, &Error{Code: "no such frame", Message: "gone"}
				}
				return map[string]any{"type": "success", "result": map[string]any{"type": "string", "value": "hidden"}}, nil
			})
			c, err := Dial(context.Background(), url)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			var notes []string
			b := &Browser{conn: c, logf: func(s string) { notes = append(notes, s) }}
			start := time.Now()
			err = b.waitShown(context.Background(), "ctx-1")
			if tc.answers == (err != nil) || tc.answers != (len(notes) == 1) {
				t.Errorf("waitShown = %v, notes %q", err, notes)
			}
			if !tc.answers && !discarded(err) {
				t.Errorf("waitShown = %v, want no such frame", err)
			}
			if d := time.Since(start); d >= tc.before {
				t.Errorf("waitShown took %s", d)
			}
		})
	}
}

// On a busy machine a new window can lose its page for good, or never
// get one in time. NewTab closes it and opens another.
func TestNewTabReplacesAWindowThatLostItsPage(t *testing.T) {
	wait, pause := shownWait, shownPause
	shownWait, shownPause = 50*time.Millisecond, time.Millisecond
	t.Cleanup(func() { shownWait, shownPause = wait, pause })
	for _, tc := range []struct {
		name   string
		listed bool // the first window stays listed, without a page
	}{{"gone", false}, {"no page in time", true}} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var created, closed []string
			url := fakeBiDi(t, func(method string, params json.RawMessage) (any, *Error) {
				mu.Lock()
				defer mu.Unlock()
				var p struct {
					Context string `json:"context"`
					Target  struct {
						Context string `json:"context"`
					} `json:"target"`
				}
				_ = json.Unmarshal(params, &p)
				switch method {
				case "browsingContext.create":
					id := fmt.Sprintf("ctx-%d", len(created)+1)
					created = append(created, id)
					return map[string]string{"context": id}, nil
				case "browsingContext.close":
					closed = append(closed, p.Context)
					return map[string]any{}, nil
				case "browsingContext.getTree":
					tree := []map[string]string{}
					for _, id := range created {
						if (id != "ctx-1" || tc.listed) && !slices.Contains(closed, id) {
							tree = append(tree, map[string]string{"context": id, "userContext": "default"})
						}
					}
					return map[string]any{"contexts": tree}, nil
				case "script.evaluate":
					if p.Target.Context == "ctx-1" {
						return nil, &Error{Code: "no such frame", Message: "DiscardedBrowsingContextError: BrowsingContext does no longer exist"}
					}
					return map[string]any{"type": "success", "result": map[string]any{"type": "string", "value": "visible"}}, nil
				}
				return map[string]string{"subscription": "s"}, nil
			})
			c, err := Dial(context.Background(), url)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			var notes []string
			b := &Browser{conn: c, logf: func(s string) { notes = append(notes, s) }}
			got, err := b.NewTab(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if id := got.(*tab).id; id != "ctx-2" || !slices.Equal(closed, []string{"ctx-1"}) || len(notes) != 1 {
				t.Errorf("NewTab = %q, created %v, closed %v, notes %q; want ctx-2 with ctx-1 closed", id, created, closed, notes)
			}
		})
	}
}

// A create Firefox doesn't answer in time (it answers once the window's
// first page has loaded) is taken like a discarded one: NewTab takes the
// window it opened.
func TestNewTabTakesTheWindowOfACreateWithNoAnswer(t *testing.T) {
	wait, pause := createWait, newTabPause
	createWait, newTabPause = 50*time.Millisecond, time.Millisecond
	t.Cleanup(func() { createWait, newTabPause = wait, pause })
	var mu sync.Mutex
	creates := 0
	opened := false
	url := fakeBiDi(t, func(method string, _ json.RawMessage) (any, *Error) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "browsingContext.create":
			creates++
			opened = true
			return noAnswer, nil
		case "browsingContext.getTree":
			tree := []map[string]string{{"context": "first", "userContext": "default"}}
			if opened {
				tree = append(tree, map[string]string{"context": "slow", "userContext": "default"})
			}
			return map[string]any{"contexts": tree}, nil
		case "script.evaluate":
			return map[string]any{"type": "success", "result": map[string]any{"type": "string", "value": "visible"}}, nil
		}
		return map[string]string{"subscription": "s"}, nil
	})
	c, err := Dial(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	b := &Browser{conn: c}
	got, err := b.NewTab(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if id := got.(*tab).id; id != "slow" || creates != 1 {
		t.Errorf("NewTab = %q after %d creates, want the window of the first", id, creates)
	}
}
