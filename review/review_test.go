// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
)

func sampleChanges() []model.Change {
	return []model.Change{
		{Class: model.Fixed, Check: "health/js-error", Title: "JS error: jQuery is not defined", Pages: []string{"/shop/"}},
		{Class: model.New, Check: "health/php-error", Title: `PHP Warning: Undefined array key "id"`, Source: "functions.php:212", Pages: []string{"/", "/shop/"}},
		{Class: model.Broke, Check: "health/status", Title: "Page returns HTTP 500", Pages: []string{"/shop/"}, Before: "200", After: "500"},
		{Class: model.Changed, Check: "visual/page", Title: "Page looks different", Pages: []string{"/"}, Score: 12.5},
	}
}

func sampleResult(changes []model.Change) report.Result {
	run := &model.Run{Schema: model.Schema, Version: "1.0.0", Target: "http://brew.test/",
		Started: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC),
		Pages:   []model.Page{{Path: "/", URL: "http://brew.test/", Status: 200}}}
	return report.Result{Run: run, Comparison: &model.Comparison{Schema: model.Schema, Version: "1.0.0", Pages: 1,
		Before: model.RunRef{Target: "http://brew.test/", Name: "good"}, Changes: changes}}
}

// recorder is a decide function that remembers its calls.
type recorder struct {
	mu    sync.Mutex
	calls []string // "accept Page returns HTTP 500"
	fail  error
}

func (r *recorder) decide(c model.Change, a Action) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.calls = append(r.calls, string(a)+" "+c.Title)
	return nil
}

func TestID(t *testing.T) {
	a := model.Change{Check: "health/status", Title: "Page returns HTTP 500"}
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(ID(a)) {
		t.Fatalf("ID = %q, want 8 hex characters", ID(a))
	}
	b := a
	b.Pages, b.Class = []string{"/x/"}, model.New
	if ID(a) != ID(b) {
		t.Error("ID changed with pages or class; it should only follow check, title and source")
	}
	b.Source = "x.php:1"
	if ID(a) == ID(b) {
		t.Error("different sources gave the same id")
	}
}

func TestParseAction(t *testing.T) {
	for _, s := range []string{"accept", "ignore", "skip"} {
		if a, err := ParseAction(s); err != nil || string(a) != s {
			t.Errorf("ParseAction(%q) = %q, %v", s, a, err)
		}
	}
	for _, s := range []string{"", "Accept", "delete"} {
		if _, err := ParseAction(s); err == nil {
			t.Errorf("ParseAction(%q) accepted", s)
		}
	}
}

