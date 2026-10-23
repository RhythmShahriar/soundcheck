// Package login signs the test account in to the site in a private
// browser session: through WordPress's own login form, or through a YAML
// login flow for other sites. Credentials come only from the
// SOUNDCHECK_USER and SOUNDCHECK_PASSWORD environment variables, never
// from flags or config, so they can't end up in shell history, config
// files or reports; they're masked in every error. Cookies stay in the
// browser session's memory and are never written anywhere.
package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/flow"
)

// Environment variables holding the test account.
const (
	EnvUser     = "SOUNDCHECK_USER"
	EnvPassword = "SOUNDCHECK_PASSWORD"
)

// Credentials returns the test account from the environment.
func Credentials() (user, pass string, ok bool) {
	user, pass = os.Getenv(EnvUser), os.Getenv(EnvPassword)
	return user, pass, user != "" && pass != ""
}

// Options for New.
type Options struct {
	Base    *url.URL // the site
	Config  config.Config
	Timeout time.Duration       // for the whole login; default 30 s
	Env     func(string) string // default os.Getenv; tests replace it
}

// New returns a function that logs the test account in within a browser
// session, matching check.Context.Login. It returns nil when
// SOUNDCHECK_USER or SOUNDCHECK_PASSWORD isn't set, so modules that need a
// logged-in user skip.
//
// With config login.flow set, that YAML flow logs in (it can use
// ${env:SOUNDCHECK_USER} and ${env:SOUNDCHECK_PASSWORD}). Otherwise the
// WordPress login form at login.url (default /wp-login.php) is used.
func New(o Options) func(ctx context.Context, sess *rod.Browser) error {
	env := o.Env
	if env == nil {
		env = os.Getenv
	}
	user, pass := env(EnvUser), env(EnvPassword)
	if user == "" || pass == "" {
		return nil
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	return func(ctx context.Context, sess *rod.Browser) error {
		ctx, cancel := context.WithTimeout(ctx, o.Timeout)
		defer cancel()
		var err error
		if o.Config.Login.Flow != "" {
			err = viaFlow(ctx, sess, o, env)
		} else {
			err = WordPress(ctx, sess, o.Base, o.Config.Login.URL, user, pass)
		}
		if err != nil {
			return errors.New(flow.Mask(err.Error(), pass, user))
		}
		return nil
	}
}

func viaFlow(ctx context.Context, sess *rod.Browser, o Options, env func(string) string) error {
	f, err := flow.ReadFile(o.Config.Login.Flow)
	if err != nil {
		return fmt.Errorf("login flow: %w", err)
	}
	page, err := browser.NewTab(sess)
	if err != nil {
		return err
	}
	defer func() { _ = page.Close() }()
	fails := flow.Run(ctx, page, f, flow.Options{Base: o.Base, Env: env, Timeout: 15 * time.Second})
	if len(fails) > 0 {
		return fmt.Errorf("login flow %q failed at %w", f.Name, fails[0])
	}
	return nil
}

// WordPress logs in through wp-login.php (or loginPath) with the given
// account. It succeeds when WordPress sets its wordpress_logged_in cookie
// and the site then shows the admin bar or the logged-in body class.
func WordPress(ctx context.Context, sess *rod.Browser, base *url.URL, loginPath, user, pass string) error {
	if loginPath == "" {
		loginPath = "/wp-login.php"
	}
	ref, err := url.Parse(loginPath)
	if err != nil {
		return err
	}
	loginURL := base.ResolveReference(ref).String()
	page, err := browser.NewTab(sess)
	if err != nil {
		return err
	}
	defer func() { _ = page.Close() }()
	page = page.Context(ctx)
	if err := page.Navigate(loginURL); err != nil {
		return fmt.Errorf("opening %s: %w", loginURL, err)
	}
	_ = page.WaitLoad()
	short := page.Timeout(10 * time.Second)
	userEl, err := short.Element("#user_login")
	if err != nil {
		return fmt.Errorf("%s has no WordPress login form (#user_login); set login.url, or login.flow for other sites", loginURL)
	}
	passEl, err := short.Element("#user_pass")
	if err != nil {
		return fmt.Errorf("%s has no password field (#user_pass)", loginURL)
	}
	for _, in := range []struct {
		el  *rod.Element
		val string
	}{{userEl, user}, {passEl, pass}} {
		if err := in.el.SelectAllText(); err != nil {
			return err
		}
		if err := in.el.Input(in.val); err != nil {
			return err
		}
	}
	if rem, err := page.Elements("#rememberme"); err == nil && len(rem) > 0 {
		_, _ = rem[0].Eval(`() => { this.checked = false; }`) // session cookies only
	}
	submit, err := short.Element("#wp-submit")
	if err != nil {
		return fmt.Errorf("%s has no login button (#wp-submit)", loginURL)
	}
	wait := page.Timeout(15 * time.Second).WaitNavigation(proto.PageLifecycleEventNameLoad)
	if err := submit.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("clicking the login button: %w", err)
	}
	wait()
	_ = page.WaitLoad()

	if !hasLoginCookie(sess) {
		msg := ""
		if res, err := page.Timeout(3 * time.Second).Eval(`() => { const e = document.querySelector('#login_error'); return e ? e.innerText.trim() : ''; }`); err == nil {
			msg = strings.Join(strings.Fields(res.Value.Str()), " ")
		}
		if msg != "" {
			return fmt.Errorf("WordPress refused the login: %s", msg)
		}
		return errors.New("WordPress didn't log in (no wordpress_logged_in cookie was set)")
	}
	// Confirm the front end sees the user: the admin bar or body.logged-in.
	if err := page.Navigate(base.String()); err != nil {
		return err
	}
	_ = page.WaitLoad()
	res, err := page.Timeout(5 * time.Second).Eval(`() => !!document.getElementById('wpadminbar') || document.body.classList.contains('logged-in')`)
	if err != nil {
		return err
	}
	if !res.Value.Bool() {
		return errors.New("logged in, but the site doesn't show the admin bar or the logged-in body class (a cache may be serving anonymous pages to logged-in users)")
	}
	return nil
}

