package cache

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Identity is what identifies the test user in a page.
type Identity struct{ Name, Email, Login string }

// identifyJS reads the logged-in user through the REST API (users/me with
// context=edit needs a REST nonce: wpApiSettings on the page, or
// admin-ajax's rest-nonce action), falling back to the profile form.
const identifyJS = `async (base) => {
  const out = {name: '', email: '', login: ''};
  let nonce = (window.wpApiSettings && window.wpApiSettings.nonce) || '';
  if (!nonce) {
    try {
      const r = await fetch(base + 'wp-admin/admin-ajax.php?action=rest-nonce', {credentials: 'same-origin'});
      const t = r.ok ? (await r.text()).trim() : '';
      if (/^[0-9a-f]{10}$/.test(t)) nonce = t;
    } catch (e) {}
  }
  if (nonce) {
    for (const u of [base + 'wp-json/wp/v2/users/me?context=edit', base + '?rest_route=/wp/v2/users/me&context=edit']) {
      try {
        const r = await fetch(u, {credentials: 'same-origin', headers: {'X-WP-Nonce': nonce}});
        if (!r.ok) continue;
        const me = await r.json();
        out.name = me.name || ''; out.email = me.email || ''; out.login = me.username || '';
        break;
      } catch (e) {}
    }
  }
  const val = (sel) => {
    const el = document.querySelector(sel);
    if (!el) return '';
    if (el.tagName === 'SELECT') { const o = el.options[el.selectedIndex]; return o ? o.text : ''; }
    return el.value || '';
  };
  out.email = out.email || val('#email');
  out.login = out.login || val('#user_login');
  out.name = out.name || val('#display_name');
  return out;
}`

// identify reads the logged-in test user's display name, email and
// username through sess, from /wp-admin/profile.php (read only).
func identify(ctx context.Context, sess *rod.Browser, base string) (Identity, error) {
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	tab, err := sess.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return Identity{}, err
	}
	defer func() { _ = tab.Close() }()
	tab = tab.Context(ctx).Timeout(45 * time.Second)
	if err := tab.Navigate(base + "wp-admin/profile.php"); err != nil {
		return Identity{}, err
	}
	if err := tab.WaitLoad(); err != nil {
		return Identity{}, err
	}
	res, err := tab.Eval(identifyJS, base)
	if err != nil {
		return Identity{}, err
	}
	id := Identity{
		Name:  strings.TrimSpace(res.Value.Get("name").Str()),
		Email: strings.TrimSpace(res.Value.Get("email").Str()),
		Login: strings.TrimSpace(res.Value.Get("login").Str()),
	}
	if id.Email == "" && id.Login == "" {
		return id, errors.New("couldn't read the test user's details (is the login working, and is this WordPress?)")
	}
	return id, nil
}

var (
	adminBar  = regexp.MustCompile(`(?i)id\s*=\s*["']?wpadminbar\b`)
	howdy     = regexp.MustCompile(`\bHowdy,`)
	maxLeakAt = 10 // pages visited logged in
)

// marker is something that shows a page was rendered for the test user.
type marker struct {
	what  string // "email", "admin bar"…
	value string // the text searched for
	mask  bool   // the value is personal or secret and must be masked
}

// markers lists what the anonymous HTML must never contain.
func markers(id Identity, loggedIn string, fresh string) []marker {
	var out []marker
	add := func(what, v string) {
		if len([]rune(v)) >= 3 {
			out = append(out, marker{what: what, value: v, mask: true})
		}
	}
	add("email", id.Email)
	add("username", id.Login)
	if id.Name != id.Login {
		add("display name", id.Name)
	}
	// REST and form nonces in the logged-in render are tied to the user.
	for _, n := range Nonces(loggedIn) {
		if !strings.Contains(fresh, n.Value) {
			out = append(out, marker{what: n.Kind + " of the logged-in user", value: n.Value, mask: true})
		}
	}
	return out
}

// found returns the markers in body that a fresh anonymous render of the
// same page doesn't have.
func found(body, fresh string, ms []marker) []string {
	var out []string
	lb, lf := strings.ToLower(body), strings.ToLower(fresh)
	for _, m := range ms {
		v := strings.ToLower(m.value)
		if strings.Contains(lb, v) && !strings.Contains(lf, v) {
			shown := m.value
			if m.mask {
				shown = maskValue(m.value)
			}
			out = append(out, fmt.Sprintf("%s (%s)", m.what, shown))
		}
	}
	if adminBar.MatchString(body) && !adminBar.MatchString(fresh) {
		out = append(out, "admin bar (#wpadminbar)")
	}
	if loggedInClass(body) && !loggedInClass(fresh) {
		out = append(out, `body class "logged-in"`)
	}
	if howdy.MatchString(body) && !howdy.MatchString(fresh) {
		out = append(out, `"Howdy," greeting`)
	}
	return out
}

