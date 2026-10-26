// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y_test

import (
	"fmt"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

func overlayOn(p *model.Page) []model.Issue {
	var out []model.Issue
	for _, i := range p.Issues {
		if i.Check == "a11y/overlay" {
			out = append(out, i)
		}
	}
	return out
}

func TestOverlayInBrowser(t *testing.T) {
	needBrowser(t)
	page := `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Shop</title>%s</head><body><main><h1>Shop</h1></main></body></html>`
	srv := pages(t, map[string]string{
		"/global/": fmt.Sprintf(page, `<script>window.acsbJS = {init: function () {}}; window.UserWay = 1;</script>`),
		"/plugin/": fmt.Sprintf(page, `<script src="/wp-content/plugins/pojo-accessibility/assets/js/app.js"></script>`),
		"/own/":    fmt.Sprintf(page, `<script src="/js/userway-analytics.js"></script><script>var acsb = 1;</script>`),
		"/none/":   fmt.Sprintf(page, ""),
		// A global whose getter never returns: the check gives up on it
		// and the page is still audited.
		"/stuck/": fmt.Sprintf(page, `<script>Object.defineProperty(window, "acsbJS", {get: function () { for (;;) {} }});</script>`),
		"/wp-content/plugins/pojo-accessibility/assets/js/app.js": "",
		"/js/userway-analytics.js":                                "",
	})
	r := audited(t, srv, "2.1", "", "/global/", "/plugin/", "/own/", "/none/", "/stuck/")
	got := overlayOn(r.Page("/global/"))
	if len(got) != 1 || got[0].Source != "window.acsbJS" || got[0].Level != model.Notice || got[0].Impact != "minor" {
		t.Errorf("/global/: %+v", got)
	}
	got = overlayOn(r.Page("/plugin/"))
	if len(got) != 1 || got[0].Source != "pojo-accessibility" {
		t.Errorf("/plugin/: %+v (resources %+v)", got, r.Page("/plugin/").Resources)
	}
	stuck := r.Page("/stuck/")
	if a, ok := stuck.Audits["a11y"]; !ok || !a.Finished() {
		t.Errorf("/stuck/ wasn't audited: %+v", a)
	}
	for _, path := range []string{"/own/", "/none/", "/stuck/"} {
		if got := overlayOn(r.Page(path)); len(got) > 0 {
			t.Errorf("%s: %+v", path, got)
		}
	}
}
