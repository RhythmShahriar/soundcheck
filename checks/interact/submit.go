// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/model"
)

// profile says how a form plugin shows the outcome of a submission.
// Selectors are matched document-wide, since some plugins replace the
// form with their confirmation.
type profile struct {
	success  string // a confirmation appeared
	failure  string // the plugin says sending failed
	invalid  string // validation messages
	endpoint string // the AJAX endpoint the plugin posts to (substring)
}

var profiles = map[string]profile{
	"cf7": {
		success:  ".wpcf7 form.sent, .wpcf7-mail-sent-ok",
		failure:  ".wpcf7 form.failed, .wpcf7 form.aborted, .wpcf7 form.spam, .wpcf7-mail-sent-ng, .wpcf7-spam-blocked",
		invalid:  ".wpcf7 form.invalid, .wpcf7 form.unaccepted, .wpcf7-not-valid-tip, .wpcf7-validation-errors",
		endpoint: "/contact-form-7/v1/",
	},
	"wpforms": {
		success:  ".wpforms-confirmation-container, .wpforms-confirmation-container-full, [id^=wpforms-confirmation]",
		failure:  ".wpforms-error-container, .wpforms-error-alert",
		invalid:  "label.wpforms-error, em.wpforms-error, .wpforms-has-error",
		endpoint: "admin-ajax.php",
	},
	"gravity": {
		success:  ".gform_confirmation_message, [id^=gform_confirmation_message], .gform_confirmation_wrapper",
		failure:  ".gform_submission_error",
		invalid:  ".gfield_error, .validation_error, .gform_validation_errors",
		endpoint: "admin-ajax.php",
	},
	"elementor": {
		success:  ".elementor-message-success",
		failure:  ".elementor-message-danger",
		invalid:  ".elementor-field-group.elementor-error, .elementor-message.elementor-help-inline",
		endpoint: "admin-ajax.php",
	},
	"woo-cart": {
		success: ".woocommerce-message, a.added_to_cart",
		failure: ".woocommerce-error",
		invalid: ".woocommerce-error",
	},
	"woo-checkout": {
		success:  ".woocommerce-order-received, .woocommerce-thankyou-order-received",
		failure:  ".woocommerce-NoticeGroup-checkout .woocommerce-error",
		invalid:  ".woocommerce-error, .woocommerce-invalid",
		endpoint: "wc-ajax=checkout",
	},
	"": {
		failure: ".alert-danger, .alert-error, .error-message, .form-error, .notice-error, .message.error",
		invalid: "[aria-invalid=true], .invalid-feedback, .field-error, .has-error, .is-invalid, input.error, .error:not(form):not(input)",
	},
}

// eligible decides whether level 3 may submit a form. On production URLs
// only forms matched by interactions.forms are submitted.
func eligible(fm formInfo, production bool) (bool, string) {
	switch {
	case fm.Allowed:
		return true, ""
	case production:
		return false, "production URL; allowlist the form in interactions.forms to submit it"
	case !fm.Visible:
		return false, "hidden"
	case fm.Password:
		return false, "has a password field"
	case fm.Search || (fm.Method != "post" && fm.Profile == ""):
		return false, "search or GET form"
	case fm.Comment:
		return false, "comment form; allowlist it in interactions.forms to test it"
	case fm.CartForm:
		return false, "cart form"
	case strings.Contains(fm.Action, "/wp-admin") || strings.Contains(fm.Action, "wp-login.php"):
		return false, "posts to WordPress admin"
	case fm.Fields == 0 && fm.Profile != "woo-cart":
		return false, "no fields"
	}
	return true, ""
}

// submits runs level 3 on the page's forms.
func (m *Module) submits(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page, f *facts) {
	pageURL := orDefault(p.FinalURL, p.URL)
	prod := cc.Config.IsProduction(pageURL) || cc.Config.IsProduction(p.URL)
	fake := flow.Fake{Seed: cc.Run.Started.String()}
	record := func(fm formInfo, outcome, detail string) {
		p.Interact = append(p.Interact, model.Interaction{Kind: "submit", Selector: fm.Sel, Label: fm.Name, Outcome: outcome, Detail: detail})
	}
	done := 0
	for _, fm := range f.Forms {
		if ctx.Err() != nil || done >= 5 {
			return
		}
		ok, why := eligible(fm, prod)
		if !ok {
			if prod {
				m.warnOnce(cc, "production", "Form submits skipped on production URLs: allowlist forms in interactions.forms to test them there")
			}
			if fm.Visible && !fm.Search && fm.Method == "post" || prod {
				record(fm, "skipped", why)
			}
			continue
		}
		done++
		m.submitForm(ctx, cc, tab, p, pageURL, fm, fake, record)
	}
	// No reload at the end: interact's Live hook runs last, and the mobile
	// pass navigates to the page afresh.
}

