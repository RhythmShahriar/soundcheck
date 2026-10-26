// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

func needBrowser(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// serve starts h on the first free port of 127.0.0.1:37028-37039, the
// range set aside for this package's fixtures.
func serve(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	for port := 37028; port < 37040; port++ {
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
	t.Fatal("no free port in 37028-37039")
	return nil
}

// pages serves each path's HTML and counts the loads of each.
type pages struct {
	html  map[string]string
	mu    sync.Mutex
	loads map[string]int
}

func (s *pages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, ok := s.html[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	if s.loads == nil {
		s.loads = map[string]int{}
	}
	s.loads[r.URL.Path]++
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, body)
}

func (s *pages) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads[path]
}

// chrome is one browser for a test, closed with it.
type chrome struct {
	t    *testing.T
	sess *rod.Browser
}

func launch(t *testing.T) *chrome {
	t.Helper()
	needBrowser(t)
	// The browser lives as long as its context: the whole test.
	ctx, cancel := context.WithCancel(context.Background())
	b, err := browser.Launch(ctx, browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(); cancel() })
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	return &chrome{t: t, sess: sess}
}

// open loads raw in a new foreground tab and returns it with the page
// record the runner would have made.
func (c *chrome) open(raw string) (*rod.Page, *model.Page) {
	c.t.Helper()
	tab, err := browser.NewTab(c.sess)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { _ = tab.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(20*time.Second))
	defer cancel()
	if err := tab.Context(ctx).Navigate(raw); err != nil {
		c.t.Fatal(err)
	}
	if err := tab.Context(ctx).WaitLoad(); err != nil {
		c.t.Fatal(err)
	}
	path := strings.TrimPrefix(raw, "http://")
	path = path[strings.Index(path, "/"):]
	return tab, &model.Page{ID: model.PageID(raw), URL: raw, Path: path, Status: 200}
}

// runContext is a check.Context that stores files in a temporary run
// folder and collects warnings.
func runContext(t *testing.T) *check.Context {
	t.Helper()
	return &check.Context{Run: &model.Run{}, Dir: t.TempDir()}
}

func withSaver(cc *check.Context) *check.Context {
	cc.Save = store.Saver(cc.Dir)
	return cc
}

