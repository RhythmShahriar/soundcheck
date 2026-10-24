// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/browser"
)

func TestParse(t *testing.T) {
	f, err := Parse([]byte(`name: Checkout
start: /shop/
login: true
steps:
  - goto: /product/mug/
  - click: "button.single_add_to_cart_button"
  - click: {text: View cart}
  - fill: {selector: "#email", value: "${fake:email}"}
  - select: {selector: "#country", value: NZ}
  - check: "#terms"
  - check: {selector: "#news", checked: false}
  - submit: form.checkout
  - submit:
  - wait: 500
  - wait: .done
  - expect: {text: Thanks, url: /order-received/, status: 200}
  - expect: Order received
  - screenshot: done
`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range f.Steps {
		got = append(got, s.String())
	}
	want := []string{"goto /product/mug/", "click button.single_add_to_cart_button", `click "View cart"`, "fill #email",
		"select #country", "check #terms", "check #news", "submit form.checkout", "submit", "wait 500 ms", "wait for .done",
		`expect text "Thanks", url /order-received/, status 200`, `expect text "Order received"`, "screenshot done"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("steps:\n got %q\nwant %q", got, want)
	}
	if !f.Login || f.Start != "/shop/" || *f.Steps[6].Check.Checked {
		t.Errorf("flow: %+v", f)
	}
	for in, msg := range map[string]string{
		"steps: [{click: x}]":                                            "needs a name",
		"name: x\nsteps: []":                                             "at least one step",
		"name: x\nsteps: [{hover: a}]":                                   "unknown step",
		"name: x\nsteps: [{click: a, fill: b}]":                          "one action",
		"name: x\nsteps: [{fill: {value: a}}]":                           "needs a selector",
		"name: x\nsteps: [{fill: {selector: a, vlue: b}}]":               "vlue",
		"name: x\nsteps: [{fill: {selector: a, value: '${fake:nope}'}}]": "unknown ${fake:nope}",
		"name: x\nsteps: [{wait: 0}]":                                    "wait needs",
		"name: x\nsteps: [{expect: {}}]":                                 "expect needs",
		"name: x\nsteps: [{click: a}]\nextra: 1":                         "extra",
	} {
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: got %v, want %q", in, err, msg)
		}
	}
}

func TestExpandAndMask(t *testing.T) {
	env := func(k string) string { return map[string]string{"PASS": "hunter2"}[k] }
	fake := Fake{Seed: "run-1"}
	v, secrets, err := Expand("${env:PASS} / ${fake:email}", fake, env)
	if err != nil {
		t.Fatal(err)
	}
	if v != "hunter2 / soundcheck-test+"+fake.Tag()+"@example.com" || len(secrets) != 1 {
		t.Errorf("expand: %q %v", v, secrets)
	}
	if _, _, err := Expand("${env:MISSING}", fake, env); err == nil {
		t.Error("missing env var accepted")
	}
	if got := Mask("login hunter2 failed", secrets...); got != "login **** failed" {
		t.Errorf("mask: %q", got)
	}
	if a, b, a2 := (Fake{Seed: "a"}).Tag(), (Fake{Seed: "b"}).Tag(), (Fake{Seed: "a"}).Tag(); a == b || a != a2 {
		t.Error("tags should depend only on the seed")
	}
}

func TestFakeFor(t *testing.T) {
	f := Fake{Seed: "x"}
	for _, c := range []struct {
		in   Field
		want string
	}{
		{Field{Type: "email"}, "soundcheck-test+" + f.Tag() + "@example.com"},
		{Field{Type: "text", Name: "your-email"}, "soundcheck-test+" + f.Tag() + "@example.com"},
		{Field{Type: "tel"}, "+1 555 0100"},
		{Field{Type: "url"}, "https://example.com/soundcheck-test"},
		{Field{Type: "number", Min: "5", Max: "10"}, "5"},
		{Field{Type: "number", Max: "0"}, "0"},
		{Field{Type: "number"}, "1"},
		{Field{Type: "date", Min: "2031-02-01"}, "2031-02-01"},
		{Field{Type: "date", Max: "2025-01-01"}, "2025-01-01"},
		{Field{Type: "text", Name: "first_name"}, "Soundcheck"},
		{Field{Type: "text", Label: "Last name"}, "Test"},
		{Field{Type: "text", Name: "your-name"}, "Soundcheck Test"},
		{Field{Type: "text", Name: "zip", MaxLength: 3}, "123"},
		{Field{Type: "text", Name: "code", MinLength: 20}, "soundcheck-testxxxxx"},
		{Field{Tag: "textarea"}, "soundcheck-test: an automated form check by Rhythm Soundcheck (" + f.Tag() + "). Please ignore."},
	} {
		if got := f.For(c.in); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLoadAndInit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flows")
	if fl, err := Load(dir); err != nil || fl != nil {
		t.Fatalf("missing folder: %v %v", fl, err)
	}
	path, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Init(dir); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fl, err := Load(dir)
	if err != nil || len(fl) != 1 || fl[0].Name != "Contact form sends" || fl[0].File != path {
		t.Fatalf("load: %+v %v", fl, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.yml"), []byte("name: x\nsteps: [{nope: 1}]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "bad.yml") {
		t.Errorf("bad flow: %v", err)
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		t.Error("example missing")
	}
}

func TestRun(t *testing.T) {
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	var got url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/signup/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><form method="post" action="/done/">
<input id="email" name="email"><input id="pass" name="pass" type="password">
<select id="plan" name="plan"><option value="">Pick</option><option value="pro">Pro plan</option></select>
<label><input type="checkbox" id="terms" name="terms"> Terms</label><button>Sign up</button></form></body></html>`)
	})
	mux.HandleFunc("/done/", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		fmt.Fprint(w, `<html><body><h1>Welcome aboard</h1></body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	page, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(srv.URL)
	f, err := Parse([]byte(`name: Sign up
start: /signup/
steps:
  - fill: {selector: "#email", value: "${fake:email}"}
  - fill: {selector: "#pass", value: "${env:SC_PASS}"}
  - select: {selector: "#plan", value: Pro plan}
  - check: "#terms"
  - click: {text: Sign up}
  - expect: {text: Welcome aboard, url: /done/, status: 200}
  - expect: {text: "${env:SC_PASS}"}
  - expect: {selector: h2}
  - click: "#gone"
  - expect: {text: never reached}
`))
	if err != nil {
		t.Fatal(err)
	}
	saved := map[string]int{}
	fails := Run(context.Background(), page, f, Options{Base: base, Timeout: 2e9, Fake: Fake{Seed: "s"},
		Env:  func(k string) string { return map[string]string{"SC_PASS": "s3cret-pw"}[k] },
		Save: func(name string, data []byte) error { saved[name] = len(data); return nil }})
	if got.Get("email") != "soundcheck-test+"+(Fake{Seed: "s"}).Tag()+"@example.com" || got.Get("pass") != "s3cret-pw" ||
		got.Get("plan") != "pro" || got.Get("terms") != "on" {
		t.Errorf("posted %v", got)
	}
	var steps []string
	for _, fl := range fails {
		steps = append(steps, fmt.Sprintf("%d %s: %v", fl.Step, fl.Desc, fl.Err))
		if strings.Contains(fl.Err.Error(), "s3cret-pw") {
			t.Errorf("secret leaked: %v", fl.Err)
		}
	}
	want := []string{
		`7 expect text "${env:SC_PASS}": the page doesn't show "****"`,
		"8 expect h2: h2 isn't visible",
		"9 click #gone: #gone not found",
	}
	if strings.Join(steps, "\n") != strings.Join(want, "\n") {
		t.Errorf("failures:\n%s\nwant:\n%s", strings.Join(steps, "\n"), strings.Join(want, "\n"))
	}
}
