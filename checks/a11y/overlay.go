// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"net/url"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/model"
)

// overlayHosts serve accessibility overlay widgets: scripts that promise
// to make a site accessible by adding a toolbar. They don't. The
// FTC fined the best known of them for saying so. A host matches itself
// and its subdomains.
var overlayHosts = []string{"acsbapp.com", "acsbap.com", "cdn.userway.org", "cdn.equalweb.com", "wsmcdn.audioeye.com",
	"cdn.enable.co.il", "max.access-ally.com"}

// overlayPlugin is the path of the WordPress plugin that adds one
// (One Click Accessibility), served from the site itself.
const overlayPlugin = "/wp-content/plugins/pojo-accessibility/"

// overlayGlobals are the globals overlay scripts define, for one loaded
// some other way (bundled, or from a host not listed). Only whether they
// exist is read, never what they hold.
var overlayGlobals = []struct{ name, what string }{
	{"acsbJS", "the accessiBe widget (acsbJS)"},
	{"UserWay", "the UserWay widget (UserWay)"},
	{"AudioEye", "the AudioEye widget (AudioEye)"},
}

// ftcOrder is the FTC's press release on its 2025 order against
// accessiBe.
const ftcOrder = "https://www.ftc.gov/news-events/news/press-releases/2025/04/ftc-approves-final-order-requiring-accessibe-pay-1-million"

// overlayScript returns the overlay among the page's scripts: its host,
// or the plugin's slug for the WordPress plugin. ok is false when there
// is none.
func overlayScript(resources []model.Resource) (source, what string, ok bool) {
	for _, r := range resources {
		if !strings.EqualFold(r.Type, "script") {
			continue
		}
		u, err := url.Parse(r.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		host := strings.ToLower(u.Hostname())
		for _, h := range overlayHosts {
			if host == h || strings.HasSuffix(host, "."+h) {
				return host, host, true
			}
		}
		if strings.HasPrefix(u.EscapedPath(), overlayPlugin) {
			return "pojo-accessibility", "the WordPress plugin pojo-accessibility", true
		}
	}
	return "", "", false
}

// overlayGlobalJS sets one bit per overlay global the page defines. It
// runs in the page's own world, where the globals are: typeof reads
// whether a name exists without calling anything of the page's.
const overlayGlobalJS = `(typeof acsbJS !== "undefined" ? 1 : 0) | (typeof UserWay !== "undefined" ? 2 : 0) | (typeof AudioEye !== "undefined" ? 4 : 0)`

// overlayGlobal returns the overlay global the page defines, if any.
func overlayGlobal(tab *rod.Page) (source, what string, ok bool) {
	res, err := proto.RuntimeEvaluate{Expression: overlayGlobalJS, ReturnByValue: true}.Call(tab)
	if err != nil || res.ExceptionDetails != nil || res.Result == nil {
		return "", "", false
	}
	bits := res.Result.Value.Int()
	for n, g := range overlayGlobals {
		if bits&(1<<n) != 0 {
			return "window." + g.name, g.what, true
		}
	}
	return "", "", false
}

// overlayIssue is the a11y/overlay notice for a page that loads what.
// It names no WCAG criterion: an overlay fails none by itself. It's
// there because owners are told it makes their site compliant.
func overlayIssue(source, what string) model.Issue {
	i := model.Issue{Check: "a11y/overlay", Level: model.Notice, Title: "Accessibility overlay widget found",
		Detail: "This page loads " + what + ". Overlay widgets don't make a site accessible; the FTC's 2025 order against accessiBe is about claims like that: " + ftcOrder,
		Source: source, Impact: "minor",
		Fix: "Fix the problems the other checks report in the site itself; don't rely on the widget or claim it makes the site accessible."}
	i.Identify()
	return i
}

// overlay returns the page's a11y/overlay notice: one per page, from its
// scripts or, failing that, its globals.
func overlay(tab *rod.Page, p *model.Page) (model.Issue, bool) {
	source, what, ok := overlayScript(p.Resources)
	if !ok {
		source, what, ok = overlayGlobal(tab)
	}
	if !ok {
		return model.Issue{}, false
	}
	return overlayIssue(source, what), true
}
