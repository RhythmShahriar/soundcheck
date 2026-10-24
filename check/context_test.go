// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/model"
)

// These tests are the spec of the API modules use.

func TestAddIssueSetsTheKey(t *testing.T) {
	cc := &Context{Run: &model.Run{}}
	cc.AddIssue(model.Issue{Check: "health/links", Level: model.Error, Title: "Broken link", Source: "/gone/", Pages: []string{"/"}})
	if len(cc.Run.Issues) != 1 {
		t.Fatalf("issues = %+v", cc.Run.Issues)
	}
	if got := cc.Run.Issues[0]; got.Key != "health/links|Broken link|/gone/" || !slices.Equal(got.Pages, []string{"/"}) {
		t.Errorf("issue = %+v", got)
	}
}

func TestWarn(t *testing.T) {
	cc := &Context{Run: &model.Run{}}
	cc.Warn("one")
	cc.Warn("two")
	if !slices.Equal(cc.Run.Warnings, []string{"one", "two"}) {
		t.Errorf("warnings = %q", cc.Run.Warnings)
	}
}

func TestPageSetsTheKey(t *testing.T) {
	p := &model.Page{}
	Page(p, model.Issue{Check: "health/php-error", Title: "PHP Warning", Source: "themes/x/functions.php:212", Detail: "line noise"})
	Page(p, model.Issue{Check: "health/status", Title: "Server error 500"})
	if len(p.Issues) != 2 || p.Issues[0].Key != "health/php-error|PHP Warning|themes/x/functions.php:212" ||
		p.Issues[1].Key != "health/status|Server error 500|" {
		t.Errorf("issues = %+v", p.Issues)
	}
}

// Get sends the run's user agent through the run's client and waits for
// the host's turn.
func TestGet(t *testing.T) {
	var gotUA, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotMethod = r.UserAgent(), r.Method
	}))
	defer srv.Close()
	cc := &Context{HTTP: srv.Client(), UserAgent: "soundcheck-test", Hosts: ratelimit.NewHosts(0)}
	resp, err := cc.Get(context.Background(), http.MethodHead, srv.URL+"/a/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if gotUA != "soundcheck-test" || gotMethod != http.MethodHead {
		t.Errorf("server saw %s with user agent %q", gotMethod, gotUA)
	}

	// A canceled context stops it before the request goes out.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gotMethod = ""
	cc.Hosts = ratelimit.NewHosts(0.001)
	u, _ := url.Parse(srv.URL)
	_ = cc.Hosts.For(u.Host).Wait(context.Background()) // use up the host's turn
	if resp, err := cc.Get(ctx, http.MethodGet, srv.URL+"/b/"); err == nil {
		_ = resp.Body.Close()
		t.Error("Get with a canceled context succeeded")
	}
	if gotMethod != "" {
		t.Error("the request went out")
	}
	if resp, err := cc.Get(context.Background(), http.MethodGet, "http://[::1"); err == nil {
		_ = resp.Body.Close()
		t.Error("Get of a bad URL succeeded")
	}
}

// Pairs matches pages by path, in the after run's order and leaves out
// pages only one run has.
func TestPairs(t *testing.T) {
	c := &CompareContext{
		Before: &model.Run{Pages: []model.Page{{Path: "/", Title: "a"}, {Path: "/gone/"}, {Path: "/shop/", Title: "b"}}},
		After:  &model.Run{Pages: []model.Page{{Path: "/shop/", Title: "B"}, {Path: "/new/"}, {Path: "/", Title: "A"}}},
	}
	var got []string
	for _, p := range c.Pairs() {
		got = append(got, p[0].Path+" "+p[0].Title+">"+p[1].Title)
	}
	if want := []string{"/shop/ b>B", "/ a>A"}; !slices.Equal(got, want) {
		t.Errorf("Pairs = %q, want %q", got, want)
	}
}
