// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package eaa

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// eaaSite serves four pages that break the way an update could for a
// keyboard or screen reader user: a keyboard trap in the newsletter field
// on /, a dialog on /about/ that drops focus when it closes and a
// checkout field that loses its label. /help/ never changes.
func eaaSite(t *testing.T, broken *atomic.Bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// Broken: Tab in the field puts focus straight back on it.
		trap := ""
		if broken.Load() {
			trap = `<script>document.getElementById("news").addEventListener("keydown", function (e) {
  if (e.key === "Tab") { e.preventDefault(); document.getElementById("news").focus(); }
});</script>`
		}
		eaaPage(w, "Brew", `<p>Fresh coffee, roasted daily.</p>
<form action="/" method="get"><label for="news">Newsletter</label> <input id="news" type="email" autocomplete="email">
<button type="button">Sign up</button></form><p><a href="/help/">Questions?</a></p>`+trap)
	})
	mux.HandleFunc("/checkout/", func(w http.ResponseWriter, _ *http.Request) {
		label := `<label for="email">Email</label> `
		if broken.Load() {
			label = ""
		}
		eaaPage(w, "Checkout", `<form action="/checkout/" method="get">`+label+`<input id="email" name="email" type="email" autocomplete="email">
<button type="button">Continue</button></form>`)
	})
	mux.HandleFunc("/about/", func(w http.ResponseWriter, _ *http.Request) {
		// Good: closing the dialog returns focus to the button that
		// opened it. Broken: the dialog is removed and focus is lost.
		restore := `document.getElementById("hours-open").focus();`
		if broken.Load() {
			restore = ""
		}
		eaaPage(w, "About", `<p>We roast in small batches.</p>
<button type="button" id="hours-open" aria-haspopup="dialog">Opening hours</button>
<div id="hours" role="dialog" aria-modal="true" aria-labelledby="hours-title" hidden>
<h2 id="hours-title">Opening hours</h2><p>Monday to Friday, 8 to 18.</p><button type="button" id="hours-close">Close</button></div>
<script>
var dialog = document.getElementById("hours");
document.getElementById("hours-open").addEventListener("click", function () {
  dialog.hidden = false; document.getElementById("hours-close").focus();
});
document.getElementById("hours-close").addEventListener("click", function () {
  dialog.hidden = true; `+restore+`
});
document.addEventListener("keydown", function (e) { if (e.key === "Escape" && !dialog.hidden) { document.getElementById("hours-close").click(); } });
// A modal keeps Tab inside: Close is its only control.
dialog.addEventListener("keydown", function (e) { if (e.key === "Tab") { e.preventDefault(); document.getElementById("hours-close").focus(); } });
</script>`)
	})
	mux.HandleFunc("/help/", func(w http.ResponseWriter, _ *http.Request) {
		eaaPage(w, "Help", `<p>Write to us any time.</p>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// The whole --eaa round trip in a real browser: a snapshot of the good
// site, a compare of the unchanged site (exit 0, nothing found), then a
// compare of the broken site: the trap is BROKE, the field without a label is
// NEW and --fail-on broke exits 1.
func TestEAAEndToEnd(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome three times")
	}
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("CI", "1") // no one-time tip
	var broken atomic.Bool
	site := eaaSite(t, &broken)

	code, out, errOut := runCLI(t, append([]string{"snapshot", site.URL, "--eaa", "--name", "before"}, eaaCommon...)...)
	if code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	if run := readRunJSON(t, filepath.Join(".soundcheck", "snapshots", "before")); !run.Options.EAA || run.Options.WCAG != "2.1" {
		t.Errorf("snapshot options %+v", run.Options)
	}

	// Unchanged: nothing to report.
	code, out, errOut = runCLI(t, append([]string{"compare", "--eaa", "--fail-on", "broke"}, eaaCommon...)...)
	if code != exitOK {
		t.Fatalf("clean compare: exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "Nothing changed; no failures found by these tests") {
		t.Errorf("clean compare: no verdict in\n%s", out)
	}
	if !strings.Contains(strings.Join(strings.Fields(out), " "), model.EvidenceNote) {
		t.Errorf("clean compare: no evidence note in\n%s", out)
	}

	broken.Store(true)
	code, c := compareJSON(t, append([]string{"--eaa", "--fail-on", "broke"}, eaaCommon...)...)
	has := func(class, check, page string) bool {
		return slices.ContainsFunc(c.Changes, func(ch model.Change) bool {
			return ch.Class == class && ch.Check == check && slices.Contains(ch.Pages, page)
		})
	}
	if !has(model.New, "a11y/label", "/checkout/") {
		t.Errorf("no NEW a11y/label on /checkout/: %s", changeList(c))
	}
	if has(model.New, "a11y/label", "/help/") || slices.ContainsFunc(c.Changes, func(ch model.Change) bool { return slices.Contains(ch.Pages, "/help/") }) {
		t.Errorf("the unchanged page changed: %s", changeList(c))
	}
	if c.EAA == nil || c.EAA.EvidenceNote != model.EvidenceNote {
		t.Errorf("no eaa block: %+v", c.EAA)
	}
	if !has(model.Broke, "keyboard/trap", "/") || code != exitFindings {
		t.Errorf("want BROKE keyboard/trap on / and exit 1, got exit %d: %s", code, changeList(c))
	}
	// The dialog that drops focus is a warning, so NEW, not BROKE.
	if !has(model.New, "keyboard/focus-lost", "/about/") {
		t.Errorf("want NEW keyboard/focus-lost on /about/: %s", changeList(c))
	}
}
