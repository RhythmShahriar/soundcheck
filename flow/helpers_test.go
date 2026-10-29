// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/flow/internal/flowtest"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
)

// The helpers the flow tests share (see flowtest).
var (
	listen      = flowtest.Listen
	needChrome  = flowtest.NeedChrome
	isolatedTab = flowtest.IsolatedTab
	warmTab     = flowtest.WarmTab
	hostOf      = flowtest.HostOf
)

const canaryEnv = flowtest.CanaryEnv

var canary = flowtest.Canary

// keyOptions are a key flow's options on base, with the canary to read.
func keyOptions(base string) Options {
	u, _ := url.Parse(base)
	return Options{Base: u, Timeout: testenv.Timeout(3 * time.Second), Fake: Fake{Seed: "k"}, MaxTabs: 12,
		Env: func(k string) string { return map[string]string{canaryEnv: canary}[k] }}
}

// describe lists a result's failures one per line.
func describe(res Result) string {
	var out []string
	for _, f := range res.Failures {
		out = append(out, fmt.Sprintf("%d %s: %v", f.Step, f.Desc, f.Err))
	}
	return strings.Join(out, "\n")
}

// The key flow tests in package keyflow expect a KeyFailure to keep this
// many focus stops.
func TestKeptPath(t *testing.T) {
	if pathLen != 6 {
		t.Errorf("pathLen %d: change it in keyflow's tests too", pathLen)
	}
}
