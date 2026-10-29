// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// serveFixture starts h on the first free port of 127.0.0.1:40400-40449.
func serveFixture(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	for port := 40400; port < 40450; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv := httptest.NewUnstartedServer(h)
		_ = srv.Listener.Close()
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	t.Fatal("no free port in 40400-40449")
	return nil
}

// A mouse-only control the snapshot reported is never FIXED by a later
// visit that couldn't judge it or that took a page-wide key handler for
// its answer. In "async" its click asks the server and adds an item when
// the reply comes; the later page also has a ticking clock, so the walk
// can't tell the click did anything: the issue is kept, not checked. In
// "marker" the later page adds a script that marks the page as used by
// keyboard on any key press: the control still ignores Enter and Space,
// so the issue is still open.
func TestMouseOnlyNeverFalselyFixed(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome twice per case")
	}
	cases := []struct {
		name, control, head, after string
		kept                       bool
	}{
		{name: "async", kept: true,
			control: `<div id="more" tabindex="0" role="button">Load more</div><ul id="list"></ul>
<script>document.getElementById("more").addEventListener("click", function () {
  fetch("/api/more").then(function (r) { return r.text(); }).then(function (t) {
    var li = document.createElement("li"); li.textContent = t; document.getElementById("list").appendChild(li);
  });
});</script>`,
			after: `<p id="clock"></p><script>setInterval(function () { document.getElementById("clock").textContent = Date.now(); }, 50);</script>`},
		{name: "marker",
			control: `<div id="more" tabindex="0" role="button">Menu</div><p id="out"></p>
<script>document.getElementById("more").addEventListener("click", function () { document.getElementById("out").textContent = "Opened"; });</script>`,
			head: `<script>document.addEventListener("keydown", function () { document.documentElement.classList.add("kbd-user"); });</script>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
			t.Setenv("SOUNDCHECK_ECHO", "")
			t.Setenv("CI", "1")
			var later atomic.Bool
			site := serveFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/more":
					_, _ = fmt.Fprint(w, "Item")
				case "/":
					var b strings.Builder
					b.WriteString(c.control)
					if later.Load() {
						b.WriteString(c.after)
					}
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					head := ""
					if later.Load() {
						head = c.head
					}
					_, _ = fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Shop</title><link rel="icon" href="data:,">%s</head><body><main><h1>Shop</h1>%s</main></body></html>`, head, b.String())
				default:
					http.NotFound(w, r)
				}
			}))
			ours := func(ch model.Change) bool { return ch.Check == "keyboard/not-operable" && ch.Source == "#more" }

			code, out, errOut := runCLI(t, append([]string{"snapshot", site.URL + "/", "--eaa", "--name", "before"}, eaaCommon...)...)
			if code != exitOK && code != exitFindings {
				t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
			}
			snap, err := store.LoadRun(filepath.Join(".soundcheck", "snapshots", "before"))
			if err != nil {
				t.Fatal(err)
			}
			if countIssues(snap, "keyboard/not-operable") != 1 {
				t.Fatalf("the snapshot didn't report the mouse-only control:\n%s", out)
			}

			later.Store(true)
			_, cmp := compareJSON(t, append([]string{"--eaa"}, eaaCommon...)...)
			for _, ch := range cmp.Changes {
				if ours(ch) && ch.Class == model.Fixed {
					t.Errorf("the control still ignores Enter and Space, yet it is FIXED:%s", changeList(cmp))
				}
			}
			kept := false
			for _, ch := range cmp.Ignored {
				if ours(ch) {
					kept = strings.HasPrefix(ch.Reason, model.NotCheckedReason)
				}
			}
			if kept != c.kept {
				t.Errorf("kept as not checked: %v, want %v (ignored %+v)", kept, c.kept, cmp.Ignored)
			}
			if !c.kept {
				code, out, _ := runCLI(t, append([]string{"compare", "--eaa"}, eaaCommon...)...)
				if strings.Contains(out, "FIXED") || strings.Contains(out, "no failures found") {
					t.Errorf("compare: exit %d\n%s", code, out)
				}
			}
		})
	}
}
