// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"context"

	"github.com/go-rod/rod"
)

// TabGuard keeps a tab in the run's network scope (capture.Scope): the
// requests the scope doesn't allow, such as a page's redirect to a
// private address or the cloud metadata service, are blocked until
// release is called.
type TabGuard func(tab *rod.Page) (release func(), err error)

type tabGuardKey struct{}

// WithTabGuard returns ctx carrying g, so a module (or the login) that
// opens a tab of its own can keep it in the run's scope with GuardTab.
// The runner sets it on the context modules get; the check package
// can't hold the scope itself, as capture imports check.
func WithTabGuard(ctx context.Context, g TabGuard) context.Context {
	return context.WithValue(ctx, tabGuardKey{}, g)
}

// GuardTab keeps tab, which the caller opened itself, in the scope of
// the run ctx belongs to, until release is called (call it before the
// tab closes or after; both are fine). Without a guard in ctx, as in a
// unit test, it guards nothing.
func GuardTab(ctx context.Context, tab *rod.Page) (release func(), err error) {
	g, _ := ctx.Value(tabGuardKey{}).(TabGuard)
	if g == nil {
		return func() {}, nil
	}
	return g(tab)
}
