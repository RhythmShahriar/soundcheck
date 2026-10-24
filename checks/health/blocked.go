// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package health

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Check IDs for a page that a firewall or a password answered instead of
// the site. They are a contract: testbed scenarios expect them.
const (
	checkBlocked = "health/blocked"
	checkAuth    = "health/auth-required"
)

// challenge is a firewall or bot-protection page recognized by blockedBy.
type challenge struct {
	vendor   string // "Cloudflare", or "" when no vendor is recognizable
	evidence string // what gave it away, for the issue's detail
}

// challengeWords are what bot challenges and firewall block pages say.
// They only count on a 403 or 429, so an article about captchas is fine.
var challengeWords = []string{
	"captcha", "verify you are human", "verifying you are human", "are you a robot", "checking your browser",
	"checking if the site connection is secure", "bot protection", "automated access", "unusual traffic",
}

// challengeScripts are scripts and frames that challenges load. Those
// marked interstitial also run on ordinary pages (Turnstile on contact
// forms, Imperva's per-page script), so they only count on a page that
// looks like an interstitial: see interstitial.
var challengeScripts = []struct {
	vendor, part string
	interstitial bool
}{
	{"Cloudflare", "/cdn-cgi/challenge-platform/", true},
	{"Cloudflare", "challenges.cloudflare.com/turnstile", true},
	{"Akamai", "/_sec/cp_challenge/", false},
	{"Imperva", "/_incapsula_resource", true},
	{"DataDome", "captcha-delivery.com", false},
	{"", "hcaptcha.com/", false},
	{"", "google.com/recaptcha/", false},
	{"", "recaptcha.net/recaptcha/", false},
}

// blockedBy reports whether p is a firewall's block or challenge page
// rather than the site: Cloudflare, Sucuri, Akamai, Imperva, DataDome and
// AWS WAF by their headers and pages, any other by a 403 or 429 that asks
// for a captcha or says it checks the browser.
func blockedBy(p *model.Page) (challenge, bool) {
	h := p.Headers
	title := strings.ToLower(strings.TrimSpace(p.Title))
	text := strings.ToLower(p.Text[:min(len(p.Text), 5000)])
	server := strings.ToLower(h["server"])
	refused := p.Status == 403 || p.Status == 429

	// Headers a firewall sets only when it challenges or blocks.
	switch {
	case strings.EqualFold(h["cf-mitigated"], "challenge"):
		return challenge{"Cloudflare", "cf-mitigated: challenge"}, true
	case h["x-amzn-waf-action"] != "":
		return challenge{"AWS WAF", "x-amzn-waf-action: " + h["x-amzn-waf-action"]}, true
	case p.Status >= 400 && h["x-sucuri-block"] != "":
		return challenge{"Sucuri", "x-sucuri-block: " + h["x-sucuri-block"]}, true
	case refused && h["x-datadome"] != "":
		return challenge{"DataDome", fmt.Sprintf("x-datadome header on a %d", p.Status)}, true
	}
	// A firewall's interstitial refuses the request or carries the
	// vendor's challenge title. Cloudflare's challenge platform also
	// serves invisible checks on the site's own error pages, so for it
	// only the title counts.
	cfTitle := strings.HasPrefix(title, "just a moment") || strings.Contains(title, "attention required")
	interstitial := func(part string) bool {
		if part == "/cdn-cgi/challenge-platform/" {
			return strings.HasPrefix(title, "just a moment")
		}
		return refused || p.Status == 503 || cfTitle
	}
	script := ""
	for _, u := range resourceURLs(p) {
		lu := strings.ToLower(u)
		for _, s := range challengeScripts {
			if strings.Contains(lu, s.part) {
				if s.vendor != "" {
					if !s.interstitial || interstitial(s.part) {
						return challenge{s.vendor, "loads " + s.part}, true
					}
					continue
				}
				script = s.part
			}
		}
	}
	switch {
	case p.Status >= 400 && (strings.Contains(server, "sucuri") || h["x-sucuri-id"] != "") &&
		(strings.Contains(title, "sucuri") || strings.Contains(text, "sucuri website firewall")):
		return challenge{"Sucuri", fmt.Sprintf("Sucuri firewall page (%d)", p.Status)}, true
	case p.Status == 403 && strings.Contains(server, "akamaighost") && strings.Contains(title, "access denied"):
		return challenge{"Akamai", "Akamai \"Access Denied\" page (403)"}, true
	case p.Status == 403 && server == "cloudflare" && strings.Contains(title, "attention required"):
		return challenge{"Cloudflare", "Cloudflare \"Attention Required\" page (403)"}, true
	case strings.Contains(text, "incapsula incident id"):
		return challenge{"Imperva", "Incapsula incident page"}, true
	case !refused:
		return challenge{}, false
	case script != "":
		return challenge{"", fmt.Sprintf("a %d that loads %s", p.Status, script)}, true
	}
	for _, w := range challengeWords {
		if strings.Contains(title, w) || strings.Contains(text, w) {
			return challenge{"", fmt.Sprintf("a %d that says %q", p.Status, w)}, true
		}
	}
	return challenge{}, false
}

