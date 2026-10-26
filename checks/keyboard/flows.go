// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/keyflows"
)

// CheckSite runs the key flows once every page is visited.
func (m *Module) CheckSite(ctx context.Context, cc *check.Context) {
	m.mu.Lock()
	o := m.opts
	m.mu.Unlock()
	keyflows.Run(ctx, cc, o)
}
