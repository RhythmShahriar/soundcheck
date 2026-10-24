// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package cache

import (
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(Module{}) }

// Checks lists the checks the module reports.
func (Module) Checks() []check.CheckInfo {
	return []check.CheckInfo{
		{ID: "cache/leak", Level: model.Error, Title: "Logged-in page shown to visitors", Doc: "The cache served a logged-in page to a visitor."},
		{ID: "cache/private-cached", Level: model.Error, Title: "Private page cached", Doc: "A page meant for one visitor is cached for all."},
		{ID: "cache/expired-nonce", Level: model.Warning, Title: "Cached page has an expired token", Doc: "A cached form's nonce is too old to submit."},
		{ID: "cache/stale", Level: model.Warning, Title: "Stale cached page", Doc: "The cache serves an old copy of the page."},
		{ID: "cache/none", Level: model.Notice, Title: "No page cache", Doc: "Pages aren't cached."},
		{ID: "cache/never-cached", Level: model.Notice, Title: "Pages never served from the cache", Doc: "The cache never answers for pages."},
		{ID: "cache/stopped", Level: model.Warning, Title: "Page cache stopped working", Doc: "The cache answered before and no longer does."},
	}
}
