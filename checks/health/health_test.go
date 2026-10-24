// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package health

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

func ctx() *check.Context {
	u, _ := url.Parse("https://shop.example/")
	return &check.Context{Base: u, Run: &model.Run{}}
}

func titles(p *model.Page) map[string]model.Issue {
	m := map[string]model.Issue{}
	for _, i := range p.Issues {
		m[i.Title] = i
	}
	return m
}

func TestPHPErrors(t *testing.T) {
	p := &model.Page{Status: 200, TextLength: 900, Text: `Welcome
Warning: Undefined array key "id" in /var/www/html/wp-content/themes/shopy/functions.php on line 212
Deprecated: Creation of dynamic property Foo::$bar is deprecated in /var/www/html/wp-content/plugins/legacy/legacy.php on line 41
Fatal error: Uncaught Error: Call to undefined function wc_get_x() in /home/site/public_html/wp-content/plugins/shop-ext/ext.php:88 Stack trace: #0 {main} thrown in /home/site/public_html/wp-content/plugins/shop-ext/ext.php on line 88`}
	Module{}.CheckPage(ctx(), p)
	got := titles(p)
	w := got[`PHP Warning: Undefined array key "id"`]
	if w.Level != model.Warning || w.Source != "themes/shopy/functions.php:212" || w.Key == "" {
		t.Errorf("warning = %+v", w)
	}
	if d := got["PHP Deprecated: Creation of dynamic property Foo::$bar is deprecated"]; d.Level != model.Notice || d.Source != "plugins/legacy/legacy.php:41" {
		t.Errorf("deprecated = %+v", d)
	}
	fatal := false
	for title, i := range got {
		if strings.HasPrefix(title, "PHP Fatal Error: Uncaught Error: Call to undefined function wc_get_x()") && i.Level == model.Error && i.Source == "plugins/shop-ext/ext.php:88" {
			fatal = true
		}
	}
	if !fatal {
		t.Errorf("no fatal error in %v", got)
	}
}

func TestErrorScreens(t *testing.T) {
	cases := map[string]*model.Page{
		"WordPress \"critical error\" screen": {Status: 500, Text: "There has been a critical error on this website. Learn more about troubleshooting WordPress."},
		"Application error page":              {Status: 200, TextLength: 50, Text: "Whoops, looks like something went wrong."},
		"Database error on the page":          {Status: 200, TextLength: 40, Text: "Error establishing a database connection"},
		"Blank page (white screen)":           {Status: 200},
		"Server error 503":                    {Status: 503, Text: "busy"},
		"Page not found (404)":                {Status: 404, Text: "nope"},
	}
	for want, p := range cases {
		Module{}.CheckPage(ctx(), p)
		if _, ok := titles(p)[want]; !ok {
			t.Errorf("%q not found in %v", want, p.Issues)
		}
	}
}

