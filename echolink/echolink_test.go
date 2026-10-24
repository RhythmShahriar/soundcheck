// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package echolink

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	echomodel "github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/echo/wire"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/capture/intercept"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

func TestActionIDs(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"/contact/", "form.wpcf7-form"}:            "form:/contact/#form.wpcf7-form",
		{"/caf%C3%A9/?a=1&b=2", `form[name="x y"]`}: "form:/caf_C3_A9/_a=1_b=2#form[name=_x_y_]",
		{"/", "#gform_1, .x+y"}:                     "form:/##gform_1__.x_y",
		{"/" + strings.Repeat("a", 300), "form"}:    "form:/" + strings.Repeat("a", 194),
	} {
		if got := FormAction(in[0], in[1]); got != want {
			t.Errorf("FormAction(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	if got := FlowAction("Checkout; pay by card"); got != "flow:Checkout__pay_by_card" {
		t.Errorf("FlowAction = %q", got)
	}
	at := time.Date(2026, 10, 24, 9, 3, 4, 567e6, time.FixedZone("NZ", 13*3600))
	if got := RunID(at); got != "20261023T200304.567Z" {
		t.Errorf("RunID = %q", got)
	}
}

func TestSummary(t *testing.T) {
	msgs := []echomodel.Message{{Channel: echomodel.Webhook}, {Channel: echomodel.Email}, {Channel: echomodel.Email}, {Channel: echomodel.SMS}}
	if got := Summary(msgs); got != "sent 2 emails, 1 SMS, 1 webhook" {
		t.Errorf("Summary = %q", got)
	}
	if got := Summary(nil); got != "no message went out" {
		t.Errorf("Summary(nil) = %q", got)
	}
	if got := Recipient(echomodel.Message{URL: "https://hooks.zapier.com/hooks/catch/1/?k=v"}); got != "hooks.zapier.com/hooks/catch/1/" {
		t.Errorf("Recipient = %q", got)
	}
}

func TestIssues(t *testing.T) {
	a := model.EchoAction{Action: "form:/contact/#form", Label: "Contact form", Kind: "form", Page: "/contact/",
		Messages: []echomodel.Message{
			{ID: "1", Channel: echomodel.Email, To: []string{"admin@shop.test"}, Subject: "New enquiry from [your-name]", Text: "A visitor wrote to you.", Status: echomodel.Faked},
			{ID: "2", Channel: echomodel.Email, To: []string{"admin@shop.test"}, Subject: "New enquiry from [your-name]", Text: "A visitor wrote to you.", Status: echomodel.Faked},
		}}
	var got []model.Issue
	for _, i := range Issues(a, "shop.test") {
		if i.Check == "echo/content/placeholder" {
			got = append(got, i)
		}
	}
	if len(got) != 1 { // two messages with the same problem: one issue
		t.Fatalf("issues: %+v", Issues(a, "shop.test"))
	}
	i := got[0]
	if i.Check != "echo/content/placeholder" || i.Level != model.Error || i.Source != "Contact form" || i.Key == "" ||
		!strings.Contains(i.Title, "[your-name]") {
		t.Errorf("issue: %+v", i)
	}
	n := NoMessage(a, 1500*time.Millisecond)
	if n.Check != "echo/no-message" || n.Level != model.Notice || n.Title != "Form says sent, but no message went out" ||
		!strings.Contains(n.Detail, "within 1.5s") || n.Key == "" {
		t.Errorf("no-message: %+v", n)
	}
}

func TestConnect(t *testing.T) {
	inbox := catcher.NewInbox(10)
	srv := httptest.NewServer(catcher.NewServer(inbox, catcher.Config{Mode: catcher.Observe, Token: "secret"}))
	defer srv.Close()
	cfg := config.Default().Echo
	cfg.URL = srv.URL
	if _, err := Connect(context.Background(), cfg, "wrong"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong token: %v", err)
	}
	l, err := Connect(context.Background(), cfg, "secret")
	if err != nil || l.Mode != catcher.Observe || l.URL != srv.URL {
		t.Fatalf("connect: %+v %v", l, err)
	}
	r1, r2 := l.ForRun(time.Unix(1, 0)), l.ForRun(time.Unix(2, 0))
	if r1.Run == r2.Run || r1.mu != r2.mu || l.Run != "" {
		t.Errorf("ForRun: %q %q", r1.Run, r2.Run)
	}
	inbox.Add(echomodel.Message{Run: r1.Run, Action: "form:/#f", Channel: echomodel.Email})
	inbox.Add(echomodel.Message{Run: r2.Run, Action: "form:/#f", Channel: echomodel.Email})
	r1.Quiet, r1.Timeout = 100*time.Millisecond, time.Second
	msgs, err := r1.Wait(context.Background(), "form:/#f")
	if err != nil || len(msgs) != 1 || msgs[0].Run != r1.Run {
		t.Errorf("wait: %+v %v", msgs, err)
	}

	srv.Close()
	if _, err := Connect(context.Background(), cfg, "secret"); err == nil || !strings.Contains(err.Error(), "start it with: rhythm echo up") {
		t.Errorf("not running: %v", err)
	}
}

// Tag shares the tab's interceptor with the run's other handlers (the -H
// headers): the site gets both, another origin neither.
func TestTagSharesTheInterceptor(t *testing.T) {
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	var mu sync.Mutex
	got := map[string]string{} // host+path → "tag|header|cookie"
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie(wire.RunHeader)
		v := ""
		if c != nil {
			v = c.Value
		}
		mu.Lock()
		got[r.Host+r.URL.Path] = r.Header.Get(wire.RunHeader) + "|" + r.Header.Get("X-Staging") + "|" + v
		mu.Unlock()
		if r.URL.Path == "/" {
			fmt.Fprintf(w, `<!doctype html><title>t</title><script>fetch('/api'); fetch('%s/x', {mode: 'no-cors'});</script>`, r.URL.Query().Get("other"))
		}
	})
	srv, other := httptest.NewServer(h), httptest.NewServer(h)
	defer srv.Close()
	defer other.Close()
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := sess.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	tab = tab.Timeout(20 * time.Second)
	removeHeaders, err := intercept.Add(tab, func(r *intercept.Request) {
		if r.URL != nil && r.URL.Host == strings.TrimPrefix(srv.URL, "http://") {
			r.SetHeader("X-Staging", "yes")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer removeHeaders()
	l := (&Link{}).ForRun(time.Unix(1, 0))
	untag, err := l.Tag(context.Background(), tab, srv.URL+"/", "form:/#f")
	if err != nil {
		t.Fatal(err)
	}
	_ = tab.Navigate(srv.URL + "/?other=" + other.URL)
	_ = tab.WaitLoad()
	tab.WaitRequestIdle(300*time.Millisecond, nil, nil, nil)()
	untag()
	_ = tab.Navigate(srv.URL + "/after")
	_ = tab.WaitLoad()

	mu.Lock()
	defer mu.Unlock()
	site, oth := strings.TrimPrefix(srv.URL, "http://"), strings.TrimPrefix(other.URL, "http://")
	tag := client.RunValue(l.Run, "form:/#f")
	for _, path := range []string{"/", "/api"} {
		if v := got[site+path]; v != tag+"|yes|"+tag {
			t.Errorf("site %s: %q", path, v)
		}
	}
	if v, ok := got[oth+"/x"]; !ok || strings.HasPrefix(v, tag) || strings.Contains(v, "yes") {
		t.Errorf("other origin: %q (seen %v)", v, ok)
	}
	if v := got[site+"/after"]; v != "|yes|" {
		t.Errorf("after untag: %q", v)
	}
}
