// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"fmt"
	"encoding/json"
	"strconv"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/internal/focus"
)

// Unlinked is a form in a key flow that showed errors after a submit or
// click step while focus stayed away from them and no field pointed at
// its message with aria-describedby or aria-errormessage.
type Unlinked struct {
	Step   int    // the 1-based step after which the errors showed
	Form   string // the form's selector
	Fields int    // the fields that show an error (or, with none marked, the error messages)
	Focus  string // the selector of the focused element, "body" for none
}

// errorWait is how long a submit may take to show its errors.
const errorWait = time.Second

// errorWatch is what a submit or click step on a form knew before its
// key press: the form, and in window[name] the errors already shown and
// the fields already invalid.
type errorWatch struct {
	form string
	name string
}

// errorSel matches the elements that show a form error: live regions and
// the classes common form plugins use (WooCommerce, Bootstrap, Contact
// Form 7).
const errorSel = `[role=alert],[aria-live],.error,.woocommerce-error,.invalid-feedback,.wpcf7-not-valid-tip`

// formOfJS finds the form a step works on: the element itself, or the
// form of a button-like control in it. A checkbox, field or link in a
// form isn't one: a click on it doesn't submit. It returns the form's
// path the way focus.Active builds a stop's, so a11y.Selector names it
// the same way, and remembers the errors already shown and the fields
// already invalid in window[name].
const formOfJS = `function(submit, name, errorSel) {
  let form = null;
  if (this.localName === 'form') form = this;
  else {
    const type = (this.getAttribute('type') || '').toLowerCase();
    const role = (this.getAttribute('role') || '').toLowerCase();
    const button = this.localName === 'button' ? type !== 'reset' :
      (this.localName === 'input' && ['submit', 'image', 'button'].includes(type)) || role === 'button';
    if (submit || button) form = this.form || (this.closest && this.closest('form'));
  }
  if (!form) return '';
  const visible = e => !!(e.offsetWidth || e.offsetHeight || e.getClientRects().length);
  const shown = new Set([...document.querySelectorAll(errorSel)].filter(e => visible(e) && e.textContent.trim()));
  const invalid = new Set([...form.elements].filter(e => e.matches(':invalid')));
  window[name] = {shown, invalid};
  const trees = [];
  let tree = [];
  for (let n = form; n && n.nodeType === 1;) {
    const p = n.parentNode;
    const same = p ? Array.from(p.children || []).filter(c => c.localName === n.localName) : [];
    tree.unshift({tag: n.localName, id: n.id || '', name: n.getAttribute('name') || '', nth: same.length > 1 ? same.indexOf(n) + 1 : 0});
    if (p && p.nodeType === 11 && p.host) {
      trees.unshift(tree);
      tree = [];
      n = p.host;
    } else {
      n = n.parentElement;
    }
  }
  trees.unshift(tree);
  return JSON.stringify(trees);
}`

// watchErrors starts watching the form of a submit or click step on el,
// before its key is pressed. It returns nil when the step isn't on a
// form, or the form can't be read.
func (r *runner) watchErrors(el *rod.Element, submit bool) *errorWatch {
	name := "__soundcheckErr" + strconv.FormatInt(time.Now().UnixNano(), 36)
	res, err := el.Timeout(3*time.Second).Eval(formOfJS, submit, name, errorSel)
	if err != nil || res.Value.Str() == "" {
		return nil
	}
	var path [][]a11y.Elem
	if err := json.Unmarshal([]byte(res.Value.Str()), &path); err != nil {
		return nil
	}
	return &errorWatch{form: a11y.Selector(path), name: name}
}

