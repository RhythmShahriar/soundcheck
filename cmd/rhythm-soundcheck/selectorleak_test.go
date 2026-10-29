// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/internal/canarytest"
)

// eaaCommon are the flags the browser tests share: one viewport, no
// download, a quick rate and plain output. The e2e package's tests use
// the same.
var eaaCommon = []string{"--no-download", "--viewport", "desktop", "--rate", "50", "--plain"}

// selectorCanaries are values a site put in the ids, names and classes
// of its elements: a GitHub token, a JWT and email addresses. Each is
// assembled at runtime from a random part of letters g to z, so the a11y
// module doesn't take an id for a generated one (digits or hex) and
// leave it out of the selector.
func selectorCanaries(t *testing.T) canarytest.Canaries {
	t.Helper()
	const letters = "ghijklmnopqrstuvwxyzGHIJKLMNOPQRSTUVWXYZ"
	random := func(n int) string {
		b := make([]byte, n)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		for i := range b {
			b[i] = letters[int(b[i])%len(letters)]
		}
		return string(b)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return canarytest.Canaries{
		"id-token":   "gh" + "p_" + random(36),
		"name-jwt":   enc([]byte(`{"alg":"HS256"}`)) + "." + enc([]byte(`{"sub":"`+random(12)+`"}`)) + "." + random(24),
		"id-email":   "b-mark." + random(8) + "@" + "idcanary.test",
		"class-tok":  "gh" + "p_" + random(36),
		"trap-email": "s-jane." + random(8) + "@" + "mailcanary.test",
	}
}

// selectorPage is a page whose unlabeled controls carry c in their id,
// name and class, with a keyboard trap that keeps a select whose id is
// an email address out of reach.
func selectorPage(c canarytest.Canaries) string {
	return fmt.Sprintf(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Ids</title><link rel="icon" href="data:,"></head>
<body><main><h1>Ids</h1>
<select id="%s"><option>a</option></select>
<select name="%s"><option>b</option></select>
<button id="%s"></button>
<select class="%s"><option>c</option></select>
<input id="trap" aria-label="Coupon" onkeydown="if (event.key === 'Tab') { event.preventDefault(); this.focus(); }">
<select id="%s" aria-label="Zone"><option>z</option></select>
</main></body></html>`, c["id-token"], c["name-jwt"], c["id-email"], c["class-tok"], c["trap-email"])
}

// Selectors name elements by their id or name, which can hold a token
// or an email address. The a11y and keyboard issues, their details,
// the keyboard trace and every output mask them: none of the values is
// written anywhere, the stored run included.
func TestEAASelectorCanaries(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome")
	}
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("CI", "1")
	c := selectorCanaries(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, selectorPage(c))
	}))
	defer srv.Close()
	if err := os.Mkdir("out", 0o700); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, append([]string{"run", srv.URL + "/ids", "--eaa", "-o", "out/r.json", "-o", "out/r.md", "-o", "out/r.html", "-o", "out/r.xml"}, eaaCommon...)...)
	if code != exitOK && code != exitFindings {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	report, err := os.ReadFile(filepath.Join("out", "r.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a11y/select-name", "a11y/button-name", "keyboard/trap", `#gh` + `p_•••••`, `***@***`} {
		if !strings.Contains(string(report), want) {
			t.Errorf("r.json lacks %q, so the canaries weren't in play", want)
		}
	}
	for _, h := range c.Sweep(t, map[string]string{"stdout": out, "stderr": errOut}, ".soundcheck", "out") {
		t.Errorf("selector canary written: %s", h)
	}
}

// An --against comparison's comparison.json masks the query values of
// its page paths, as every report of it does: nothing picks pages from
// it later. Only the stored runs keep them.
func TestEAAAgainstComparisonMasked(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome")
	}
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("CI", "1")
	c := canarytest.New(t, "api-key")
	page := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Ids</title><link rel="icon" href="data:,"></head><body><main><h1>Ids</h1>%s</main></body></html>`, body)
		}))
	}
	against, target := page(`<select><option>a</option></select>`), page("")
	defer against.Close()
	defer target.Close()
	code, out, errOut := runCLI(t, append([]string{target.URL, "--against", against.URL + "/ids?api_key=" + c["api-key"], "--eaa"}, eaaCommon...)...)
	if code != exitOK && code != exitFindings {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "FIXED") {
		t.Fatalf("no FIXED change, so the comparison names no page:\n%s", out)
	}
	stored := 0
	for _, h := range c.Sweep(t, map[string]string{"stdout": out, "stderr": errOut}, ".soundcheck") {
		name, _, _ := strings.Cut(h, ": ")
		if base := path.Base(name); base == "run.json" || base == "pages.jsonl" {
			stored++
			continue
		}
		t.Errorf("query canary written: %s", h)
	}
	if stored == 0 {
		t.Error("the stored runs lost the page's query")
	}
}
