// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
	"github.com/RhythmShahriar/soundcheck/runner"
)

func needChrome(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// eaaReport writes the HTML report of an --eaa comparison with a trace
// and a crop and returns it.
func eaaReport(t *testing.T) []byte {
	t.Helper()
	r := eaaComparison()
	r.Dir = eaaDir(t, r.Run, 12)
	var b bytes.Buffer
	if err := HTML(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestReportOffline opens the report from disk in Chrome with the
// network blocked and every request logged: it makes none, its CSP is
// the one it was written with and its own script runs under it.
func TestReportOffline(t *testing.T) {
	needChrome(t)
	path := filepath.Join(t.TempDir(), "report.html")
	if err := os.WriteFile(path, eaaReport(t), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	page := b.Timeout(testenv.Timeout(30 * time.Second)).MustPage("")
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests []string
	go page.EachEvent(func(e *proto.NetworkRequestWillBeSent) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, e.Request.URL)
	})()
	// Anything but the file itself fails, as offline.
	router := page.HijackRequests()
	router.MustAdd("*", func(h *rod.Hijack) {
		if strings.HasPrefix(h.Request.URL().String(), "file:") {
			h.ContinueRequest(&proto.FetchContinueRequest{})
			return
		}
		h.Response.Fail(proto.NetworkErrorReasonInternetDisconnected)
	})
	go router.Run()
	t.Cleanup(router.MustStop)
	page.MustEvalOnNewDocument(`window.__csp = []; document.addEventListener("securitypolicyviolation", function (e) { window.__csp.push(e.violatedDirective + " " + e.blockedURI); });`)
	// A Windows path (C:\Users\...) becomes /C:/Users/... in a file URL.
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	file := (&url.URL{Scheme: "file", Path: slashed}).String()
	page.MustNavigate(file).MustWaitLoad()

	page.MustElement("#tab-a11y").MustClick()
	if page.MustElement("#panel-a11y").MustAttribute("hidden") != nil {
		t.Fatal("the Accessibility tab didn't open: the report's script didn't run")
	}
	for _, d := range page.MustElements("details") {
		d.MustEval(`() => { this.open = true; }`)
	}
	page.MustElement("#theme").MustClick()
	page.MustWaitIdle()
	if got := page.MustEval(`() => document.querySelector('meta[http-equiv="Content-Security-Policy"]').content`).String(); got != CSP {
		t.Errorf("CSP in the page = %q, want %q", got, CSP)
	}
	if v := page.MustEval(`() => window.__csp.join("|")`).String(); v != "" {
		t.Errorf("CSP violations: %s", v)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, u := range requests {
		if u != file && !strings.HasPrefix(u, "data:") {
			t.Errorf("the report made a request: %s", u)
		}
	}
}

// serveReport serves report variants on the first free port of
// 127.0.0.1:37080-37089 and 404 for anything else.
func serveReport(t *testing.T, pages map[string][]byte) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	})
	for port := 37080; port < 37090; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv := httptest.NewUnstartedServer(h)
		_ = srv.Listener.Close()
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	t.Fatal("no free port in 37080-37089")
	return nil
}

// TestReportPassesA11y runs Soundcheck's own accessibility module on
// the report, both tabs, in the light and the dark theme: it finds
// nothing.
func TestReportPassesA11y(t *testing.T) {
	needChrome(t)
	html := eaaReport(t)
	// Both tabs and every closed section open, so axe sees all of it.
	opened := strings.NewReplacer(` role="tabpanel" aria-labelledby="tab-a11y" hidden>`, ` role="tabpanel" aria-labelledby="tab-a11y">`,
		`<details class="kb">`, `<details class="kb" open>`, `<details class="pages">`, `<details class="pages" open>`,
		`<details class="allpages">`, `<details class="allpages" open>`, `<details class="notes">`, `<details class="notes" open>`).Replace(string(html))
	dark := strings.Replace(opened, `<html lang="en">`, `<html lang="en" data-theme="dark">`, 1)
	srv := serveReport(t, map[string][]byte{"/": html, "/open/": []byte(opened), "/dark/": []byte(dark)})

	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 1
	cfg.Viewports = []string{"desktop", "mobile"}
	cfg.Modules = []string{"a11y"}
	run, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: srv.URL, Paths: []string{"/", "/open/", "/dark/"},
		Dir: t.TempDir(), Version: "test", Modules: []check.Module{a11y.Module{}}, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Pages) != 3 {
		t.Fatalf("checked %d pages, want 3: %v", len(run.Pages), run.Warnings)
	}
	for _, p := range run.Pages {
		if p.Error != "" {
			t.Errorf("%s: %s", p.Path, p.Error)
		}
		for _, i := range p.Issues {
			if strings.HasPrefix(i.Check, "a11y/") {
				t.Errorf("%s: %s %s [%s]: %s", p.Path, i.Check, i.Title, i.Source, i.Detail)
			}
		}
	}
}