func loggedInClass(body string) bool { return slices.Contains(BodyClasses(body), "logged-in") }

// maskValue shows the first two characters only.
func maskValue(s string) string {
	r := []rune(s)
	return string(r[:min(2, len(r))]) + "***"
}

// leak logs in through a separate session, visits pages as the test user
// so a misconfigured cache may store them, then fetches the same pages
// anonymously and looks for the user's details and logged-in markers.
func leak(ctx context.Context, cc *check.Context, results []*result) {
	if cc.Login == nil || cc.NewSession == nil {
		cc.Warn("cache: skipped the cache leak test; set SOUNDCHECK_USER and SOUNDCHECK_PASSWORD to a test account to run it")
		return
	}
	if cc.Config.IsProduction(cc.Base.String()) {
		cc.Warn("cache: skipped the cache leak test on a production URL: a leaking cache would show the test account to real visitors. Run it on staging")
		return
	}
	var pages []*result
	for _, r := range results {
		if r.idx >= 0 && r.second != nil && r.second.html() && len(pages) < maxLeakAt {
			pages = append(pages, r)
		}
	}
	if len(pages) == 0 {
		return
	}
	sess, err := cc.NewSession()
	if err != nil {
		cc.Warn("cache: leak test: " + err.Error())
		return
	}
	defer func() { _ = sess.Close() }()
	if err := cc.Login(ctx, sess); err != nil {
		cc.Warn("cache: leak test: couldn't log in: " + err.Error())
		return
	}
	id, err := identify(ctx, sess, cc.Base.String())
	if err != nil {
		cc.Warn("cache: leak test: " + err.Error())
		return
	}

	// Visit each page as the test user, keeping the HTML for its nonces.
	rendered := map[*result]string{}
	timeout := time.Duration(max(cc.Config.Timeout, 10)) * time.Second
	for _, r := range pages {
		if err := cc.Hosts.For(cc.Base.Host).Wait(ctx); err != nil {
			return
		}
		html, err := visitAs(ctx, sess, r.second.url, timeout)
		if err != nil {
			cc.Warn(fmt.Sprintf("cache: leak test: %s: %v", r.path, err))
			continue
		}
		rendered[r] = html
	}

	authed := false
	for _, r := range pages {
		html, ok := rendered[r]
		if !ok {
			continue
		}
		anon := fetch(ctx, cc, r.second.url)
		fresh := fetch(ctx, cc, bust(r.second.url))
		if !anon.html() || !fresh.html() {
			continue
		}
		if adminBar.MatchString(fresh.body) || loggedInClass(fresh.body) {
			authed = true // our "anonymous" requests carry a login (e.g. --cookie); can't tell
			continue
		}
		hits := found(anon.body, fresh.body, markers(id, html, fresh.body))
		if len(hits) == 0 {
			continue
		}
		served := orDefault(anon.read.Header, "the page cache")
		issue := model.Issue{Check: "cache/leak", Level: model.Error, Title: "Cache leaks logged-in content to visitors",
			Detail: "Anonymous visitors get a page rendered for the logged-in test user, served by " + served + ": " + strings.Join(hits, ", "),
			Source: anon.read.Layer,
			Fix: "Purge the page cache now. Then make every cache layer (" + orDefault(strings.Join(anon.read.Layers, ", "), "plugin, server and CDN") +
				") bypass requests carrying wordpress_logged_in_* or other session cookies, and never store responses rendered for a logged-in user."}
		cc.Lock()
		if r.idx < len(cc.Run.Pages) {
			check.Page(&cc.Run.Pages[r.idx], issue)
		}
		cc.Unlock()
	}
	if authed {
		cc.Warn("cache: leak test skipped pages whose anonymous fetch looked logged in; don't pass login cookies with --cookie or -H")
	}
}

// visitAs loads raw in the logged-in session and returns the HTML.
func visitAs(ctx context.Context, sess *rod.Browser, raw string, timeout time.Duration) (string, error) {
	tab, err := sess.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return "", err
	}
	defer func() { _ = tab.Close() }()
	tab = tab.Context(ctx).Timeout(timeout)
	if err := tab.Navigate(raw); err != nil {
		return "", err
	}
	if err := tab.WaitLoad(); err != nil {
		return "", err
	}
	return tab.HTML()
}
