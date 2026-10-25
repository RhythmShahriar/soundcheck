// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cache

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

func needBrowser(t *testing.T) *browser.Browser {
	t.Helper()
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

const (
	userNonce = "5e5e5e5e5e" // the REST nonce WordPress gives the logged-in test user
	anonNonce = "a0a0a0a0a0"
	session   = "wordpress_logged_in_test"
)

// leakyWP is a fake WordPress with a test user, a REST API and a page
// cache that stores whatever it rendered last, logged in or not. It
// serves it to everyone: the bug the leak test exists to catch. /safe/
// sits behind a cache that bypasses logged-in requests, as it should.
func leakyWP(t *testing.T, noREST bool) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	stored := map[string]string{}
	loggedIn := func(r *http.Request) bool {
		c, err := r.Cookie(session)
		return err == nil && c.Value == "jane"
	}
	render := func(r *http.Request, title string) string {
		classes, bar, nonce, greet := "page", "", anonNonce, "Welcome, guest"
		if loggedIn(r) {
			classes, nonce = "page logged-in admin-bar", userNonce
			bar = `<div id="wpadminbar"><a href="/wp-admin/profile.php">Howdy, <span class="display-name">Jane Tester</span></a></div>`
			greet = "Signed in as jane@example.test (janet)"
		}
		return fmt.Sprintf(`<!doctype html><html><head><title>%s</title><link rel="https://api.w.org/" href="/wp-json/">
<script>var wpApiSettings = {"root":"/wp-json/","nonce":"%s"};</script></head><body class="%s">%s
<main><h1>%s</h1><p>%s</p><p>Posted by Jane Tester.</p></main></body></html>`, title, nonce, classes, bar, title, greet)
	}
	leaky := func(title string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			if r.URL.RawQuery != "" { // not cached: a fresh render for whoever asked
				w.Header().Set("X-Cache", "MISS")
				fmt.Fprint(w, render(r, title))
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if loggedIn(r) {
				stored[r.URL.Path] = render(r, title) // the bug: logged-in renders are stored
				w.Header().Set("X-Cache", "MISS")
				fmt.Fprint(w, stored[r.URL.Path])
				return
			}
			body, ok := stored[r.URL.Path]
			if ok {
				w.Header().Set("X-Cache", "HIT")
				w.Header().Set("Age", "5")
			} else {
				body = render(r, title)
				stored[r.URL.Path] = body
				w.Header().Set("X-Cache", "MISS")
			}
			fmt.Fprint(w, body)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", leaky("Home"))
	mux.HandleFunc("/shop/", leaky("Shop"))
	mux.HandleFunc("/safe/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Cache", "HIT")
		if loggedIn(r) || r.URL.RawQuery != "" {
			w.Header().Set("X-Cache", "BYPASS")
		}
		fmt.Fprint(w, render(r, "Safe"))
	})
	mux.HandleFunc("/wp-admin/profile.php", func(w http.ResponseWriter, r *http.Request) {
		if !loggedIn(r) {
			http.Redirect(w, r, "/wp-login.php", http.StatusFound)
			return
		}
		fmt.Fprintf(w, `<html><head><script>var wpApiSettings = {"root":"/wp-json/","nonce":"%s"};</script></head><body class="wp-admin">
<input id="user_login" value="janet" disabled><input id="email" value="jane@example.test"></body></html>`, userNonce)
	})
	mux.HandleFunc("/wp-json/wp/v2/users/me", func(w http.ResponseWriter, r *http.Request) {
		if noREST || !loggedIn(r) || r.Header.Get("X-WP-Nonce") != userNonce || r.URL.Query().Get("context") != "edit" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"code":"rest_not_logged_in"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":2,"name":"Jane Tester","username":"janet","email":"jane@example.test","slug":"janet"}`)
	})
	mux.HandleFunc("/wp-login.php", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// login stands in for the login package: it sets the session cookie and
// opens the site, as a real wp-login.php round trip would.
func login(srv *httptest.Server) func(context.Context, *rod.Browser) error {
	return func(ctx context.Context, sess *rod.Browser) error {
		if err := sess.SetCookies([]*proto.NetworkCookieParam{{Name: session, Value: "jane", URL: srv.URL}}); err != nil {
			return err
		}
		tab, err := sess.Page(proto.TargetCreateTarget{URL: srv.URL + "/wp-admin/profile.php"})
		if err != nil {
			return err
		}
		defer func() { _ = tab.Close() }()
		return tab.Context(ctx).WaitLoad()
	}
}

func TestIdentify(t *testing.T) {
	b := needBrowser(t)
	srv := leakyWP(t, false)
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	if err := login(srv)(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	id, err := identify(context.Background(), sess, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if id != (Identity{Name: "Jane Tester", Email: "jane@example.test", Login: "janet"}) {
		t.Errorf("identity = %+v", id)
	}

	// Without the REST API, the profile form still gives email and login.
	noREST := leakyWP(t, true)
	if err := login(noREST)(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if id, err := identify(context.Background(), sess, noREST.URL+"/"); err != nil || id != (Identity{Email: "jane@example.test", Login: "janet"}) {
		t.Errorf("profile fallback = %+v, %v", id, err)
	}

	anon, _ := b.Session()
	if _, err := identify(context.Background(), anon, srv.URL); err == nil {
		t.Error("an anonymous session has no identity")
	}
}

func TestCacheLeak(t *testing.T) {
	b := needBrowser(t)
	srv := leakyWP(t, false)
	cc := context4(t, srv, []string{"/", "/shop/", "/safe/"})
	cc.NewSession = b.Session
	cc.Login = login(srv)
	Module{}.CheckSite(context.Background(), cc)

	const title = "Cache leaks logged-in content to visitors"
	for _, path := range []string{"/", "/shop/"} {
		i, ok := issuesOf(cc.Run.Page(path))[title]
		if !ok || i.Level != model.Error {
			t.Errorf("%s: no leak reported: %v (warnings %v)", path, cc.Run.Page(path).Issues, cc.Run.Warnings)
			continue
		}
		for _, want := range []string{"email (ja•••••)", "username (j•••••)", "admin bar (#wpadminbar)", `body class "logged-in"`, `"Howdy," greeting`,
			"wpApiSettings nonce of the logged-in user (5e•••••)", "x-cache: HIT"} {
			if !strings.Contains(i.Detail, want) {
				t.Errorf("%s: detail lacks %q: %s", path, want, i.Detail)
			}
		}
		// The display name is public (author bylines), so it isn't evidence.
		for _, secret := range []string{"jane@example.test", "janet", userNonce, "display name"} {
			if strings.Contains(i.Detail, secret) {
				t.Errorf("%s: detail shows %q: %s", path, secret, i.Detail)
			}
		}
	}
	if _, ok := issuesOf(cc.Run.Page("/safe/"))[title]; ok {
		t.Errorf("/safe/ bypasses logged-in requests but was reported: %v", cc.Run.Page("/safe/").Issues)
	}
	if len(cc.Run.Warnings) != 0 {
		t.Errorf("warnings: %v", cc.Run.Warnings)
	}
}

func TestLeakSkips(t *testing.T) {
	srv := leakyWP(t, false)
	cc := context4(t, srv, []string{"/"})
	Module{}.CheckSite(context.Background(), cc)
	if len(cc.Run.Warnings) != 1 || !strings.Contains(cc.Run.Warnings[0], "SOUNDCHECK_PASSWORD") {
		t.Errorf("no login: warnings = %v", cc.Run.Warnings)
	}

	cc = context4(t, srv, []string{"/"})
	cc.Login = func(context.Context, *rod.Browser) error { t.Error("logged in on production"); return nil }
	cc.NewSession = func() (*rod.Browser, error) { t.Error("opened a session on production"); return nil, nil }
	cc.Config.Production = []string{srv.URL}
	Module{}.CheckSite(context.Background(), cc)
	if len(cc.Run.Warnings) != 1 || !strings.Contains(cc.Run.Warnings[0], "production") {
		t.Errorf("production: warnings = %v", cc.Run.Warnings)
	}
}

func TestFoundMasksValues(t *testing.T) {
	values := map[string]string{ // value → how it is shown (secrets.Mask keeps at most a third)
		"Ω":                     "•••••",
		"Zqk":                   "Z•••••",
		"jane.doe@example.test": "ja•••••",
	}
	var ms []marker
	body := "<html>"
	for v := range values {
		ms = append(ms, marker{what: "secret", value: v, mask: true})
		body += "<p>" + v + "</p>"
	}
	hits := found(body, "<html></html>", ms)
	for _, shown := range values {
		if !slices.Contains(hits, "secret ("+shown+")") {
			t.Errorf("hits %q lack %q", hits, shown)
		}
	}
	// What a leak issue stores in run.json never holds the values.
	dir := t.TempDir()
	run := &model.Run{Pages: []model.Page{{Path: "/", Issues: []model.Issue{{Check: "cache/leak", Detail: strings.Join(hits, ", ")}}}}}
	if err := store.SaveRun(dir, run); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	for v := range values {
		if strings.Contains(string(raw), v) {
			t.Errorf("run.json holds %q: %s", v, raw)
		}
	}
}
