// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package health

import (
	"context"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

const cfScript = "https://shop.example/cdn-cgi/challenge-platform/h/b/orchestrate/chl_page/v1?ray=8a1b2c3d4e5f"

func res(urls ...string) []model.Resource {
	var out []model.Resource
	for _, u := range urls {
		out = append(out, model.Resource{URL: u, Type: "Script", Status: 200})
	}
	return out
}

// Pages a firewall answered, one or more per vendor: each is one
// health/blocked error naming the vendor and nothing else.
func TestBlockedByChallenge(t *testing.T) {
	for _, c := range []struct {
		name, title string
		p           model.Page
	}{
		{"cloudflare header", "Blocked by a bot challenge (Cloudflare)", model.Page{Status: 403, Title: "Just a moment...",
			Headers: map[string]string{"cf-mitigated": "challenge", "server": "cloudflare", "cf-ray": "8a1b2c3d4e5f-AMS"},
			Text:    "Verifying you are human. This may take a few seconds.", Resources: res(cfScript)}},
		{"cloudflare interstitial", "Blocked by a bot challenge (Cloudflare)", model.Page{Status: 503, Title: "Just a moment...",
			Headers: map[string]string{"server": "cloudflare"}, Text: "Checking your browser before accessing shop.example.",
			Resources: res(cfScript)}},
		{"cloudflare block page", "Blocked by a bot challenge (Cloudflare)", model.Page{Status: 403,
			Title:   "Attention Required! | Cloudflare",
			Headers: map[string]string{"server": "cloudflare"}, Text: "Sorry, you have been blocked. Cloudflare Ray ID: 8a1b2c3d4e5f"}},
		{"turnstile", "Blocked by a bot challenge (Cloudflare)", model.Page{Status: 200, Title: "Security check",
			Resources: res("https://challenges.cloudflare.com/turnstile/v0/api.js")}},
		{"sucuri header", "Blocked by a bot challenge (Sucuri)", model.Page{Status: 403, Title: "Sucuri WebSite Firewall - Access Denied",
			Headers: map[string]string{"server": "Sucuri/Cloudproxy", "x-sucuri-id": "16014", "x-sucuri-block": "BOT01"}}},
		{"sucuri page", "Blocked by a bot challenge (Sucuri)", model.Page{Status: 403, Title: "Sucuri WebSite Firewall - Access Denied",
			Headers: map[string]string{"server": "Sucuri/Cloudproxy"}, Text: "Access Denied - Sucuri Website Firewall"}},
		{"akamai page", "Blocked by a bot challenge (Akamai)", model.Page{Status: 403, Title: "Access Denied",
			Headers: map[string]string{"server": "AkamaiGHost"},
			Text:    "You don't have permission to access \"http://shop.example/\" on this server. Reference #18.6f2c1302.1729771200.1a2b3c"}},
		{"akamai challenge", "Blocked by a bot challenge (Akamai)", model.Page{Status: 428, Title: "Please wait",
			Resources: res("https://shop.example/_sec/cp_challenge/sec-cpt-int-4-3.js")}},
		{"imperva", "Blocked by a bot challenge (Imperva)", model.Page{Status: 200,
			Text: "Request unsuccessful. Incapsula incident ID: 1234000560012345678-1234567890"}},
		{"datadome", "Blocked by a bot challenge (DataDome)", model.Page{Status: 403,
			Headers: map[string]string{"x-datadome": "protected"}, Resources: res("https://geo.captcha-delivery.com/captcha/?initialCid=x")}},
		{"aws waf", "Blocked by a bot challenge (AWS WAF)", model.Page{Status: 202,
			Headers: map[string]string{"x-amzn-waf-action": "challenge"}}},
		{"generic captcha", "Blocked by a bot challenge", model.Page{Status: 403, Title: "Access check",
			Text: "Please complete the CAPTCHA below to continue."}},
		{"generic rate limit", "Blocked by a bot challenge", model.Page{Status: 429, Title: "Slow down",
			Resources: res("https://www.google.com/recaptcha/api.js")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := c.p
			p.Console = []string{"Uncaught TypeError: challenge is not a function"} // the firewall's, not the site's
			Module{}.CheckPage(ctx(), &p)
			if len(p.Issues) != 1 {
				t.Fatalf("issues = %+v, want only the block", p.Issues)
			}
			i := p.Issues[0]
			if i.Check != "health/blocked" || i.Level != model.Error || i.Title != c.title {
				t.Errorf("issue = %+v, want %q", i, c.title)
			}
			if !strings.Contains(i.Fix, "Allowlist the runner's IP address") || !strings.Contains(i.Fix, `-H "Name: value"`) {
				t.Errorf("fix = %q", i.Fix)
			}
			if !strings.HasPrefix(i.Detail, "The firewall answered instead of the site (") {
				t.Errorf("detail = %q", i.Detail)
			}
		})
	}
}

// Pages that only look a little like a challenge stay the site's.
func TestNotBlocked(t *testing.T) {
	for _, c := range []struct {
		name  string
		p     model.Page
		title string // the one issue expected, or "" for none
	}{
		{"article mentioning the phrase", model.Page{Status: 200, Title: "How we roast", TextLength: 900,
			Text: "Just a moment... that's how long the beans rest before we grind them. Then we check your browser history? No."}, ""},
		{"article titled like one", model.Page{Status: 200, Title: "Just a moment of calm", TextLength: 900,
			Text: "A slow morning with coffee.", Resources: res("https://shop.example/app.js")}, ""},
		{"cloudflare bot script on a normal page", model.Page{Status: 200, Title: "Shop", TextLength: 900, Text: "Fresh coffee.",
			Headers:   map[string]string{"server": "cloudflare", "cf-ray": "8a1b2c3d4e5f-AMS"},
			Resources: res("https://shop.example/cdn-cgi/challenge-platform/scripts/jsd/main.js")}, ""},
		{"200 behind cloudflare", model.Page{Status: 200, Title: "Shop", TextLength: 900, Text: "Fresh coffee.",
			Headers: map[string]string{"server": "cloudflare", "cf-ray": "8a1b2c3d4e5f-AMS", "cf-cache-status": "HIT"}}, ""},
		{"captcha on a contact form", model.Page{Status: 200, Title: "Contact", TextLength: 900,
			Text: "Fill in the captcha to send us a message.", Resources: res("https://www.google.com/recaptcha/api.js")}, ""},
		{"datadome on a normal page", model.Page{Status: 200, Title: "Shop", TextLength: 900, Text: "Fresh coffee.",
			Headers: map[string]string{"x-datadome": "protected"}}, ""},
		{"plain 403", model.Page{Status: 403, Title: "403 Forbidden", Text: "Forbidden. You don't have permission to access this resource.",
			Headers: map[string]string{"server": "nginx"}}, "Page blocked (403)"},
		{"401 without a prompt", model.Page{Status: 401, Title: "Unauthorized"}, "Page blocked (401)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := c.p
			Module{}.CheckPage(ctx(), &p)
			var got []string
			for _, i := range p.Issues {
				got = append(got, i.Title)
			}
			if strings.Join(got, "|") != c.title {
				t.Errorf("issues = %q, want %q", got, c.title)
			}
		})
	}
}

