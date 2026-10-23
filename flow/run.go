package flow

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
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
}

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
	v, secrets, err := Expand(s, r.o.Fake, r.o.Env)
	r.secrets = append(r.secrets, secrets...)
	return v, err
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
	if err := r.t().Navigate(target); err != nil {
		return err
	}
	r.settle()
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
		if err := el.Timeout(r.o.Timeout).Click(proto.InputMouseButtonLeft, 1); err != nil {
			return fmt.Errorf("couldn't click: %w", err)
		}
		r.settle()
	case s.Fill != nil:
		v, err := r.expand(s.Fill.Value)
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
		v, err := r.expand(s.Select.Value)
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
	return nil
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
