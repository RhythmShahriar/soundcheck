// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
)

// headlessAddr is where the headless check serves the drafts: the
// statement lane's ports are 37060 to 37079.
const headlessAddr = "127.0.0.1:37062"

// V1 and L2: the HTML draft of hostile input, opened in Chrome, shows
// no dialog and makes no request besides its own page. A page with a
// script and an image proves the check sees both.
func TestHTMLOpensQuietly(t *testing.T) {
	if testing.Short() {
		t.Skip("opens Chrome")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	pages := map[string][]byte{"/control.html": []byte(`<!doctype html><title>c</title><img src="/pixel.png"><script>alert(1)</script>`)}
	for _, layout := range Layouts {
		in := fixtures(t)["hostile"]
		in.Layout = layout
		var b bytes.Buffer
		if err := HTML(&b, mustBuild(t, in)); err != nil {
			t.Fatal(err)
		}
		pages["/"+layout+".html"] = b.Bytes()
	}
	ln, err := net.Listen("tcp", headlessAddr)
	if err != nil {
		t.Skip("port in use: ", err)
	}
	var mu sync.Mutex
	served := map[string]int{}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		served[r.URL.Path]++
		mu.Unlock()
		if p, ok := pages[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(p)
			return
		}
		http.NotFound(w, r)
	})}
	go func() { _ = srv.Serve(ln) }()
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
	open := func(path string) (dialogs, requests int) {
		tab, err := sess.Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tab.Close() }()
		tab = tab.Timeout(testenv.Timeout(20 * time.Second))
		url := "http://" + headlessAddr + path
		var count sync.Mutex
		stop := tab.EachEvent(func(e *proto.PageJavascriptDialogOpening) {
			count.Lock()
			dialogs++
			count.Unlock()
			go func() { _ = proto.PageHandleJavaScriptDialog{Accept: false}.Call(tab) }()
		}, func(e *proto.NetworkRequestWillBeSent) {
			if e.Request.URL != url {
				count.Lock()
				requests++
				count.Unlock()
			}
		})
		go stop()
		if err := tab.Navigate(url); err != nil {
			t.Fatal(err)
		}
		_ = tab.WaitLoad()
		time.Sleep(300 * time.Millisecond) // late events
		count.Lock()
		defer count.Unlock()
		return dialogs, requests
	}
	if d, r := open("/control.html"); d == 0 || r == 0 {
		t.Fatalf("the check misses what the control page does: %d dialogs, %d requests", d, r)
	}
	for _, layout := range Layouts {
		if d, r := open("/" + layout + ".html"); d != 0 || r != 0 {
			t.Errorf("%s draft: %d dialogs, %d requests", layout, d, r)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for path, n := range served {
		// Chrome asks for /favicon.ico itself, whatever the page says.
		if _, ok := pages[path]; !ok && path != "/pixel.png" && path != "/favicon.ico" {
			t.Errorf("the drafts asked for %s %d times", path, n)
		}
	}
}
