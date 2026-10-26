// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/capture/webdriver"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

func needFirefox(t *testing.T) {
	t.Helper()
	if _, err := webdriver.FindFirefox(""); err != nil {
		t.Skip(err)
	}
}

// brokenSite has one page for each kind of health issue a browser
// reports: a PHP warning, a JS error, a broken image, a failed API call,
// a server error, a missing page and a redirect chain.
func brokenSite(t *testing.T) *httptest.Server {
	t.Helper()
	page := func(w http.ResponseWriter, title, body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width"><title>%s</title>`+
			`<link rel="icon" href="data:,"></head><body><nav><a href="/">Home</a> <a href="/js/">JS</a> <a href="/img/">Img</a> `+
			`<a href="/api/">API</a> <a href="/crit/">Crit</a> <a href="/gone/">Gone</a> <a href="/r1/">Old</a></nav>%s</body></html>`, title, body)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			page(w, "Brew", `<p>Coffee</p><br /><b>Warning</b>:  Undefined array key "id" in <b>/var/www/html/wp-content/themes/brew/functions.php</b> on line <b>212</b><br />`)
		case "/js/":
			page(w, "JS", `<p>Shop</p><script>jQuery('.cart').show();</script>`)
		case "/img/":
			page(w, "Img", `<p>Gallery</p><img src="/missing.png">`)
		case "/api/":
			page(w, "API", `<p>Cart</p><script>fetch("/wp-json/cart");</script>`)
		case "/crit/":
			w.WriteHeader(http.StatusInternalServerError)
			page(w, "Error", `<p>There has been a critical error on this website.</p>`)
		case "/r1/":
			http.Redirect(w, r, "/r2/", http.StatusMovedPermanently)
		case "/r2/":
			http.Redirect(w, r, "/r3/", http.StatusFound)
		case "/r3/":
			http.Redirect(w, r, "/landing/", http.StatusMovedPermanently)
		case "/landing/":
			page(w, "Landing", `<p>Welcome back</p>`)
		case "/wp-json/cart":
			http.Error(w, "nope", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
			page(w, "Not found", `<p>Nothing here</p>`)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// healthIssues runs health on srv in engine and returns its issues as
// "path check title" lines.
func healthIssues(t *testing.T, srv *httptest.Server, engine capture.Engine) ([]string, *model.Run) {
	t.Helper()
	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 2
	cfg.Viewports = []string{"desktop", "mobile"}
	cfg.Modules = []string{"health"}
	// shooter{"shots", ...}: screenshots, without the visual module.
	run, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Dir: t.TempDir(), Version: "test",
		Engine: engine, Modules: []check.Module{health.Module{}, shooter{"shots", true, false}}, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatalf("%s: %v", engine, err)
	}
	var out []string
	for _, p := range run.Pages {
		for _, i := range p.Issues {
			out = append(out, p.Path+" "+i.Check+" "+i.Title)
		}
	}
	for _, i := range run.Issues {
		out = append(out, "site "+i.Check+" "+i.Title)
	}
	slices.Sort(out)
	return out, run
}

// The same site gives the same health issues in Chrome and in Firefox:
// the checks read what the engines report the same way.
func TestEnginesFindTheSameHealthIssues(t *testing.T) {
	needBrowser(t)
	needFirefox(t)
	srv := brokenSite(t)
	chrome, chromeRun := healthIssues(t, srv, capture.Chrome)
	if chromeRun.Options.Engine != "" {
		t.Errorf("a Chrome run stored engine %q", chromeRun.Options.Engine)
	}
	firefox, run := healthIssues(t, srv, capture.Firefox)
	if !slices.Equal(chrome, firefox) {
		t.Errorf("issues differ\nchrome:\n  %s\nfirefox:\n  %s", strings.Join(chrome, "\n  "), strings.Join(firefox, "\n  "))
	}
	for _, want := range []string{
		"/ health/php-error PHP Warning: Undefined array key \"id\"",
		"/js/ health/js-error JS error: Uncaught ReferenceError: jQuery is not defined",
		"/img/ health/broken-asset Broken image: /missing.png (404)",
		"/api/ health/failed-request GET /wp-json/cart returned 500",
		"/crit/ health/status Server error 500",
		"/gone/ health/status Page not found (404)",
		"/r1/ health/redirect-chain Redirects 3 times before loading",
	} {
		if !slices.Contains(firefox, want) {
			t.Errorf("firefox is missing %q in\n  %s", want, strings.Join(firefox, "\n  "))
		}
	}
	// Shots are named by engine, so they're never compared across engines.
	for _, p := range run.Pages {
		for vp, s := range p.Shots {
			if !strings.HasSuffix(s.File, "-"+vp+"-firefox.png") {
				t.Errorf("%s %s shot is %s", p.Path, vp, s.File)
			}
		}
		if p.Path == "/" && (p.Shots["mobile"].Width != 390 || p.Shots["desktop"].Width != 1366) {
			t.Errorf("shots of / are %+v", p.Shots)
		}
	}
}

// In Firefox, modules that need Chrome are skipped with a warning, as is
// the login; modules written against check.Tab run.
func TestChromeOnlyModulesSkipOnOtherEngines(t *testing.T) {
	needFirefox(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Brew</title><script>console.error("boom", 42)</script><h1>Hi</h1>`)
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Tabs = []string{"health", "a11y", "mutates", "tabmod"}, []string{"desktop"}, 0, 1
	rec := &recorder{}
	tm := &tabMod{rec: rec}
	run, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Paths: []string{"/"}, Dir: t.TempDir(),
		Version: "test", Engine: capture.Firefox, Modules: []check.Module{a11y.Module{}, liveMod{"mutates", check.PhaseMutate, rec}, tm},
		Login: func(*url.URL) func(context.Context, *rod.Browser) error {
			return func(context.Context, *rod.Browser) error { t.Error("logged in"); return nil }
		}})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Pages) != 1 || run.Pages[0].Error != "" {
		t.Fatalf("the page wasn't checked in Firefox: %+v (warnings %q)", run.Pages, run.Warnings)
	}
	if !slices.Equal(rec.calls, []string{"tabmod"}) {
		t.Errorf("hooks ran %v, want only tabmod", rec.calls)
	}
	if tm.title != "no rod page" || !tm.png {
		t.Errorf("title %q, png %v", tm.title, tm.png)
	}
	tm.mu.Lock()
	events := slices.Clone(tm.events)
	tm.mu.Unlock()
	if !slices.Contains(events, "error: boom 42") {
		t.Errorf("console events = %q", events)
	}
	for _, w := range []string{
		"mutates was skipped in firefox: it needs Chrome",
		"the login was skipped in firefox: logging in needs Chrome",
	} {
		if !slices.Contains(run.Warnings, w) {
			t.Errorf("no warning %q in %q", w, run.Warnings)
		}
	}
	// The page says a11y didn't audit it, so it isn't taken for a page
	// without accessibility problems.
	skipped := model.Audit{Status: model.AuditSkipped, Reason: "needs Chrome"}
	if a := run.Pages[0].Audits; !reflect.DeepEqual(a, map[string]model.Audit{"a11y": skipped, "mutates": skipped}) {
		t.Errorf("audits = %+v", a)
	}
	if slices.Contains(run.Options.Modules, "mutates") || !slices.Contains(run.Options.Modules, "tabmod") || run.Options.LoggedIn ||
		run.Options.Engine != "firefox" {
		t.Errorf("run options: %+v", run.Options)
	}
}

func TestChromeFree(t *testing.T) {
	var warned []string
	kept, skipped := chromeFree([]check.Module{plain("health"), liveMod{name: "interact"}, &tabMod{}},
		capture.WebKit, func(s string) { warned = append(warned, s) })
	var names []string
	for _, m := range kept {
		names = append(names, m.Name())
	}
	if !slices.Equal(names, []string{"health", "tabmod"}) || !slices.Equal(skipped, []string{"interact"}) {
		t.Errorf("kept %v, skipped %v", names, skipped)
	}
	if !slices.Equal(warned, []string{"interact was skipped in webkit: it needs Chrome"}) {
		t.Errorf("warned %q", warned)
	}
	if cmpEngine("") != capture.Chrome || cmpEngine(capture.Firefox) != capture.Firefox {
		t.Error("cmpEngine")
	}
	if _, err := launchDriver(context.Background(), "lynx", browser.Options{}); err == nil {
		t.Error("launched an unknown engine")
	}
}

// A page records the modules left out for needing Chrome as skipped;
// a Chrome run records nothing for them.
func TestSkippedAudits(t *testing.T) {
	p := &model.Page{Path: "/"}
	skippedAudits(p, nil)
	if p.Audits != nil {
		t.Errorf("audits without skipped modules = %+v", p.Audits)
	}
	p.Audits = map[string]model.Audit{"seo": {Status: model.AuditOK}}
	skippedAudits(p, []string{"a11y", "keyboard"})
	want := map[string]model.Audit{"seo": {Status: model.AuditOK},
		"a11y":     {Status: model.AuditSkipped, Reason: "needs Chrome"},
		"keyboard": {Status: model.AuditSkipped, Reason: "needs Chrome"}}
	if !reflect.DeepEqual(p.Audits, want) {
		t.Errorf("audits = %+v", p.Audits)
	}
}