func TestRequestsConsoleAndHygiene(t *testing.T) {
	p := &model.Page{Status: 200, Title: "", TextLength: 500, FinalURL: "https://shop.example/cart/", Robots: "noindex, follow",
		Console: []string{"Uncaught ReferenceError: jQuery is not defined (https://shop.example/wp-content/plugins/x/app.js?ver=1.2:14)", "Order 12345 failed"},
		Failed: []model.Request{
			{URL: "https://shop.example/wp-admin/admin-ajax.php", Method: "POST", Type: "XHR", Status: 500},
			{URL: "https://shop.example/wp-content/uploads/hero.jpg", Type: "Image", Status: 404},
			{URL: "https://cdn.example/lib.js", Type: "Script", Error: "net::ERR_NAME_NOT_RESOLVED"},
			{URL: "https://shop.example/favicon.ico", Type: "Other", Status: 404},
		},
		Resources: []model.Resource{{URL: "http://shop.example/logo.png"}},
		Shots:     map[string]model.Shot{"mobile": {Width: 980}},
	}
	Module{}.CheckPage(ctx(), p)
	got := titles(p)
	for _, want := range []string{
		"JS error: Uncaught ReferenceError: jQuery is not defined",
		"JS error: Order N failed",
		"POST /wp-admin/admin-ajax.php returned 500",
		"Broken image: /wp-content/uploads/hero.jpg (404)",
		"Request failed (ERR_NAME_NOT_RESOLVED): https://cdn.example/lib.js",
		"Insecure (http://) resource on an https page",
		"Page has no <title>",
		"Page is set to noindex",
		"Page renders 980 px wide on a phone",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q; got %v", want, keys(got))
		}
	}
	if _, ok := got["No favicon"]; ok {
		t.Error("a missing favicon is reported for the site, not on the page that asked first")
	}
	if js := got["JS error: Uncaught ReferenceError: jQuery is not defined"]; js.Source != "https://shop.example/wp-content/plugins/x/app.js:14" {
		t.Errorf("js source = %q", js.Source)
	}
	if ajax := got["POST /wp-admin/admin-ajax.php returned 500"]; ajax.Level != model.Error {
		t.Errorf("a 500 from admin-ajax is an error: %+v", ajax)
	}
}

func TestCleanPageHasNoIssues(t *testing.T) {
	p := &model.Page{Status: 200, Title: "Home", TextLength: 2000, Text: "Welcome to our shop. Warning: prices may change.",
		FinalURL: "https://shop.example/", Shots: map[string]model.Shot{"mobile": {Width: 390}}}
	Module{}.CheckPage(ctx(), p)
	if len(p.Issues) != 0 {
		t.Errorf("clean page got %v", p.Issues)
	}
}

func TestCompare(t *testing.T) {
	before := &model.Run{Pages: []model.Page{{Path: "/a", Status: 200, Title: "A", TextLength: 1000, FinalURL: "https://x/a"}}}
	after := &model.Run{Pages: []model.Page{{Path: "/a", Status: 200, Title: "A new", TextLength: 100, FinalURL: "https://x/login"}}}
	changes := Module{}.Compare(context.Background(), &check.CompareContext{Before: before, After: after})
	var got []string
	for _, c := range changes {
		got = append(got, c.Check)
	}
	if len(changes) != 3 {
		t.Errorf("changes = %v", got)
	}
}

func keys(m map[string]model.Issue) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestInlineScriptErrorsGroup(t *testing.T) {
	cc := &check.Context{}
	var keys []string
	for i, path := range []string{"/", "/about/"} {
		u := "http://x.test" + path
		p := &model.Page{URL: u, FinalURL: u, Path: path, Status: 200, TextLength: 100,
			Console: []string{fmt.Sprintf("Uncaught TypeError: Cannot read properties of undefined (reading 'init') (%s:%d)", u, 200+i*80)}}
		Module{}.CheckPage(cc, p)
		for _, is := range p.Issues {
			if is.Check == "health/js-error" {
				keys = append(keys, is.Key)
				if is.Source != "inline script" {
					t.Errorf("source = %q", is.Source)
				}
			}
		}
	}
	if len(keys) != 2 || keys[0] != keys[1] {
		t.Errorf("the same inline error on two pages must share a key: %v", keys)
	}
}

func TestWhiteScreenIgnoresFavicon(t *testing.T) {
	p := &model.Page{URL: "http://x.test/a/", Path: "/a/", Status: 200, Resources: []model.Resource{
		{URL: "http://x.test/wp-includes/images/w-logo-blue-white-bg.png", Type: "Image", Status: 200},
	}}
	Module{}.CheckPage(&check.Context{}, p)
	if len(p.Issues) == 0 || p.Issues[0].Check != "health/white-screen" {
		t.Errorf("a blank WordPress page must be a white screen: %+v", p.Issues)
	}
	p = &model.Page{URL: "http://x.test/g/", Path: "/g/", Status: 200, Resources: []model.Resource{{URL: "http://x.test/photo.jpg", Type: "Image", Status: 200}}}
	Module{}.CheckPage(&check.Context{}, p)
	for _, i := range p.Issues {
		if i.Check == "health/white-screen" {
			t.Error("a page showing an image isn't blank")
		}
	}
}

