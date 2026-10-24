// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// panicky is a module with a bug in one of its hooks.
type panicky struct{ in string }

func (panicky) Name() string { return "panicky" }

func (m panicky) boom(hook string) {
	if m.in == hook {
		var seen map[string]bool
		seen[hook] = true // assignment to entry in nil map
	}
}

func (m panicky) Prepare(context.Context, *check.Context, *rod.Page) error {
	m.boom("Prepare")
	return nil
}
func (m panicky) Live(context.Context, *check.Context, *rod.Page, *model.Page) { m.boom("Live") }
func (m panicky) CheckPage(*check.Context, *model.Page)                       { m.boom("CheckPage") }
func (m panicky) CheckSite(context.Context, *check.Context)                    { m.boom("CheckSite") }

// A module that panics in any hook costs a warning, not the run: every
// page is still recorded and the other modules' issues are all there.
func TestModulePanicIsAWarning(t *testing.T) {
	needBrowser(t)
	var broken atomic.Bool
	broken.Store(true)
	srv := site(t, &broken)
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Tabs = []string{"health"}, []string{"desktop"}, 0, 2
	for _, hook := range []string{"Prepare", "Live", "CheckPage", "CheckSite"} {
		t.Run(hook, func(t *testing.T) {
			run, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Paths: []string{"/", "/contact/"},
				Dir: t.TempDir(), Version: "test", Browser: browser.Options{NoDownload: true},
				Modules: []check.Module{panicky{in: hook}, health.Module{}}})
			if err != nil {
				t.Fatal(err)
			}
			prefix := "panicky: panic in " + hook + ": assignment to entry in nil map (runner.panicky.boom safe_test.go:"
			if !slices.ContainsFunc(run.Warnings, func(w string) bool { return strings.HasPrefix(w, prefix) }) {
				t.Errorf("warnings = %q, want one starting %q", run.Warnings, prefix)
			}
			has := func(path, check string) bool {
				p := run.Page(path)
				return p != nil && slices.ContainsFunc(p.Issues, func(i model.Issue) bool { return i.Check == check })
			}
			if !has("/", "health/php-error") || !has("/contact/", "health/status") {
				t.Errorf("health's issues are missing: %+v", run.Pages)
			}
		})
	}
}
