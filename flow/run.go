// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	echomodel "github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/echolink"
)

// Options for running a flow.
type Options struct {
	Base    *url.URL      // relative paths resolve against it
	Timeout time.Duration // per step; default 10 s
	Fake    Fake
	Env     func(string) string                  // default os.Getenv
	Save    func(name string, data []byte) error // stores screenshots; nil skips them
	// Production reports URLs that are live sites. Click and submit steps
	// there are refused (ErrProduction) unless the flow sets production.
	Production func(url string) bool
	// Messages returns what the flow has made the site send so far, as
	// Echo captured it, for expect: {message: …}. Nil when Echo isn't
	// running; such expects then fail with a hint.
	Messages func(ctx context.Context) ([]echomodel.Message, error)
	// AllowEnv names variables ${env:…} may read besides SOUNDCHECK_*
	// ones (--allow-env). A flow reading any other fails before it starts.
	AllowEnv []string
	// AllowHosts lists hosts besides Base's that goto and start may open
	// (--allow-host), as config.HostKey gives them. Others are refused
	// before the browser asks for them. A click or submit that lands on
	// another host stops the flow too.
	AllowHosts []string
	// PlainHTTP lets a flow on an https site send what it read from the
	// environment to a plain http page (--insecure-login). Otherwise fill
	// and select steps with ${env:…} refuse an http page unless it's this
	// machine. So do clicks and submits after them.
	PlainHTTP bool
}

// ErrOffSite is a goto or start refused because it leaves the site, or
// a click or submit that left it.
var ErrOffSite = errors.New("isn't the site being checked")

// ErrPlainHTTP is a value from the environment kept from a plain http
// page of an https site.
var ErrPlainHTTP = errors.New("is plain http on an https site (--insecure-login allows it)")

// ErrProduction is a click or submit refused on a production URL.
var ErrProduction = errors.New("refused on a production URL (set production: true in the flow to allow it)")

// Failure is a step that didn't work. Step is 1-based; 0 is the start
// page.
type Failure struct {
	Step    int
	Desc    string // the step, from its definition
	Err     error  // what went wrong, with secrets masked
	Refused bool   // a write step refused by the production guard
}

func (f Failure) Error() string {
	if f.Step == 0 {
		return f.Desc + ": " + f.Err.Error()
	}
	return fmt.Sprintf("step %d (%s): %v", f.Step, f.Desc, f.Err)
}

// Run plays f in page and returns the steps that failed. A failed action
// stops the flow, since later steps depend on it; a failed expect is
// reported and the flow goes on.
func Run(ctx context.Context, page *rod.Page, f Flow, o Options) []Failure {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	r := &runner{page: page.Context(ctx), o: o, ctx: ctx, name: slug(f.Name)}
	stop := r.watch()
	defer stop()
	var out []Failure
	fail := func(step int, desc string, err error) {
		out = append(out, Failure{Step: step, Desc: desc, Err: errors.New(Mask(err.Error(), r.secrets...)),
			Refused: errors.Is(err, ErrProduction)})
	}
	start := f.Start
	if start == "" {
		start = "/"
	}
	if err := f.CheckEnv(o.AllowEnv); err != nil {
		fail(0, "read the environment", err)
		return out
	}
	if err := r.gotoURL(start); err != nil {
		fail(0, "open "+start, err)
		return out
	}
	for i, s := range f.Steps {
		if ctx.Err() != nil {
			fail(i+1, s.String(), ctx.Err())
			return out
		}
		if (s.Click != nil || s.Submit != nil) && !f.Production && r.onProduction() {
			fail(i+1, s.String(), ErrProduction)
			return out
		}
		if err := r.step(i+1, s); err != nil {
			fail(i+1, s.String(), err)
			if s.Expect == nil {
				return out
			}
		}
	}
	return out
}

type runner struct {
	ctx     context.Context
	page    *rod.Page
	o       Options
	name    string
	secrets []string
	status  atomic.Int64 // last main-document HTTP status
}

