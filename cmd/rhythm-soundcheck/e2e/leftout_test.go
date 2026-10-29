// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/internal/canarytest"
)

// plainPage writes a page with a title, a heading and body. It has no
// links but the body's.
func plainPage(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>%s</title><link rel="icon" href="data:,"></head>
<body><main><h1>%s</h1>%s</main></body></html>`, title, title, body)
}

// A page the run leaves out gets a note in the terminal, masked as the
// run's outputs mask it: a snapshot page with a session the site put in
// a link (excluded on compare) and a --page with a reset token the
// default excludes drop. The raw values appear nowhere but the stored
// run.
func TestEAALeftOutPageMasked(t *testing.T) {
	needBrowser(t)
	if testing.Short() {
		t.Skip("runs Chrome twice")
	}
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("CI", "1")
	c := canarytest.New(t, "sid", "reset")
	sid := "tk_" + c["sid"]
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		plainPage(w, "Home", `<p><a href="/acct.html?sid=`+sid+`">My account</a></p>`)
	})
	mux.HandleFunc("/acct.html", func(w http.ResponseWriter, _ *http.Request) {
		plainPage(w, "Account", `<p>Your orders.</p>`)
	})
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	printed := map[string]string{}
	code, out, errOut := runCLI(t, append([]string{"snapshot", site.URL + "/", "--eaa", "--limit", "4", "--name", "b",
		"--page", "/acct.html?reset_token=" + c["reset"]}, eaaCommon...)...)
	printed["snapshot stdout"], printed["snapshot stderr"] = out, errOut
	if code != exitOK && code != exitFindings {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut = runCLI(t, append([]string{"compare", "--eaa", "--exclude", "/acct*"}, eaaCommon...)...)
	printed["compare stdout"], printed["compare stderr"] = out, errOut
	if code != exitOK && code != exitFindings {
		t.Fatalf("compare: exit %d\n%s\n%s", code, out, errOut)
	}
	all := strings.Join([]string{printed["snapshot stdout"], printed["snapshot stderr"], printed["compare stdout"], printed["compare stderr"]}, "\n")
	for _, want := range []string{
		"--page /acct.html?reset_token=••••• matches the default exclude *?*token=*; not checked",
		"page /acct.html?sid=••••• matches the exclude /acct*; not checked",
		"1 page from the snapshot wasn't checked this time",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in what the runs printed:\n%s", want, all)
		}
	}
	for _, h := range c.Sweep(t, printed, ".soundcheck") {
		name, _, _ := strings.Cut(h, ": ")
		if base := path.Base(filepath.ToSlash(name)); base == "run.json" || base == "pages.jsonl" {
			continue // the stored run keeps the path to visit it again
		}
		t.Errorf("left-out page canary written: %s", h)
	}
}
