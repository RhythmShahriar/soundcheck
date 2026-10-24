// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seo

import (
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(Module{}) }

// Checks lists the checks the module reports.
func (Module) Checks() []check.CheckInfo {
	return []check.CheckInfo{
		{ID: "seo/canonical", Level: model.Notice, Title: "Canonical link changed", Doc: "The canonical URL changed or is gone."},
		{ID: "seo/description", Level: model.Notice, Title: "Meta description changed", Doc: "The meta description changed or is gone."},
		{ID: "seo/robots-meta", Level: model.Warning, Title: "Robots meta changed", Doc: "The page's robots meta changed, e.g. to noindex."},
		{ID: "seo/h1", Level: model.Notice, Title: "Headings changed", Doc: "The page lost its <h1> or has several."},
		{ID: "seo/hreflang", Level: model.Notice, Title: "Language links changed", Doc: "hreflang links changed or are gone."},
		{ID: "seo/og", Level: model.Notice, Title: "Open Graph tags changed", Doc: "Sharing tags changed or are gone."},
		{ID: "seo/jsonld", Level: model.Notice, Title: "Structured data changed", Doc: "JSON-LD was removed or is invalid."},
		{ID: "seo/redirect", Level: model.Notice, Title: "Redirect changed", Doc: "Where the page redirects to changed."},
		{ID: "seo/robots-txt", Level: model.Warning, Title: "robots.txt changed", Doc: "robots.txt changed, is gone or blocks the site."},
		{ID: "seo/sitemap", Level: model.Warning, Title: "Sitemap changed", Doc: "The sitemap is gone, fails or lists fewer pages."},
	}
}
