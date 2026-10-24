// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Ctrl+C while a page loads: that page is marked canceled instead of
// being recorded as a blank page, finished pages are kept as they are
// and no page starts after the cancel.
func TestCancelMarksUnfinishedPages(t *testing.T) {
	needBrowser(t)
	slowAsked := make(chan struct{}, 1)
	var later atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/fast", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Fast</title><p>Here already.</p>`)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case slowAsked <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("/later", func(w http.ResponseWriter, r *http.Request) {
		later.Add(1)
		fmt.Fprint(w, `<!doctype html><title>Later</title><p>Too late.</p>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Tabs, cfg.Timeout = []string{"health"}, []string{"desktop"}, 0, 2, 30
	run, err := Run(ctx, Options{Config: cfg, Target: srv.URL + "/fast", Paths: []string{"/fast", "/slow", "/later"},
		Dir: t.TempDir(), Version: "test", Browser: browser.Options{NoDownload: true}, Modules: []check.Module{health.Module{}},
		Hooks: Hooks{Page: func(p *model.Page, _, _ int) {
			if p.Path != "/fast" {
				return
			}
			// /fast's tab is still taken here, so /later can't start
			// before the cancel.
			select {
			case <-slowAsked:
			case <-time.After(20 * time.Second):
				t.Error("/slow was never requested")
			}
			cancel()
		}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
	if run == nil {
		t.Fatal("no partial run")
	}
	if fast := run.Page("/fast"); fast == nil || fast.Error != "" || fast.Title != "Fast" || fast.TextLength == 0 {
		t.Errorf("/fast: %+v", fast)
	}
	slow := run.Page("/slow")
	if slow == nil || slow.Error != "context canceled" {
		t.Fatalf("/slow: %+v", slow)
	}
	if slow.Title != "" || slow.TextHash != "" || slow.Status != 0 || len(slow.Issues) != 0 || len(slow.Shots) != 0 ||
		len(slow.Resources) != 0 || slow.Metrics != (model.Metrics{}) {
		t.Errorf("/slow kept facts from the canceled visit: %+v", slow)
	}
	if run.Page("/later") != nil || later.Load() != 0 {
		t.Errorf("/later started after the cancel: %d requests", later.Load())
	}
	if len(run.Pages) != 2 {
		t.Errorf("pages = %+v", run.Pages)
	}
}
