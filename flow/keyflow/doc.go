// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package keyflow plays key flows (keyboard: send and keyboard: true)
// against real Chrome: a checkout finished by keyboard, one a mouse-only
// control or a key that does nothing stops, the requests a dry run holds
// and the notes it leaves. The tests live apart from package flow so each
// test binary stays well under Go's 10 minute default timeout with
// -race; they skip with -short or without Chrome.
package keyflow