func TestReviewable(t *testing.T) {
	var got []string
	for _, c := range Reviewable(sampleChanges()) {
		got = append(got, c.Class)
	}
	if want := "broke new changed"; strings.Join(got, " ") != want {
		t.Errorf("Reviewable order = %v, want %s (report order, no fixed)", got, want)
	}
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestTerminalModelKeys(t *testing.T) {
	cs := Reviewable(sampleChanges())
	rec := &recorder{}
	m := newTerminalModel(cs, rec.decide)
	if v := m.View(); !strings.Contains(v, "Change 1 of 3") || !strings.Contains(v, "Page returns HTTP 500") || !strings.Contains(v, "200 → 500") {
		t.Fatalf("first view:\n%s", v)
	}
	m.Update(key("x")) // unknown keys do nothing
	m.Update(key("a"))
	m.Update(key("s"))
	if !strings.Contains(m.View(), "Change 3 of 3") || !strings.Contains(m.View(), "Skipped: ") {
		t.Fatalf("after a and s:\n%s", m.View())
	}
	_, cmd := m.Update(key("i"))
	if cmd == nil || !m.done {
		t.Fatal("the last decision didn't end the review")
	}
	want := []string{"accept Page returns HTTP 500", "ignore Page looks different"}
	if strings.Join(rec.calls, "|") != strings.Join(want, "|") {
		t.Errorf("decide calls = %q, want %q", rec.calls, want)
	}
	if m.tally != (Tally{Accepted: 1, Ignored: 1, Skipped: 1, Total: 3}) {
		t.Errorf("tally = %+v", m.tally)
	}
}

func TestTerminalModelDecideFails(t *testing.T) {
	rec := &recorder{fail: errors.New("snapshot is read-only")}
	m := newTerminalModel(Reviewable(sampleChanges()), rec.decide)
	m.Update(key("a"))
	if m.at != 0 || m.tally.Accepted != 0 {
		t.Fatal("a failed accept moved on")
	}
	if !strings.Contains(m.View(), "Couldn't accept it: snapshot is read-only") {
		t.Errorf("error not shown:\n%s", m.View())
	}
	rec.fail = nil
	m.Update(key("a"))
	if m.at != 1 || len(rec.calls) != 1 {
		t.Error("retry after a failure didn't accept")
	}
}

func TestTerminalModelQuit(t *testing.T) {
	for _, k := range []tea.KeyMsg{key("q"), {Type: tea.KeyCtrlC}, {Type: tea.KeyEsc}} {
		rec := &recorder{}
		m := newTerminalModel(Reviewable(sampleChanges()), rec.decide)
		if _, cmd := m.Update(k); cmd == nil || !m.done {
			t.Errorf("%s didn't quit", k)
		}
		if len(rec.calls) != 0 {
			t.Errorf("%s called decide", k)
		}
	}
}

func TestTerminalViewStripsEscapes(t *testing.T) {
	c := model.Change{Class: model.New, Check: "health/title", Title: "Evil\x1b[2J\x1b]0;owned\x07 title", Detail: "a\x9bb"}
	v := newTerminalModel([]model.Change{c}, (&recorder{}).decide).View()
	for _, bad := range []string{"\x1b[2J", "\x1b]0;", "\x07", "\x9b"} {
		if strings.Contains(v, bad) {
			t.Errorf("view keeps the site's control sequence %q", bad)
		}
	}
	if !strings.Contains(v, "Evil") || !strings.Contains(v, "title") {
		t.Errorf("view lost the title text:\n%q", v)
	}
}

func TestTerminalRefusesWithoutTTY(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	for name, in := range map[string]io.Reader{"buffer": strings.NewReader("a"), "pipe": r} {
		var out bytes.Buffer
		err := Terminal(context.Background(), in, &out, sampleChanges(), (&recorder{}).decide)
		if !errors.Is(err, ErrNoTTY) {
			t.Errorf("%s: err = %v, want ErrNoTTY", name, err)
		}
	}
}

// startServer runs a review server for res on an httptest listener.
func startServer(t *testing.T, res report.Result, rec *recorder) (*server, *httptest.Server) {
	t.Helper()
	s, err := newServer(res, rec.decide)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	s.host = ts.Listener.Addr().String()
	return s, ts
}

// post sends a decision and returns the response status.
func post(t *testing.T, ts *httptest.Server, token, body string, edit func(*http.Request)) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/decide", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	if edit != nil {
		edit(req)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestServePage(t *testing.T) {
	s, ts := startServer(t, sampleResult(sampleChanges()), &recorder{})
	resp, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("page without token: %d, want 403", resp.StatusCode)
	}
	resp, err = ts.Client().Get(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page with token: %d", resp.StatusCode)
	}
	for _, want := range []string{`id="rv-banner"`, "Page returns HTTP 500", "</body>"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("page lacks %q", want)
		}
	}
	// The page runs the report's script and the toolbar's, each allowed
	// by its hash and no other; the report's own stricter meta policy
	// is gone, or the browser would block the toolbar under it.
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"frame-ancestors 'none'", "connect-src 'self'", "'" + report.ScriptHash + "'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %q", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline'; connect") || strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Errorf("CSP lets any inline script run: %q", csp)
	}
	scripts := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindAllSubmatch(body, -1)
	if len(scripts) != 2 {
		t.Fatalf("%d scripts on the review page, want 2", len(scripts))
	}
	for _, sc := range scripts {
		if !strings.Contains(csp, report.CSPHash(string(sc[1]))) {
			t.Errorf("CSP doesn't allow the script %.60q", sc[1])
		}
	}
	if bytes.Contains(body, []byte(`http-equiv="Content-Security-Policy"`)) {
		t.Error("the review page keeps the report's CSP meta")
	}
	data := barJSON(t, body)
	if len(data.Items) != 4 || data.Items[3] != nil || data.Items[0].Class != model.Broke || data.Token != s.token {
		t.Errorf("bar data doesn't follow the report order: %+v", data)
	}
}

// barJSON pulls the bar's data out of the served page.
func barJSON(t *testing.T, page []byte) barData {
	t.Helper()
	m := regexp.MustCompile(`var rv = (.*);\n`).FindSubmatch(page)
	if m == nil {
		t.Fatal("no bar data in the page")
	}
	var d barData
	if err := json.Unmarshal(m[1], &d); err != nil {
		t.Fatalf("bar data isn't JSON: %v\n%s", err, m[1])
	}
	return d
}

func TestServeForeignHost(t *testing.T) {
	s, ts := startServer(t, sampleResult(sampleChanges()), &recorder{})
	req, _ := http.NewRequest(http.MethodGet, s.URL(), nil)
	req.Host = "rebind.attacker.test:" + strings.Split(s.host, ":")[1]
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host: %d, want 403", resp.StatusCode)
	}
	rec := &recorder{}
	s2, ts2 := startServer(t, sampleResult(sampleChanges()), rec)
	id := ID(sampleChanges()[2])
	status := post(t, ts2, s2.token, `{"id":"`+id+`","action":"accept"}`, func(r *http.Request) { r.Host = "localhost.attacker.test" })
	if status != http.StatusForbidden || len(rec.calls) != 0 {
		t.Errorf("foreign Host POST: %d, decide calls %v", status, rec.calls)
	}
}