func TestCheckSiteLinks(t *testing.T) {
	var cartHit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Has("add-to-cart"):
			cartHit.Store(true)
		case r.URL.Path == "/about/", r.URL.Path == "/":
			return // 200; note /about without the slash would 404
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL + "/")
	run := &model.Run{Pages: []model.Page{{URL: srv.URL + "/", Path: "/", Status: 200,
		Links: []string{srv.URL + "/about/", srv.URL + "/gone/", srv.URL + "/shop/?add-to-cart=12", srv.URL + "/private/x/"}}}}
	cfg := config.Default()
	cfg.Pages.Exclude = []string{"/private/*"}
	cc := &check.Context{Config: cfg, Base: base, Run: run, HTTP: srv.Client(), Hosts: ratelimit.NewHosts(0)}
	Module{}.CheckSite(context.Background(), cc)
	var titles []string
	for _, i := range run.Issues {
		titles = append(titles, i.Title)
	}
	if len(titles) != 1 || titles[0] != "Broken link to /gone/ (404)" {
		t.Errorf("issues = %v; want only the missing /gone/, keeping its slash", titles)
	}
	if len(run.Issues) == 1 && run.Issues[0].Detail != "" {
		t.Errorf("every link was checked, yet the detail says %q", run.Issues[0].Detail)
	}
	if cartHit.Load() {
		t.Error("an add-to-cart link must never be requested")
	}
}

