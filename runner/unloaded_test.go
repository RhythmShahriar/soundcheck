// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// auditMod records an audit of every page it is given, the way a11y
// and keyboard do.
type auditMod struct{ name string }

func (m auditMod) Name() string { return m.name }
func (auditMod) AuditsPages()   {}
func (m auditMod) Live(_ context.Context, _ *check.Context, _ *rod.Page, p *model.Page) {
	if p.Audits == nil {
		p.Audits = map[string]model.Audit{}
	}
	p.Audits[m.name] = model.Audit{Status: model.AuditOK}
}

// A page that never loaded gets an error audit from every module that
// records audits, so it can't read as tested with nothing found. One a
// module already recorded stays; other modules and loaded pages get
// nothing.
func TestUnloadedAudits(t *testing.T) {
	mods := []check.Module{health.Module{}, auditMod{"a11y"}, auditMod{"keyboard"}}
	loaded := &model.Page{Path: "/"}
	unloadedAudits(loaded, mods)
	if loaded.Audits != nil {
		t.Errorf("loaded page: audits %+v", loaded.Audits)
	}
	p := &model.Page{Path: "/slow", Error: "timed out",
		Audits: map[string]model.Audit{"keyboard": {Status: model.AuditSkipped, Reason: "needs Chrome"}}}
	unloadedAudits(p, mods)
	want := map[string]model.Audit{
		"a11y":     {Status: model.AuditError, Reason: "page didn't load: timed out"},
		"keyboard": {Status: model.AuditSkipped, Reason: "needs Chrome"},
	}
	if !reflect.DeepEqual(p.Audits, want) {
		t.Errorf("audits = %+v", p.Audits)
	}
	mail := "jane.doe" + "@" + "example.test"
	q := &model.Page{Path: "/x", Error: "net::ERR_FAILED " + mail}
	unloadedAudits(q, mods)
	if r := q.Audits["a11y"].Reason; r != "page didn't load: net::ERR_FAILED ***@***" {
		t.Errorf("reason = %q", r)
	}
}

// A run records the error audits on a page that timed out: the run's
// pages say which audits are missing.
func TestTimedOutPageNotAudited(t *testing.T) {
	needBrowser(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(8 * time.Second):
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `<!doctype html><title>Slow</title><input>`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Home</title><p>Welcome.</p>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Tabs, cfg.Timeout = []string{"health"}, []string{"desktop"}, 0, 2, 3
	run, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Paths: []string{"/", "/slow"},
		Dir: t.TempDir(), Version: "test", Browser: browser.Options{NoDownload: true},
		Modules: []check.Module{health.Module{}, auditMod{"a11y"}, auditMod{"keyboard"}}})
	if err != nil {
		t.Fatal(err)
	}
	home, slow := run.Page("/"), run.Page("/slow")
	if home == nil || home.Audits["a11y"].Status != model.AuditOK || home.Audits["keyboard"].Status != model.AuditOK {
		t.Fatalf("home: %+v", home)
	}
	if slow == nil || slow.Error != "timed out" {
		t.Fatalf("slow: %+v", slow)
	}
	for _, m := range []string{"a11y", "keyboard"} {
		if a := slow.Audits[m]; a.Status != model.AuditError || a.Reason != "page didn't load: timed out" {
			t.Errorf("slow %s audit = %+v", m, a)
		}
	}
}
