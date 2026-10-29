// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/crawl"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
)

// A page named with --page, pages: extra: or a path to re-check that the
// frontier leaves out gets a note saying why, instead of only a skip
// count. The note names where the page came from and masks it as the
// run's outputs do.
func TestListedPageDropIsLogged(t *testing.T) {
	base, _ := url.Parse("http://127.0.0.1:20281/")
	// A session the site put in a link and a reset token: made at run
	// time, so no secret-looking value is committed.
	sid := "tk_" + strings.Repeat("Q9x", 8)
	token := strings.Repeat("ab12", 6)
	for _, c := range []struct {
		raw, named string
		eaa        bool
		want       string // "" logs nothing
	}{
		{"/x?s=1", listedFlag, false, "!--page /x?s=1 matches the default exclude *?s=*; not checked"},
		{"/private/a/", listedFlag, false, "!--page /private/a/ matches the exclude /private*; not checked"},
		{"/private/a/", listedConfig, false, "!pages.extra /private/a/ matches the exclude /private*; not checked"},
		{"/wp-admin/", listedSnapshot, false, "!page /wp-admin/ matches the default exclude /wp-admin*; not checked"},
		{"http://127.0.0.1:20282/a/", listedFlag, false, "!--page http://127.0.0.1:20282/a/ is on another site; not checked"},
		{"https://other.test/a/", listedFlag, false, "!--page https://other.test/a/ is on another site; not checked"},
		{"/file.pdf", listedFlag, false, "!--page /file.pdf isn't a page; not checked"},
		{"/shop/", listedFlag, false, ""},
		// A stored path with a session, left out on compare.
		{"/private/acct.html?sid=" + sid, listedSnapshot, true, "!page /private/acct.html?sid=••••• matches the exclude /private*; not checked"},
		{"/acct.html?reset_token=" + token, listedFlag, true, "!--page /acct.html?reset_token=••••• matches the default exclude *?*token=*; not checked"},
		{"/acct.html?reset_token=" + token, listedConfig, true, "!pages.extra /acct.html?reset_token=••••• matches the default exclude *?*token=*; not checked"},
		// Without --eaa a credential-named value is still masked.
		{"/acct.html?reset_token=" + token, listedConfig, false, "!pages.extra /acct.html?reset_token=••••• matches the default exclude *?*token=*; not checked"},
	} {
		t.Run(c.named+c.raw, func(t *testing.T) {
			exclude := []string{"/private*"}
			f := crawl.NewFrontier(base, crawl.Options{IgnoreRobots: true, Exclude: exclude})
			var got []string
			addListed(f, base, c.raw, c.named, c.eaa, exclude, func(s string) { got = append(got, s) })
			var want []string
			if c.want != "" {
				want = []string{c.want}
			}
			if !slices.Equal(got, want) {
				t.Errorf("logged %q, want %q", got, want)
			}
			for _, secret := range []string{sid, token} {
				if strings.Contains(strings.Join(got, "\n"), secret) {
					t.Errorf("the note shows %s", secret)
				}
			}
		})
	}
	// A page listed twice is checked once: that's no drop.
	f := crawl.NewFrontier(base, crawl.Options{IgnoreRobots: true})
	var got []string
	for range 2 {
		addListed(f, base, "/shop/", listedFlag, false, nil, func(s string) { got = append(got, s) })
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
// excludes drop is named in the log. So is a pages: extra: path of the
// config file, which isn't a --page.
func TestSiteProgressAndDroppedPage(t *testing.T) {
	needBrowser(t)
	var broken atomic.Bool
	srv := site(t, &broken)
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Tabs = []string{"health"}, []string{"desktop"}, 0, 2
	cfg.Pages.Extra = []string{"/x?s=1", "/y?s=2"}
	cfg.Pages.Flag = []string{"/x?s=1"}
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
		"!pages.extra /y?s=2 matches the default exclude *?s=*; not checked",
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
