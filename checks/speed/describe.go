// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package speed

import (
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func init() { check.Register(New()) }

// Checks lists the checks the module reports.
// Metric checks are named after their metric, speed/<metric>.
func (*Module) Checks() []check.CheckInfo {
	return []check.CheckInfo{
		{ID: "speed/lcp", Level: model.Warning, Title: "Slow largest paint (LCP)", Doc: "The main content takes long to show."},
		{ID: "speed/tbt", Level: model.Warning, Title: "Blocked main thread (TBT)", Doc: "Scripts keep the page from responding."},
		{ID: "speed/cls", Level: model.Warning, Title: "Layout shift (CLS)", Doc: "Content moves while the page loads."},
		{ID: "speed/ttfb", Level: model.Warning, Title: "Slow server response (TTFB)", Doc: "The server takes long to answer."},
		{ID: "speed/weight", Level: model.Warning, Title: "Heavy page", Doc: "The page downloads a lot."},
		{ID: "speed/requests", Level: model.Notice, Title: "Many requests", Doc: "The page makes many requests."},
		{ID: "speed/oversized-images", Level: model.Notice, Title: "Oversized images", Doc: "Images are bigger than they're shown."},
		{ID: "speed/lazy-load", Level: model.Notice, Title: "Images not lazy-loaded", Doc: "Images below the fold load at once."},
		{ID: "speed/render-blocking", Level: model.Notice, Title: "Render-blocking files", Doc: "Scripts or styles hold up the first paint."},
		{ID: "speed/unused-css", Level: model.Notice, Title: "Mostly unused CSS", Doc: "Style files are mostly unused."},
		{ID: "speed/unused-js", Level: model.Notice, Title: "Mostly unused JavaScript", Doc: "Script files are mostly unused."},
		{ID: "speed/compression", Level: model.Warning, Title: "Files sent uncompressed", Doc: "Text files are sent without gzip or brotli."},
		{ID: "speed/http-version", Level: model.Notice, Title: "Old HTTP version", Doc: "The site doesn't use HTTP/2 or HTTP/3."},
		{ID: "speed/cache-headers", Level: model.Notice, Title: "Files without cache headers", Doc: "Static files can't be cached by browsers."},
		{ID: "speed/third-party", Level: model.Notice, Title: "Heavy third-party scripts", Doc: "Other sites' scripts add weight."},
		{ID: "speed/site", Level: model.Notice, Title: "Site-wide speed", Doc: "Speed across all pages."},
		{ID: "speed/plugin-cost", Level: model.Notice, Title: "Plugin adds weight", Doc: "A plugin adds weight to pages."},
		{ID: "speed/theme-cost", Level: model.Notice, Title: "Theme adds weight", Doc: "The theme adds weight to pages."},
		{ID: "speed/host-cost", Level: model.Notice, Title: "Third-party host adds weight", Doc: "Another site adds weight to pages."},
	}
}
