// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/runner"
)

const writesPage = `<!doctype html><html><head><title>Poll</title></head><body>
<main>
  <p id="out">idle</p>
  <button type="button" id="note">Save note</button>
  <button type="button" id="rate">Rate it</button>
  <button type="button" id="ping">Track view</button>
  <button type="button" id="go">Next step</button>
  <button type="button" id="fresh">Refresh stock</button>
  <button type="button" id="like">Like</button>
  <form id="f" method="post" action="/api/form" style="display:none"><input name="a" value="1"></form>
</main>
<script>
  const out = document.getElementById('out');
  document.getElementById('note').addEventListener('click', () => { fetch('/api/note', {method: 'POST', body: 'x'}); });
  document.getElementById('rate').addEventListener('click', () => { const x = new XMLHttpRequest(); x.open('POST', '/api/rate'); x.send('5'); });
  document.getElementById('ping').addEventListener('click', () => { navigator.sendBeacon('/api/beacon', 'v'); });
  document.getElementById('go').addEventListener('click', () => { document.getElementById('f').submit(); });
  document.getElementById('fresh').addEventListener('click', async () => { const r = await fetch('/api/fresh'); out.textContent = await r.text(); });
  document.getElementById('like').addEventListener('click', () => { fetch('/api/like', {method: 'POST'}); });
</script></body></html>`

// Clicks explore without changing anything: whatever they would post is
// held and listed, GET requests still go out and the submit level
// still posts its test form.
func TestClicksHoldWrites(t *testing.T) {
	needBrowser(t)
	fx := newFixture(t)
	var mu sync.Mutex
	var writes []string
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mu.Lock()
			writes = append(writes, r.Method+" "+r.URL.Path)
			mu.Unlock()
			return
		}
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, writesPage)
		case "/api/fresh":
			mu.Lock()
			gets++
			mu.Unlock()
			fmt.Fprint(w, "12 in stock")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	fx.srv.Config.Handler = srv.Config.Handler

	cfg := config.Default()
	cfg.Interactions.Levels = []string{"click"}
	p := fx.run(t, cfg, "/").Page("/")
	if p == nil || p.Error != "" {
		t.Fatalf("page: %+v", p)
	}
	mu.Lock()
	if len(writes) != 0 {
		t.Errorf("clicks sent %v", writes)
	}
	if gets == 0 {
		t.Error("the GET button's request never went out")
	}
	mu.Unlock()
	details := map[string]string{}
	for _, i := range p.Interact {
		if i.Kind == "click" {
			details[i.Label] = i.Outcome + ": " + i.Detail
		}
	}
	for label, want := range map[string]string{
		"Save note":  "ok: would have sent POST /api/note",
		"Rate it":    "ok: would have sent POST /api/rate",
		"Track view": "ok: would have sent POST /api/beacon",
		"Next step":  "ok: would have sent POST /api/form",
		"Like":       "skipped: looks destructive",
	} {
		if details[label] != want {
			t.Errorf("%s: %q, want %q", label, details[label], want)
		}
	}
	if d := details["Refresh stock"]; !strings.HasPrefix(d, "ok: ") || strings.Contains(d, "would have sent") {
		t.Errorf("Refresh stock: %q", d)
	}
	for _, i := range p.Issues {
		if strings.HasPrefix(i.Check, "interact/click") || i.Check == "interact/dead-click" {
			t.Errorf("held request reported as a problem: %s (%s)", i.Title, i.Detail)
		}
	}
}

// With click and submit levels both on, the hold ends with the clicks:
// the submit level still posts its test form.
func TestSubmitAfterClicks(t *testing.T) {
	needBrowser(t)
	fx := newFixture(t)
	cfg := config.Default()
	cfg.Interactions.Levels = []string{"click", "submit"}
	p := fx.run(t, cfg, "/contact/").Page("/contact/")
	if got := outcomes(p, "submit")["Contact Form 7 #42"]; got != "success" {
		t.Errorf("submit outcome %q (%+v)", got, p.Interact)
	}
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if len(fx.posts) != 2 {
		t.Errorf("posts: %q", fx.posts)
	}
}

// A click whose handler posts later (a timer or a debounce) can't send
// once the hold ends: the page is reloaded while writes are still held.
func TestClicksHoldDelayedWrites(t *testing.T) {
	needBrowser(t)
	fx := newFixture(t)
	var mu sync.Mutex
	var writes []string
	loads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writes = append(writes, r.Method+" "+r.URL.Path)
			mu.Unlock()
			return
		}
		loads++
		n := loads
		mu.Unlock()
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// Every load after the first is slow, so the clicked page is
		// still open when its timer fires.
		if n > 1 {
			time.Sleep(2500 * time.Millisecond)
		}
		fmt.Fprint(w, `<!doctype html><title>Later</title><main><button type="button" id="later">Save later</button></main>
<script>document.getElementById('later').addEventListener('click', () => setTimeout(() => fetch('/api/later', {method: 'POST'}), 1800));</script>`)
	}))
	defer srv.Close()
	fx.srv.Config.Handler = srv.Config.Handler

	// The mobile pass loads the page again after the clicks: the window
	// a timer could post in.
	cfg := config.Default()
	cfg.Interactions.Levels, cfg.Modules, cfg.Rate, cfg.Tabs = []string{"click"}, []string{"interact"}, 0, 1
	run, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: fx.srv.URL, Paths: []string{"/"},
		Dir: t.TempDir(), Version: "test", Modules: []check.Module{New()}, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	p := run.Page("/")
	if p == nil || p.Error != "" {
		t.Fatalf("page: %+v", p)
	}
	if got := outcomes(p, "click")["Save later"]; got == "" {
		t.Fatalf("Save later wasn't clicked: %+v", p.Interact)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 0 {
		t.Errorf("a delayed write went out after the clicks: %v", writes)
	}
}
