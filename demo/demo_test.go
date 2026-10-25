// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package demo

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// get serves one request in the given state and returns the status, the
// Location header and the body.
func get(t *testing.T, broken bool, method, path string) (int, string, string) {
	t.Helper()
	var s Site
	s.SetBroken(broken)
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(url.Values{"name": {"Ada"}, "email": {"ada@example.test"}, "message": {"Lemon"}}.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if body != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Code, w.Header().Get("Location"), w.Body.String()
}

// Each planted break: present in the broken state (positive), absent in
// the working one (negative).
func TestBreaks(t *testing.T) {
	for _, tc := range []struct {
		name         string
		method, path string
		check        func(code int, loc, body string) bool
	}{
		{"PHP warning in the footer", "GET", "/about/", func(_ int, _, b string) bool {
			return strings.Contains(b, "<b>Warning</b>:  Undefined array key") && strings.Contains(b, "footer.php</b> on line <b>14</b>")
		}},
		{"JS error on the home page", "GET", "/", func(_ int, _, b string) bool {
			return !strings.Contains(b, "window.demoCart = {")
		}},
		{"dead button", "GET", "/about/", func(_ int, _, b string) bool {
			return strings.Contains(b, `class="team-toggle"`) && strings.Contains(b, "querySelector('.js-team-toggle')")
		}},
		{"missing title", "GET", "/blog/", func(_ int, _, b string) bool {
			return !strings.Contains(b, "<title>")
		}},
		{"missing page", "GET", "/pricing/", func(c int, _, _ string) bool { return c == http.StatusNotFound }},
		{"form answers 500", "POST", "/contact/", func(c int, _, _ string) bool { return c == http.StatusInternalServerError }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, loc, body := get(t, true, tc.method, tc.path); !tc.check(code, loc, body) {
				t.Errorf("broken: not planted (%d)\n%s", code, body)
			}
			if code, loc, body := get(t, false, tc.method, tc.path); tc.check(code, loc, body) {
				t.Errorf("working: planted anyway (%d)\n%s", code, body)
			}
		})
	}
}

func TestWorkingSite(t *testing.T) {
	for _, path := range []string{"/", "/about/", "/pricing/", "/blog/", "/contact/", "/contact/thanks/"} {
		code, _, body := get(t, false, "GET", path)
		if code != http.StatusOK || !strings.Contains(body, "<title>") || strings.Contains(body, "Warning") {
			t.Errorf("%s: %d\n%s", path, code, body)
		}
	}
	code, loc, _ := get(t, false, "POST", "/contact/")
	if code != http.StatusSeeOther || loc != "/contact/thanks/" {
		t.Errorf("form: %d %q", code, loc)
	}
	// Every page links to the others, so a crawl from / finds all five.
	_, _, home := get(t, false, "GET", "/")
	for _, path := range []string{`href="/about/"`, `href="/pricing/"`, `href="/blog/"`, `href="/contact/"`} {
		if !strings.Contains(home, path) {
			t.Errorf("home has no %s", path)
		}
	}
}

func TestNotFound(t *testing.T) {
	for _, broken := range []bool{false, true} {
		if code, _, _ := get(t, broken, "GET", "/nope/"); code != http.StatusNotFound {
			t.Errorf("broken=%v: %d", broken, code)
		}
	}
}

func TestStart(t *testing.T) {
	var s Site
	srv, err := Start(&s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") {
		t.Errorf("URL = %s", srv.URL)
	}
	resp, err := http.Get(srv.URL + "/pricing/") //nolint:noctx // a test against our own server
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("working /pricing/: %d", resp.StatusCode)
	}
	s.SetBroken(true)
	resp, err = http.Get(srv.URL + "/pricing/") //nolint:noctx // as above
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("broken /pricing/: %d", resp.StatusCode)
	}
	if err := srv.Close(); err != nil {
		t.Error(err)
	}
}

func TestDescribe(t *testing.T) {
	got := Describe()
	if !strings.HasPrefix(got, Breaks[0]+", ") || !strings.HasSuffix(got, " and "+Breaks[len(Breaks)-1]) || strings.Contains(got, ", and") { // style:ignore: checks there is no comma before "and"
		t.Errorf("Describe() = %q", got)
	}
}