// A big site has more links than CheckSite requests; a broken one says
// how many were checked, so the rest aren't taken for fine.
func TestCheckSiteLinksSampled(t *testing.T) {
	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.URL.Path == "/a/0007/" {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL + "/")
	home := model.Page{URL: srv.URL + "/", Path: "/", Status: 200}
	for i := range 1240 {
		home.Links = append(home.Links, fmt.Sprintf("%s/a/%04d/", srv.URL, i))
	}
	run := &model.Run{Pages: []model.Page{home}}
	cc := &check.Context{Config: config.Default(), Base: base, Run: run, HTTP: srv.Client(), Hosts: ratelimit.NewHosts(0)}
	Module{}.CheckSite(context.Background(), cc)
	if len(run.Issues) != 1 || run.Issues[0].Title != "Broken link to /a/0007/ (404)" {
		t.Fatalf("issues = %+v", run.Issues)
	}
	if got, want := run.Issues[0].Detail, "Checked 300 of 1,240 links found on the visited pages; the rest weren't requested."; got != want {
		t.Errorf("detail = %q, want %q", got, want)
	}
	if n := asked.Load(); n != 300 {
		t.Errorf("requested %d links, want 300", n)
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 300: "300", 1240: "1,240", 999999: "999,999", 1234567: "1,234,567", -1240: "-1,240"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}

// favicon404 is a page whose visit the browser used for its one
// /favicon.ico request of the session, which got a 404.
func favicon404(path string) model.Page {
	return model.Page{URL: "https://shop.example" + path, Path: path, Status: 200, Title: "T", TextLength: 500,
		Failed: []model.Request{{URL: "https://shop.example/favicon.ico", Type: "Other", Status: 404}}}
}

func siteRun(pages ...model.Page) *model.Run {
	run := &model.Run{Pages: pages}
	cc := ctx()
	cc.Run = run
	cc.Config = config.Default()
	for i := range run.Pages {
		Module{}.CheckPage(cc, &run.Pages[i])
	}
	Module{}.CheckSite(context.Background(), cc)
	return run
}

func TestFaviconIsSiteLevel(t *testing.T) {
	plain := func(path string) model.Page {
		return model.Page{URL: "https://shop.example" + path, Path: path, Status: 200, Title: "T", TextLength: 500}
	}
	// The same site visited in a different order: the favicon request
	// fails on whichever page asked first.
	before := siteRun(favicon404("/"), plain("/about/"), plain("/shop/"))
	after := siteRun(plain("/shop/"), favicon404("/about/"), plain("/"))

	var favicons []model.Issue
	for _, i := range after.Issues {
		if i.Check == "health/favicon" {
			favicons = append(favicons, i)
		}
	}
	if len(favicons) != 1 || favicons[0].Level != model.Notice || favicons[0].Title != "No favicon" ||
		favicons[0].Source != "/favicon.ico" || !slices.Equal(favicons[0].Pages, []string{"/about/"}) {
		t.Errorf("want one site-level notice, got %+v", favicons)
	}
	for _, p := range after.Pages {
		for _, i := range p.Issues {
			if i.Check == "health/favicon" {
				t.Errorf("%s has a page-level favicon issue", p.Path)
			}
		}
	}
	if cs := compare.Issues(before, after); len(cs) != 0 {
		t.Errorf("an unchanged site visited in another order must have no changes: %+v", cs)
	}
}

func TestFaviconPresent(t *testing.T) {
	ok := model.Page{URL: "https://shop.example/", Path: "/", Status: 200, Title: "T", TextLength: 500,
		Resources: []model.Resource{{URL: "https://shop.example/favicon.ico", Type: "Other", Status: 200}}}
	if run := siteRun(ok); len(run.Issues) != 0 || len(run.Pages[0].Issues) != 0 {
		t.Errorf("a site with a favicon has no issue: %+v %+v", run.Issues, run.Pages[0].Issues)
	}
}

func TestFilterChangesFaviconMoved(t *testing.T) {
	// A snapshot from before the favicon issue was site-level has it on a
	// page; the new run has it on the site. That is the same issue.
	before := &model.Run{Pages: []model.Page{favicon404("/")}}
	i := model.Issue{Check: "health/favicon", Level: model.Notice, Title: "No favicon", Source: "/favicon.ico"}
	i.Identify()
	before.Pages[0].Issues = []model.Issue{i}
	after := siteRun(favicon404("/"))
	other := model.Change{Class: model.New, Check: "health/js-error", Title: "JS error: x"}
	cs := append(compare.Issues(before, after), other)
	if got := (Module{}).FilterChanges(cs); len(got) != 1 || got[0].Check != "health/js-error" {
		t.Errorf("filtered = %+v", got)
	}
	// A favicon that really went missing stays NEW.
	gone := []model.Change{{Class: model.New, Check: "health/favicon", Title: "No favicon", Source: "/favicon.ico"}}
	if got := (Module{}).FilterChanges(gone); len(got) != 1 {
		t.Errorf("a new missing favicon must stay: %+v", got)
	}
}

func TestLostTitleIsOneLine(t *testing.T) {
	page := func(title string) model.Page {
		return model.Page{Path: "/about/", URL: "https://shop.example/about/", FinalURL: "https://shop.example/about/", Status: 200, Title: title, TextLength: 500}
	}
	before, after := siteRun(page("About us")), siteRun(page(""))
	cc := &check.CompareContext{Before: before, After: after}
	all := append(compare.Issues(before, after), Module{}.Compare(context.Background(), cc)...)
	if len(all) != 1 || all[0].Class != model.New || all[0].Title != "Page has no <title>" {
		t.Errorf("a lost title must be one NEW line: %+v", all)
	}
	// A title that really changed is still CHANGED, with nothing NEW.
	after = siteRun(page("About the shop"))
	cc.After = after
	all = append(compare.Issues(before, after), Module{}.Compare(context.Background(), cc)...)
	if len(all) != 1 || all[0].Check != "health/title" || all[0].Before != "About us" || all[0].After != "About the shop" {
		t.Errorf("a changed title = %+v", all)
	}
}
