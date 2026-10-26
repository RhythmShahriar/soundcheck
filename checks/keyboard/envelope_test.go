// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/internal/testenv"
	"github.com/RhythmShahriar/soundcheck/model"
)

// site is a fixture that counts what reaches it: every GET of a path
// that would change something, every write and every WebSocket
// handshake.
type site struct {
	html   string
	gets   atomic.Int32 // /logout, ?add-to-cart, ?action=delete, /away
	writes atomic.Int32 // anything but GET and HEAD
	ws     atomic.Int32 // WebSocket handshakes
	file   string       // the download's name
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.ws.Add(1)
		http.Error(w, "no", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writes.Add(1)
		return
	}
	q := r.URL.Query()
	if r.URL.Path == "/logout" || r.URL.Path == "/away" || q.Has("add-to-cart") || q.Get("action") == "delete" {
		s.gets.Add(1)
	}
	switch r.URL.Path {
	case "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, s.html)
	case "/sw.js":
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = fmt.Fprint(w, `self.addEventListener('install', e => self.skipWaiting());
self.addEventListener('activate', e => e.waitUntil(clients.claim()));
self.addEventListener('message', e => { fetch('/sw-write', {method: 'POST', body: 'x'}); });
self.addEventListener('fetch', e => e.respondWith(fetch(e.request)));`)
	case "/" + s.file:
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename="+s.file)
		_, _ = fmt.Fprint(w, "a,b\n")
	default:
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "ok")
	}
}

// envelopePage does everything a page shouldn't get to do when a
// keyboard user only moves focus: each control acts on focus.
const envelopePage = `<!doctype html><html lang="en"><head><title>Envelope</title></head><body><main>
<a id="cart" href="/?add-to-cart=1">Add to cart</a>
<a id="logout" href="/logout">Log out</a>
<a id="del" href="/?action=delete">Delete</a>
<a id="go" href="#go" onfocus="location.href='/away'">Go</a>
<a id="ws" href="#ws" onfocus="const s = new WebSocket('ws://' + location.host + '/socket'); s.onopen = () => s.send('hi');">Live</a>
<a id="sw" href="#sw" onfocus="navigator.serviceWorker.controller && navigator.serviceWorker.controller.postMessage('go'); fetch('/through-sw', {method: 'POST', body: 'x'});">Sync</a>
<a id="dl" href="#dl" onfocus="const a = document.createElement('a'); a.href = '/FILE'; a.download = 'FILE'; document.body.appendChild(a); a.click();">Download</a>
<a id="pick" href="#pick" onfocus="document.getElementById('file').showPicker ? document.getElementById('file').showPicker() : document.getElementById('file').click();">Upload</a>
<input id="file" type="file" tabindex="-1" style="position:absolute;left:-999px" aria-label="File">
<a id="alert" href="#alert" onfocus="alert('Hi'); confirm('Sure?')">Alert</a>
<a id="pop" href="#pop" onfocus="window.open('/popup', '_blank')">Popup</a>
<a id="key" href="#key" onfocus="navigator.credentials.get({publicKey: {challenge: new Uint8Array(16), timeout: 1000}}).catch(e => { document.title = e.name; })">Passkey</a>
<a id="geo" href="#geo" onfocus="navigator.geolocation.getCurrentPosition(() => { document.title = 'located'; }, () => { document.title = 'denied'; })">Locate</a>
<button id="e-post" aria-expanded="false" onclick="fetch('/enter-write', {method: 'POST', body: 'x'})">Save view</button>
<button id="e-dl" aria-expanded="false" onclick="const a = document.createElement('a'); a.href = '/FILE?enter=1'; a.download = 'FILE'; document.body.appendChild(a); a.click();">Export</button>
<button id="e-pick" aria-expanded="false" onclick="document.getElementById('file').click()">Attach</button>
<button id="e-alert" aria-expanded="false" onclick="alert('Enter')">Notice</button>
<button id="e-pop" aria-expanded="false" onclick="window.open('/popup-enter', '_blank')">Help</button>
<button id="e-key" aria-expanded="false" onclick="navigator.credentials.get({publicKey: {challenge: new Uint8Array(16), timeout: 1000}}).catch(() => {})">Passkey sign-in</button>
<script>navigator.serviceWorker.register('/sw.js');</script>
</main></body></html>`

