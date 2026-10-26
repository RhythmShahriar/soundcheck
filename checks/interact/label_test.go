// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/runner"
	"github.com/RhythmShahriar/soundcheck/store"
)

// prefilled is an address a site filled in for a signed-in customer,
// assembled at run time so the test reads like any other value.
var prefilled = "jane.doe" + "@" + "example.org"

// listenLabels serves h on a free port of 37040-37059, the ports this
// lane's tests use.
func listenLabels(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	for port := 37040; port <= 37059; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)
		return srv
	}
	t.Fatal("ports 37040-37059 are all taken")
	return nil
}

// labelPage has fields filled with the customer's address and buttons
// whose value is their caption. %s is the address.
const labelPage = `<!doctype html><html lang="en"><body>
<form action="/save/" method="post">
<input id="ph" type="email" value="%[1]s" placeholder="Your email">
<input id="bare" type="email" value="%[1]s">
<input id="named" type="email" value="%[1]s" name="billing_email">
<span id="lb">Work email</span><input id="by" type="email" value="%[1]s" aria-labelledby="lb">
<label id="wrap">Email <input id="wrapped" type="email" value="%[1]s"></label>
<textarea id="ta">%[1]s</textarea>
<select id="sel"><option>%[1]s</option></select>
<input id="send" type="submit" value="Send now">
<input id="clear" type="reset" value="Start over">
<button id="btn" value="%[1]s">Save</button>
</form></body></html>`

// labelOf names a field by its aria-label, aria-labelledby text,
// <label>, placeholder or name, never by its value; a button's value is
// its caption.
func TestLabelOfNeverValue(t *testing.T) {
	needBrowser(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, labelPage, prefilled) })
	srv := listenLabels(t, mux)
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	page, err := b.Page(proto.TargetCreateTarget{URL: srv.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	if err := page.WaitLoad(); err != nil {
		t.Fatal(err)
	}
	for sel, want := range map[string]string{
		"#ph": "Your email", "#bare": "", "#named": "billing_email", "#by": "Work email", "#wrapped": "Email",
		"#ta": "", "#sel": "", "#send": "Send now", "#clear": "Start over", "#btn": "Save",
	} {
		res, err := page.Eval(`(sel) => {`+helpersJS+` return labelOf(document.querySelector(sel)); }`, sel)
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Value.Str(); got != want {
			t.Errorf("labelOf(%s) = %q, want %q", sel, got, want)
		}
	}
}

// A form field the site prefilled with a customer's email keeps the
// address out of every title, key, run.json and report: an unnamed
// field is named by its placeholder or selector.
func TestPrefilledValueStaysOut(t *testing.T) {
	needBrowser(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/account/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintf(w, labelPage, prefilled) })
	srv := listenLabels(t, mux)
	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 1
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"interact"}
	cfg.Interactions.Levels = []string{"inspect"}
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
	for _, want := range []string{"Form field has no name: Your email", "Form field has no name: #bare", "Form field has no name: Work email"} {
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
	if strings.Contains(out.String(), prefilled) {
		t.Errorf("the prefilled address is in the run or its reports")
	}
}

// A key flow (keyboard: true or send) is the keyboard module's: interact
// plays only the mouse flows, so no flow runs twice.
func TestFlowsSkipKeyFlows(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"mouse.yaml": "name: By mouse\nsteps: [{click: '#a'}]\n",
		"dry.yaml":   "name: Dry keys\nkeyboard: true\nsteps: [{click: '#a'}]\n",
		"send.yaml":  "name: Sent keys\nkeyboard: send\nsteps: [{click: '#a'}]\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Flows = dir
	cfg.Interactions.Levels = []string{"flows"}
	run := func(cfg config.Config) []string {
		cc := &check.Context{Config: cfg, Run: &model.Run{}}
		cc.NewSession = func() (*rod.Browser, error) { return nil, errors.New("no browser here") }
		New().CheckSite(context.Background(), cc)
		return cc.Run.Warnings
	}
	// Without the keyboard module the key flows would run nowhere, so
	// the run says so.
	off := `keyboard: 2 key flows need the keyboard module (--eaa or +keyboard); not run`
	if w := run(cfg); !slices.Equal(w, []string{off, `flow "By mouse": no browser here`}) {
		t.Errorf("keyboard off: warnings %q", w)
	}
	cfg.Modules = append(slices.Clone(cfg.Modules), "keyboard")
	if w := run(cfg); !slices.Equal(w, []string{`flow "By mouse": no browser here`}) {
		t.Errorf("warnings %q: want only the mouse flow tried", w)
	}
	// A folder of key flows only isn't empty: with the keyboard module
	// on it says nothing here.
	if err := os.Remove(filepath.Join(dir, "mouse.yaml")); err != nil {
		t.Fatal(err)
	}
	if w := run(cfg); len(w) != 0 {
		t.Errorf("warnings %q", w)
	}
	cfg.Modules = slices.DeleteFunc(slices.Clone(cfg.Modules), func(m string) bool { return m == "keyboard" })
	if w := run(cfg); !slices.Equal(w, []string{off}) {
		t.Errorf("keyboard off, key flows only: warnings %q", w)
	}
	if err := os.Remove(filepath.Join(dir, "send.yaml")); err != nil {
		t.Fatal(err)
	}
	one := `keyboard: 1 key flow needs the keyboard module (--eaa or +keyboard); not run`
	if w := run(cfg); !slices.Equal(w, []string{one}) {
		t.Errorf("keyboard off, one key flow: warnings %q", w)
	}
}