func hasLoginCookie(sess *rod.Browser) bool {
	cookies, err := sess.GetCookies()
	if err != nil {
		return false
	}
	for _, c := range cookies {
		if strings.HasPrefix(c.Name, "wordpress_logged_in") {
			return true
		}
	}
	return false
}

// User is the logged-in account as WordPress knows it.
type User struct {
	Name  string `json:"name"`  // display name
	Email string `json:"email"` //
	Login string `json:"login"` // user_login
}

// Values returns the non-empty identifying strings, e.g. to search
// anonymous pages for them.
func (u User) Values() []string {
	var out []string
	for _, v := range []string{u.Name, u.Email, u.Login} {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// WhoAmI returns the display name, email and login of the user logged in
// within sess. It asks the REST API (/wp-json/wp/v2/users/me) with the
// session's cookies and a REST nonce (from wpApiSettings on the home
// page, or admin-ajax.php?action=rest-nonce), and falls back to reading
// the profile page (/wp-admin/profile.php). It only reads; nothing is
// changed.
func WhoAmI(ctx context.Context, sess *rod.Browser, base *url.URL) (User, error) {
	page, err := browser.NewTab(sess)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = page.Close() }()
	page = page.Context(ctx).Timeout(30 * time.Second)
	if err := page.Navigate(base.String()); err != nil {
		return User{}, err
	}
	_ = page.WaitLoad()
	var u User
	if res, err := page.Eval(restMeJS, base.String()); err == nil {
		_ = json.Unmarshal([]byte(res.Value.JSON("", "")), &u)
	}
	if u.Login != "" || u.Email != "" {
		return u, nil
	}
	profile := base.ResolveReference(&url.URL{Path: "/wp-admin/profile.php"}).String()
	if err := page.Navigate(profile); err != nil {
		return User{}, err
	}
	_ = page.WaitLoad()
	res, err := page.Eval(`() => {
	  const v = s => { const e = document.querySelector(s); return e ? (e.value || '').trim() : ''; };
	  const d = document.querySelector('#display_name');
	  const name = d && d.selectedOptions && d.selectedOptions[0] ? d.selectedOptions[0].text.trim() : '';
	  return {name: name || v('#nickname'), email: v('#email'), login: v('#user_login')};
	}`)
	if err != nil {
		return User{}, err
	}
	_ = json.Unmarshal([]byte(res.Value.JSON("", "")), &u)
	if u.Login == "" && u.Email == "" {
		return u, errors.New("couldn't read the logged-in user (REST API and profile page both failed); is the session logged in?")
	}
	return u, nil
}

// restMeJS fetches /wp/v2/users/me in the page, with the session's
// cookies and a REST nonce. It returns {} when the API isn't reachable.
const restMeJS = `async (base) => {
  try {
    const s = window.wpApiSettings || {};
    let nonce = s.nonce || '';
    if (!nonce) {
      const r = await fetch(new URL('/wp-admin/admin-ajax.php?action=rest-nonce', base), {credentials: 'same-origin'});
      const t = (await r.text()).trim();
      if (r.ok && /^[0-9a-f]{6,}$/i.test(t)) nonce = t;
    }
    const roots = [];
    if (s.root) roots.push(s.root.replace(/\/?$/, '/') + 'wp/v2/users/me?context=edit');
    roots.push(new URL('/wp-json/wp/v2/users/me?context=edit', base).href);
    roots.push(new URL('/?rest_route=/wp/v2/users/me&context=edit', base).href);
    for (const u of roots) {
      const r = await fetch(u, {credentials: 'same-origin', headers: nonce ? {'X-WP-Nonce': nonce} : {}});
      if (!r.ok) continue;
      const j = await r.json();
      if (j && j.id) return {name: j.name || '', email: j.email || '', login: j.username || j.slug || ''};
    }
  } catch (e) {}
  return {};
}`
