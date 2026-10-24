package echo_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/echo/catcher"
	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/wire"
	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	echomod "github.com/RhythmShahriar/soundcheck/checks/echo"
	"github.com/RhythmShahriar/soundcheck/checks/interact"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
	"github.com/RhythmShahriar/soundcheck/store"
)

func needBrowser(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// Site states.
const (
	good   = iota // the admin is emailed and the visitor gets an autoresponder
	broken        // the admin email is lost
	silent        // the form says sent and nothing goes out
)

// shop is a site with a Contact Form 7-like form whose handler sends its
// emails through Echo, the way a connector would: it reads X-Echo-Run
// from the request (header first, then cookie) and posts a capture.
type shop struct {
	srv, other *httptest.Server
	echo       *httptest.Server
	state      atomic.Int32
	mu         sync.Mutex
	tags       []string // "header|cookie" seen by the form handler
	crossRun   []string // X-Echo-Run seen by the other origin (must stay empty)
}

const contactPage = `<!doctype html><html><head><title>Contact</title></head><body>
<h1>Contact</h1>
<div class="wpcf7" id="wpcf7-f7-o1">
<form action="/contact/#wpcf7-f7-o1" method="post" class="wpcf7-form init" novalidate>
  <input type="hidden" name="_wpcf7" value="7">
  <p><label>Your name <input name="your-name" aria-required="true"></label></p>
  <p><label>Your email <input type="email" name="your-email" aria-required="true"></label></p>
  <p><label>Message <textarea name="your-message"></textarea></label></p>
  <p><input type="submit" value="Send"></p>
  <div class="wpcf7-response-output"></div>
</form></div>
<script>
  const form = document.querySelector('.wpcf7-form');
  const out = form.querySelector('.wpcf7-response-output');
  form.addEventListener('submit', async e => {
    e.preventDefault();
    // A third-party call during the submit (a CAPTCHA, a CRM): it must not
    // break because Soundcheck tags the submit for Echo.
    const ok = await fetch('OTHER/verify', {method: 'POST', body: 'token'}).then(r => r.ok).catch(() => false);
    if (!ok) { form.className = 'wpcf7-form failed'; out.textContent = 'Verification failed.'; return; }
    const r = await fetch('/wp-json/contact-form-7/v1/contact-forms/7/feedback', {method: 'POST', body: new FormData(form)});
    const j = await r.json();
    form.className = 'wpcf7-form ' + ({mail_sent: 'sent', validation_failed: 'invalid'})[j.status];
    out.textContent = j.message;
  });
</script></body></html>`

func newShop(t *testing.T) *shop {
	t.Helper()
	s := &shop{}
	inbox := catcher.NewInbox(1000)
	s.echo = httptest.NewServer(catcher.NewServer(inbox, catcher.Config{Version: "test"}))
	t.Cleanup(s.echo.Close)
	s.other = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*") // no Allow-Headers: a preflight would fail
		if v := r.Header.Get(wire.RunHeader); v != "" {
			s.mu.Lock()
			s.crossRun = append(s.crossRun, v)
			s.mu.Unlock()
		}
	}))
	t.Cleanup(s.other.Close)
	ec := client.New(s.echo.URL, "")
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><html><head><title>Shop</title></head><body><a href="/contact/">Contact</a></body></html>`)
	})
	mux.HandleFunc("/contact/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.ReplaceAll(contactPage, "OTHER", s.other.URL))
	})
	mux.HandleFunc("/wp-json/contact-form-7/v1/contact-forms/7/feedback", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		name, email := r.PostForm.Get("your-name"), r.PostForm.Get("your-email")
		if name == "" || email == "" {
			fmt.Fprint(w, `{"status":"validation_failed","message":"One or more fields have an error."}`)
			return
		}
		run := r.Header.Get(wire.RunHeader)
		cookie := ""
		if c, err := r.Cookie(wire.RunHeader); err == nil {
			cookie = c.Value
		}
		s.mu.Lock()
		s.tags = append(s.tags, run+"|"+cookie)
		s.mu.Unlock()
		if run == "" {
			run = cookie
		}
		send := func(to, subject, text string) {
			_, err := ec.Capture(r.Context(), wire.Capture{Kind: "email", Run: run,
				Source: wire.Source{Via: "wordpress", Stack: "wordpress", Plugin: "contact-form-7", Hook: "wp_mail", Host: r.Host},
				Email:  &wire.Email{From: "wordpress@shop.test", To: []string{to}, Subject: subject, Text: text}})
			if err != nil {
				t.Errorf("capture: %v", err)
			}
		}
		switch s.state.Load() {
		case good:
			send("admin@shop.test", "New enquiry from "+name, "From: "+name+" <"+email+">\n\n"+r.PostForm.Get("your-message"))
			send(email, "Thanks for getting in touch", "Hi "+name+", we'll reply within a day.")
		case broken:
			send(email, "Thanks for getting in touch", "Hi "+name+", we'll reply within a day.")
		}
		fmt.Fprint(w, `{"status":"mail_sent","message":"Thank you for your message. It has been sent."}`)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

const contactFlow = `name: Contact sends
start: /contact/
steps:
  - fill: {selector: "[name=your-name]", value: "${fake:name}"}
  - fill: {selector: "[name=your-email]", value: "${fake:email}"}
  - click: {text: Send}
  - expect: {text: Thank you for your message}
  - expect: {message: {channel: email, to: admin@shop.test, subject: New enquiry}}
  - expect: {message: {channel: email, to: "${fake:email}", subject: thanks}}