// outcome is what happened after a submit.
type outcome struct {
	kind   string // success, validation, failed, none
	status int    // HTTP status when the server failed
	msg    string
}

func (m *Module) submitForm(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page, pageURL string, fm formInfo, fake flow.Fake, record func(formInfo, string, string)) {
	base, _ := url.Parse(pageURL)
	fail := func(o outcome) {
		if o.status >= 500 {
			check.Page(p, model.Issue{Check: "interact/form-error", Level: model.Error,
				Title:  fmt.Sprintf("Form submission fails (HTTP %d): %s", o.status, fm.Name),
				Detail: o.msg, Fix: "Check the PHP error log at the time of the submission; a plugin's handler crashed."})
		}
	}
	prof := profiles[fm.Profile]

	// Woo checkout only with a payment method that takes no money.
	if fm.Profile == "woo-checkout" {
		if err := reload(ctx, tab, pageURL); err != nil {
			return
		}
		res, err := tab.Timeout(10*time.Second).Eval(gatewayJS, fm.Sel)
		if err != nil || !res.Value.Bool() {
			record(fm, "skipped", "no offline (cod, bacs, cheque) or test-mode payment method") //nolint:misspell // WooCommerce gateway id
			return
		}
	}

	// 1. Empty: expect validation.
	if fm.Required || fm.Profile != "" && fm.Profile != "woo-cart" {
		if err := reload(ctx, tab, pageURL); err != nil {
			record(fm, "skipped", "couldn't reload the page")
			return
		}
		res, err := tab.Timeout(10*time.Second).Eval(clearJS, fm.Sel)
		switch {
		case err != nil:
			record(fm, "skipped", "form not found")
			return
		case res.Value.Get("html5").Bool():
			record(fm, "validation", "browser validation blocks an empty submit")
		default:
			o := m.send(ctx, tab, fm, prof, base)
			switch {
			case o.status >= 500:
				record(fm, "failed", o.msg)
				fail(o)
			case o.kind == "validation" || o.kind == "failed":
				record(fm, "validation", o.msg)
			default:
				record(fm, "failed", "an empty submit showed no validation message")
				check.Page(p, model.Issue{Check: "interact/form-validation", Level: model.Warning,
					Title: "Form shows no validation: " + fm.Name, Detail: orDefault(o.msg, "an empty submit showed no error message"),
					Fix: "Mark required fields (required attribute or the plugin's setting) so visitors see what's missing."})
			}
		}
	}

	// 2. Valid data: expect success.
	if err := reload(ctx, tab, pageURL); err != nil {
		record(fm, "skipped", "couldn't reload the page")
		return
	}
	if fm.Profile == "woo-checkout" {
		_, _ = tab.Timeout(10*time.Second).Eval(gatewayJS, fm.Sel)
	}
	if err := fill(tab, fm.Sel, fake); err != nil {
		record(fm, "skipped", err.Error())
		return
	}
	act := model.EchoAction{Action: echolink.FormAction(p.Path, fm.Sel), Label: fm.Name, Kind: "form", Page: p.Path}
	o := m.sendTagged(ctx, cc, tab, fm, prof, base, act.Action)
	switch {
	case o.kind == "success":
		detail := o.msg
		if cc.Echo != nil {
			if sent, ok := m.messages(ctx, cc, p, base, act); ok {
				detail = strings.TrimSpace(detail + " · " + sent)
			}
		}
		record(fm, "success", detail)
	case o.status >= 500:
		record(fm, "failed", o.msg)
		fail(o)
	default:
		record(fm, "failed", o.msg)
		check.Page(p, model.Issue{Check: "interact/form-submit", Level: model.Error,
			Title: "Form no longer submits: " + fm.Name, Detail: o.msg,
			Fix: "Submit the form by hand and watch the browser's network tab; check the plugin's settings, mail setup and spam protection."})
	}
}