// The walk moves focus over controls that act on focus: nothing they
// try reaches the site or the disk, the walk completes and the trace
// lists what was stopped. Every request Chrome made went to the fixture.
func TestEnvelope(t *testing.T) {
	file := fmt.Sprintf("soundcheck-envelope-%d.csv", time.Now().UnixNano())
	s := &site{html: strings.ReplaceAll(envelopePage, "FILE", file), file: file}
	srv := serve(t, s)
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	// Let the service worker take control of the page first.
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(15*time.Second))
	defer cancel()
	if _, err := tab.Context(ctx).Eval(`async () => { await navigator.serviceWorker.ready;
	  for (let i = 0; i < 50 && !navigator.serviceWorker.controller; i++) await new Promise(r => setTimeout(r, 100)); }`); err != nil {
		t.Fatal(err)
	}
	var hosts sync.Map
	_ = proto.NetworkEnable{}.Call(tab)
	go tab.Context(ctx).EachEvent(func(e *proto.NetworkRequestWillBeSent) {
		if u, err := url.Parse(e.Request.URL); err == nil && u.Scheme != "data" && u.Scheme != "blob" {
			hosts.Store(u.Host, true)
		}
	})()
	cc := withSaver(runContext(t))
	live(t, New(), cc, tab, p)
	if a := p.Audits["keyboard"]; a.Status != "ok" {
		t.Fatalf("audit %+v, warnings %v", a, cc.Run.Warnings)
	}
	time.Sleep(300 * time.Millisecond) // anything late would show now
	if n := s.gets.Load(); n != 0 {
		t.Errorf("%d GETs of links that change something", n)
	}
	if n := s.writes.Load(); n != 0 {
		t.Errorf("%d writes reached the site", n)
	}
	if n := s.ws.Load(); n != 0 {
		t.Errorf("%d WebSocket handshakes", n)
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	hosts.Range(func(k, _ any) bool {
		if k.(string) != host {
			t.Errorf("a request went to %q", k)
		}
		return true
	})
	if info, err := tab.Info(); err != nil || info.URL != srv.URL+"/" {
		t.Errorf("the tab is at %v (%v)", info, err)
	}
	data, err := os.ReadFile(filepath.Join(cc.Dir, "keyboard", p.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	tr, err := DecodeTrace(data)
	if err != nil {
		t.Fatal(err)
	}
	// The download is stopped as the navigation it starts as, before
	// its request leaves; Chrome's download refusal is the backstop.
	if !slices.Contains(tr.Events, "navigation stopped: GET /"+file) && !slices.Contains(tr.Events, "download blocked: GET /"+file) {
		t.Errorf("events %q lack the download", tr.Events)
	}
	for _, want := range []string{"navigation stopped: GET /away", "WebSocket refused: " + host,
		"file chooser canceled", "dialog dismissed: alert", "dialog dismissed: confirm", "passkey request refused: get"} {
		if !slices.Contains(tr.Events, want) {
			t.Errorf("events %q lack %q", tr.Events, want)
		}
	}
	if !slices.ContainsFunc(tr.Events, func(e string) bool { return strings.HasPrefix(e, "window opened and closed: ") }) {
		t.Errorf("events %q lack the window", tr.Events)
	}
	if !slices.Contains(tr.Held, "POST /through-sw") || !slices.Contains(tr.Held, "POST /sw-write") {
		t.Errorf("held %q", tr.Held)
	}
	// The buttons acting on Enter were pressed, under the same envelope.
	var pressed []string
	for _, r := range tr.Widgets {
		pressed = append(pressed, r.Trigger+" "+strings.Join(r.Keys, ","))
	}
	for _, id := range []string{"#e-post", "#e-dl", "#e-pick", "#e-alert", "#e-pop", "#e-key"} {
		if !slices.Contains(pressed, id+" Enter,Space") {
			t.Errorf("%s not pressed: %q", id, pressed)
		}
	}
	if !slices.Contains(tr.Held, "POST /enter-write") || !slices.Contains(tr.Events, "dialog dismissed: alert") {
		t.Errorf("held %q events %q", tr.Held, tr.Events)
	}
	if title, _ := tab.Eval(`() => document.title`); title == nil || title.Value.Str() == "located" {
		t.Errorf("geolocation was granted")
	}
	// Nothing landed on disk: not in the run folder, not where Chrome
	// saves downloads.
	var found []string
	_ = filepath.WalkDir(cc.Dir, func(path string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() && !strings.HasSuffix(path, ".json") {
			found = append(found, path)
		}
		return nil
	})
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, "Downloads", file)); err == nil {
			found = append(found, filepath.Join(home, "Downloads", file))
		}
	}
	if len(found) > 0 {
		t.Errorf("files written: %v", found)
	}
}