`

func (s *shop) config(t *testing.T, flows bool) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Rate, cfg.Tabs, cfg.Timeout = 0, 1, 5
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"interact", "echo"}
	cfg.Interactions.Levels = []string{"submit"}
	cfg.Echo.URL, cfg.Echo.Quiet, cfg.Echo.Timeout = s.echo.URL, 300*time.Millisecond, 3*time.Second
	if flows {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "contact.yaml"), []byte(contactFlow), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg.Interactions.Levels = append(cfg.Interactions.Levels, "flows")
		cfg.Flows = dir
	}
	return cfg
}

func (s *shop) run(t *testing.T, cfg config.Config, snapshot bool) *model.Run {
	t.Helper()
	link, err := echolink.Connect(context.Background(), cfg.Echo, "")
	if err != nil {
		t.Fatal(err)
	}
	st := store.Store{Root: t.TempDir()}
	dir, _ := st.NewDir("runs", "now")
	run, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: s.srv.URL, Paths: []string{"/contact/"},
		Snapshot: snapshot, Dir: dir, Version: "test", Modules: modules(), Echo: link,
		Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func modules() []check.Module { return []check.Module{interact.New(), echomod.Module{}} }

func issueTitles(issues []model.Issue) map[string]model.Issue {
	m := map[string]model.Issue{}
	for _, i := range issues {
		m[i.Title] = i
	}
	return m
}

func TestFormMessagesSnapshotAndCompare(t *testing.T) {
	needBrowser(t)
	s := newShop(t)
	cfg := s.config(t, true)

	before := s.run(t, cfg, true)
	p := before.Page("/contact/")
	if p == nil || len(p.Echo) != 1 {
		t.Fatalf("echo actions: %+v", p)
	}
	act := p.Echo[0]
	if act.Action != "form:/contact/#form.wpcf7-form" || act.Kind != "form" || act.Label != "Contact Form 7 #7" || len(act.Messages) != 2 {
		t.Fatalf("form action: %+v", act)
	}
	var detail string
	for _, i := range p.Interact {
		if i.Kind == "submit" && i.Outcome == "success" {
			detail = i.Detail
		}
	}
	if !strings.Contains(detail, "sent 2 emails") {
		t.Errorf("interaction detail: %q", detail)
	}
	if len(before.Echo) != 1 || before.Echo[0].Action != "flow:Contact_sends" || len(before.Echo[0].Messages) != 2 {
		t.Errorf("flow action: %+v", before.Echo)
	}
	if len(before.Issues) != 0 {
		t.Errorf("good state has site issues: %+v", before.Issues)
	}
	if !before.Options.Echo || before.Options.EchoMode != "seal" {
		t.Errorf("options: %+v", before.Options)
	}
	for _, i := range p.Issues {
		if strings.HasPrefix(i.Check, "echo/") {
			t.Errorf("good state has an echo issue: %+v", i)
		}
	}
	s.mu.Lock()
	for _, tag := range s.tags {
		h, c, _ := strings.Cut(tag, "|")
		if h == "" || h != c || !strings.HasPrefix(h, echolink.RunID(before.Started)+"/") {
			t.Errorf("the form handler saw header and cookie %q", tag)
		}
	}
	if len(s.crossRun) > 0 {
		t.Errorf("the tag leaked to another origin: %q", s.crossRun)
	}
	s.mu.Unlock()

	s.state.Store(broken)
	after := s.run(t, cfg, false)
	cc := &check.CompareContext{Config: cfg, Before: before, After: after, Dir: t.TempDir()}
	c := compare.Compare(context.Background(), cc, modules(), "test")
	var lost *model.Change
	for i, ch := range c.Changes {
		if ch.Title == "Email to admin@shop.test is no longer sent" && ch.Source == "Contact Form 7 #7" {
			lost = &c.Changes[i]
		}
	}
	if lost == nil || lost.Class != model.Broke || lost.Check != "echo/message/missing" || len(lost.Pages) != 1 || lost.Pages[0] != "/contact/" {
		t.Fatalf("changes: %+v", c.Changes)
	}
	flowFail := `Flow "Contact sends" step 5 failed: expect message email to "admin@shop.test" subject "New enquiry"`
	f, ok := issueTitles(after.Issues)[flowFail]
	if !ok || f.Level != model.Error || !strings.Contains(f.Detail, "no such message was sent") || !strings.Contains(f.Detail, "Thanks for getting in touch") {
		t.Errorf("flow failure: %+v", after.Issues)
	}
	broke := 0
	for _, ch := range c.Changes {
		if ch.Class == model.Broke {
			broke++
		}
		if ch.Check == "echo/no-message" {
			t.Errorf("the autoresponder still goes out: %+v", ch)
		}
	}
	if broke != 3 { // the form's admin email, the flow's, and the flow's failed expect
		t.Errorf("%d BROKE changes: %+v", broke, c.Changes)
	}
}

func TestFormSaysSentButNothingGoesOut(t *testing.T) {
	needBrowser(t)
	s := newShop(t)
	cfg := s.config(t, false)
	before := s.run(t, cfg, true)

	s.state.Store(silent)
	after := s.run(t, cfg, false)
	p := after.Page("/contact/")
	i, ok := issueTitles(p.Issues)["Form says sent, but no message went out"]
	if !ok || i.Level != model.Notice || i.Check != "echo/no-message" || i.Source != "Contact Form 7 #7" {
		t.Fatalf("no-message notice: %+v", p.Issues)
	}
	if len(p.Echo) != 1 || len(p.Echo[0].Messages) != 0 {
		t.Errorf("echo actions: %+v", p.Echo)
	}

	// Compared with the snapshot, the lost emails are BROKE, and the notice
	// isn't repeated as NEW.
	cc := &check.CompareContext{Config: cfg, Before: before, After: after, Dir: t.TempDir()}
	c := compare.Compare(context.Background(), cc, modules(), "test")
	got := map[string]string{}
	for _, ch := range c.Changes {
		got[ch.Title] = ch.Class
	}
	if got["Email to admin@shop.test is no longer sent"] != model.Broke || got["Email to the test submitter is no longer sent"] != model.Broke {
		t.Errorf("changes: %v", got)
	}
	if _, dup := got["Form says sent, but no message went out"]; dup {
		t.Errorf("no-message notice repeated: %v", got)
	}
}
