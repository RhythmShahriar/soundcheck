// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"context"

	"github.com/go-rod/rod/lib/launcher"
)

// process is a Chrome started through rod's launcher. macOS has no
// parent-death signal, so leakless (a tiny guard process rod ships)
// watches Soundcheck and kills Chrome's process group when it dies,
// even by SIGKILL.
type process struct{ l *launcher.Launcher }

func start(_ context.Context, l *launcher.Launcher, bin string) (string, *process, error) {
	u, err := l.Bin(bin).Leakless(true).Launch()
	if err != nil {
		return "", nil, err
	}
	return u, &process{l}, nil
}

// stop kills Chrome's process group and removes its profile.
func (p *process) stop() {
	p.l.Kill()
	cleanup(p.l)
}
