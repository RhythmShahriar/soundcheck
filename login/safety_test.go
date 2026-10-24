// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package login

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/config"
)

// lookalike is another host with a copy of the login form. It records
// everything posted to it: the attacker, in the negative fixtures.
func lookalike(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path+" "+r.PostForm.Encode())
		mu.Unlock()
		_, _ = w.Write([]byte(`<html><body><form method="post" action="/wp-login.php"><input name="log" id="user_login">
<input type="password" name="pwd" id="user_pass"><input type="submit" id="wp-submit"></form></body></html>`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }
}

func TestLoginStaysOnSite(t *testing.T) {
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	site := wordpress(t, true)
	evil, posted := lookalike(t)
	base, _ := url.Parse(site.URL)

	// login.url on another host: refused before the browser opens it.
	cfg := config.Default()
	cfg.Login.URL = evil.URL + "/wp-login.php"
	sess, _ := b.Session()
	err = New(Options{Base: base, Config: cfg, Env: env(testUser, testPass)})(context.Background(), sess)
	_ = sess.Close()
	if err == nil || !strings.Contains(err.Error(), "login.url is on 127.0.0.1:") || !strings.Contains(err.Error(), "not the site being checked") {
		t.Errorf("off-site login.url: %v", err)
	}
	if got := posted(); len(got) != 0 {
		t.Errorf("the other host was asked: %v", got)
	}
	// Soundcheck's own words aren't masked: only text from the site can
	// hold the password, so one that is "site" leaves the note readable.
	sess, _ = b.Session()
	err = New(Options{Base: base, Config: cfg, Env: env("a", "site")})(context.Background(), sess)
	_ = sess.Close()
	if err == nil || !strings.Contains(err.Error(), "not the site being checked") || !strings.Contains(err.Error(), "login.url") {
		t.Errorf("the note was masked: %v", err)
	}

	// The site's login page redirects away: nothing is typed there.
	mux := http.NewServeMux()
	mux.HandleFunc("/wp-login.php", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/wp-login.php", http.StatusFound)
	})
	moved := httptest.NewServer(mux)
	defer moved.Close()
	movedBase, _ := url.Parse(moved.URL)
	sess, _ = b.Session()
	err = New(Options{Base: movedBase, Config: config.Default(), Env: env(testUser, testPass)})(context.Background(), sess)
	_ = sess.Close()
	if err == nil || !strings.Contains(err.Error(), "redirected to "+evil.URL+"/wp-login.php") {
		t.Errorf("redirected login: %v", err)
	}
	for _, g := range posted() {
		if strings.Contains(g, "pwd") || strings.Contains(g, "POST") {
			t.Errorf("the password reached the other host: %s", g)
		}
	}

	// A login flow can't goto another host either.
	file := filepath.Join(t.TempDir(), "login.yaml")
	_ = os.WriteFile(file, []byte("name: Log in\nstart: "+evil.URL+"/wp-login.php\nsteps:\n"+
		"  - fill: {selector: \"#user_pass\", value: \"${env:SOUNDCHECK_PASSWORD}\"}\n  - click: \"#wp-submit\"\n"), 0o600)
	cfg = config.Default()
	cfg.Login.Flow = file
	sess, _ = b.Session()
	err = New(Options{Base: base, Config: cfg, Env: env(testUser, testPass)})(context.Background(), sess)
	_ = sess.Close()
	if err == nil || !strings.Contains(err.Error(), "--allow-host") {
		t.Errorf("off-site login flow: %v", err)
	}
	for _, g := range posted() {
		if strings.Contains(g, "POST") {
			t.Errorf("the login flow posted to the other host: %s", g)
		}
	}

	// A login on this machine over plain http still works (TestWordPressLogin
	// covers the form; this is the path through New with a login.url).
	cfg = config.Default()
	cfg.Login.URL = site.URL + "/wp-login.php"
	sess, _ = b.Session()
	defer func() { _ = sess.Close() }()
	if err := New(Options{Base: base, Config: cfg, Env: env(testUser, testPass)})(context.Background(), sess); err != nil {
		t.Errorf("same-host absolute login.url: %v", err)
	}
}

// Plain http to a public host is refused before any browser is needed.
func TestLoginRefusesPlainHTTP(t *testing.T) {
	file := filepath.Join(t.TempDir(), "login.yaml")
	_ = os.WriteFile(file, []byte("name: Log in\nsteps:\n  - fill: {selector: \"#p\", value: \"${env:SOUNDCHECK_PASSWORD}\"}\n"), 0o600)
	for _, flowFile := range []string{"", file} {
		cfg := config.Default()
		cfg.Login.Flow = flowFile
		base, _ := url.Parse("http://staging.example.test/")
		err := New(Options{Base: base, Config: cfg, Env: env(testUser, testPass)})(context.Background(), nil)
		if err == nil || err.Error() != "not typing the password over plain http to staging.example.test: use https, or pass --insecure-login" {
			t.Errorf("flow %q: %v", flowFile, err)
		}
	}
	for raw, insecure := range map[string]bool{"https://example.test/": false, "http://127.0.0.1:8080/": false,
		"http://localhost/": false, "http://[::1]:80/": false, "http://example.test/": true} {
		u, _ := url.Parse(raw)
		if err := secure(u, insecure); err != nil {
			t.Errorf("%s insecure=%v: %v", raw, insecure, err)
		}
	}
	for _, raw := range []string{"http://example.test/", "http://127.0.0.1.example.test/", "http://localhost.example.test/"} {
		u, _ := url.Parse(raw)
		if secure(u, false) == nil {
			t.Errorf("%s: plain http accepted", raw)
		}
	}
}
