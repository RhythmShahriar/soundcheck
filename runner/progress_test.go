// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/crawl"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
)

// A page named with --page (or a path to re-check) that the frontier
// leaves out gets a note saying why, instead of only a skip count.
func TestListedPageDropIsLogged(t *testing.T) {
	base, _ := url.Parse("http://127.0.0.1:20281/")
	for _, c := range []struct {
		raw, source string
		want        string // "" logs nothing
	}{
		{"/x?s=1", "config", "!--page /x?s=1 matches the default exclude *?s=*; not checked"},
		{"/private/a/", "config", "!--page /private/a/ matches the exclude /private*; not checked"},
		{"/wp-admin/", "snapshot", "!page /wp-admin/ matches the default exclude /wp-admin*; not checked"},
		{"http://127.0.0.1:20282/a/", "config", "!--page http://127.0.0.1:20282/a/ is on another site; not checked"},
		{"https://other.test/a/", "config", "!--page https://other.test/a/ is on another site; not checked"},
		{"/file.pdf", "config", "!--page /file.pdf isn't a page; not checked"},
		{"/shop/", "config", ""},
	} {
		t.Run(c.raw, func(t *testing.T) {
			exclude := []string{"/private*"}
			f := crawl.NewFrontier(base, crawl.Options{IgnoreRobots: true, Exclude: exclude})
			var got []string
			addListed(f, base, c.raw, c.source, exclude, func(s string) { got = append(got, s) })
			var want []string
			if c.want != "" {
				want = []string{c.want}
			}
			if !slices.Equal(got, want) {
				t.Errorf("logged %q, want %q", got, want)
			}
		})
	}
	// A page listed twice is checked once: that's no drop.
	f := crawl.NewFrontier(base, crawl.Options{IgnoreRobots: true})
	var got []string
	for range 2 {
		addListed(f, base, "/shop/", "config", nil, func(s string) { got = append(got, s) })
	}
	if len(got) != 0 {
		t.Errorf("a duplicate logged %q", got)
	}
}

// progressing is a SiteChecker that reports progress through its step.
type progressing struct{}

func (progressing) Name() string { return "progressing" }
func (progressing) CheckSite(_ context.Context, cc *check.Context) {
	for i := range 3 {
		cc.Progress("Checking links", i+1, 3)
	}
}

// After the last page, a run says it's checking site-wide issues while
// a SiteChecker's progress reaches Hooks.Progress. A --page the default
// excludes drop is named in the log.
func TestSiteProgressAndDroppedPage(t *testing.T) {
	needBrowser(t)
	var broken atomic.Bool
	srv := site(t, &broken)
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Tabs = []string{"health"}, []string{"desktop"}, 0, 2
	cfg.Pages.Extra = []string{"/x?s=1"}
	var logs, progress []string
	run, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Dir: t.TempDir(), Version: "test",
		Browser: browser.Options{NoDownload: true}, Modules: []check.Module{progressing{}},
		Hooks: Hooks{
			Log: func(s string) { logs = append(logs, s) },
			Progress: func(label string, done, total int) {
				progress = append(progress, fmt.Sprintf("%s %d/%d", label, done, total))
			},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Page("/x?s=1") != nil {
		t.Error("/x?s=1 was checked")
	}
	for _, want := range []string{"!--page /x?s=1 matches the default exclude *?s=*; not checked",
		"~Checking site-wide issues (links, sitemap)..."} {
		if !slices.Contains(logs, want) {
			t.Errorf("log %q is missing from %q", want, logs)
		}
	}
	if want := []string{"Checking links 1/3", "Checking links 2/3", "Checking links 3/3"}; !slices.Equal(progress, want) {
		t.Errorf("progress = %q, want %q", progress, want)
	}
}

// Without a Hooks.Progress, Progress is a no-op.
func TestProgressWithoutHook(t *testing.T) {
	(&check.Context{}).Progress("Checking links", 1, 2)
}
