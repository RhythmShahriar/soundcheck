// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/model"
)

// launch starts a real browser, or skips when none is installed.
func launch(t *testing.T) *browser.Browser {
	t.Helper()
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

func fixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/page", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noarchive")
		fmt.Fprint(w, `<!doctype html><html><head><title> Shop </title><meta name="robots" content="index,follow"><meta name="viewport" content="width=device-width, initial-scale=1"></head><body>
<h1>Hello</h1><p>Some text.</p>
<img src="/missing.png">
<a href="/about/">About</a> <a href="https://other.example/">Other</a>
<script>
console.error("checkout widget failed");
fetch("/api/cart").then(r => r.text());
setTimeout(() => { undefinedFunction(); }, 10);
</script>
<div style="height:3000px">tall</div>
</body></html>`)
	})
	mux.HandleFunc("/api/cart", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) })
	mux.HandleFunc("/missing.png", http.NotFound)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestVisit(t *testing.T) {
	b := launch(t)
	srv := fixture(t)
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := sess.Page(protoBlank())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dir := t.TempDir()
	var mu sync.Mutex
	// 40 s gives reading the page a 10 s budget (readBudget): two tall
	// desktop screenshots 1.2 s apart can take more than the 5 s a 20 s
	// timeout gives on a Windows runner. The second is then dropped.
	opts := Options{Timeout: 40 * time.Second, Ignore: []string{"h1"}, Twice: true,
		Save: func(name string, data []byte) error {
			mu.Lock()
			defer mu.Unlock()
			p := filepath.Join(dir, name)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			return os.WriteFile(p, data, 0o600)
		}}
	rec, err := Start(ctx, tab, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Stop()
	p, err := Visit(ctx, rec, srv.URL+"/old", opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := Mobile(ctx, rec, p, srv.URL+"/old", opts); err != nil {
		t.Fatal(err)
	}

	if p.Status != 200 || p.Title != "Shop" || !strings.HasSuffix(p.FinalURL, "/page") {
		t.Errorf("status %d title %q final %q", p.Status, p.Title, p.FinalURL)
	}
	if len(p.Redirects) != 1 || !strings.HasPrefix(p.Redirects[0], "301 ") {
		t.Errorf("redirects = %v", p.Redirects)
	}
	if !strings.Contains(p.Robots, "index,follow") || !strings.Contains(p.Robots, "noarchive") {
		t.Errorf("robots = %q", p.Robots)
	}
	joined := strings.Join(p.Console, "\n")
	if !strings.Contains(joined, "checkout widget failed") || !strings.Contains(joined, "undefinedFunction") {
		t.Errorf("console = %v", p.Console)
	}
	var failed []string
	for _, f := range p.Failed {
		failed = append(failed, fmt.Sprintf("%d %s", f.Status, f.URL[len(srv.URL):]))
	}
	if !slices.Contains(failed, "404 /missing.png") || !slices.Contains(failed, "500 /api/cart") {
		t.Errorf("failed = %v", failed)
	}
	if !slices.Contains(p.Links, srv.URL+"/about/") || slices.Contains(p.Links, "https://other.example/") {
		t.Errorf("links = %v", p.Links)
	}
	if p.Metrics.TTFB <= 0 || p.Metrics.Requests < 2 || p.TextLength == 0 {
		t.Errorf("metrics = %+v text %d", p.Metrics, p.TextLength)
	}
	d, m := p.Shots["desktop"], p.Shots["mobile"]
	if d.Width != 1366 || d.Height < 3000 || m.Width != 390 || d.Second == "" || len(d.Masked) != 1 {
		t.Errorf("shots: desktop %+v mobile %+v", d, m)
	}
	for _, f := range []string{d.File, d.Second, m.File} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
	_ = model.Schema
}

func TestVisitUnreachable(t *testing.T) {
	b := launch(t)
	sess, _ := b.Session()
	tab, _ := sess.Page(protoBlank())
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	rec, _ := Start(context.Background(), tab, Options{})
	defer rec.Stop()
	p, err := Visit(context.Background(), rec, srv.URL+"/x", Options{Timeout: 10 * time.Second})
	if err != nil || !strings.Contains(p.Error, "ERR_CONNECTION_REFUSED") {
		t.Errorf("error = %q, %v", p.Error, err)
	}
}

// A password-protected page fails to navigate in Chrome; the page still
// records the 401 and its WWW-Authenticate header.
func TestVisitPasswordPrompt(t *testing.T) {
	b := launch(t)
	sess, _ := b.Session()
	tab, _ := sess.Page(protoBlank())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Staging"`)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "<title>401</title>Password required")
	}))
	defer srv.Close()
	rec, _ := Start(context.Background(), tab, Options{})
	defer rec.Stop()
	p, err := Visit(context.Background(), rec, srv.URL+"/x", Options{Timeout: 10 * time.Second})
	if err != nil || p.Error == "" || p.Status != http.StatusUnauthorized || p.Headers["www-authenticate"] != `Basic realm="Staging"` {
		t.Errorf("error %q status %d headers %v, %v", p.Error, p.Status, p.Headers, err)
	}
}

