// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyflow

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/flow/internal/flowtest"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
)

// The helpers the flow tests share (see flowtest).
var (
	listen      = flowtest.Listen
	needChrome  = flowtest.NeedChrome
	isolatedTab = flowtest.IsolatedTab
	hostOf      = flowtest.HostOf
)

const canaryEnv = flowtest.CanaryEnv

var canary = flowtest.Canary

// keyOptions are a key flow's options on base, with the canary to read.
func keyOptions(base string) flow.Options {
	u, _ := url.Parse(base)
	return flow.Options{Base: u, Timeout: testenv.Timeout(3 * time.Second), Fake: flow.Fake{Seed: "k"}, MaxTabs: 12,
		Env: func(k string) string { return map[string]string{canaryEnv: canary}[k] }}
}

// describe lists a result's failures one per line.
func describe(res flow.Result) string {
	var out []string
	for _, f := range res.Failures {
		out = append(out, fmt.Sprintf("%d %s: %v", f.Step, f.Desc, f.Err))
	}
	return strings.Join(out, "\n")
}

// pathLen is how many focus stops a KeyFailure keeps: flow's own
// pathLen, which TestKeptPath in package flow holds to this value.
const pathLen = 6