// sendTagged is send with the submit's requests tagged for Echo, when it
// runs. Tagged submits take turns across tabs: the tag's cookie is shared
// by the browser session.
func (m *Module) sendTagged(ctx context.Context, cc *check.Context, tab *rod.Page, fm formInfo, prof profile, base *url.URL, action string) outcome {
	if cc.Echo == nil {
		return m.send(ctx, tab, fm, prof, base)
	}
	defer cc.Echo.Turn()()
	untag, err := cc.Echo.Tag(ctx, tab, base.String(), action)
	if err != nil {
		m.warnOnce(cc, "echo-tag", "echo: couldn't tag form submits, so their messages can't be told apart: "+err.Error())
	}
	defer untag()
	return m.send(ctx, tab, fm, prof, base)
}

// messages asks Echo what a successful submit sent, records it on the
// page with the problems Echo finds in it, and sums it up for the
// interaction's detail. ok is false when Echo couldn't be asked.
func (m *Module) messages(ctx context.Context, cc *check.Context, p *model.Page, base *url.URL, act model.EchoAction) (string, bool) {
	msgs, err := cc.Echo.Wait(ctx, act.Action)
	if err != nil {
		m.warnOnce(cc, "echo-wait", "echo: couldn't get the messages form submits sent: "+err.Error())
		return "", false
	}
	act.Messages = msgs
	p.Echo = append(p.Echo, act)
	for _, i := range echolink.Issues(act, base.Hostname()) {
		check.Page(p, i)
	}
	if len(msgs) == 0 {
		check.Page(p, echolink.NoMessage(act, cc.Echo.Quiet))
	}
	return echolink.Summary(msgs), true
}

// fill types fake data into a form's fields.
func fill(tab *rod.Page, sel string, fake flow.Fake) error {
	t := tab.Timeout(10 * time.Second)
	res, err := t.Eval(fieldsJS, sel)
	if err != nil {
		return fmt.Errorf("form not found: %w", err)
	}
	var fields []struct {
		I int `json:"i"`
		flow.Field
	}
	if err := json.Unmarshal([]byte(res.Value.JSON("", "")), &fields); err != nil {
		return err
	}
	values := map[string]string{}
	for _, f := range fields {
		values[strconv.Itoa(f.I)] = fake.For(f.Field)
	}
	res, err = t.Eval(fillJS, sel, values)
	if err != nil {
		return err
	}
	if msg := res.Value.Get("invalid").Str(); msg != "" {
		return fmt.Errorf("couldn't fill it validly: %s", msg)
	}
	return nil
}