// A section a script fetches 1.5 s after the page started is waited for:
// its broken image is recorded. Timers set after load (a carousel's next
// slide) and long ones don't hold the visit up.
func TestVisitWaitsForLateContent(t *testing.T) {
	b := launch(t)
	sess, _ := b.Session()
	mux := http.NewServeMux()
	mux.HandleFunc("/part", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<h2>Team</h2><img src="/missing.png" width="10" height="10">`)
	})
	mux.HandleFunc("/missing.png", http.NotFound)
	mux.HandleFunc("/late", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Late</title><div id="late">Loading</div><script>
setTimeout(() => fetch('/part').then(r => r.text()).then(h => { document.getElementById('late').innerHTML = h; }), 1500);
</script>`)
	})
	mux.HandleFunc("/busy", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>Busy</title><p>Slides.</p><script>
addEventListener('load', () => { const next = () => setTimeout(next, 200); next(); });
setTimeout(() => {}, 60000);
</script>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	opts := Options{Timeout: 20 * time.Second}
	visit := func(path string) *model.Page {
		tab, _ := sess.Page(protoBlank())
		rec, err := Start(context.Background(), tab, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer rec.Stop()
		p, err := Visit(context.Background(), rec, srv.URL+path, opts)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := visit("/late")
	if !slices.ContainsFunc(p.Failed, func(r model.Request) bool { return strings.HasSuffix(r.URL, "/missing.png") && r.Status == 404 }) {
		t.Errorf("the late image wasn't recorded: failed %+v", p.Failed)
	}
	if p = visit("/busy"); p.Duration > 6000 {
		t.Errorf("a timer set after load or a long one held the visit up: %.0f ms", p.Duration)
	}
}

// slowFixture answers /slow?wait=N after N seconds. Its /busy is a page
// whose script blocks the main thread for 12 seconds right after load.
// The block starts from a MessageChannel task queued by the load handler:
// a setTimeout can be throttled in a background tab and start after the
// read is done, while a blocking load handler would hold back load.
func slowFixture(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
		select {
		case <-time.After(time.Duration(wait) * time.Second):
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `<!doctype html><html><head><title>Slow</title></head><body><p>Worth the wait.</p></body></html>`)
	})
	mux.HandleFunc("/busy", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><html><head><title>Busy</title></head><body><p>Busy.</p>
<script>addEventListener('load', () => {
  const c = new MessageChannel();
  c.port1.onmessage = () => { const t = Date.now(); while (Date.now() - t < 12000) {} };
  c.port2.postMessage(0);
});</script></body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestVisitSlow(t *testing.T) {
	b := launch(t)
	srv := slowFixture(t)
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Timeout: 5 * time.Second}
	visit := func(path string) (*model.Page, error) {
		t.Helper()
		tab, err := sess.Page(protoBlank())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tab.Close() }()
		rec, err := Start(context.Background(), tab, opts)
		if err != nil {
			t.Fatal(err)
		}
		defer rec.Stop()
		return Visit(context.Background(), rec, srv.URL+path, opts)
	}

	t.Run("answers just inside the timeout", func(t *testing.T) {
		p, err := visit("/slow?wait=4")
		if err != nil || p.Error != "" || p.Title != "Slow" || p.TextLength == 0 {
			t.Errorf("error %q (%v), title %q, text %d", p.Error, err, p.Title, p.TextLength)
		}
		if p.Duration < 4000 {
			t.Errorf("duration = %v ms", p.Duration)
		}
	})
	t.Run("answers after the timeout", func(t *testing.T) {
		p, err := visit("/slow?wait=7")
		if err != nil || p.Error != "timed out" {
			t.Errorf("error %q (%v)", p.Error, err)
		}
		if p.Duration < 4500 || p.Duration > 6500 {
			t.Errorf("duration = %v ms, want about 5 s", p.Duration)
		}
	})
	t.Run("can't be read after load", func(t *testing.T) {
		p, err := visit("/busy")
		if err == nil || p.Error != "timed out reading the page after load" {
			t.Errorf("error %q (%v)", p.Error, err)
		}
		if p.Status != 200 || p.Duration < 5000 {
			t.Errorf("status %d duration %v ms", p.Status, p.Duration)
		}
	})
}
