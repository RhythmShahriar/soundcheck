// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

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
	for _, tc := range []struct {
		name  string
		fails int
		ok    bool
	}{{"recovers", 2, true}, {"gives up", 10, false}} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			creates := 0
			url := fakeBiDi(t, func(method string, _ json.RawMessage) (any, *Error) {
				if method != "browsingContext.create" {
					return map[string]string{"subscription": "s"}, nil
				}
				mu.Lock()
				defer mu.Unlock()
				creates++
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
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), "no such frame") || creates != 4) {
				t.Errorf("NewTab: %v after %d tries, want the error after 4", err, creates)
			}
		})
	}
}
