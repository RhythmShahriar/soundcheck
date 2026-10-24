// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/wire"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	echomod "github.com/RhythmShahriar/soundcheck/checks/echo"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
)

// A form's email with a reset link and a one-time code is stored masked,
// and since both runs are masked alike, a comparison still matches it
// although the link and code differ on every send.
func TestEchoMessagesStoredRedacted(t *testing.T) {
	needBrowser(t)
	echo := httptest.NewServer(catcher.NewServer(catcher.NewInbox(100), catcher.Config{Version: "test"}))
	defer echo.Close()
	ec := client.New(echo.URL, "")
	var sends atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/contact/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, contact) })
	mux.HandleFunc("/wp-json/contact-form-7/v1/contact-forms/42/feedback", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("your-name") == "" || r.PostForm.Get("your-email") == "" {
			fmt.Fprint(w, `{"status":"validation_failed","message":"One or more fields have an error.","invalid":[]}`)
			return
		}
		n := sends.Add(1)
		key := fmt.Sprintf("Rk%018d", n)
		otp := fmt.Sprintf("%06d", 482900+n)
		run := r.Header.Get(wire.RunHeader)
		_, err := ec.Capture(r.Context(), wire.Capture{Kind: "email", Run: run,
			Source: wire.Source{Via: "wordpress", Stack: "wordpress", Hook: "wp_mail", Host: r.Host},
			Email: &wire.Email{From: "wordpress@shop.test", To: []string{"sam@shop.test"}, Subject: "Your sign-in code",
				Text: "Your code is " + otp + ". Or reset: http://" + r.Host + "/wp-login.php?action=rp&key=" + key + "&login=sam",
				HTML: `<p>Your code is <b>` + otp + `</b>.</p><a href="http://` + r.Host + `/wp-login.php?action=rp&amp;key=` + key + `&amp;login=sam">Reset</a>`}})
		if err != nil {
			t.Errorf("capture: %v", err)
		}
		fmt.Fprint(w, `{"status":"mail_sent","message":"Thank you for your message. It has been sent."}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := config.Default()
	cfg.Rate, cfg.Tabs, cfg.Timeout = 0, 1, 10
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"interact", "echo"}
	cfg.Interactions.Levels = []string{"submit"}
	cfg.Echo.URL, cfg.Echo.Quiet, cfg.Echo.Timeout = echo.URL, 300*time.Millisecond, 3*time.Second
	modules := []check.Module{New(), echomod.Module{}}
	run := func(snapshot bool) *model.Run {
		t.Helper()
		link, err := echolink.Connect(context.Background(), cfg.Echo, "")
		if err != nil {
			t.Fatal(err)
		}
		r, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: srv.URL, Paths: []string{"/contact/"},
			Snapshot: snapshot, Dir: t.TempDir(), Version: "test", Modules: modules, Echo: link,
			Browser: browser.Options{NoDownload: true}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	before := run(true)
	time.Sleep(5 * time.Millisecond) // a run ID per start time, to the millisecond
	after := run(false)

	p := after.Page("/contact/")
	if p == nil || len(p.Echo) != 1 || len(p.Echo[0].Messages) != 1 {
		t.Fatalf("echo actions: %+v", p)
	}
	m := p.Echo[0].Messages[0]
	for _, part := range []string{m.Text, m.HTML} {
		if strings.Contains(part, "Rk0") || strings.Contains(part, "4829") {
			t.Errorf("stored unmasked: %q", part)
		}
		if !strings.Contains(part, "key=•••••") || !strings.Contains(part, "•••••") {
			t.Errorf("not masked as expected: %q", part)
		}
	}
	if m.Subject != "Your sign-in code" || m.To[0] != "sam@shop.test" {
		t.Errorf("message: %+v", m)
	}
	cmp := compare.Compare(context.Background(), &check.CompareContext{Config: cfg, Before: before, After: after}, modules, "test")
	for _, c := range cmp.Changes {
		if strings.HasPrefix(c.Check, "echo/") {
			t.Errorf("the masked message reads as changed: %s %s (%s)", c.Class, c.Title, c.Detail)
		}
	}
}
