// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !linux && !darwin && !windows

package browser

import (
	"context"

	"github.com/go-rod/rod/lib/launcher"
)

// process is a Chrome started through rod's launcher, in its own
// process group so stop reaches its children. leakless has no build for
// these systems, so a Soundcheck killed by SIGKILL can leave Chrome
// running.
type process struct{ l *launcher.Launcher }

func start(_ context.Context, l *launcher.Launcher, bin string) (string, *process, error) {
	u, err := l.Bin(bin).Leakless(false).Launch()
	if err != nil {
		return "", nil, err
	}
	return u, &process{l}, nil
}

func (p *process) stop() {
	p.l.Kill()
	p.l.Cleanup()
}