func TestSameDocument(t *testing.T) {
	p := pageAt("http://127.0.0.1:1/a?x=1")
	for raw, want := range map[string]bool{"http://127.0.0.1:1/a?x=1#main": true, "http://127.0.0.1:1/a?x=1": true,
		"http://127.0.0.1:1/b": false, "http://127.0.0.1:1/a?x=2#main": false, "://": false} {
		if got := sameDocument(p, raw); got != want {
			t.Errorf("sameDocument(%q) = %v", raw, got)
		}
	}
	if o := pageOrigin(p); o != "http://127.0.0.1:1" {
		t.Errorf("origin %q", o)
	}
	if o := pageOrigin(pageAt("not a url")); o != "" {
		t.Errorf("origin of nothing %q", o)
	}
	if got := pathOfRaw("POST", "http://h/x/y?secret=1"); got != "POST /x/y" {
		t.Errorf("pathOfRaw %q", got)
	}
}

func pageAt(raw string) *model.Page { return &model.Page{URL: raw} }

// A page that fires the refusal events itself can't write a passkey or
// WebSocket refusal into the trace: the passkey event's name is the
// walk's own and a policy violation must come from Chrome.
func TestEnvelopeIgnoresForgedRefusals(t *testing.T) {
	page := `<!doctype html><html lang="en"><body><main>
<a id="a" href="#a" onfocus="forge()">First</a> <a id="b" href="#b">Second</a>
<script>
function forge() {
  window.dispatchEvent(new CustomEvent('soundcheck-keyboard-passkey', {detail: 'get'}));
  const e = new SecurityPolicyViolationEvent('securitypolicyviolation', {blockedURI: 'wss://forged.invalid/x',
    violatedDirective: 'connect-src', effectiveDirective: 'connect-src', originalPolicy: 'x', disposition: 'enforce', statusCode: 200});
  document.dispatchEvent(e);
}
</script></main></body></html>`
	srv := serve(t, &pages{html: map[string]string{"/": page}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	tr := walkPage(t, tab, p, DefaultSettings())
	for _, e := range tr.Events {
		if strings.HasPrefix(e, "passkey request refused") || strings.HasPrefix(e, "WebSocket refused") {
			t.Errorf("a forged refusal was recorded: %q", tr.Events)
		}
	}
	if !tr.Wrapped() {
		t.Errorf("the walk didn't finish: %q", forwardSelectors(tr))
	}
}

// Each walk gets its own passkey event name; the scripts use it.
func TestPasskeyEventName(t *testing.T) {
	a, b := passkeyEvent(), passkeyEvent()
	if a == b || !strings.HasPrefix(a, "soundcheck-keyboard-passkey-") || len(a) != len("soundcheck-keyboard-passkey-")+16 {
		t.Errorf("names %q %q", a, b)
	}
	if !strings.Contains(webAuthnJS(a), `"`+a+`"`) || !strings.Contains(webAuthnLogJS(a), `"`+a+`"`) {
		t.Error("the scripts don't use the name")
	}
}
