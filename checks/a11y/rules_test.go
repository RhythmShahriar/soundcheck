// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y_test

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// pages serves each path's HTML from 127.0.0.1:20320-20339; a path not
// in the map is a 404.
func pages(t *testing.T, html map[string]string) *httptest.Server {
	t.Helper()
	return serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := html[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, body)
	}))
}

// audited runs the a11y module alone over paths of srv with WCAG version
// (empty for a run made before 1.3) and the a11y: block body.
func audited(t *testing.T, srv *httptest.Server, version, block string, paths ...string) *model.Run {
	t.Helper()
	cfg := config.Default()
	if block != "" {
		file := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(file, []byte("a11y:\n"+block), 0o600); err != nil {
			t.Fatal(err)
		}
		var err error
		if cfg, err = config.Load(file); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Rate, cfg.Tabs = 0, 1
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"a11y"}
	if err := check.Configure(a11y.Module{}, cfg); err != nil {
		t.Fatal(err)
	}
	r, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: srv.URL, Paths: paths, Dir: t.TempDir(), Version: "test",
		Modules: []check.Module{a11y.Module{}}, Browser: browser.Options{NoDownload: true}, WCAG: version})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func checksOn(p *model.Page) []string {
	var out []string
	for _, i := range p.Issues {
		out = append(out, i.Check)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

const plain = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Card</title></head>
<body><main><h1>Pay</h1><form><label for="card">Card number</label> <input id="card" name="card" autocomplete="%s"></form></main></body></html>`

// autocomplete-valid is a WCAG 2.1 rule (1.3.5): the 2.0 rules never
// ran it, so a misspelled token went unnoticed.
func TestRulesFollowTheVersion(t *testing.T) {
	needBrowser(t)
	srv := pages(t, map[string]string{
		"/typo/":  fmt.Sprintf(plain, "credit-card-numbr"),
		"/valid/": fmt.Sprintf(plain, "cc-number"),
	})
	r21 := audited(t, srv, "2.1", "", "/typo/", "/valid/")
	r20 := audited(t, srv, "2.0", "", "/typo/", "/valid/")
	old := audited(t, srv, "", "", "/typo/")
	if got := checksOn(r21.Page("/typo/")); !slices.Equal(got, []string{"a11y/autocomplete-valid"}) {
		t.Errorf("2.1, misspelled token: %v", got)
	}
	if got := checksOn(r21.Page("/valid/")); len(got) > 0 {
		t.Errorf("2.1, valid token: %v", got)
	}
	if got := checksOn(r20.Page("/typo/")); len(got) > 0 {
		t.Errorf("2.0, misspelled token: %v", got)
	}
	if got := checksOn(old.Page("/typo/")); len(got) > 0 {
		t.Errorf("no version (a 1.2 run) ran more than the 2.0 rules: %v", got)
	}
	for _, i := range r21.Page("/typo/").Issues {
		if !slices.Equal(i.WCAG, []string{"1.3.5"}) || !slices.Equal(i.EN301549, []string{"9.1.3.5"}) || i.Impact != "serious" {
			t.Errorf("issue = %+v", i)
		}
	}
}

// The run records the axe build and every rule it applied, so a
// comparison can tell a rule the snapshot never ran from a new problem.
// The goldens are the rule ids axe.getRules gives in Chrome; regenerate
// them with go test -run TestRulesRecorded -update after updating axe.
func TestRulesRecorded(t *testing.T) {
	needBrowser(t)
	srv := pages(t, map[string]string{"/": fmt.Sprintf(plain, "cc-number"), "/b/": fmt.Sprintf(plain, "cc-number")})
	for _, v := range a11y.Versions {
		t.Run(v, func(t *testing.T) {
			r := audited(t, srv, v, "  max_elements: 33\n", "/", "/b/")
			got := r.Options.A11y
			if got == nil {
				t.Fatal("the run has no a11y options")
			}
			if got.Axe != "4.14.0" || len(got.AxeSHA256) != 64 || got.MaxElements != 33 || r.Options.WCAG != v {
				t.Errorf("a11y options = %+v, wcag %q", got, r.Options.WCAG)
			}
			if !slices.Contains(got.Tags, "wcag2aa") || slices.Contains(got.Tags, "wcag22aa") != (v == "2.2") ||
				slices.Contains(got.Tags, "wcag21aa") != (v != "2.0") {
				t.Errorf("tags = %v", got.Tags)
			}
			if !slices.IsSorted(got.Rules) || slices.Contains(got.Rules, "") {
				t.Errorf("rules aren't sorted ids: %v", got.Rules)
			}
			// The goldens are axe's rules; the module's own checks are
			// recorded with them.
			own := a11y.OwnRules(v)
			for _, r := range own {
				if !slices.Contains(got.Rules, r) {
					t.Errorf("rules don't list the module's own %s", r)
				}
			}
			axeRules := slices.DeleteFunc(slices.Clone(got.Rules), func(r string) bool { return slices.Contains(own, r) })
			golden := filepath.Join("testdata", "rules-"+v+".golden")
			text := strings.Join(axeRules, "\n") + "\n"
			if *update {
				if err := os.WriteFile(golden, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if text != string(want) {
				t.Errorf("rules for WCAG %s differ from %s (go test -run TestRulesRecorded -update rewrites it):\n%s", v, golden, text)
			}
		})
	}
}

// A run where no page could be audited records no rules: it applied
// none.
func TestRulesNotRecordedWithoutAnAudit(t *testing.T) {
	needBrowser(t)
	srv := pages(t, map[string]string{})
	r := audited(t, srv, "2.1", "", "/gone/")
	if r.Options.A11y != nil {
		t.Errorf("a11y options = %+v", r.Options.A11y)
	}
}

// An issue from the browser carries axe's criteria, clauses and impact
// for the rule.
func TestIssueCriteriaInBrowser(t *testing.T) {
	needBrowser(t)
	srv := pages(t, map[string]string{"/": page(true)})
	r := audited(t, srv, "2.1", "", "/")
	var label *model.Issue
	for i, is := range r.Page("/").Issues {
		if is.Check == "a11y/label" {
			label = &r.Page("/").Issues[i]
		}
		if len(is.WCAG) == 0 || len(is.EN301549) == 0 || is.Impact == "" {
			t.Errorf("issue without criteria: %+v", is)
		}
	}
	if label == nil {
		t.Fatalf("no a11y/label issue: %+v", r.Page("/").Issues)
	}
	if !slices.Equal(label.WCAG, []string{"4.1.2"}) || !slices.Equal(label.EN301549, []string{"9.4.1.2"}) || label.Impact != "critical" {
		t.Errorf("a11y/label: wcag %v, en %v, impact %q", label.WCAG, label.EN301549, label.Impact)
	}
}
