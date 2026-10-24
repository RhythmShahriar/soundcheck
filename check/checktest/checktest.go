// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package checktest has fakes for testing Soundcheck modules without
// Chrome: a Tab that answers Eval from a table and plays back events,
// and a Context to run hooks against.
package checktest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Tab is a fake check.Tab. Set its fields before the hook runs.
type Tab struct {
	// Results answers Eval: the value for each script, as the page would
	// return it. A script that isn't listed fails, so a test notices a
	// changed script.
	Results map[string]any
	// Shot is what Screenshot returns.
	Shot []byte
	// Events are sent to each On subscriber as it subscribes, in order.
	Events []check.Event

	mu    sync.Mutex
	evals []string
}

var _ check.Tab = (*Tab)(nil)

// Eval answers from Results through JSON, the way a real page's result
// is decoded.
func (t *Tab) Eval(_ context.Context, js string, out any, _ ...any) error {
	t.mu.Lock()
	t.evals = append(t.evals, js)
	t.mu.Unlock()
	v, ok := t.Results[js]
	if !ok {
		return fmt.Errorf("checktest: no result for script %q", js)
	}
	if out == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// Screenshot returns Shot.
func (t *Tab) Screenshot(context.Context, bool) ([]byte, error) { return t.Shot, nil }

// On sends Events to fn before it returns.
func (t *Tab) On(_ context.Context, fn func(check.Event)) func() {
	for _, e := range t.Events {
		fn(e)
	}
	return func() {}
}

// Rod is nil: there is no browser.
func (t *Tab) Rod() *rod.Page { return nil }

// Evals lists the scripts Eval ran, in order.
func (t *Tab) Evals() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.evals...)
}

// Context returns a check.Context for the site at base, with the default
// config, an empty run, no rate limit and saved files kept in Files.
func Context(base string) (*check.Context, *Files) {
	u, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	files := &Files{m: map[string][]byte{}}
	return &check.Context{
		Config: config.Default(), Base: u, Run: &model.Run{Target: base}, HTTP: http.DefaultClient,
		Hosts: ratelimit.NewHosts(0), Save: files.save,
	}, files
}

// Files holds what a Context saved.
type Files struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (f *Files) save(name string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[name] = append([]byte(nil), data...)
	return nil
}

// Get returns the file saved as name, or nil.
func (f *Files) Get(name string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[name]
}