// resourceURLs are the URLs a page loaded or tried to load.
func resourceURLs(p *model.Page) []string {
	urls := make([]string, 0, len(p.Resources)+len(p.Failed))
	for _, r := range p.Resources {
		urls = append(urls, r.URL)
	}
	for _, r := range p.Failed {
		urls = append(urls, r.URL)
	}
	return urls
}

// gatekeeper reports a page that a firewall or a password prompt
// answered instead of the site and returns whether it did: nothing else
// on such a page is the site's, so CheckPage stops there.
func gatekeeper(p *model.Page) bool {
	if p.Status == 401 && p.Headers["www-authenticate"] != "" {
		check.Page(p, model.Issue{Check: checkAuth, Level: model.Error, Title: "Password required (401)",
			Detail: "The server asks for a login: WWW-Authenticate: " + p.Headers["www-authenticate"],
			Fix: `Pass the login with -H "Authorization: Basic <base64 of user:password>", ` +
				"or allowlist the runner's IP address in the server's access rules."})
		return true
	}
	c, ok := blockedBy(p)
	if !ok {
		return false
	}
	title, fix := "Blocked by a bot challenge", "Allowlist the runner's IP address in the firewall or bot protection"
	if c.vendor != "" {
		title += " (" + c.vendor + ")"
		fix = "Allowlist the runner's IP address in " + c.vendor
	}
	if c.vendor == "Cloudflare" {
		fix += " with a WAF custom rule that skips bot checks for it"
	}
	check.Page(p, model.Issue{Check: checkBlocked, Level: model.Error, Title: title,
		Detail: "The firewall answered instead of the site (" + c.evidence + "), so nothing else on this page was checked.",
		Fix:    fix + `, or pass a header it lets through with -H "Name: value".`})
	return true
}

// gated drops changes that only concern pages a NEW or BROKE
// health/blocked or health/auth-required change lists: once a firewall
// answers, every other difference on those pages (titles, screenshots,
// issues the site had) has that one cause.
func gated(changes []model.Change) []model.Change {
	pages := map[string]bool{}
	for _, c := range changes {
		if (c.Check == checkBlocked || c.Check == checkAuth) && c.Class != model.Fixed {
			for _, p := range c.Pages {
				pages[p] = true
			}
		}
	}
	if len(pages) == 0 {
		return changes
	}
	return slices.DeleteFunc(changes, func(c model.Change) bool {
		if c.Check == checkBlocked || c.Check == checkAuth || len(c.Pages) == 0 {
			return false
		}
		return !slices.ContainsFunc(c.Pages, func(p string) bool { return !pages[p] })
	})
}
