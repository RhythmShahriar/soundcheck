// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package focus

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
)

// serve starts h on the first free port of 127.0.0.1:37020-37027, the
// range set aside for this package's fixtures.
func serve(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	for port := 37020; port < 37028; port++ {
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
	t.Fatal("no free port in 37020-37027")
	return nil
}

// html serves one page per path.
type html map[string]string

func (h html) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, ok := h[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, body)
}

// tabAt opens raw in a foreground tab of a browser that lives as long
// as the test.
func tabAt(t *testing.T, raw string) *rod.Page {
	t.Helper()
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	b, err := browser.Launch(ctx, browser.Options{NoDownload: true})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(); cancel() })
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := browser.NewTab(sess)
	if err != nil {
		t.Fatal(err)
	}
	lctx, lcancel := context.WithTimeout(context.Background(), testenv.Timeout(20*time.Second))
	defer lcancel()
	if err := tab.Context(lctx).Navigate(raw); err != nil {
		t.Fatal(err)
	}
	if err := tab.Context(lctx).WaitLoad(); err != nil {
		t.Fatal(err)
	}
	return tab
}

// walkTabs presses Tab n times and returns each stop.
func walkTabs(t *testing.T, tab *rod.Page, n int) []Stop {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(30*time.Second))
	defer cancel()
	r := NewReader(tab)
	var out []Stop
	for range n {
		if err := Press(ctx, tab, Tab); err != nil {
			t.Fatal(err)
		}
		s, err := r.Active(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func selectors(stops []Stop) []string {
	var out []string
	for _, s := range stops {
		out = append(out, s.Selector)
	}
	return out
}

// hostile lies about focus in every way the main world can: the
// activeElement getters, matches, closest and getComputedStyle. It
// also moves a decoy's focus on each focusin, which only a reader in
// the page's own world would see wrongly. The real order is #a, #b,
// x-box >>> #c, #d.
const hostile = `<!doctype html><html lang="en"><body>
<button id="a">A</button>
<a id="b" href="#x">B</a>
<x-box></x-box>
<div role="dialog" aria-modal="true" id="modal"><button id="d">D</button></div>
<div aria-modal="true" style="display:none"><button id="hidden">H</button></div>
<script>
customElements.define('x-box', class extends HTMLElement {
  connectedCallback() { this.attachShadow({mode: 'open'}).innerHTML = '<input id="c" aria-label="C">'; }
});
Object.defineProperty(Document.prototype, 'activeElement', {get() { return document.getElementById('a'); }});
Object.defineProperty(ShadowRoot.prototype, 'activeElement', {get() { return null; }});
HTMLElement.prototype.matches = () => false;
Element.prototype.closest = () => null;
Element.prototype.getBoundingClientRect = () => ({x: -9999, y: -9999, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0});
window.getComputedStyle = () => ({display: 'none', visibility: 'hidden'});
</script>
</body></html>`

// A page that lies about focus in its own world can't change what the
// isolated world reads (security X2).
func TestActiveIgnoresPageLies(t *testing.T) {
	srv := serve(t, html{"/": hostile})
	tab := tabAt(t, srv.URL+"/")
	got := walkTabs(t, tab, 4)
	want := []string{"#a", "#b", "x-box >>> #c", "#d"}
	if s := selectors(got); strings.Join(s, ",") != strings.Join(want, ",") {
		t.Fatalf("order %q, want %q", s, want)
	}
	for i, s := range got {
		if s.Box[2] <= 0 || s.Box[0] < 0 || !s.InViewport || !s.Connected || s.BackendNodeID == 0 {
			t.Errorf("stop %d: %+v", i, s)
		}
	}
	if got[0].Role != "button" || got[0].Name != "A" || got[0].NameLen != 1 || got[1].Role != "link" || got[2].Name != "C" {
		t.Errorf("roles and names: %+v", got)
	}
	if got[0].InDialog || got[2].InDialog || !got[3].InDialog {
		t.Errorf("in_dialog: %v %v %v", got[0].InDialog, got[2].InDialog, got[3].InDialog)
	}
}

// prefilled is a checkout with a customer's details already in the
// fields and an email in a label. canary is the email, assembled so the
// test file holds no address.
func prefilled(canary string) string {
	return `<!doctype html><html lang="en"><body>
<label for="email">Email</label> <input id="email" type="email" value="` + canary + `">
<input name="phone" value="` + canary + `">
<textarea aria-label="Notes">` + canary + `</textarea>
<div contenteditable="true" tabindex="0">` + canary + `</div>
<select aria-label="Country"><option selected>` + canary + `</option></select>
<button aria-label="Signed in as ` + canary + `">Account</button>
<input type="password" aria-label="Password" value="` + canary + `">
</body></html>`
}

// No value a visitor typed, chose or had prefilled ends up in a stop:
// names come from Chrome's accessibility tree and are redacted
// (security M1).
func TestActiveNeverReadsValues(t *testing.T) {
	canary := "jane.canary" + "@" + "example.com"
	srv := serve(t, html{"/": prefilled(canary)})
	tab := tabAt(t, srv.URL+"/")
	got := walkTabs(t, tab, 7)
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "canary") {
		t.Fatalf("a value leaked into the stops: %s", data)
	}
	if got[0].Name != "Email" || got[0].Selector != "#email" || got[2].Name != "Notes" || got[4].Name != "Country" {
		t.Errorf("names: %+v", got)
	}
	if got[5].Name != "Signed in as ***@***" || got[5].NameLen != len("Signed in as "+canary) {
		t.Errorf("button name %q (%d runes)", got[5].Name, got[5].NameLen)
	}
}

// Focus inside a same-origin frame is followed into it; focus inside a
// frame from another origin stops at the frame element, with its host
// and nothing from inside (security S2).
func TestActiveFrames(t *testing.T) {
	inner := `<!doctype html><html lang="en"><body><button>Secret canary button</button><input name="card" aria-label="Card canary"></body></html>`
	other := serve(t, html{"/pay": inner})
	page := `<!doctype html><html lang="en"><body>
<a id="first" href="#x">First</a>
<iframe id="same" src="/inner" title="Search"></iframe>
<iframe id="pay" src="` + other.URL + `/pay" title="Payment"></iframe>
<a id="last" href="#y">Last</a>
</body></html>`
	srv := serve(t, html{"/": page, "/inner": `<!doctype html><html lang="en"><body><input name="q" aria-label="Search"></body></html>`})
	tab := tabAt(t, srv.URL+"/")
	got := walkTabs(t, tab, 5)
	want := []string{"#first", `#same >>> input[name="q"]`, "#pay", "#pay", "#last"}
	if s := selectors(got); strings.Join(s, ",") != strings.Join(want, ",") {
		t.Fatalf("order %q, want %q", s, want)
	}
	if got[1].IsFrame || got[1].Name != "Search" || got[1].Box[0] < 8 {
		t.Errorf("same-origin stop %+v", got[1])
	}
	host := strings.TrimPrefix(other.URL, "http://")
	for _, s := range got[2:4] {
		if !s.IsFrame || s.FrameHost != host || s.Name != "" || s.Role != "" || s.NameLen != 0 || s.Tag != "iframe" {
			t.Errorf("cross-origin stop %+v, want frame %s only", s, host)
		}
	}
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "canary") || strings.Contains(string(data), "card") {
		t.Errorf("read inside the cross-origin frame: %s", data)
	}
	if got[2].BackendNodeID != got[3].BackendNodeID {
		t.Error("two stops in one frame have different nodes")
	}
}

