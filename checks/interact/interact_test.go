package interact

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

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
	"github.com/RhythmShahriar/soundcheck/store"
)

func needBrowser(t *testing.T) {
	t.Helper()
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// fixture is a small WordPress-looking site with working, dead, failing
// and dangerous controls, and a Contact Form 7-like form.
type fixture struct {
	srv       *httptest.Server
	formBreak atomic.Int32 // 0 works, 1 HTTP 500, 2 mail_failed, 3 accepts anything
	deletes   atomic.Int32 // requests to the destructive endpoint
	mu        sync.Mutex
	posts     []string // bodies posted to the form endpoint
	lastDir   string   // the last run's directory
}

const home = `<!doctype html><html><head><title>Brew</title>
<link rel="https://api.w.org/" href="/wp-json/"></head><body>
<header>
  <button class="menu-toggle" aria-expanded="false" aria-controls="nav">Menu</button>
  <nav id="nav" style="display:none"><a href="/">Home</a> <a href="/contact/">Contact</a></nav>
</header>
<main>
  <button type="button" id="dead">Subscribe</button>
  <button type="button" id="boom">Show offers</button>
  <button type="button" id="stock">Check stock</button>
  <button type="button" id="del">Delete account</button>
  <a href="#" class="share">Share</a>
  <details><summary>Shipping</summary><p>Two days.</p></details>
  <p id="dup">one</p><p id="dup">two</p>
  <form method="post" action="/missing-handler/">
    <input placeholder="Your email" type="email">
    <input name="phone" required>
    <button>Join</button>
  </form>
</main>
<script>
  document.querySelector('.menu-toggle').addEventListener('click', e => {
    const open = e.target.getAttribute('aria-expanded') === 'true';
    e.target.setAttribute('aria-expanded', String(!open));
    document.getElementById('nav').style.display = open ? 'none' : 'block';
  });
  document.getElementById('dead').addEventListener('mouseover', () => {});
  document.getElementById('boom').addEventListener('click', () => { offers.render(); });
  document.getElementById('stock').addEventListener('click', () => { fetch('/api/stock'); });
  document.getElementById('del').addEventListener('click', () => { fetch('/api/delete', {method: 'POST'}); });
</script></body></html>`

const contact = `<!doctype html><html><head><title>Contact</title>
<script src="/wp-includes/js/jquery.js"></script></head><body>
<h1>Contact</h1>
<div class="wpcf7" id="wpcf7-f42-o1">
<form action="/contact/#wpcf7-f42-o1" method="post" class="wpcf7-form init" novalidate>
  <input type="hidden" name="_wpcf7" value="42">
  <p><label>Your name <input name="your-name" aria-required="true" class="wpcf7-validates-as-required"></label></p>
  <p><label>Your email <input type="email" name="your-email" aria-required="true"></label></p>
  <p><label>Message <textarea name="your-message"></textarea></label></p>
  <p><input type="submit" value="Send"></p>
  <div class="wpcf7-response-output" aria-hidden="true"></div>
</form></div>
<script>
  const form = document.querySelector('.wpcf7-form');
  const out = form.querySelector('.wpcf7-response-output');
  form.addEventListener('submit', async e => {
    e.preventDefault();
    form.querySelectorAll('.wpcf7-not-valid-tip').forEach(t => t.remove());
    const r = await fetch('/wp-json/contact-form-7/v1/contact-forms/42/feedback', {method: 'POST', body: new FormData(form)});
    if (!r.ok) { form.className = 'wpcf7-form failed'; out.textContent = 'There was an error trying to send your message.'; return; }
    const j = await r.json();
    form.className = 'wpcf7-form ' + ({mail_sent: 'sent', validation_failed: 'invalid', mail_failed: 'failed'})[j.status];
    out.textContent = j.message;
    if (j.status === 'validation_failed') for (const n of j.invalid) {
      const s = document.createElement('span'); s.className = 'wpcf7-not-valid-tip'; s.textContent = 'Please fill out this field.';
      form.querySelector('[name="' + n + '"]').after(s);
    }
  });
</script></body></html>`

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fx := &fixture{}
	mux := http.NewServeMux()
	page := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }
	}
	mux.HandleFunc("/{$}", page(home))
	mux.HandleFunc("/contact/", page(contact))
	mux.HandleFunc("/wp-includes/js/jquery.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
	})
	mux.HandleFunc("/api/stock", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "stock service down", http.StatusInternalServerError)
	})
	mux.HandleFunc("/api/delete", func(w http.ResponseWriter, r *http.Request) {
		fx.deletes.Add(1)
	})
	mux.HandleFunc("/wp-json/contact-form-7/v1/contact-forms/42/feedback", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fx.mu.Lock()
		fx.posts = append(fx.posts, r.PostForm.Encode())
		fx.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		var invalid []string
		for _, n := range []string{"your-name", "your-email"} {
			if r.PostForm.Get(n) == "" {
				invalid = append(invalid, `"`+n+`"`)
			}
		}
		switch {
		case len(invalid) > 0 && fx.formBreak.Load() != 3:
			fmt.Fprintf(w, `{"status":"validation_failed","message":"One or more fields have an error.","invalid":[%s]}`, strings.Join(invalid, ","))
		case fx.formBreak.Load() == 1:
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `<b>Fatal error</b>`)
		case fx.formBreak.Load() == 2:
			fmt.Fprint(w, `{"status":"mail_failed","message":"There was an error trying to send your message."}`)
		default:
			fmt.Fprint(w, `{"status":"mail_sent","message":"Thank you for your message. It has been sent."}`)
		}
	})
	fx.srv = httptest.NewServer(mux)
	t.Cleanup(fx.srv.Close)
	return fx
}

