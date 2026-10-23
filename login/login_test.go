package login

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/config"
)

const (
	testUser = "editor-amy"
	testPass = "correct horse battery"
	nonce    = "a1b2c3d4e5"
)

// wordpress is a fake WordPress with a login form, a front page that
// shows the admin bar when logged in, and the users/me REST endpoint.
func wordpress(t *testing.T, restNonceOnPage bool) *httptest.Server {
	t.Helper()
	logged := func(r *http.Request) bool {
		c, err := r.Cookie("wordpress_logged_in_abc")
		return err == nil && c.Value == "amy|token"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/wp-login.php", func(w http.ResponseWriter, r *http.Request) {
		errMsg := ""
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if r.PostForm.Get("log") == testUser && r.PostForm.Get("pwd") == testPass {
				http.SetCookie(w, &http.Cookie{Name: "wordpress_logged_in_abc", Value: "amy|token", Path: "/", HttpOnly: true})
				http.Redirect(w, r, "/wp-admin/", http.StatusFound)
				return
			}
			errMsg = `<div id="login_error"><strong>Error:</strong> The password you entered for the username <strong>` + r.PostForm.Get("log") + `</strong> is incorrect.</div>`
		}
		fmt.Fprintf(w, `<html><body class="login">%s<form name="loginform" id="loginform" action="/wp-login.php" method="post">
<input type="text" name="log" id="user_login"><input type="password" name="pwd" id="user_pass">
<input name="rememberme" type="checkbox" id="rememberme" value="forever">
<input type="submit" name="wp-submit" id="wp-submit" value="Log In"></form></body></html>`, errMsg)
	})
	mux.HandleFunc("/wp-admin/", func(w http.ResponseWriter, r *http.Request) {
		if !logged(r) {
			http.Redirect(w, r, "/wp-login.php", http.StatusFound)
			return
		}
		if r.URL.Path == "/wp-admin/admin-ajax.php" && r.URL.Query().Get("action") == "rest-nonce" {
			fmt.Fprint(w, nonce)
			return
		}
		fmt.Fprint(w, `<html><body class="wp-admin"><div id="wpadminbar"></div>Dashboard</body></html>`)
	})
	mux.HandleFunc("/wp-json/wp/v2/users/me", func(w http.ResponseWriter, r *http.Request) {
		if !logged(r) || r.Header.Get("X-WP-Nonce") != nonce {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"code":"rest_not_logged_in"}`)
			return
		}
		fmt.Fprint(w, `{"id":7,"name":"Amy Editor","username":"editor-amy","email":"amy@example.com","slug":"editor-amy"}`)
	})
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		if !logged(r) {
			fmt.Fprint(w, `<html><body class="home">Hello</body></html>`)
			return
		}
		script := ""
		if restNonceOnPage {
			script = `<script>var wpApiSettings = {"root":"/wp-json/","nonce":"` + nonce + `"};</script>`
		}
		fmt.Fprint(w, `<html><body class="home logged-in"><div id="wpadminbar"></div>Hello Amy`+script+`</body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func env(user, pass string) func(string) string {
	return func(k string) string {
		return map[string]string{EnvUser: user, EnvPassword: pass}[k]
	}
}

func TestNewNeedsCredentials(t *testing.T) {
	if New(Options{Env: env("", "")}) != nil || New(Options{Env: env("a", "")}) != nil {
		t.Error("login without credentials")
	}
	if New(Options{Env: env("a", "b")}) == nil {
		t.Error("no login with credentials")
	}
}

func TestWordPressLogin(t *testing.T) {
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()

	for _, onPage := range []bool{true, false} {
		srv := wordpress(t, onPage)
		base, _ := url.Parse(srv.URL + "/")
		sess, err := b.Session()
		if err != nil {
			t.Fatal(err)
		}
		login := New(Options{Base: base, Config: config.Default(), Env: env(testUser, testPass)})
		if err := login(ctx, sess); err != nil {
			t.Fatalf("login: %v", err)
		}
		u, err := WhoAmI(ctx, sess, base)
		if err != nil {
			t.Fatal(err)
		}
		if u != (User{Name: "Amy Editor", Email: "amy@example.com", Login: "editor-amy"}) {
			t.Errorf("nonce on page %v: user %+v", onPage, u)
		}
		_ = sess.Close()
	}

	// A wrong password fails, and the error never shows the credentials.
	srv := wordpress(t, true)
	base, _ := url.Parse(srv.URL)
	sess, _ := b.Session()
	defer func() { _ = sess.Close() }()
	err = New(Options{Base: base, Config: config.Default(), Env: env(testUser, "wrong-pass")})(ctx, sess)
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("wrong password: %v", err)
	}
	if strings.Contains(err.Error(), "wrong-pass") || strings.Contains(err.Error(), testUser) {
		t.Errorf("credentials leaked: %v", err)
	}
	if cookies, _ := sess.GetCookies(); len(cookies) != 0 {
		t.Errorf("cookies after a failed login: %v", cookies)
	}
}

func TestFlowLogin(t *testing.T) {
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	srv := wordpress(t, true)
	base, _ := url.Parse(srv.URL)
	file := filepath.Join(t.TempDir(), "login.yaml")
	if err := os.WriteFile(file, []byte(`name: Log in
start: /wp-login.php
steps:
  - fill: {selector: "#user_login", value: "${env:SOUNDCHECK_USER}"}
  - fill: {selector: "#user_pass", value: "${env:SOUNDCHECK_PASSWORD}"}
  - click: "#wp-submit"
  - expect: {text: Dashboard}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Login.Flow = file
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	sess, _ := b.Session()
	if err := New(Options{Base: base, Config: cfg, Env: env(testUser, testPass)})(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	sess2, _ := b.Session()
	err = New(Options{Base: base, Config: cfg, Env: env(testUser, "nope-nope")})(context.Background(), sess2)
	if err == nil || strings.Contains(err.Error(), "nope-nope") {
		t.Errorf("bad flow login: %v", err)
	}
}