// links is a clean page with n links and no trap.
func links(n int, extra string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><title>Links</title></head><body><main>`)
	for i := range n {
		fmt.Fprintf(&b, `<a href="#s%d" id="l%d">Link %d</a> `, i, i, i)
	}
	b.WriteString(extra + `</main></body></html>`)
	return b.String()
}

func live(t *testing.T, m *Module, cc *check.Context, tab *rod.Page, p *model.Page) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(90*time.Second))
	defer cancel()
	m.Live(ctx, cc, tab, p)
}

// A walk that finishes records ok, its summary and its trace, readable
// by its owner only.
func TestLiveAuditOK(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/": links(3, "")}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	cc := withSaver(runContext(t))
	live(t, New(), cc, tab, p)
	if a := p.Audits["keyboard"]; a.Status != model.AuditOK || a.Reason != "" {
		t.Fatalf("audit %+v, warnings %v", a, cc.Run.Warnings)
	}
	sum := p.Keyboard
	if sum == nil || sum.Stops != 3 || !sum.Wrapped || sum.Capped || sum.Evidence != "keyboard/"+p.ID+".json" {
		t.Fatalf("summary %+v", sum)
	}
	info, err := os.Stat(filepath.Join(cc.Dir, filepath.FromSlash(sum.Evidence)))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("trace mode %v, want 0600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(filepath.Join(cc.Dir, filepath.FromSlash(sum.Evidence)))
	tr, err := DecodeTrace(data)
	if err != nil || tr.Page != "/" || len(tr.Forward) < 4 {
		t.Errorf("trace %+v, %v", tr, err)
	}
}

// slow is a page whose links each hold the page up for 100 ms when
// they get focus, so a 1 s walk can't finish.
var slow = links(60, `<script>
document.addEventListener('focusin', () => { const end = Date.now() + 100; while (Date.now() < end) {} });
</script>`)

// A walk that runs out of time records timeout with the reason and a
// warning. It keeps the stops it saw.
func TestLiveAuditTimeout(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/": slow}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	cc := withSaver(runContext(t))
	m := New()
	m.limit = time.Second
	live(t, m, cc, tab, p)
	if a := p.Audits["keyboard"]; a.Status != model.AuditTimeout || a.Reason != "the walk took over 1 s" {
		t.Fatalf("audit %+v", a)
	}
	want := "keyboard: /: stopped after 1 s; the rest of the page wasn't walked"
	if len(cc.Run.Warnings) != 1 || cc.Run.Warnings[0] != want {
		t.Errorf("warnings %q, want %q", cc.Run.Warnings, want)
	}
	if p.Keyboard == nil || p.Keyboard.Stops == 0 || !p.Keyboard.Capped {
		t.Errorf("summary %+v", p.Keyboard)
	}
}

// A probe that crashes records error, not a missing status.
func TestLiveAuditCrash(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/": links(2, "")}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	cc := withSaver(runContext(t))
	m := New()
	m.walk = func(_ context.Context, w *walker) (*Trace, error) {
		w.begin()
		panic("probe returned nothing")
	}
	live(t, m, cc, tab, p)
	a := p.Audits["keyboard"]
	if a.Status != model.AuditError || !strings.HasPrefix(a.Reason, "the walk failed: ") {
		t.Fatalf("audit %+v", a)
	}
	if len(cc.Run.Warnings) != 1 || !strings.HasPrefix(cc.Run.Warnings[0], "keyboard: /: the walk failed: ") {
		t.Errorf("warnings %q", cc.Run.Warnings)
	}
}

// After interact clicked on a page, the walk loads it once more;
// otherwise it walks the page as loaded.
func TestLiveReloadsAfterInteract(t *testing.T) {
	srv := &pages{html: map[string]string{"/": links(2, "")}}
	ts := serve(t, srv)
	c := launch(t)
	tab, p := c.open(ts.URL + "/")
	live(t, New(), withSaver(runContext(t)), tab, p)
	if n := srv.count("/"); n != 1 {
		t.Errorf("%d loads without interact, want 1", n)
	}
	tab, p = c.open(ts.URL + "/")
	p.Interact = []model.Interaction{{Kind: "form"}, {Kind: "click", Selector: "#l0", Outcome: "ok"}}
	live(t, New(), withSaver(runContext(t)), tab, p)
	if n := srv.count("/"); n != 3 {
		t.Errorf("%d loads after a click, want 3 (one more)", n)
	}
	if p.Audits["keyboard"].Status != model.AuditOK || p.Keyboard.Stops != 2 {
		t.Errorf("audit %+v, summary %+v", p.Audits, p.Keyboard)
	}
	// Inspecting forms alone changes nothing on the page.
	if interacted(&model.Page{Interact: []model.Interaction{{Kind: "form"}}}) {
		t.Error("an inspected form counts as an interaction")
	}
}

// Pages the walk can't or mustn't walk are recorded as skipped, with
// why.
func TestLiveSkips(t *testing.T) {
	cc := runContext(t)
	var calls atomic.Int32
	m := New()
	m.walk = func(context.Context, *walker) (*Trace, error) { calls.Add(1); return nil, nil }
	cases := []struct {
		tab    *rod.Page
		page   model.Page
		reason string
	}{
		{nil, model.Page{Path: "/", Status: 200}, "needs Chrome"},
		{&rod.Page{}, model.Page{Path: "/gone", Status: 404}, "HTTP 404"},
		{&rod.Page{}, model.Page{Path: "/x", Error: "timeout"}, "the page didn't load"},
		{&rod.Page{}, model.Page{Path: "/wp-admin/edit.php", Status: 200}, "admin pages are never walked"},
		{&rod.Page{}, model.Page{Path: "/wp-login.php?redirect_to=x", Status: 200}, "admin pages are never walked"},
	}
	for _, c := range cases {
		p := c.page
		m.Live(context.Background(), cc, c.tab, &p)
		if a := p.Audits["keyboard"]; a.Status != model.AuditSkipped || a.Reason != c.reason {
			t.Errorf("%s: audit %+v, want skipped: %s", p.Path, a, c.reason)
		}
	}
	if calls.Load() != 0 || len(cc.Run.Warnings) != 0 {
		t.Errorf("%d walks, warnings %v", calls.Load(), cc.Run.Warnings)
	}
}

// A run that asks for WCAG 2.2 gets 2.2's criteria; any other run 2.1's.
func TestWCAGVersion(t *testing.T) {
	for v, want := range map[string]string{"2.2": "2.2", "2.1": "2.1", "2.0": "2.1", "": "2.1"} {
		cc := &check.Context{Run: &model.Run{Options: model.Options{WCAG: v}}}
		if got := wcagVersion(cc); got != want {
			t.Errorf("wcagVersion(%q) = %q, want %q", v, got, want)
		}
	}
	if got := wcagVersion(&check.Context{}); got != "2.1" {
		t.Errorf("without a run: %q", got)
	}
	i := newIssue("keyboard/focus-obscured", "2.2", "#a", "d")
	if i.Title != "Focused element is covered by fixed content" || i.Impact != "moderate" || len(i.WCAG) != 1 || i.EN301549[0] != "9.2.4.11" {
		t.Errorf("2.2 issue %+v", i)
	}
	if i := newIssue("keyboard/focus-obscured", "2.1", "#a", "d"); i.WCAG != nil || i.EN301549 != nil {
		t.Errorf("2.1 issue %+v", i)
	}
}

// The walk and the classifier together: the date picker's trap is an
// error on its page, keyed by check and element; the clean page has no
// keyboard issue.
func TestLiveReportsTrap(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/": datePicker, "/clean": links(3, "")}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	cc := withSaver(runContext(t))
	cc.Run.Options.WCAG = "2.2"
	live(t, New(), cc, tab, p)
	if len(p.Issues) != 1 {
		t.Fatalf("issues %+v", p.Issues)
	}
	i := p.Issues[0]
	if i.Key != "keyboard/trap|#date" || i.Level != model.Error || len(i.FocusPath) == 0 || i.WCAG[0] != "2.1.2" {
		t.Errorf("issue %+v", i)
	}
	if p.Audits["keyboard"].Status != model.AuditOK {
		t.Errorf("audit %+v", p.Audits)
	}
	tab, p = c.open(srv.URL + "/clean")
	live(t, New(), withSaver(runContext(t)), tab, p)
	if len(p.Issues) != 0 {
		t.Errorf("clean page issues %+v", p.Issues)
	}
}
