// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"context"

	"github.com/go-rod/rod/lib/launcher"

	"github.com/RhythmShahriar/soundcheck/internal/proc"
)

// process is a Chrome started through rod's launcher.
//
// leakless stays off on Windows: its guard executable, unpacked into
// the temp folder at run time, is regularly flagged by antivirus as a
// false positive, which quarantines it and breaks every run. Instead
// Soundcheck puts itself in a job object that kills everything in it
// when Soundcheck exits, however it exits (proc.KillOnExit). Chrome and
// its children inherit the job, so they go with it.
type process struct{ l *launcher.Launcher }

func start(_ context.Context, l *launcher.Launcher, bin string) (string, *process, error) {
	proc.KillOnExit()
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