// send submits the form and waits up to 10 s for an outcome.
func (m *Module) send(ctx context.Context, tab *rod.Page, fm formInfo, prof profile, base *url.URL) outcome {
	w := watch(ctx, tab)
	defer w.stop()
	before := counts(tab, prof)
	w.reset()
	if _, err := tab.Timeout(10*time.Second).Eval(submitJS, fm.Sel); err != nil {
		if ctx.Err() != nil {
			return outcome{kind: "none", msg: ctx.Err().Error()}
		}
		// The page navigated during the call: the watcher has the rest.
	}
	start := time.Now()
	var ajaxOK time.Time
	for time.Since(start) < 10*time.Second && ctx.Err() == nil {
		settle(ctx, 150*time.Millisecond)
		e := w.report()
		for _, q := range e.failed {
			if q.Status >= 500 && (q.Type == "XHR" || q.Type == "Fetch" || q.Type == "Document" || strings.Contains(q.URL, prof.endpoint) && prof.endpoint != "") {
				return outcome{kind: "failed", status: q.Status, msg: describe(q, base)}
			}
		}
		if e.nav != nil {
			if e.nav.status == 0 && e.nav.failed == "" {
				continue // still loading
			}
			if e.nav.failed != "" {
				return outcome{kind: "failed", msg: "the page failed to load after submitting (" + e.nav.failed + ")"}
			}
			if e.nav.status >= 500 {
				return outcome{kind: "failed", status: e.nav.status, msg: fmt.Sprintf("%s returned %d", shortURL(base, e.nav.url), e.nav.status)}
			}
			_ = tab.Timeout(10 * time.Second).WaitLoad()
			settle(ctx, 300*time.Millisecond)
			after := counts(tab, prof)
			switch {
			case after.Success > 0:
				return outcome{kind: "success", msg: orDefault(after.Text, "redirected to "+shortURL(base, e.nav.url))}
			case after.Failure > 0:
				return outcome{kind: "failed", msg: orDefault(after.Text, "the plugin reported a failure")}
			case after.Invalid > 0:
				return outcome{kind: "validation", msg: orDefault(after.Text, "validation errors shown")}
			case e.nav.status >= 400:
				return outcome{kind: "failed", msg: fmt.Sprintf("%s returned %d", shortURL(base, e.nav.url), e.nav.status)}
			}
			return outcome{kind: "success", msg: "redirected to " + shortURL(base, e.nav.url)}
		}
		now := counts(tab, prof)
		switch {
		case now.Success > before.Success:
			return outcome{kind: "success", msg: now.Text}
		case now.Failure > before.Failure:
			return outcome{kind: "failed", msg: orDefault(now.Text, "the plugin reported a failure")}
		case now.Invalid > before.Invalid:
			return outcome{kind: "validation", msg: orDefault(now.Text, "validation errors shown")}
		case now.HTML5:
			return outcome{kind: "validation", msg: "the browser blocked the submit"}
		}
		// A generic form posting by AJAX: a 2xx answer with no error shown.
		if e.pending == 0 && e.requests > 0 && len(e.failed) == 0 {
			if ajaxOK.IsZero() {
				ajaxOK = time.Now()
			} else if time.Since(ajaxOK) > 1500*time.Millisecond && prof.success == "" {
				return outcome{kind: "success", msg: "the server answered without an error"}
			}
		}
	}
	e := w.report()
	if len(e.failed) > 0 {
		q := e.failed[0]
		return outcome{kind: "failed", msg: describe(q, base)}
	}
	if !ajaxOK.IsZero() {
		return outcome{kind: "none", msg: "the server answered, but no confirmation appeared within 10 s"}
	}
	return outcome{kind: "none", msg: "nothing happened within 10 s"}
}

type formCounts struct {
	Success int    `json:"success"`
	Failure int    `json:"failure"`
	Invalid int    `json:"invalid"`
	HTML5   bool   `json:"html5"`
	Text    string `json:"text"`
}

func counts(tab *rod.Page, prof profile) formCounts {
	var c formCounts
	res, err := tab.Timeout(3*time.Second).Eval(countsJS, prof.success, prof.failure, prof.invalid)
	if err == nil {
		_ = json.Unmarshal([]byte(res.Value.JSON("", "")), &c)
	}
	return c
}

const countsJS = `(success, failure, invalid) => {` + helpersJS + `
  const count = s => { if (!s) return []; try { return [...document.querySelectorAll(s)].filter(visible); } catch (e) { return []; } };
  const ok = count(success), bad = count(failure), inv = count(invalid);
  const msg = [...document.querySelectorAll('.wpcf7-response-output,.wpforms-confirmation-container,.gform_confirmation_message,.elementor-message,.woocommerce-message,.woocommerce-error,[role=alert]')].filter(visible).map(e => clean(e.innerText)).filter(Boolean);
  const first = msg[0] || clean(((ok[0] || bad[0] || inv[0]) || {}).innerText);
  return {success: ok.length, failure: bad.length, invalid: inv.length, html5: !!window.__scInvalid, text: (first || '').slice(0, 160)};
}`

// clearJS empties the form's text fields and reports whether the
// browser's own validation would block an empty submit.
const clearJS = `(sel) => {
  const f = document.querySelector(sel);
  if (!f) throw new Error('form not found');
  const set = (e, v) => { const d = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e), 'value'); d && d.set ? d.set.call(e, v) : (e.value = v); e.dispatchEvent(new Event('input', {bubbles: true})); e.dispatchEvent(new Event('change', {bubbles: true})); };
  for (const e of f.elements) {
    if (e.matches('textarea') || e.matches('input') && !['hidden', 'submit', 'button', 'reset', 'image', 'checkbox', 'radio', 'file', 'range', 'color'].includes(e.type)) set(e, '');
  }
  return {html5: !f.noValidate && !f.checkValidity()};
}`

