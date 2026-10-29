// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package mouseonly runs the keyboard module's search for controls only
// a mouse can use against real Chrome: which controls it reports and
// presses, plus the page's own background activity and the foreign
// navigations it must not take for an answer. The tests live apart from
// package keyboard so each test binary stays well under Go's 10 minute
// default timeout with -race; they skip with -short or without Chrome.
package mouseonly
