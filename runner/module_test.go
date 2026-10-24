// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

type shooter struct {
	name          string
	shots, second bool
}

func (s shooter) Name() string                   { return s.name }
func (s shooter) NeedsShots() bool               { return s.shots }
func (s shooter) NeedsSecondShot(snap bool) bool { return s.second && snap }

type plain string

func (p plain) Name() string { return string(p) }

func TestCaptureNeeds(t *testing.T) {
	with := func(mods ...string) config.Config {
		cfg := config.Default()
		cfg.Modules = mods
		return cfg
	}
	for _, c := range []struct {
		name               string
		cfg                config.Config
		mods               []check.Module
		snapshot           bool
		wantShots, wantTwo bool
	}{
		{"no module asks", with("health"), []check.Module{plain("health")}, true, false, false},
		{"a module asks for shots", with("health"), []check.Module{shooter{"a11y", true, false}}, true, true, false},
		{"and a second one in snapshots", with("x"), []check.Module{shooter{"x", true, true}}, true, true, true},
		{"but not in runs", with("x"), []check.Module{shooter{"x", true, true}}, false, true, false},
		{"no second shot without shots", with("x"), []check.Module{shooter{"x", false, true}}, true, false, false},
		// The fallback for a visual module without CaptureNeeds.
		{"legacy visual, snapshot", with("visual"), []check.Module{plain("visual")}, true, true, true},
		{"legacy visual, run", with("visual"), []check.Module{plain("visual")}, false, true, false},
		{"visual enabled without the module", with("health", "visual"), []check.Module{plain("health")}, true, true, true},
		{"visual that says no", with("visual"), []check.Module{shooter{"visual", false, false}}, true, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			shots, two := captureNeeds(c.cfg, c.mods, c.snapshot)
			if shots != c.wantShots || two != c.wantTwo {
				t.Errorf("captureNeeds = %v, %v; want %v, %v", shots, two, c.wantShots, c.wantTwo)
			}
		})
	}
}

// recorder notes the order its hooks ran in, across modules.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) note(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

type liveMod struct {
	name  string
	phase int
	rec   *recorder
}

func (m liveMod) Name() string { return m.name }
func (m liveMod) Phase() int   { return m.phase }
func (m liveMod) Live(context.Context, *check.Context, *rod.Page, *model.Page) {
	m.rec.note(m.name)
}

// tabMod uses the Tab API against real Chrome: it watches console errors
// from before navigation, reads the title and takes a screenshot.
type tabMod struct {
	rec    *recorder
	mu     sync.Mutex
	events []string
	stop   func()
	title  string
	png    bool
}

func (*tabMod) Name() string { return "tabmod" }

func (m *tabMod) PrepareTab(ctx context.Context, _ *check.Context, tab check.Tab) error {
	m.stop = tab.On(ctx, func(e check.Event) {
		if e.Kind == check.Console {
			m.mu.Lock()
			m.events = append(m.events, e.Level+": "+e.Text)
			m.mu.Unlock()
		}
	})
	return nil
}

func (m *tabMod) LiveTab(ctx context.Context, _ *check.Context, tab check.Tab, p *model.Page) {
	m.rec.note("tabmod")
	defer m.stop()
	_ = tab.Eval(ctx, `(sep) => document.title + sep + location.pathname`, &m.title, "|")
	shot, err := tab.Screenshot(ctx, false)
	m.png = err == nil && bytes.HasPrefix(shot, []byte("\x89PNG"))
	if tab.Rod() == nil {
		m.title = "no rod page"
	}
}

// The runner calls TabPreparer and TabLive with a real tab and calls the
// modules' Live hooks in phase order.
func TestTabHooksAndPhases(t *testing.T) {
	needBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Brew</title><script>console.error("boom", 42)</script><h1>Hi</h1>`)
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Tabs = []string{"health"}, []string{"desktop"}, 0, 1
	rec := &recorder{}
	tm := &tabMod{rec: rec}
	_, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Paths: []string{"/"}, Dir: t.TempDir(),
		Version: "test", Browser: browser.Options{NoDownload: true}, Modules: []check.Module{
			liveMod{"mutates", check.PhaseMutate, rec}, tm, liveMod{"inspects", check.PhaseInspect, rec},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tabmod", "inspects", "mutates"}; !slices.Equal(rec.calls, want) {
		t.Errorf("hooks ran %v, want %v", rec.calls, want)
	}
	if tm.title != "Brew|/" || !tm.png {
		t.Errorf("title %q, png %v", tm.title, tm.png)
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if !slices.Contains(tm.events, "error: boom 42") {
		t.Errorf("console events = %q", tm.events)
	}
}