// watch records the HTTP status of each page load, for expect: status.
func (r *runner) watch() func() {
	_ = proto.NetworkEnable{}.Call(r.page)
	ctx, cancel := context.WithCancel(r.ctx)
	wait := r.page.Context(ctx).EachEvent(func(e *proto.NetworkResponseReceived) {
		if e.Type == proto.NetworkResourceTypeDocument && e.FrameID == r.page.FrameID {
			r.status.Store(int64(e.Response.Status))
		}
	})
	go wait()
	return cancel
}

// onProduction reports whether the current page is on a production URL.
func (r *runner) onProduction() bool {
	if r.o.Production == nil {
		return false
	}
	info, err := r.page.Timeout(5 * time.Second).Info()
	return err != nil || r.o.Production(info.URL) // unknown counts as production
}

func (r *runner) t() *rod.Page { return r.page.Timeout(r.o.Timeout) }

func (r *runner) expand(s string) (string, error) {
	// Run checked the whole flow already; this keeps every read honest.
	if err := envError(envRefs(s), r.o.AllowEnv); err != nil {
		return "", err
	}
	v, secrets, err := Expand(s, r.o.Fake, r.o.Env)
	r.secrets = append(r.secrets, secrets...)
	return v, err
}

// expandToType expands a value a fill or select step is about to type,
// checking the page first when it reads the environment.
func (r *runner) expandToType(s string) (string, error) {
	n := len(r.secrets)
	v, err := r.expand(s)
	if err != nil || len(r.secrets) == n {
		return v, err
	}
	return v, r.typeSecret()
}

func (r *runner) resolve(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if r.o.Base != nil {
		u = r.o.Base.ResolveReference(u)
	}
	return u.String(), nil
}

func (r *runner) gotoURL(raw string) error {
	raw, err := r.expand(raw)
	if err != nil {
		return err
	}
	target, err := r.resolve(raw)
	if err != nil {
		return err
	}
	if err := r.onSite(target); err != nil {
		return err
	}
	if err := r.t().Navigate(target); err != nil {
		return err
	}
	r.settle()
	return nil
}

// onSite refuses a URL on another host than Base, unless AllowHosts has
// it, so a flow can't carry what it typed or read to someone else's
// server. A different port is a different host; www. and the apex are one.
func (r *runner) onSite(target string) error {
	if r.o.Base == nil {
		return nil
	}
	u, err := url.Parse(target)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s: only http and https pages can be opened", u.Scheme)
	}
	site, host := config.HostKey(r.o.Base), config.HostKey(u)
	if host == site || slices.Contains(r.o.AllowHosts, host) {
		return nil
	}
	return fmt.Errorf("%s %w (%s); pass --allow-host %s to open it", host, ErrOffSite, site, host)
}

// secretSafe checks that target may get what the flow read from the
// environment: it's on the site and, when the site uses https, not plain
// http unless it's this machine or PlainHTTP is set.
func (r *runner) secretSafe(target string) error {
	if err := r.onSite(target); err != nil || r.o.Base == nil || r.o.PlainHTTP {
		return err
	}
	u, err := url.Parse(target)
	if err != nil {
		return err
	}
	if r.o.Base.Scheme == "https" && u.Scheme == "http" && !config.IsLoopback(u.Hostname()) {
		return fmt.Errorf("%s %w", u.Host, ErrPlainHTTP)
	}
	return nil
}

// typeSecret checks the current page before a fill or select types a
// value from the environment into it. The page may have moved since the
// last goto: a script redirect, or a click that a check let through.
func (r *runner) typeSecret() error {
	info, err := r.page.Timeout(5 * time.Second).Info()
	if err != nil {
		return fmt.Errorf("couldn't tell which page this is, so nothing from the environment is typed: %w", err)
	}
	if err := r.secretSafe(info.URL); err != nil {
		return fmt.Errorf("not typing what the flow read from the environment: %w", err)
	}
	return nil
}

// sendSecret refuses a click or submit that would send the page, with
// what the flow read from the environment typed into it, off the site or
// down to plain http. It reads where a plain link or form goes; a script
// on the page can still send the value anywhere, which no check here can
// see.
func (r *runner) sendSecret(el *rod.Element, submit bool) error {
	if len(r.secrets) == 0 {
		return nil
	}
	res, err := el.Timeout(r.o.Timeout).Eval(destJS, submit)
	if err != nil {
		return err
	}
	dest := res.Value.Str()
	if u, err := url.Parse(dest); dest == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil // nothing to follow, or a javascript: or mailto: link
	}
	if err := r.secretSafe(dest); err != nil {
		return fmt.Errorf("not sending what the flow typed: %w", err)
	}
	return nil
}

