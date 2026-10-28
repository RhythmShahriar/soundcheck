// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// eaaPage writes a page of the EAA fixture sites: a title, a nav and
// body, with a label on every field unless body leaves it out.
func eaaPage(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>%s</title><link rel="icon" href="data:,"></head><body>
<header><nav aria-label="Main"><a href="/">Home</a> <a href="/checkout/">Checkout</a> <a href="/about/">About</a> <a href="/help/">Help</a></nav></header>
<main><h1>%s</h1>%s</main></body></html>`, title, title, body)
}

// eaaSite serves four pages that break the way an update could for a
// keyboard or screen reader user: a keyboard trap in the newsletter field
// on /, a dialog on /about/ that drops focus when it closes and a
// checkout field that loses its label. /help/ never changes.
func eaaSite(t *testing.T, broken *atomic.Bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// Broken: Tab in the field puts focus straight back on it.
		trap := ""
		if broken.Load() {
			trap = `<script>document.getElementById("news").addEventListener("keydown", function (e) {
  if (e.key === "Tab") { e.preventDefault(); document.getElementById("news").focus(); }
});</script>`
		}
		eaaPage(w, "Brew", `<p>Fresh coffee, roasted daily.</p>
<form action="/" method="get"><label for="news">Newsletter</label> <input id="news" type="email" autocomplete="email">
<button type="button">Sign up</button></form><p><a href="/help/">Questions?</a></p>`+trap)
	})
	mux.HandleFunc("/checkout/", func(w http.ResponseWriter, _ *http.Request) {
		label := `<label for="email">Email</label> `
		if broken.Load() {
			label = ""
		}
		eaaPage(w, "Checkout", `<form action="/checkout/" method="get">`+label+`<input id="email" name="email" type="email" autocomplete="email">
<button type="button">Continue</button></form>`)
	})
	mux.HandleFunc("/about/", func(w http.ResponseWriter, _ *http.Request) {
		// Good: closing the dialog returns focus to the button that
		// opened it. Broken: the dialog is removed and focus is lost.
		restore := `document.getElementById("hours-open").focus();`
		if broken.Load() {
			restore = ""
		}
		eaaPage(w, "About", `<p>We roast in small batches.</p>
<button type="button" id="hours-open" aria-haspopup="dialog">Opening hours</button>
<div id="hours" role="dialog" aria-modal="true" aria-labelledby="hours-title" hidden>
<h2 id="hours-title">Opening hours</h2><p>Monday to Friday, 8 to 18.</p><button type="button" id="hours-close">Close</button></div>
<script>
var dialog = document.getElementById("hours");
document.getElementById("hours-open").addEventListener("click", function () {
  dialog.hidden = false; document.getElementById("hours-close").focus();
});
document.getElementById("hours-close").addEventListener("click", function () {
  dialog.hidden = true; `+restore+`
});
document.addEventListener("keydown", function (e) { if (e.key === "Escape" && !dialog.hidden) { document.getElementById("hours-close").click(); } });
// A modal keeps Tab inside: Close is its only control.
dialog.addEventListener("keydown", function (e) { if (e.key === "Tab") { e.preventDefault(); document.getElementById("hours-close").focus(); } });
</script>`)
	})
	mux.HandleFunc("/help/", func(w http.ResponseWriter, _ *http.Request) {
		eaaPage(w, "Help", `<p>Write to us any time.</p>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// eaaCommon are the flags the browser tests share: one viewport, no
// download, a quick rate and plain output.
var eaaCommon = []string{"--no-download", "--viewport", "desktop", "--rate", "50", "--plain"}

// The whole --eaa round trip in a real browser: a snapshot of the good
// site, a compare of the unchanged site (exit 0, nothing found), then a
// compare of the broken site: the trap is BROKE, the field without a label is
// NEW and --fail-on broke exits 1.
func TestEAAEndToEnd(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome three times")
	}
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("CI", "1") // no one-time tip
	var broken atomic.Bool
	site := eaaSite(t, &broken)

	code, out, errOut := runCLI(t, append([]string{"snapshot", site.URL, "--eaa", "--name", "before"}, eaaCommon...)...)
	if code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	if run := readRunJSON(t, filepath.Join(".soundcheck", "snapshots", "before")); !run.Options.EAA || run.Options.WCAG != "2.1" {
		t.Errorf("snapshot options %+v", run.Options)
	}

	// Unchanged: nothing to report.
	code, out, errOut = runCLI(t, append([]string{"compare", "--eaa", "--fail-on", "broke"}, eaaCommon...)...)
	if code != exitOK {
		t.Fatalf("clean compare: exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "Nothing changed; no failures found by these tests") {
		t.Errorf("clean compare: no verdict in\n%s", out)
	}
	if !strings.Contains(strings.Join(strings.Fields(out), " "), model.EvidenceNote) {
		t.Errorf("clean compare: no evidence note in\n%s", out)
	}

	broken.Store(true)
	code, c := compareJSON(t, append([]string{"--eaa", "--fail-on", "broke"}, eaaCommon...)...)
	has := func(class, check, page string) bool {
		return slices.ContainsFunc(c.Changes, func(ch model.Change) bool {
			return ch.Class == class && ch.Check == check && slices.Contains(ch.Pages, page)
		})
	}
	if !has(model.New, "a11y/label", "/checkout/") {
		t.Errorf("no NEW a11y/label on /checkout/: %s", changeList(c))
	}
	if has(model.New, "a11y/label", "/help/") || slices.ContainsFunc(c.Changes, func(ch model.Change) bool { return slices.Contains(ch.Pages, "/help/") }) {
		t.Errorf("the unchanged page changed: %s", changeList(c))
	}
	if c.EAA == nil || c.EAA.EvidenceNote != model.EvidenceNote {
		t.Errorf("no eaa block: %+v", c.EAA)
	}
	if !has(model.Broke, "keyboard/trap", "/") || code != exitFindings {
		t.Errorf("want BROKE keyboard/trap on / and exit 1, got exit %d: %s", code, changeList(c))
	}
	// The dialog that drops focus is a warning, so NEW, not BROKE.
	if !has(model.New, "keyboard/focus-lost", "/about/") {
		t.Errorf("want NEW keyboard/focus-lost on /about/: %s", changeList(c))
	}
}

// changeList shows the changes of c one per line, for a failure message.
func changeList(c *model.Comparison) string {
	var b strings.Builder
	for _, ch := range c.Changes {
		fmt.Fprintf(&b, "\n  %s %s %v", ch.Class, ch.Check, ch.Pages)
	}
	return b.String()
}

// canaries are values a run must never write anywhere: each is
// assembled at runtime from a random part, so no file in the repository
// holds one and a hit can't come from anywhere but the run.
type canaries map[string]string

func newCanaries(t *testing.T, names ...string) canaries {
	t.Helper()
	b := make([]byte, 8)
	c := canaries{}
	for _, n := range names {
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		c[n] = "cnry" + strings.ReplaceAll(n, "-", "") + hex.EncodeToString(b)
	}
	return c
}

// sweep returns "file: name" for each canary found in a file under the
// roots or in the extra texts. Screenshots are left out: they show what
// the page showed and are tested on their own (S1, M5).
func (c canaries) sweep(t *testing.T, texts map[string]string, roots ...string) []string {
	t.Helper()
	var hits []string
	look := func(where, text string) {
		for _, n := range slices.Sorted(maps.Keys(c)) {
			if strings.Contains(text, c[n]) {
				hits = append(hits, where+": "+n)
			}
		}
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.EqualFold(filepath.Ext(path), ".png") {
				return err
			}
			data, err := os.ReadFile(path) //nolint:gosec // a file the test's own run wrote
			if err != nil {
				return err
			}
			look(filepath.ToSlash(path), string(data))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for where, text := range texts {
		look(where, text)
	}
	return hits
}

// canarySite serves a site that hides a canary in each place a run could
// pick one up: a field's value, a data-* attribute, a link's query
// string, a frame from another site, a cookie, an aria-label, a field
// on the page behind the login. Elements axe reports hold three more: a
// prefilled textarea with no label, the selected option of a select
// with no name and an email address in text with too little contrast. The frame is served by the second
// server, reached as localhost so it is another site, not only another
// origin.
func canarySite(t *testing.T, c canaries, user, pass string) (*httptest.Server, *canaryHits) {
	t.Helper()
	var hits canaryHits
	frame := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.frames.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><title>Widget</title></head><body>
<p>%s</p><label>Token <input value="%s"></label><button aria-label="%s">Go</button></body></html>`, c["frame-text"], c["frame-value"], c["frame-label"])
	}))
	t.Cleanup(frame.Close)
	frameURL := strings.Replace(frame.URL, "127.0.0.1", "localhost", 1) + "/"

	loggedIn := func(r *http.Request) bool {
		ck, err := r.Cookie("sid")
		ok := err == nil && ck.Value == c["cookie"]
		if ok {
			hits.accounts.Add(1)
		}
		return ok
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Canary") == c["header"] {
			hits.headers.Add(1)
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		eaaPage(w, "Home", fmt.Sprintf(`<div data-user="%s"><p>Welcome.</p></div>
<form action="/" method="get"><label for="q">Search</label> <input id="q" name="q" value="%s"></form>
<p><a href="/account/?token=%s">Your account</a> <a href="/contact/">Contact</a></p>
<iframe title="Widget" src="%s" width="300" height="120"></iframe>
<textarea>ta-%s</textarea> <select><option>Pick one</option><option selected>opt-%s</option></select>
<p style="color:#ccc">low contrast %s@example.org</p>`, c["data"], c["value"], c["query"], frameURL, c["textarea"], c["option"], c["email"]))
	})
	mux.HandleFunc("/contact/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			hits.posts.Add(1)
		}
		eaaPage(w, "Contact", `<form action="/contact/" method="post"><label for="pw">Passphrase</label> <input id="pw" type="password">
<label for="msg">Message</label> <textarea id="msg"></textarea><button type="submit">Send</button></form>`)
	})
	mux.HandleFunc("/account/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if !loggedIn(r) {
			eaaPage(w, "Account", `<p><a href="/wp-login.php">Log in</a> to see your account.</p>`)
			return
		}
		// Logged in: what only the account holder sees.
		eaaPage(w, "Account", fmt.Sprintf(`<p>Signed in.</p><button type="button" aria-label="Account %s">Menu</button>
<label for="phone">Phone</label> <input id="phone" value="%s">`, c["aria-label"], c["account-value"]))
	})
	mux.HandleFunc("/wp-login.php", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err == nil && r.PostForm.Get("log") == user && r.PostForm.Get("pwd") == pass {
				http.SetCookie(w, &http.Cookie{Name: "sid", Value: c["cookie"], Path: "/", HttpOnly: true})
				http.Redirect(w, r, "/account/", http.StatusFound)
				return
			}
		}
		eaaPage(w, "Log in", `<form method="post" action="/wp-login.php"><label for="user_login">Username</label> <input id="user_login" name="log">
<label for="user_pass">Password</label> <input id="user_pass" name="pwd" type="password"><input type="submit" id="wp-submit" value="Log in"></form>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits
}

// canaryHits counts what reached the canary site, so the sweep can't
// pass because the canaries never got into the run: requests carrying
// the -H header, frame loads, logged-in account pages and POSTs.
type canaryHits struct {
	headers, frames, accounts, posts atomic.Int64
}

// The canary sweep (M4): a run with --eaa, every output format and
// --verbose, then the statement in every format, writes none of the
// values the site and the command line hid. Every file under .soundcheck/
// and every -o file is read, as is what the commands printed.
func TestEAACanarySweep(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome")
	}
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("CI", "1")
	c := newCanaries(t, "value", "data", "query", "frame-text", "frame-value", "frame-label",
		"flow-password", "header", "cookie", "aria-label", "account-value", "login-password",
		"textarea", "option", "email")
	user := "tester"
	t.Setenv("SOUNDCHECK_USER", user)
	t.Setenv("SOUNDCHECK_PASSWORD", c["login-password"])
	t.Setenv("CANARY_PASSPHRASE", c["flow-password"])
	site, hits := canarySite(t, c, user, c["login-password"])

	writeConfig(t, `statement:
  owner: Example Coffee Ltd
  website: https://shop.example
  contact:
    email: access@shop.example
`)
	flowYAML := `name: Contact by passphrase
start: /contact/
steps:
  - fill: {selector: "#pw", value: "${env:CANARY_PASSPHRASE}"}
  - fill: {selector: "#msg", value: "${fake:message}"}
  - expect: {text: Contact}
`
	if err := os.WriteFile(filepath.Join(".soundcheck", "flows", "contact.yaml"), []byte(flowYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("out", 0o700); err != nil {
		t.Fatal(err)
	}

	printed := map[string]string{}
	args := append([]string{"run", site.URL, "--eaa", "--modules", "+interact,cache", "--level", "flows",
		"--allow-env", "CANARY_PASSPHRASE", "-H", "X-Canary: " + c["header"], "--verbose",
		"-o", "out/r.json", "-o", "out/r.md", "-o", "out/r.html", "-o", "out/r.xml"}, eaaCommon...)
	code, out, errOut := runCLI(t, args...)
	printed["run stdout"], printed["run stderr"] = out, errOut
	if code != exitOK && code != exitFindings {
		t.Fatalf("run: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, f := range []string{"r.json", "r.md", "r.html", "r.xml"} {
		if fi, err := os.Stat(filepath.Join("out", f)); err != nil || fi.Size() == 0 {
			t.Errorf("out/%s wasn't written: %v", f, err)
		}
	}
	if n := hits.posts.Load(); n != 0 {
		t.Errorf("the run sent %d POST requests", n)
	}
	// The canaries were in play: the header was sent, the frame loaded
	// and the cache check saw the account page logged in.
	if hits.headers.Load() == 0 || hits.frames.Load() == 0 || hits.accounts.Load() == 0 {
		t.Errorf("canaries not reached: header %d, frame %d, account %d\n%s\n%s",
			hits.headers.Load(), hits.frames.Load(), hits.accounts.Load(), printed["run stdout"], printed["run stderr"])
	}
	// axe reported the elements that hold the textarea, option and
	// email canaries, so their details were written and swept.
	if report, err := os.ReadFile(filepath.Join("out", "r.json")); err == nil {
		for _, check := range []string{`"a11y/label"`, `"a11y/select-name"`, `"a11y/color-contrast"`} {
			if !strings.Contains(string(report), check) {
				t.Errorf("out/r.json has no %s issue, so its canary wasn't in play", check)
			}
		}
	}
	if !strings.Contains(printed["run stdout"]+printed["run stderr"], "Contact by passphrase") {
		t.Errorf("the flow didn't run:\n%s\n%s", printed["run stdout"], printed["run stderr"])
	}

	code, out, errOut = runCLI(t, "statement", "-o", "out/s.md", "-o", "out/s.html", "-o", "out/s.json", "-o", "out/s.jsonld")
	printed["statement stdout"], printed["statement stderr"] = out, errOut
	if code != exitOK {
		t.Errorf("statement: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, f := range []string{"s.md", "s.html", "s.json", "s.jsonld"} {
		if fi, err := os.Stat(filepath.Join("out", f)); err != nil || fi.Size() == 0 {
			t.Errorf("out/%s wasn't written: %v", f, err)
		}
	}
	code, out, errOut = runCLI(t, "evidence", "verify")
	printed["evidence stdout"], printed["evidence stderr"] = out, errOut
	if code != exitOK {
		t.Errorf("evidence verify: exit %d\n%s\n%s", code, out, errOut)
	}

	if hits := c.sweep(t, printed, ".soundcheck", "out"); len(hits) > 0 {
		t.Errorf("canaries written:\n%s", strings.Join(hits, "\n"))
	}
}

// The sweep finds a canary in a file, in a nested folder or in printed
// text; it skips screenshots.
func TestCanarySweepFinds(t *testing.T) {
	dir := t.TempDir()
	c := newCanaries(t, "a", "b", "c", "query")
	if err := os.MkdirAll(filepath.Join(dir, "x", "y"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"x/y/run.json": `{"v":"` + c["a"] + `"}`, "shot.png": c["b"], "clean.md": "nothing",
		"pages.jsonl": `{"links":["/a/?t=` + c["query"] + `"]}`, "report.md": "/a/?t=" + c["query"]} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := c.sweep(t, map[string]string{"stderr": "note: " + c["c"]}, dir)
	want := []string{filepath.ToSlash(filepath.Join(dir, "x", "y", "run.json")) + ": a", "stderr: c",
		filepath.ToSlash(filepath.Join(dir, "report.md")) + ": query", filepath.ToSlash(filepath.Join(dir, "pages.jsonl")) + ": query"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("hits %q, want %q", got, want)
	}
	if c["a"] == c["b"] || !strings.HasPrefix(c["a"], "cnrya") {
		t.Errorf("canaries %q", c)
	}
}
