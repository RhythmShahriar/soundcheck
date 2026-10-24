// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package health

import (
	"slices"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(Module{}) }

// Checks lists the checks health reports from Go, then its built-in
// rules. A project's own rules are listed by `rules list`.
func (m Module) Checks() []check.CheckInfo {
	out := slices.Clone(goChecks)
	for _, r := range m.rules().Rules() {
		if !slices.ContainsFunc(out, func(c check.CheckInfo) bool { return c.ID == r.ID }) {
			out = append(out, check.CheckInfo{ID: r.ID, Level: r.Level, Title: r.Title, Doc: r.Fix})
		}
	}
	return out
}

var goChecks = []check.CheckInfo{
	{ID: "health/unreachable", Level: model.Error, Title: "Page can't be reached", Doc: "The server didn't answer or the connection failed."},
	{ID: "health/blocked", Level: model.Error, Title: "Blocked by a firewall or bot check", Doc: "A security plugin, CDN or host refused the visit."},
	{ID: "health/auth-required", Level: model.Error, Title: "Password required (401)", Doc: "The page asks for a login; pass -H or --cookie for staging gates."},
	{ID: "health/status", Level: model.Error, Title: "Page returns an error status", Doc: "The page answered 4xx or 5xx."},
	{ID: "health/redirect-chain", Level: model.Notice, Title: "Long redirect chain", Doc: "The page takes several redirects to reach."},
	{ID: "health/redirect", Level: model.Notice, Title: "Page now redirects", Doc: "The page moved somewhere else."},
	{ID: "health/php-error", Level: model.Error, Title: "PHP errors and warnings", Doc: "PHP printed an error, warning or notice into the page."},
	{ID: "health/white-screen", Level: model.Error, Title: "White screen", Doc: "The page loaded with almost nothing on it."},
	{ID: "health/wp-critical", Level: model.Error, Title: "WordPress critical error", Doc: "WordPress shows its 'critical error' page."},
	{ID: "health/database", Level: model.Error, Title: "Database error", Doc: "The site can't reach its database."},
	{ID: "health/error-page", Level: model.Error, Title: "Framework error page", Doc: "The site shows a server or framework error page."},
	{ID: "health/js-error", Level: model.Warning, Title: "JavaScript errors", Doc: "A script threw an error in the browser console."},
	{ID: "health/failed-request", Level: model.Warning, Title: "Failed requests", Doc: "A request the page made failed or returned an error."},
	{ID: "health/broken-asset", Level: model.Warning, Title: "Broken images, scripts and styles", Doc: "A file the page loads returns 404 or fails."},
	{ID: "health/mixed-content", Level: model.Warning, Title: "Mixed content", Doc: "An https page loads files over plain http."},
	{ID: "health/no-title", Level: model.Notice, Title: "No page title", Doc: "The page has no <title>."},
	{ID: "health/noindex", Level: model.Notice, Title: "Hidden from search engines", Doc: "The page says noindex."},
	{ID: "health/not-responsive", Level: model.Notice, Title: "Not responsive", Doc: "The page is wider than a phone screen."},
	{ID: "health/broken-link", Level: model.Warning, Title: "Broken links", Doc: "A link on the site leads to a 404 or an error."},
	{ID: "health/favicon", Level: model.Notice, Title: "No favicon", Doc: "The site's favicon is missing."},
	{ID: "health/title", Level: model.Notice, Title: "Page title changed", Doc: "The page's title is different."},
	{ID: "health/content-lost", Level: model.Warning, Title: "Content lost", Doc: "Most of the page's text is gone."},
}
