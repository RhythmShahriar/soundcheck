// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package webdriver drives Firefox and WebKit for Soundcheck through the
// W3C WebDriver protocols. WebDriver BiDi is a JSON protocol over a
// WebSocket that Firefox serves itself; WebKit's drivers fall back to
// classic WebDriver over HTTP when they don't offer BiDi. Its
// tabs implement capture.Tab, so a visit records the same facts it does
// in Chrome wherever the protocol allows.
package webdriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"

	"golang.org/x/net/websocket"
)

// Conn is a WebDriver BiDi connection: commands with their replies, and
// events for whoever subscribed to them. Its methods are safe to call
// from several goroutines.
type Conn struct {
	ws *websocket.Conn

	mu      sync.Mutex
	next    int64
	pending map[int64]chan reply
	subs    map[int]*sub
	nextSub int
	err     error // why the connection ended; set once
	done    chan struct{}
}

type reply struct {
	result json.RawMessage
	err    error
}

// Error is a command the browser answered with an error.
type Error struct {
	Code    string // "unknown command", "no such frame"…
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// Unsupported reports whether err is a command the browser doesn't have,
// so a caller can fall back to another way or go without.
func Unsupported(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code == "unknown command" || e.Code == "unsupported operation")
}

// Dial connects to a BiDi endpoint such as ws://127.0.0.1:9222/session.
// It sends an Origin of http://127.0.0.1, which Firefox accepts only
// when it was started with that in --remote-allow-origins.
func Dial(ctx context.Context, wsURL string) (*Conn, error) {
	loc, err := url.Parse(wsURL)
	if err != nil {
		return nil, err
	}
	cfg := &websocket.Config{Location: loc, Origin: &url.URL{Scheme: "http", Host: "127.0.0.1"}, Version: websocket.ProtocolVersionHybi13}
	ws, err := cfg.DialContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", wsURL, err)
	}
	// Full-page screenshots arrive in one message, as base64.
	ws.MaxPayloadBytes = 256 << 20
	c := &Conn{ws: ws, pending: map[int64]chan reply{}, subs: map[int]*sub{}, done: make(chan struct{})}
	go c.read()
	return c, nil
}

// message is any message the browser sends: a reply has an ID, an event
// a method.
type message struct {
	Type    string          `json:"type"` // success, error or event
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (c *Conn) read() {
	for {
		var data []byte
		if err := websocket.Message.Receive(c.ws, &data); err != nil {
			c.fail(fmt.Errorf("browser connection closed: %w", err))
			return
		}
		var m message
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		switch {
		case m.ID != nil:
			c.mu.Lock()
			ch := c.pending[*m.ID]
			delete(c.pending, *m.ID)
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			if m.Type == "error" {
				ch <- reply{err: &Error{Code: m.Error, Message: m.Message}}
			} else {
				ch <- reply{result: m.Result}
			}
		case m.Method != "":
			c.mu.Lock()
			for _, s := range c.subs {
				s.push(Event{Method: m.Method, Params: m.Params})
			}
			c.mu.Unlock()
		}
	}
}

// fail ends the connection: commands waiting for a reply get err.
func (c *Conn) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = err
	for id, ch := range c.pending {
		ch <- reply{err: err}
		delete(c.pending, id)
	}
	for id, s := range c.subs {
		s.close()
		delete(c.subs, id)
	}
	close(c.done)
}

// Call sends a command and decodes its result into out (nil discards
// it). It returns when the browser replies, ctx ends or the connection
// does.
func (c *Conn) Call(ctx context.Context, method string, params, out any) error {
	if params == nil {
		params = struct{}{}
	}
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()
	data, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err == nil {
		err = websocket.Message.Send(c.ws, string(data))
	}
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, err)
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("%s: %w", method, r.err)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(r.result, out)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Event is an event the browser sent.
type Event struct {
	Method string // "log.entryAdded", "network.responseCompleted"…
	Params json.RawMessage
}

// On calls fn with every event from now on, in order, on a goroutine of
// its own: fn may send commands. It stops when stop is called or the
// connection ends. Events reach the browser's connection only after a
// session.subscribe for them.
func (c *Conn) On(fn func(Event)) (stop func()) {
	s := &sub{wake: make(chan struct{}, 1), quit: make(chan struct{})}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return func() {}
	}
	c.nextSub++
	id := c.nextSub
	c.subs[id] = s
	c.mu.Unlock()
	go s.run(fn)
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			delete(c.subs, id)
			c.mu.Unlock()
			s.close()
		})
	}
}

// Done is closed when the connection ends.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Close closes the connection.
func (c *Conn) Close() error {
	err := c.ws.Close()
	c.fail(errors.New("browser connection closed"))
	return err
}

// sub queues events for one subscriber without limit: the reader must
// never wait on a slow subscriber, or replies to the subscriber's own
// commands would wait behind it.
type sub struct {
	mu     sync.Mutex
	queue  []Event
	closed bool
	wake   chan struct{}
	quit   chan struct{}
}

func (s *sub) push(e Event) {
	s.mu.Lock()
	if !s.closed {
		s.queue = append(s.queue, e)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *sub) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.quit)
	}
}

func (s *sub) run(fn func(Event)) {
	for {
		s.mu.Lock()
		q := s.queue
		s.queue = nil
		s.mu.Unlock()
		for _, e := range q {
			select {
			case <-s.quit:
				return
			default:
			}
			fn(e)
		}
		select {
		case <-s.wake:
		case <-s.quit:
			return
		}
	}
}