// destJS returns where clicking or submitting this element sends the
// page: a link's href, or its form's action (a submit button's
// formaction first). It returns "" for anything else. The formAction
// property is the page's own URL when the attribute is missing, so it's
// read only when the attribute is there.
const destJS = `function(submit) {
  const a = !submit && this.closest && this.closest('a[href]');
  if (a) return a.href;
  const form = this.tagName === 'FORM' ? this : this.form || (this.closest && this.closest('form'));
  if (!form) return '';
  let btn = this;
  if (this.tagName === 'FORM') btn = form.querySelector('[type=submit],button:not([type])');
  else if (!submit && !(this.type === 'submit' || this.type === 'image' || (this.tagName === 'BUTTON' && !this.getAttribute('type')))) return '';
  return btn && btn.hasAttribute('formaction') ? btn.formAction : form.action;
}`

// stayed checks the page is still on the site after a click or submit,
// so a link or form that led to another host stops the flow before it
// types anything there.
func (r *runner) stayed() error {
	if r.o.Base == nil {
		return nil
	}
	info, err := r.page.Timeout(5 * time.Second).Info()
	if err != nil {
		return nil // can't tell; a fill with a value from the environment checks again
	}
	if u, err := url.Parse(info.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	if err := r.onSite(info.URL); err != nil {
		return fmt.Errorf("it led off the site: %w", err)
	}
	return nil
}

// settle waits for the page to finish loading after an action that may
// have navigated or fetched.
func (r *runner) settle() {
	p := r.t()
	_ = p.WaitLoad()
	p.WaitRequestIdle(400*time.Millisecond, nil, nil, nil)()
	_ = p.WaitLoad()
}

func (r *runner) element(t Target) (*rod.Element, error) {
	if t.Text == "" {
		el, err := r.t().Element(t.Selector)
		if err != nil {
			return nil, notFound(t.Selector, err)
		}
		return el, nil
	}
	el, err := r.t().ElementByJS(rod.Eval(byTextJS, t.Selector, t.Text))
	if err != nil {
		return nil, notFound(fmt.Sprintf("an element with text %q", t.Text), err)
	}
	return el, nil
}

func notFound(what string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s not found", what)
	}
	return err
}

// byTextJS finds a clickable element whose text, value or aria-label is
// the given text (exact match first, then contains), preferring visible
// ones.
const byTextJS = `(sel, text) => {
  const want = text.trim().toLowerCase();
  const all = [...document.querySelectorAll(sel || 'a,button,input[type=submit],input[type=button],[role=button],[role=tab],[role=menuitem],label,summary')];
  const label = e => (e.innerText || e.value || e.getAttribute('aria-label') || e.title || '').trim().replace(/\s+/g, ' ').toLowerCase();
  const visible = e => !!(e.offsetWidth || e.offsetHeight || e.getClientRects().length);
  return all.find(e => visible(e) && label(e) === want) || all.find(e => visible(e) && label(e).includes(want)) || null;
}`

