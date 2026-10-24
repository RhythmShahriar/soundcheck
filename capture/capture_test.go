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
	opts := Options{Timeout: 20 * time.Second, Ignore: []string{"h1"}, Twice: true,
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