// fieldsJS lists the text-like fields to fill, with the hints Fake uses.
const fieldsJS = `(sel) => {` + helpersJS + `
  const f = document.querySelector(sel);
  if (!f) throw new Error('form not found');
  const out = [];
  [...f.elements].forEach((e, i) => {
    if (!(e.matches('textarea') || e.matches('input') && !['hidden', 'submit', 'button', 'reset', 'image', 'checkbox', 'radio', 'file', 'password'].includes(e.type))) return;
    if (e.disabled || e.readOnly) return;
    out.push({i, Tag: e.tagName.toLowerCase(), Type: e.type || 'text', Name: e.name || '', ID: e.id || '',
      Label: clean((e.labels && e.labels[0] && e.labels[0].innerText) || e.getAttribute('aria-label') || e.placeholder || ''),
      Autocomplete: e.getAttribute('autocomplete') || '', Min: e.getAttribute('min') || '', Max: e.getAttribute('max') || '',
      Step: e.getAttribute('step') || '', MinLength: e.minLength > 0 ? e.minLength : 0, MaxLength: e.maxLength > 0 ? e.maxLength : 0,
      Pattern: e.getAttribute('pattern') || ''});
  });
  return out;
}`

// fillJS sets text values, picks the first real option of each select,
// ticks required and consent checkboxes, and picks a radio in each group.
const fillJS = `(sel, values) => {
  const f = document.querySelector(sel);
  if (!f) throw new Error('form not found');
  const set = (e, v) => { const d = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e), 'value'); d && d.set ? d.set.call(e, v) : (e.value = v); e.dispatchEvent(new Event('input', {bubbles: true})); e.dispatchEvent(new Event('change', {bubbles: true})); };
  const els = [...f.elements];
  els.forEach((e, i) => { if (values[i] !== undefined) set(e, values[i]); });
  for (const e of els) {
    if (e.disabled) continue;
    if (e.tagName === 'SELECT' && !e.multiple && !e.value) {
      const o = [...e.options].find(o => o.value && !o.disabled);
      if (o) { e.value = o.value; e.dispatchEvent(new Event('change', {bubbles: true})); }
    }
    if (e.type === 'checkbox' && !e.checked) {
      const hint = [e.name, e.id, e.className, e.value, e.closest('label,.wpcf7-acceptance,.wpforms-field,.gfield') && e.closest('label,.wpcf7-acceptance,.wpforms-field,.gfield').innerText].join(' ');
      if (e.required || e.getAttribute('aria-required') === 'true' || /accept|agree|consent|terms|privacy|gdpr|acceptance/i.test(hint)) e.click();
    }
    if (e.type === 'radio' && e.name && !els.some(r => r.type === 'radio' && r.name === e.name && r.checked)) e.click();
  }
  if (!f.noValidate && !f.checkValidity()) {
    const bad = els.find(e => e.willValidate && !e.checkValidity());
    return {invalid: bad ? (bad.name || bad.id || bad.type) + ': ' + bad.validationMessage : 'the browser rejects the values'};
  }
  return {invalid: ''};
}`

// submitJS submits like a visitor: through the form's own submit button
// when it has one, so the plugin's submit handlers run.
const submitJS = `(sel) => {
  const f = document.querySelector(sel);
  if (!f) throw new Error('form not found');
  window.__scInvalid = false;
  f.addEventListener('invalid', () => { window.__scInvalid = true; }, {capture: true, once: true});
  const btn = f.querySelector('[type=submit],button:not([type])');
  if (f.requestSubmit) { btn ? f.requestSubmit(btn) : f.requestSubmit(); } else f.submit();
  return true;
}`

// gatewayJS selects an offline payment method on a WooCommerce checkout.
// It returns false when there's none and the gateway isn't in test mode.
const gatewayJS = `(sel) => {
  const f = document.querySelector(sel);
  if (!f) return false;
  const offline = [...f.querySelectorAll('input[name=payment_method]')].find(r => ['cod', 'bacs', 'cheque'].includes(r.value));
  if (offline) { offline.click(); offline.checked = true; return true; }
  const pay = f.querySelector('#payment');
  return !!pay && /test ?mode|sandbox/i.test(pay.innerText || '');
}`
