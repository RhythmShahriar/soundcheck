// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package intercept

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/browser"
)

// Several owners share one tab: each handler sees every request, a block
// wins and removing handlers one by one leaves the others working.
func TestHandlersShareATab(t *testing.T) {
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	var mu sync.Mutex
	got := map[string]string{} // path → "a|b" header values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.URL.Path] = r.Header.Get("X-A") + "|" + r.Header.Get("X-B")
		mu.Unlock()
		fmt.Fprint(w, `<!doctype html><title>t</title>`)
	}))
	defer srv.Close()
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := sess.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	tab = tab.Timeout(20 * time.Second)
	visit := func(path string) {
		t.Helper()
		_ = tab.Navigate(srv.URL + path)
		_ = tab.WaitLoad()
	}
	seen := func(path string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		v, ok := got[path]
		return v, ok
	}

	removeA, err := Add(tab, func(r *Request) { r.SetHeader("X-A", "1") })
	if err != nil {
		t.Fatal(err)
	}
	removeB, err := Add(tab, func(r *Request) {
		if r.Header("X-A") != "1" {
			t.Error("a later handler doesn't see an earlier one's header")
		}
		r.SetHeader("X-B", "2")
		if r.URL != nil && r.URL.Path == "/held" {
			r.Block()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	visit("/both")
	if v, _ := seen("/both"); v != "1|2" {
		t.Errorf("both handlers: %q", v)
	}
	visit("/held")
	if v, ok := seen("/held"); ok {
		t.Errorf("a blocked request reached the server: %q", v)
	}

	removeB()
	removeB() // twice is harmless
	visit("/a")
	if v, _ := seen("/a"); v != "1|" {
		t.Errorf("after removing b: %q", v)
	}
	removeA()
	visit("/none")
	if v, _ := seen("/none"); v != "|" {
		t.Errorf("after removing all: %q", v)
	}

	// A new owner after the last one left enables interception again.
	removeC, err := Add(tab, func(r *Request) { r.SetHeader("X-B", "3") })
	if err != nil {
		t.Fatal(err)
	}
	defer removeC()
	visit("/again")
	if v, _ := seen("/again"); v != "|3" {
		t.Errorf("a fresh handler: %q", v)
	}
}

// newTab starts Chrome and opens a blank tab, or skips the test.
func newTab(t *testing.T) *rod.Page {
	t.Helper()
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := sess.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	return tab.Timeout(30 * time.Second)
}

// Unsafe names exactly the headers Chrome refuses to let Fetch set.
func TestUnsafeHeadersRefused(t *testing.T) {
	tab := newTab(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><title>t</title>`)
	}))
	defer srv.Close()
	if err := (proto.FetchEnable{Patterns: []*proto.FetchRequestPattern{
		{URLPattern: "*", RequestStage: proto.FetchRequestStageRequest},
	}}).Call(tab); err != nil {
		t.Fatal(err)
	}
	names := []string{"Host", "Content-Length", "Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade",
		"TE", "Trailer", "Proxy-Authorization", "Proxy-Foo",
		"Authorization", "X-Custom", "Cookie", "Referer", "Origin", "Accept-Encoding", "Date"}
	var mu sync.Mutex
	refused := map[string]string{}
	var name string
	stop := tab.EachEvent(func(e *proto.FetchRequestPaused) {
		mu.Lock()
		n := name
		mu.Unlock()
		err := proto.FetchContinueRequest{RequestID: e.RequestID, Headers: []*proto.FetchHeaderEntry{{Name: n, Value: "x"}}}.Call(tab)
		if err != nil {
			mu.Lock()
			refused[n] = refusedHeader(err)
			mu.Unlock()
			_ = proto.FetchContinueRequest{RequestID: e.RequestID}.Call(tab)
		}
	})
	go stop()
	for _, n := range names {
		mu.Lock()
		name = n
		mu.Unlock()
		_ = tab.Navigate(srv.URL + "/" + n)
		_ = tab.WaitLoad()
		mu.Lock()
		got, ok := refused[n]
		mu.Unlock()
		if ok != Unsafe(n) {
			t.Errorf("%s: Chrome refuses it %v, Unsafe says %v", n, ok, Unsafe(n))
		}
		if ok && !strings.EqualFold(got, n) {
			t.Errorf("%s: the error named %q", n, got)
		}
	}
}

// A handler that sets a header Chrome refuses still gets its other
// headers sent. The request keeps its real Host.
func TestUnsafeHeaderKeepsTheOthers(t *testing.T) {
	tab := newTab(t)
	var mu sync.Mutex
	var auth, host string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/page" {
			mu.Lock()
			auth, host = r.Header.Get("Authorization"), r.Host
			mu.Unlock()
		}
		fmt.Fprint(w, `<!doctype html><title>t</title>`)
	}))
	defer srv.Close()
	remove, err := Add(tab, func(r *Request) {
		r.SetHeader("Host", "staging.example.com")
		r.SetHeader("Connection", "close")
		r.SetHeader("Authorization", "Bearer t")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	_ = tab.Navigate(srv.URL + "/page")
	_ = tab.WaitLoad()
	mu.Lock()
	defer mu.Unlock()
	if auth != "Bearer t" || host != strings.TrimPrefix(srv.URL, "http://") {
		t.Errorf("Authorization %q, Host %q", auth, host)
	}
}

func TestRefusedHeader(t *testing.T) {
	for err, want := range map[error]string{
		&cdp.Error{Code: -32602, Message: "Unsafe header: Host"}:     "Host",
		&cdp.Error{Code: -32602, Message: "Invalid InterceptionId."}: "",
		errors.New("Unsafe header: Host"):                            "",
	} {
		if got := refusedHeader(err); got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
}

// Owners coming and going at once on one tab leave it working: the
// last one out disables Fetch without undoing a newcomer's.
func TestAddRemoveRace(t *testing.T) {
	tab := newTab(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("X-A"))
	}))
	defer srv.Close()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				remove, err := Add(tab, func(r *Request) {})
				if err != nil {
					t.Error(err)
					return
				}
				remove()
			}
		})
	}
	wg.Wait()
	remove, err := Add(tab, func(r *Request) { r.SetHeader("X-A", "1") })
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	_ = tab.Navigate(srv.URL)
	_ = tab.WaitLoad()
	if text, err := tab.Eval(`() => document.body.innerText`); err != nil || text.Value.Str() != "1" {
		t.Errorf("after the race: %v %v", text, err)
	}
}