// A reader keeps working after the page loads a new document, which
// takes its isolated world away; a probe runs on the focused element.
func TestReaderAcrossNavigation(t *testing.T) {
	srv := serve(t, html{"/": `<!doctype html><html lang="en"><body><a id="go" href="/next">Next</a></body></html>`,
		"/next": `<!doctype html><html lang="en"><body><button id="b">B</button></body></html>`})
	tab := tabAt(t, srv.URL+"/")
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(30*time.Second))
	defer cancel()
	r := NewReader(tab)
	if err := Press(ctx, tab, Tab); err != nil {
		t.Fatal(err)
	}
	s, tag, err := r.ActiveWith(ctx, `function () { return this.localName + ':' + this.id; }`)
	if err != nil || s.Selector != "#go" || tag != "a:go" {
		t.Fatalf("before: %+v %q %v", s, tag, err)
	}
	if err := tab.Context(ctx).Navigate(srv.URL + "/next"); err != nil {
		t.Fatal(err)
	}
	_ = tab.Context(ctx).WaitLoad()
	if err := Press(ctx, tab, Tab); err != nil {
		t.Fatal(err)
	}
	s, err = r.Active(ctx)
	if err != nil || s.Selector != "#b" {
		t.Fatalf("after: %+v %v", s, err)
	}
	if v, err := r.Eval(ctx, `String(document.querySelectorAll('button').length)`); err != nil || v != "1" {
		t.Errorf("Eval = %q, %v", v, err)
	}
	// The document's own stop has the fixed selector body.
	if _, err := r.Eval(ctx, `(document.activeElement.blur(), '')`); err != nil {
		t.Fatal(err)
	}
	if s, err := r.Active(ctx); err != nil || !s.IsBody || s.Selector != "body" {
		t.Errorf("blurred: %+v %v", s, err)
	}
}

// Nodes returns the elements an expression finds, in its order, from
// the isolated world (the page's own querySelectorAll lies here);
// Name reads a node's label, never its value.
func TestNodesAndName(t *testing.T) {
	srv := serve(t, html{"/": `<!doctype html><html lang="en"><body>
<button id="a">First</button><input id="b" aria-label="Email" value="someone@example.test"><button id="c">Third</button>
<script>Document.prototype.querySelectorAll = () => [];</script>
</body></html>`})
	tab := tabAt(t, srv.URL+"/")
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(20*time.Second))
	defer cancel()
	r := NewReader(tab)
	ids, err := r.Nodes(ctx, `[...document.querySelectorAll('button, input')].reverse()`)
	if err != nil || len(ids) != 3 {
		t.Fatalf("nodes %v, %v", ids, err)
	}
	want := []string{"Third", "Email", "First"}
	for i, id := range ids {
		role, name, n := r.Name(ctx, id)
		if name != want[i] || n != len(want[i]) || role == "" {
			t.Errorf("node %d: role %q name %q (%d), want %q", i, role, name, n, want[i])
		}
	}
	if ids, err := r.Nodes(ctx, `[]`); err != nil || len(ids) != 0 {
		t.Errorf("empty: %v, %v", ids, err)
	}
	if ids, err := r.Nodes(ctx, `[1, 'x', document]`); err != nil || len(ids) != 0 {
		t.Errorf("no elements: %v, %v", ids, err)
	}
	if _, err := r.Nodes(ctx, `(() => { throw new Error('x'); })()`); err == nil {
		t.Error("a throwing expression gave no error")
	}
}