// errorsJS reads what a form shows after its submit. A field shows an
// error when it is aria-invalid, or invalid and it was before the step
// (a form a script reset after it sent is invalid again but shows no
// error), or invalid on a new page that shows a new error. An error
// message is new when it wasn't shown before the step; on a new page
// every one is. Live regions are announced, so a new message in one
// needs no link of its own; one outside counts when no field is marked.
//
// It passes when focus is on the first such field, on an element that
// is in, holds or repeats an error message (an error summary), or when
// every such field (with none marked, every message) is tied with
// aria-describedby or aria-errormessage to text.
const errorsJS = `(sel, name, errorSel) => {
  let form = null;
  if (sel.includes('>>>')) {
    let root = document;
    for (const part of sel.split('>>>')) {
      form = root && root.querySelector(part.trim());
      root = form && (form.shadowRoot || form);
    }
  } else form = document.querySelector(sel);
  if (!form || form.localName !== 'form') return null;
  const before = window[name];
  const visible = e => !!(e.offsetWidth || e.offsetHeight || e.getClientRects().length);
  const text = e => (e.textContent || '').trim();
  const msgs = [...document.querySelectorAll(errorSel)].filter(e => visible(e) && text(e) && !(before && before.shown.has(e)));
  const live = e => !!e.closest('[role=alert],[role=status],[aria-live]:not([aria-live=off])');
  const loose = msgs.filter(e => !live(e));
  const fields = [...new Set([...form.elements, ...form.querySelectorAll('[aria-invalid=true]')])].filter(e =>
    !['fieldset', 'button', 'output', 'object'].includes(e.localName) && e.type !== 'hidden' && !e.disabled && visible(e) &&
    (e.getAttribute('aria-invalid') === 'true' ||
     (e.matches(':invalid') && (before ? before.invalid.has(e) : msgs.length > 0))));
  const n = fields.length || loose.length;
  if (!n) return {n: 0};
  let active = document.activeElement;
  while (active && active.shadowRoot && active.shadowRoot.activeElement) active = active.shadowRoot.activeElement;
  if (active && fields.length && active === fields[0]) return {n, ok: true};
  if (active && active !== document.body && active !== document.documentElement && msgs.some(m => m.contains(active) || active.contains(m) || (text(active).includes(text(m))))) return {n, ok: true};
  const refs = e => ((e.getAttribute('aria-describedby') || '') + ' ' + (e.getAttribute('aria-errormessage') || '')).split(/\s+/).filter(Boolean)
    .map(id => (e.getRootNode().getElementById ? e.getRootNode() : document).getElementById(id)).filter(Boolean);
  if (fields.length) return {n, ok: fields.every(f => refs(f).some(t => text(t)))};
  const all = [...form.elements].flatMap(refs);
  return {n, ok: loose.every(m => all.some(t => t === m || t.contains(m) || m.contains(t)))};
}`

// checkErrors waits up to errorWait after a step on w's form for errors
// to show and reports them when they aren't tied to their fields. A
// form that shows no error, or is gone (the page moved on), passes.
func (r *runner) checkErrors(n int, w *errorWatch) *Unlinked {
	var st struct {
		N  int
		OK bool
	}
	deadline := time.Now().Add(errorWait)
	for {
		res, err := r.page.Timeout(time.Second).Eval(errorsJS, w.form, w.name, errorSel)
		st.N, st.OK = 0, false
		if err == nil && res.Value.Nil() {
			return nil
		}
		if err == nil {
			// Nil parsed the value, so it is read field by field.
			st.N, st.OK = res.Value.Get("n").Int(), res.Value.Get("ok").Bool()
		}
		if st.N > 0 && st.OK {
			return nil
		}
		if time.Now().After(deadline) || r.ctx.Err() != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st.N == 0 {
		return nil
	}
	stop, err := focus.Active(r.ctx, r.page)
	on := "body"
	if err == nil {
		on = stopName(stop)
	}
	r.keys.log(fmt.Sprintf("step %d: %s shows %s not tied to its fields; focus on %s", n, w.form, plural(st.N, "error"), on))
	return &Unlinked{Step: n, Form: w.form, Fields: st.N, Focus: on}
}
