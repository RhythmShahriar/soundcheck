// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/runner"
	"github.com/RhythmShahriar/soundcheck/store"
)

// cardNo is a Luhn-valid test card number, assembled at run time.
var cardNo = "4111 1111" + " 1111 1111"

// mask masks only what was added after the given counts and keys the
// issues on the masked title.
func TestMaskAddedOnly(t *testing.T) {
	p := &model.Page{
		Issues:   []model.Issue{{Check: "health/x", Title: "Kept " + prefilled}},
		Interact: []model.Interaction{{Kind: "form", Label: "Kept " + prefilled}},
	}
	p.Issues = append(p.Issues, deadIssue("Email "+prefilled, false, "Pay with "+cardNo))
	p.Interact = append(p.Interact, model.Interaction{Kind: "click", Selector: "#pay", Label: "Email " + prefilled, Detail: "card " + cardNo})
	mask(p, 1, 1)
	if p.Issues[0].Title != "Kept "+prefilled || p.Interact[0].Label != "Kept "+prefilled {
		t.Errorf("earlier entries changed: %+v %+v", p.Issues[0], p.Interact[0])
	}
	i, x := p.Issues[1], p.Interact[1]
	if i.Title != "Button does nothing: Email ***@***" || i.Detail != "Pay with ***" {
		t.Errorf("issue %q / %q", i.Title, i.Detail)
	}
	if i.Key != "interact/dead-click|Button does nothing: Email ***@***|" {
		t.Errorf("key %q", i.Key)
	}
	if x.Label != "Email ***@***" || x.Detail != "card ***" || x.Selector != "#pay" {
		t.Errorf("interaction %+v", x)
	}
}

// listenMask serves h on a free port of 39200-39249.
func listenMask(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	for port := 39200; port <= 39249; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	t.Fatal("ports 39200-39249 are all taken")
	return nil
}

// A dead control that shows a customer's email or card number is
// reported, recorded and keyed with the values masked.
func TestDeadClickMasked(t *testing.T) {
	needBrowser(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/account/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<!doctype html><html lang="en"><body><main>
<div onclick="void 0" role="button">Email %s</div>
<button type="button" id="card">Card %s</button>
</main></body></html>`, prefilled, cardNo)
	})
	srv := listenMask(t, mux)
	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 1
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"interact"}
	st := store.Store{Root: t.TempDir()}
	dir, _ := st.NewDir("runs", "now")
	run, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: srv.URL, Paths: []string{"/account/"}, Dir: dir,
		Version: "test", Modules: []check.Module{New()}, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	p := run.Page("/account/")
	titles := map[string]bool{}
	for _, i := range p.Issues {
		titles[i.Title] = true
	}
	for _, want := range []string{"Button does nothing: Email ***@***", "Button does nothing: Card ***"} {
		if !titles[want] {
			t.Errorf("no issue %q in %v", want, keys(titles))
		}
	}
	var out bytes.Buffer
	if err := json.NewEncoder(&out).Encode(run); err != nil {
		t.Fatal(err)
	}
	for _, render := range []func(*bytes.Buffer, report.Result) error{
		func(w *bytes.Buffer, r report.Result) error { return report.HTML(w, r) },
		func(w *bytes.Buffer, r report.Result) error { return report.JSON(w, r) },
		func(w *bytes.Buffer, r report.Result) error { return report.Markdown(w, r) },
	} {
		if err := render(&out, report.Result{Run: run, Dir: dir}); err != nil {
			t.Fatal(err)
		}
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(path)
			out.Write(b)
		}
		return nil
	})
	for _, v := range []string{prefilled, cardNo} {
		if strings.Contains(out.String(), v) {
			t.Errorf("%q is in the run or its reports", v)
		}
	}
}
