// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Only the issues added after the page checks, by CheckSite, are marked.
func TestMarkSiteIssues(t *testing.T) {
	run := &model.Run{Pages: []model.Page{
		{Path: "/a/", Issues: []model.Issue{{Check: "health/php-error"}, {Check: "cache/leak"}}},
		{Path: "/b/", Issues: []model.Issue{{Check: "health/js-error"}}},
		{Path: "/c/", Issues: []model.Issue{{Check: "cache/private-cached"}}},
	}}
	markSiteIssues(run, []int{1, 1})
	want := map[string]bool{"health/php-error": false, "cache/leak": true, "health/js-error": false, "cache/private-cached": false}
	for _, p := range run.Pages {
		for _, i := range p.Issues {
			if i.FromSite != want[i.Check] {
				t.Errorf("%s on %s: FromSite = %v", i.Check, p.Path, i.FromSite)
			}
		}
	}
}