func TestAuthRequired(t *testing.T) {
	p := model.Page{Status: 401, Title: "401 Authorization Required", Headers: map[string]string{"www-authenticate": `Basic realm="Staging"`}}
	Module{}.CheckPage(ctx(), &p)
	if len(p.Issues) != 1 {
		t.Fatalf("issues = %+v", p.Issues)
	}
	i := p.Issues[0]
	if i.Check != "health/auth-required" || i.Level != model.Error || i.Title != "Password required (401)" ||
		i.Detail != `The server asks for a login: WWW-Authenticate: Basic realm="Staging"` || !strings.Contains(i.Fix, `-H "Authorization: Basic`) {
		t.Errorf("issue = %+v", i)
	}
}

// A site whose firewall starts challenging the runner is one BROKE line,
// not a changed title, lost text and every old issue FIXED on each page.
func TestCompareBlockedSite(t *testing.T) {
	page := func(path string) model.Page {
		return model.Page{URL: "https://shop.example" + path, Path: path, Status: 200, Title: "Shop " + path, TextLength: 2000,
			FinalURL: "https://shop.example" + path, Text: "Warning: Undefined array key \"id\" in /var/www/html/wp-content/themes/x/functions.php on line 12"}
	}
	blocked := func(path string) model.Page {
		return model.Page{URL: "https://shop.example" + path, Path: path, Status: 403, Title: "Just a moment...", TextLength: 60,
			FinalURL: "https://shop.example" + path, Headers: map[string]string{"cf-mitigated": "challenge"}, Resources: res(cfScript)}
	}
	before := siteRun(page("/"), page("/shop/"), page("/about/"))
	after := siteRun(blocked("/"), blocked("/shop/"), page("/about/"))
	after.Pages[2].Title = "About us" // a real change on a page that still loads
	cmp := compare.Compare(context.Background(), &check.CompareContext{Config: config.Default(), Before: before, After: after},
		[]check.Module{Module{}}, "test")
	var got []string
	for _, c := range cmp.Changes {
		got = append(got, c.Class+" "+c.Title+" "+strings.Join(c.Pages, ","))
	}
	want := []string{"broke Blocked by a bot challenge (Cloudflare) /,/shop/", "changed Page title changed /about/"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("changes = %q, want %q", got, want)
	}
}

// A change that also concerns a page the firewall still lets through
// stays: it isn't only the block's doing.
func TestGatedKeepsMixedChanges(t *testing.T) {
	changes := []model.Change{
		{Class: model.Broke, Check: "health/blocked", Title: "Blocked by a bot challenge", Pages: []string{"/"}},
		{Class: model.Changed, Check: "visual/block", Title: "New block", Pages: []string{"/", "/b/"}},
		{Class: model.Changed, Check: "visual/page", Title: "Page looks different", Pages: []string{"/"}},
		{Class: model.Slower, Check: "speed/site", Title: "Site got slower"},
	}
	got := gated(changes)
	if len(got) != 3 || got[1].Check != "visual/block" || got[2].Check != "speed/site" {
		t.Errorf("gated = %+v", got)
	}
	unblocked := []model.Change{{Class: model.Fixed, Check: "health/blocked", Pages: []string{"/"}}, {Class: model.Changed, Pages: []string{"/"}}}
	if got := gated(unblocked); len(got) != 2 {
		t.Errorf("a FIXED block hid changes: %+v", got)
	}
}