func (fx *fixture) run(t *testing.T, cfg config.Config, paths ...string) *model.Run {
	t.Helper()
	cfg.Rate, cfg.Tabs = 0, 1
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"interact"}
	st := store.Store{Root: t.TempDir()}
	dir, _ := st.NewDir("runs", "now")
	fx.lastDir = dir
	run, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: fx.srv.URL, Paths: paths, Dir: dir,
		Version: "test", Modules: []check.Module{New()}, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func issues(p *model.Page) map[string]model.Issue {
	m := map[string]model.Issue{}
	for _, i := range p.Issues {
		m[i.Title] = i
	}
	return m
}

func outcomes(p *model.Page, kind string) map[string]string {
	m := map[string]string{}
	for _, i := range p.Interact {
		if i.Kind == kind {
			m[i.Label] = i.Outcome
		}
	}
	return m
}

func TestInspectAndClick(t *testing.T) {
	needBrowser(t)
	fx := newFixture(t)
	cfg := config.Default() // inspect + click
	run := fx.run(t, cfg, "/")
	p := run.Page("/")
	if p == nil || p.Error != "" {
		t.Fatalf("page: %+v", p)
	}
	got := issues(p)
	for title, level := range map[string]model.Level{
		"Button does nothing: Subscribe":                        model.Warning,
		"Clicking Show offers throws a JS error":                model.Error,
		"Clicking Check stock makes a request that fails":       model.Error,
		"Link does nothing: Share":                              model.Warning,
		`Duplicate id: "dup"`:                                   model.Notice,
		"Form field has no name: Your email":                    model.Warning,
		"Required field has no label: phone":                    model.Notice,
		"Form sends to a missing page (404): /missing-handler/": model.Error,
		"Form has no nonce: form 1":                             model.Warning,
	} {
		i, ok := got[title]
		if !ok {
			t.Errorf("missing %q", title)
			continue
		}
		if i.Level != level || i.Key == "" {
			t.Errorf("%q: %+v", title, i)
		}
	}
	if e := got["Clicking Show offers throws a JS error"]; !strings.Contains(e.Detail, "offers is not defined") {
		t.Errorf("JS error detail: %q", e.Detail)
	}
	if e := got["Clicking Check stock makes a request that fails"]; e.Detail != "GET /api/stock returned 500" {
		t.Errorf("request detail: %q", e.Detail)
	}
	for _, bad := range []string{"Button does nothing: Menu", "Button does nothing: Shipping"} {
		if _, ok := got[bad]; ok {
			t.Errorf("working control reported: %s", bad)
		}
	}
	clicks := outcomes(p, "click")
	if clicks["Menu"] != "ok" || clicks["Shipping"] != "ok" || clicks["Subscribe"] != "dead" {
		t.Errorf("click outcomes: %v", clicks)
	}
	if clicks["Delete account"] != "skipped" {
		t.Errorf("Delete account: %q", clicks["Delete account"])
	}
	if n := fx.deletes.Load(); n != 0 {
		t.Errorf("the destructive endpoint was called %d times", n)
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSubmitForms(t *testing.T) {
	needBrowser(t)
	fx := newFixture(t)
	cfg := config.Default()
	cfg.Interactions.Levels = []string{"submit"}

	run := fx.run(t, cfg, "/contact/")
	p := run.Page("/contact/")
	subs := outcomes(p, "submit")
	if subs["Contact Form 7 #42"] != "success" {
		t.Fatalf("submit outcomes: %+v, issues %v", p.Interact, p.Issues)
	}
	if p.Interact[0].Outcome != "validation" || !strings.Contains(p.Interact[0].Detail, "One or more fields") {
		t.Errorf("empty submit: %+v", p.Interact[0])
	}
	for _, i := range p.Issues {
		if strings.HasPrefix(i.Check, "interact/form") {
			t.Errorf("working form reported: %s", i.Title)
		}
	}
	fx.mu.Lock()
	posts := append([]string(nil), fx.posts...)
	fx.mu.Unlock()
	if len(posts) != 2 || !strings.Contains(posts[1], "soundcheck-test") {
		t.Errorf("posts: %q", posts)
	}

	fx.formBreak.Store(1)
	p = fx.run(t, cfg, "/contact/").Page("/contact/")
	if i, ok := issues(p)["Form submission fails (HTTP 500): Contact Form 7 #42"]; !ok || i.Level != model.Error {
		t.Errorf("HTTP 500 not reported: %v", p.Issues)
	}

	fx.formBreak.Store(2)
	p = fx.run(t, cfg, "/contact/").Page("/contact/")
	i, ok := issues(p)["Form no longer submits: Contact Form 7 #42"]
	if !ok || !strings.Contains(i.Detail, "error trying to send") {
		t.Errorf("mail failure not reported: %v", p.Issues)
	}

	fx.formBreak.Store(3)
	p = fx.run(t, cfg, "/contact/").Page("/contact/")
	if i, ok := issues(p)["Form shows no validation: Contact Form 7 #42"]; !ok || i.Level != model.Warning {
		t.Errorf("missing validation not reported: %v", p.Issues)
	}
}

func TestSubmitRefusesProduction(t *testing.T) {
	needBrowser(t)
	fx := newFixture(t)
	cfg := config.Default()
	cfg.Interactions.Levels = []string{"submit"}
	cfg.Production = []string{fx.srv.URL}
	run := fx.run(t, cfg, "/contact/")
	p := run.Page("/contact/")
	if got := outcomes(p, "submit")["Contact Form 7 #42"]; got != "skipped" {
		t.Errorf("outcome %q", got)
	}
	if len(fx.posts) != 0 {
		t.Errorf("form posted on production: %q", fx.posts)
	}
	warned := false
	for _, w := range run.Warnings {
		warned = warned || strings.Contains(w, "production")
	}
	if !warned {
		t.Errorf("no production warning: %v", run.Warnings)
	}

	// An allowlisted form may be submitted on production.
	cfg.Interactions.Forms = []string{".wpcf7-form"}
	p = fx.run(t, cfg, "/contact/").Page("/contact/")
	if got := outcomes(p, "submit")["Contact Form 7 #42"]; got != "success" {
		t.Errorf("allowlisted form: %q (%+v)", got, p.Interact)
	}
}

func TestFlows(t *testing.T) {
	needBrowser(t)
	fx := newFixture(t)
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("contact.yaml", `name: Contact form sends
start: /contact/
steps:
  - fill: {selector: "[name=your-name]", value: "${fake:name}"}
  - fill: {selector: "[name=your-email]", value: "${fake:email}"}
  - click: {text: Send}
  - expect: {text: Thank you for your message}
  - screenshot: sent
`)
	write("menu.yaml", `name: Menu opens
steps:
  - click: .menu-toggle
  - wait: {selector: "#nav"}
  - expect: {text: Opening hours}
  - click: "#nowhere"
`)
	cfg := config.Default()
	cfg.Interactions.Levels = []string{"flows"}
	cfg.Flows = dir
	cfg.Timeout = 5
	run := fx.run(t, cfg, "/")
	got := map[string]model.Issue{}
	for _, i := range run.Issues {
		got[i.Title] = i
	}
	if len(got) != 2 {
		t.Errorf("issues: %v", keys(got))
	}
	if i, ok := got[`Flow "Menu opens" step 3 failed: expect text "Opening hours"`]; !ok || i.Level != model.Error || i.Pages[0] != "/" {
		t.Errorf("expect failure: %+v", got)
	}
	if _, ok := got[`Flow "Menu opens" step 4 failed: click #nowhere`]; !ok {
		t.Errorf("click failure: %v", keys(got))
	}
	shots, _ := filepath.Glob(filepath.Join(fx.lastDir, "flows", "*.png"))
	if len(shots) != 1 || !strings.Contains(shots[0], "contact-form-sends") {
		t.Errorf("flow screenshots: %v", shots)
	}
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if len(fx.posts) != 1 || !strings.Contains(fx.posts[0], "soundcheck-test") {
		t.Errorf("flow posts: %q", fx.posts)
	}
}
