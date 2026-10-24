// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package intercept

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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