func TestServeDecide(t *testing.T) {
	rec := &recorder{}
	s, ts := startServer(t, sampleResult(sampleChanges()), rec)
	broke := ID(sampleChanges()[2])

	if status := post(t, ts, "", `{"id":"`+broke+`","action":"accept"}`, nil); status != http.StatusForbidden {
		t.Errorf("no token: %d, want 403", status)
	}
	if status := post(t, ts, strings.Repeat("0", len(s.token)), `{"id":"`+broke+`","action":"accept"}`, nil); status != http.StatusForbidden {
		t.Errorf("wrong token: %d, want 403", status)
	}
	if status := post(t, ts, s.token, `{"id":"`+broke+`","action":"accept"}`, func(r *http.Request) { r.Header.Set("Origin", "http://evil.test") }); status != http.StatusForbidden {
		t.Errorf("foreign Origin: %d, want 403", status)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("refused requests called decide: %v", rec.calls)
	}

	if status := post(t, ts, s.token, `{"id":"`+broke+`","action":"accept"}`, nil); status != http.StatusOK {
		t.Fatalf("accept: %d", status)
	}
	if status := post(t, ts, s.token, `{"id":"`+ID(sampleChanges()[1])+`","action":"ignore"}`, nil); status != http.StatusOK {
		t.Fatalf("ignore: %d", status)
	}
	if status := post(t, ts, s.token, `{"id":"`+ID(sampleChanges()[3])+`","action":"skip"}`, nil); status != http.StatusOK {
		t.Fatalf("skip: %d", status)
	}
	want := []string{"accept Page returns HTTP 500", `ignore PHP Warning: Undefined array key "id"`}
	if strings.Join(rec.calls, "|") != strings.Join(want, "|") {
		t.Errorf("decide calls = %q, want %q", rec.calls, want)
	}
	if got := s.Decided(); got[broke] != Accept || got[ID(sampleChanges()[3])] != Skip {
		t.Errorf("Decided = %v", got)
	}

	for body, want := range map[string]int{
		`{"id":"` + ID(sampleChanges()[0]) + `","action":"accept"}`: http.StatusNotFound, // fixed changes have no button
		`{"id":"nosuch","action":"accept"}`:                         http.StatusNotFound,
		`{"id":"` + broke + `","action":"delete"}`:                  http.StatusBadRequest,
		`not json`: http.StatusBadRequest,
		`{"id":"` + strings.Repeat("x", maxBody) + `"}`: http.StatusBadRequest,
	} {
		if status := post(t, ts, s.token, body, nil); status != want {
			t.Errorf("%.40s: %d, want %d", body, status, want)
		}
	}

	rec.fail = errors.New("disk full")
	if status := post(t, ts, s.token, `{"id":"`+ID(sampleChanges()[3])+`","action":"accept"}`, nil); status != http.StatusInternalServerError {
		t.Errorf("failing decide: %d, want 500", status)
	}
	if s.Decided()[ID(sampleChanges()[3])] != Skip {
		t.Error("a failed decision was recorded")
	}

	resp, err := ts.Client().Get(ts.URL + "/decide")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /decide: %d", resp.StatusCode)
	}
}

func TestServeEscapesSiteText(t *testing.T) {
	evil := `</script><script>alert(1)</script><img src=x onerror=alert(2)>`
	cs := []model.Change{{Class: model.New, Check: "health/title", Title: evil, Pages: []string{"/"}}}
	s, ts := startServer(t, sampleResult(cs), &recorder{})
	resp, err := ts.Client().Get(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, bad := range []string{"<script>alert(1)", "<img src=x"} {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("page contains the site's markup %q unescaped", bad)
		}
	}
	if d := barJSON(t, body); d.Items[0].Title != evil {
		t.Errorf("bar title = %q, want the original text", d.Items[0].Title)
	}
}

func TestServeNeedsComparison(t *testing.T) {
	res := sampleResult(nil)
	res.Comparison = nil
	if _, err := newServer(res, (&recorder{}).decide); err == nil {
		t.Error("a single run was accepted for review")
	}
}

// syncBuffer is a bytes.Buffer safe to read while Serve writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestServeListensOnLoopback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, sampleResult(sampleChanges()), out, (&recorder{}).decide) }()
	addr := regexp.MustCompile(`http://127\.0\.0\.1:\d+/\?token=[0-9a-f]{48}`)
	deadline := time.Now().Add(5 * time.Second)
	for addr.FindString(out.String()) == "" {
		if time.Now().After(deadline) {
			t.Fatalf("no address printed: %q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp, err := http.Get(addr.FindString(out.String())) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET printed address: %d", resp.StatusCode)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Serve after cancel: %v", err)
	}
}

// A change with its own ID keeps it, so the review shows the id accept
// takes.
func TestIDPrefersChangeID(t *testing.T) {
	if got := ID(model.Change{ID: "3f2a9c01", Check: "x/y", Title: "T"}); got != "3f2a9c01" {
		t.Errorf("ID = %q", got)
	}
	if got := ID(model.Change{Check: "x/y", Title: "T"}); len(got) != 8 {
		t.Errorf("hash ID = %q", got)
	}
}
