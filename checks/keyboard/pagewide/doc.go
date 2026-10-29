// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package pagewide runs the keyboard module's search for controls only
// a mouse can use against real Chrome on pages whose key handlers,
// navigations and scroll listeners answer every press: what they do is
// no answer from the control pressed. The tests live apart from package
// keyboard so each test binary stays well under Go's 10 minute default
// timeout with -race; they skip with -short or without Chrome.
package pagewide