func (r *runner) step(n int, s Step) error {
	switch {
	case s.Goto != "":
		return r.gotoURL(s.Goto)
	case s.Click != nil:
		el, err := r.element(*s.Click)
		if err != nil {
			return err
		}
		if err := r.sendSecret(el, false); err != nil {
			return err
		}
		if err := el.Timeout(r.o.Timeout).Click(proto.InputMouseButtonLeft, 1); err != nil {
			return fmt.Errorf("couldn't click: %w", err)
		}
		r.settle()
		return r.stayed()
	case s.Fill != nil:
		v, err := r.expandToType(s.Fill.Value)
		if err != nil {
			return err
		}
		el, err := r.element(Target{Selector: s.Fill.Selector})
		if err != nil {
			return err
		}
		el = el.Timeout(r.o.Timeout)
		if err := el.SelectAllText(); err != nil {
			return fmt.Errorf("couldn't type into it: %w", err)
		}
		if err := el.Input(v); err != nil {
			return fmt.Errorf("couldn't type into it: %w", err)
		}
	case s.Select != nil:
		v, err := r.expandToType(s.Select.Value)
		if err != nil {
			return err
		}
		el, err := r.element(Target{Selector: s.Select.Selector})
		if err != nil {
			return err
		}
		res, err := el.Eval(selectJS, v)
		if err != nil {
			return err
		}
		if !res.Value.Bool() {
			return errors.New("no option with that value or label")
		}
	case s.Check != nil:
		el, err := r.element(Target{Selector: s.Check.Selector})
		if err != nil {
			return err
		}
		want := s.Check.Checked == nil || *s.Check.Checked
		cur, err := el.Property("checked")
		if err != nil {
			return err
		}
		if cur.Bool() != want {
			if err := el.Timeout(r.o.Timeout).Click(proto.InputMouseButtonLeft, 1); err != nil {
				if _, err := el.Eval(`() => { this.click(); }`); err != nil {
					return err
				}
			}
		}
	case s.Submit != nil:
		if err := r.submit(*s.Submit); err != nil {
			return err
		}
		r.settle()
		return r.stayed()
	case s.Wait != nil:
		if s.Wait.Selector != "" {
			el, err := r.element(Target{Selector: s.Wait.Selector})
			if err != nil {
				return err
			}
			if err := el.Timeout(r.o.Timeout).WaitVisible(); err != nil {
				return fmt.Errorf("%s never became visible", s.Wait.Selector)
			}
			return nil
		}
		t := time.NewTimer(time.Duration(s.Wait.MS) * time.Millisecond)
		defer t.Stop()
		select {
		case <-t.C:
		case <-r.ctx.Done():
			return r.ctx.Err()
		}
	case s.Expect != nil:
		return r.expect(*s.Expect)
	case s.Screenshot != "":
		if r.o.Save == nil {
			return nil
		}
		img, err := r.t().Screenshot(true, &proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng})
		if err != nil {
			return err
		}
		return r.o.Save(fmt.Sprintf("flows/%s-%02d-%s.png", r.name, n, slug(s.Screenshot)), img)
	}
	return nil
}

const selectJS = `function(v) {
  const o = [...this.options].find(o => o.value === v) || [...this.options].find(o => o.text.trim() === v);
  if (!o) return false;
  this.value = o.value;
  this.dispatchEvent(new Event('input', {bubbles: true}));
  this.dispatchEvent(new Event('change', {bubbles: true}));
  return true;
}`

func (r *runner) submit(t Target) error {
	if t.Selector == "" && t.Text == "" {
		t.Selector = "form"
	}
	el, err := r.element(t)
	if err != nil {
		return err
	}
	if err := r.sendSecret(el, true); err != nil {
		return err
	}
	res, err := el.Eval(`function() {
	  const form = this.tagName === 'FORM' ? this : this.form || this.closest('form');
	  if (!form) return 'no form';
	  const btn = this.tagName === 'FORM' ? form.querySelector('[type=submit],button:not([type])') : this;
	  if (form.requestSubmit) { btn && btn.form === form ? form.requestSubmit(btn) : form.requestSubmit(); } else form.submit();
	  return '';
	}`)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return nil // the page navigated away mid-call: the submit happened
	}
	if msg := res.Value.Str(); msg != "" {
		return errors.New(msg)
	}
	return nil
}

