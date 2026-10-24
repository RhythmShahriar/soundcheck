// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !unix && !windows

package store

// processAlive can't tell on this system, so it assumes the process runs:
// locks and owner files then expire by age only.
func processAlive(int) bool { return true }
