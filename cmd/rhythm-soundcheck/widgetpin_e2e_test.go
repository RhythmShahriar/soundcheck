// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// mouseOnlySite serves three pages that each hold the same control: a
// focusable div that a click changes the page with and no key does. The
// home page links /blog/zeta.html, then /blog/alpha.html; zeta links
// back to the home page by its bare path.
func mouseOnlySite(t *testing.T) *httptest.Server {
	t.Helper()
	control := `<div id="menu" tabindex="0">Menu</div><p id="out"></p>
<script>document.getElementById("menu").addEventListener("click", function () { document.getElementById("out").textContent = "Opened"; });</script>`
	pages := map[string]string{
		"/":                `<p><a href="/blog/zeta.html">Zeta</a> <a href="/blog/alpha.html">Alpha</a></p>`,
		"/blog/zeta.html":  `<p><a href="/">Home</a></p>`,
		"/blog/alpha.html": `<p>Alpha.</p>`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		eaaPage(w, "Page", body+control)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A control broken on every page of a URL group is tested on one page
// per group. A snapshot started at /?ref=mail tests the home group
// there; the comparison plans / too, which comes first by path. It must
// test the group on the snapshot's page again, so an unchanged site
// reports neither a NEW issue on / nor a FIXED one on /?ref=mail;
// --fail-on new passes.
func TestWidgetGroupSamePage(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome twice")
	}
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("CI", "1")
	site := mouseOnlySite(t)

	code, out, errOut := runCLI(t, append([]string{"snapshot", site.URL + "/?ref=mail", "--eaa", "--name", "before"}, eaaCommon...)...)
	if code != exitOK && code != exitFindings {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	snap, err := store.LoadRun(filepath.Join(".soundcheck", "snapshots", "before"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(snap.WidgetPages, "/?ref=mail") {
		t.Fatalf("the snapshot didn't test the home group on its start page: %q", snap.WidgetPages)
	}
	if snap.Page("/") == nil {
		t.Fatalf("the snapshot has no page /, so the comparison can't test it: %q", snap.WidgetPages)
	}

	code, c := compareJSON(t, append([]string{"--eaa", "--fail-on", "new"}, eaaCommon...)...)
	for _, ch := range c.Changes {
		if ch.Check == "keyboard/not-operable" {
			t.Errorf("unchanged site: %s %s %v", ch.Class, ch.Check, ch.Pages)
		}
	}
	if code != exitOK {
		t.Errorf("compare --fail-on new: exit %d:%s", code, changeList(c))
	}
	if n := countIssues(snap, "keyboard/not-operable"); n != 2 {
		t.Errorf("the snapshot found the mouse-only control on %d pages, want 2 (one per group)", n)
	}
}

// countIssues counts the issues with check on run's pages.
func countIssues(run *model.Run, check string) int {
	n := 0
	for _, p := range run.Pages {
		for _, i := range p.Issues {
			if i.Check == check {
				n++
			}
		}
	}
	return n
}