func (r *runner) expect(e Expect) error {
	deadline := time.Now().Add(r.o.Timeout)
	var last error
	for {
		last = r.check(e)
		if last == nil || time.Now().After(deadline) || r.ctx.Err() != nil {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (r *runner) check(e Expect) error {
	p := r.page.Timeout(3 * time.Second)
	if e.Text != "" {
		text, err := r.expand(e.Text)
		if err != nil {
			return err
		}
		res, err := p.Eval(`t => (document.body && document.body.innerText || '').toLowerCase().includes(t.toLowerCase())`, text)
		if err != nil {
			return err
		}
		if res.Value.Bool() == e.Absent {
			if e.Absent {
				return fmt.Errorf("the page still shows %q", text)
			}
			return fmt.Errorf("the page doesn't show %q", text)
		}
	}
	if e.Selector != "" {
		res, err := p.Eval(`s => { const e = document.querySelector(s); return !!e && !!(e.offsetWidth || e.offsetHeight || e.getClientRects().length); }`, e.Selector)
		if err != nil {
			return err
		}
		if res.Value.Bool() == e.Absent {
			if e.Absent {
				return fmt.Errorf("%s is still visible", e.Selector)
			}
			return fmt.Errorf("%s isn't visible", e.Selector)
		}
	}
	if e.URL != "" {
		info, err := p.Info()
		if err != nil {
			return err
		}
		if !strings.Contains(info.URL, e.URL) {
			return fmt.Errorf("the URL is %s", pathOf(info.URL))
		}
	}
	if e.Status != 0 {
		if got := int(r.status.Load()); got != e.Status {
			return fmt.Errorf("the page returned HTTP %d", got)
		}
	}
	if e.Message != nil {
		return r.message(*e.Message, e.Absent)
	}
	return nil
}

// message checks what the flow made the site send against m.
func (r *runner) message(m Message, absent bool) error {
	if r.o.Messages == nil {
		return errors.New("expect message needs Echo: start it (rhythm echo up) and run with --echo")
	}
	want := m
	for _, f := range []*string{&want.To, &want.Subject, &want.Text} {
		v, err := r.expand(*f)
		if err != nil {
			return err
		}
		*f = v
	}
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	msgs, err := r.o.Messages(ctx)
	if err != nil {
		return fmt.Errorf("asking Echo: %w", err)
	}
	n := 0
	for _, got := range msgs {
		if matches(want, got) {
			n++
		}
	}
	need := max(m.Count, 1)
	switch {
	case absent && n > 0:
		return fmt.Errorf("the site sent %s (%s)", plural(n, "such message"), seen(msgs))
	case !absent && n < need:
		if n == 0 {
			return fmt.Errorf("no such message was sent (%s)", seen(msgs))
		}
		return fmt.Errorf("only %d of %d such messages were sent (%s)", n, need, seen(msgs))
	}
	return nil
}

// matches reports whether got is the message want describes.
func matches(want Message, got echomodel.Message) bool {
	has := func(s, sub string) bool { return strings.Contains(strings.ToLower(s), strings.ToLower(sub)) }
	if want.Channel != "" && string(got.Channel) != want.Channel {
		return false
	}
	if want.To != "" {
		to := strings.Join(append(append(append([]string{}, got.To...), got.CC...), got.BCC...), "\n") + "\n" + got.URL
		if !has(to, want.To) {
			return false
		}
	}
	if want.Subject != "" && !has(got.Subject, want.Subject) {
		return false
	}
	return want.Text == "" || has(got.Text+"\n"+got.HTML+"\n"+got.Body, want.Text)
}

// seen sums up what was sent, so a failed expect shows what to fix. The
// summary goes into the run and its reports, so subjects and webhook URLs
// are masked as echolink.Redact masks stored messages: a one-time code
// in a subject or a token in a webhook's query stays out of them.
func seen(msgs []echomodel.Message) string {
	if len(msgs) == 0 {
		return "Echo saw nothing from this flow"
	}
	var parts []string
	for _, m := range echolink.Redact(msgs[:min(len(msgs), 3)]) {
		d := string(m.Channel)
		if to := strings.Join(m.To, ", "); to != "" {
			d += " to " + to
		} else if m.URL != "" {
			d += " to " + m.URL
		}
		if m.Subject != "" {
			d += " " + strconv.Quote(m.Subject)
		}
		parts = append(parts, d)
	}
	if len(msgs) > 3 {
		parts = append(parts, fmt.Sprintf("%d more", len(msgs)-3))
	}
	return "Echo saw: " + strings.Join(parts, "; ")
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(nonWord.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		return "shot"
	}
	return s[:min(len(s), 60)]
}

// Slug turns a flow name into a file-name-safe string.
func Slug(s string) string { return slug(s) }

func pathOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.RequestURI()
	}
	return raw
}
