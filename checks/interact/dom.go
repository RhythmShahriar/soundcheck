// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package interact

import (
	"encoding/json"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// clickTypes are listener types that make an element react to a click.
var clickTypes = map[string]bool{
	"click": true, "mousedown": true, "mouseup": true, "pointerdown": true, "pointerup": true,
	"touchstart": true, "touchend": true, "dblclick": true, "auxclick": true,
}

// markListeners asks Chrome (DOMDebugger.getEventListeners, which sees
// listeners added with addEventListener as well as onclick attributes)
// which nodes listen for clicks, and flags each with __scL so the page
// scripts below can tell handled elements from inert ones, including
// handlers delegated to a parent, the document or the window.
func markListeners(tab *rod.Page) error {
	all := -1
	doc, err := tab.Evaluate(&rod.EvalOptions{JS: `() => document`})
	if err != nil {
		return err
	}
	res, err := proto.DOMDebuggerGetEventListeners{ObjectID: doc.ObjectID, Depth: &all, Pierce: false}.Call(tab)
	if err != nil {
		return err
	}
	seen := map[proto.DOMBackendNodeID]bool{}
	for _, l := range res.Listeners {
		if !clickTypes[l.Type] || l.BackendNodeID == 0 || seen[l.BackendNodeID] {
			continue
		}
		seen[l.BackendNodeID] = true
		if len(seen) > 3000 {
			break
		}
		node, err := proto.DOMResolveNode{BackendNodeID: l.BackendNodeID}.Call(tab)
		if err != nil || node.Object == nil {
			continue
		}
		_, _ = proto.RuntimeCallFunctionOn{ObjectID: node.Object.ObjectID, FunctionDeclaration: `function() { this.__scL = true; }`}.Call(tab)
		_ = proto.RuntimeReleaseObject{ObjectID: node.Object.ObjectID}.Call(tab)
	}
	win, err := tab.Evaluate(&rod.EvalOptions{JS: `() => window`})
	if err != nil {
		return err
	}
	wl, err := proto.DOMDebuggerGetEventListeners{ObjectID: win.ObjectID}.Call(tab)
	if err == nil {
		for _, l := range wl.Listeners {
			if clickTypes[l.Type] {
				_, _ = tab.Eval(`() => { window.__scL = true; }`)
				break
			}
		}
	}
	_ = proto.RuntimeReleaseObject{ObjectID: doc.ObjectID}.Call(tab)
	_ = proto.RuntimeReleaseObject{ObjectID: win.ObjectID}.Call(tab)
	return nil
}

// guardJS makes clicks safe to explore: window.open, alert, confirm and
// prompt are replaced so nothing opens or blocks, and confirm() answers
// "Cancel", so a "Really delete?" prompt never proceeds. Each call is
// counted as a reaction to the click.
const guardJS = `() => {
  if (window.__scGuard) return;
  window.__scGuard = true;
  const s = window.__scClick = window.__scClick || {mut: 0, opened: 0, dialogs: 0};
  window.open = function() { s.opened++; return null; };
  window.alert = function() { s.dialogs++; };
  window.confirm = function() { s.dialogs++; return false; };
  window.prompt = function() { s.dialogs++; return null; };
  window.print = function() { s.dialogs++; };
}`

// helpersJS defines functions shared by the scripts below; it's
// prepended to each so every evaluation is self-contained.
const helpersJS = `
const clean = s => (s || '').replace(/\s+/g, ' ').trim();
const visible = e => !!(e && (e.offsetWidth || e.offsetHeight || e.getClientRects().length)) && getComputedStyle(e).visibility !== 'hidden';
const stateClass = /^(is-|has-)?(active|open|opened|show|shown|hover|focus|focused|selected|current|visible|hidden|collapsed|expanded|in|toggled|on|off|closed|loading|loaded|error|success|disabled)$/;
// Form plugins change these classes as a submission goes through (CF7:
// init → submitting → sent/invalid/failed…), so selectors must ignore them.
const pluginState = /^(init|sent|failed|invalid|unaccepted|spam|aborted|resetting|submitting|payment-required|wpcf7-mail-sent-ok|wpcf7-mail-sent-ng|wpcf7-validation-errors|wpforms-submitting|wpforms-submitted|wpforms-form-submitted|wpforms-has-error|wpforms-error|gform_validation_error|gform_submitting|gform_submission_error|gfield_error|elementor-form-waiting|elementor-form-success|elementor-error)$/;
const stable = s => !!s && /^[A-Za-z][\w-]*$/.test(s) && !/\d{4,}/.test(s);
const unique = s => { try { return document.querySelectorAll(s).length === 1; } catch (e) { return false; } };
const selectorOf = e => {
  if (stable(e.id) && unique('#' + CSS.escape(e.id))) return '#' + CSS.escape(e.id);
  const parts = [];
  for (let n = e; n && n.nodeType === 1 && n !== document.documentElement; n = n.parentElement) {
    if (n !== e && stable(n.id) && unique('#' + CSS.escape(n.id))) { parts.unshift('#' + CSS.escape(n.id)); break; }
    if (n === document.body) { parts.unshift('body'); break; }
    let part = n.tagName.toLowerCase();
    const cls = [...n.classList].filter(c => stable(c) && !stateClass.test(c) && !pluginState.test(c)).slice(0, 2);
    if (cls.length) part += '.' + cls.map(c => CSS.escape(c)).join('.');
    const sib = n.parentElement ? [...n.parentElement.children].filter(c => c.tagName === n.tagName) : [];
    if (sib.length > 1) part += ':nth-of-type(' + (sib.indexOf(n) + 1) + ')';
    parts.unshift(part);
    if (unique(parts.join(' > '))) break;
  }
  return parts.join(' > ');
};
const labelOf = e => {
  let l = clean(e.getAttribute('aria-label')) || clean(e.innerText) || clean(e.value) || clean(e.title) || clean(e.getAttribute('alt'));
  if (!l) { const i = e.querySelector('img[alt],svg title'); if (i) l = clean(i.alt || i.textContent); }
  if (!l && e.labels && e.labels.length) l = clean(e.labels[0].innerText);
  if (!l) l = clean(e.getAttribute('placeholder')) || clean(e.getAttribute('name'));
  return l.slice(0, 60);
};
const handled = e => { for (let n = e; n; n = n.parentNode) if (n.__scL) return true; return !!window.__scL; };
const voidHref = h => h !== null && /^\s*(#|javascript:\s*(void\s*\(?\s*0\s*\)?\s*;?|;)?\s*)$/i.test(h);
const profileOf = f => {
  if (f.closest('.wpcf7') || f.querySelector('input[name=_wpcf7]')) return 'cf7';
  if (f.classList.contains('wpforms-form') || /^wpforms-form-/.test(f.id)) return 'wpforms';
  if (/^gform_\d+/.test(f.id) || f.closest('.gform_wrapper')) return 'gravity';
  if (f.classList.contains('elementor-form')) return 'elementor';
  if (f.classList.contains('woocommerce-checkout') || f.matches('form.checkout')) return 'woo-checkout';
  if (f.matches('form.cart') && !f.classList.contains('woocommerce-cart-form')) return 'woo-cart';
  return '';
};
const formName = (f, profile) => {
  const v = s => { const e = f.querySelector(s); return e ? e.value : ''; };
  switch (profile) {
    case 'cf7': return 'Contact Form 7 #' + (v('input[name=_wpcf7]') || f.id);
    case 'wpforms': return 'WPForms #' + (f.dataset.formid || f.id.replace('wpforms-form-', ''));
    case 'gravity': return 'Gravity Forms #' + ((f.id.match(/\d+/) || [''])[0]);
    case 'elementor': return 'Elementor form ' + (clean(f.getAttribute('name')) || v('input[name=form_id]'));
    case 'woo-checkout': return 'WooCommerce checkout';
    case 'woo-cart': return 'WooCommerce add to cart';
  }
  const n = clean(f.getAttribute('aria-label')) || clean(f.getAttribute('name')) || (stable(f.id) ? '#' + f.id : '');
  if (n) return 'form ' + n;
  const h = f.querySelector('legend,h1,h2,h3,h4') || (f.previousElementSibling && f.previousElementSibling.matches('h1,h2,h3,h4') ? f.previousElementSibling : null);
  if (h && clean(h.innerText)) return 'form "' + clean(h.innerText).slice(0, 40) + '"';
  return 'form ' + ([...document.forms].indexOf(f) + 1);
};
`

// collectJS reads everything level 1 needs, plus the click candidates
// for level 2, from the loaded page. Listener marks must be set first.
const collectJS = `(allow, forms) => {` + helpersJS + `
  const matchAny = (e, list) => list.some(s => { try { return e.matches(s); } catch (x) { return false; } });
  const allowed = e => matchAny(e, allow);
  const out = {wp: false, forms: [], unnamed: [], unlabeled: [], dupIds: [], noHandler: [], cands: []};
  out.wp = !!document.querySelector('link[rel="https://api.w.org/"],meta[name=generator][content^=WordPress],script[src*="/wp-includes/"],script[src*="/wp-content/"],link[href*="/wp-content/"]');

  // Forms.
  for (const f of document.forms) {
    const profile = profileOf(f);

    const rawAction = f.getAttribute('action');
    // The button the submit level presses (as submitJS picks it) can send
    // the form elsewhere with formaction and formmethod.
    const btn = f.querySelector('[type=submit],button:not([type])');
    const btnAction = btn ? btn.getAttribute('formaction') : null;
    const btnMethod = btn ? btn.getAttribute('formmethod') : null;
    const fields = [...f.elements].filter(e => e.matches('input,select,textarea') && !['hidden', 'submit', 'button', 'reset', 'image'].includes(e.type));
    const names = [...f.elements].map(e => e.name || '');
    const method = (btnMethod || f.getAttribute('method') || 'get').toLowerCase();
    let action = rawAction && !/^\s*javascript:/i.test(rawAction) ? f.action : '';
    if (btnAction && !/^\s*javascript:/i.test(btnAction)) action = btn.formAction;
    out.forms.push({
      sel: selectorOf(f), name: formName(f, profile), profile, method,
      action,
      nonce: names.some(n => /nonce|_token|csrf|security|^_wpcf7|^gform_|^wpforms|^form_id$|^_wp_http_referer$/i.test(n)),
      password: fields.some(e => e.type === 'password'),
      search: f.getAttribute('role') === 'search' || fields.some(e => e.type === 'search' || e.name === 's') && fields.length <= 2,
      comment: f.id === 'commentform' || names.includes('comment_post_ID'),
      cartForm: f.classList.contains('woocommerce-cart-form'),
      required: fields.some(e => e.required || e.getAttribute('aria-required') === 'true' || /required/i.test(e.className)),
      fields: fields.length,
      visible: visible(f),
      allowed: matchAny(f, forms),
    });
    if (rawAction && !/^\s*javascript:/i.test(rawAction)) {
      for (const e of f.elements) {
        if (e.matches('input,select,textarea') && !e.name && !e.disabled && !['submit', 'button', 'reset', 'image'].includes(e.type))
          out.unnamed.push({sel: selectorOf(e), label: labelOf(e) || selectorOf(e), form: formName(f, profile)});
      }
    }
  }

  // Required fields nobody can read the label of.
  for (const e of document.querySelectorAll('input[required],select[required],textarea[required],[aria-required=true]')) {
    if (!e.matches('input,select,textarea') || e.type === 'hidden' || !visible(e)) continue;
    if ((e.labels && e.labels.length) || clean(e.getAttribute('aria-label')) || e.getAttribute('aria-labelledby') || clean(e.title)) continue;
    out.unlabeled.push({sel: selectorOf(e), label: clean(e.getAttribute('placeholder')) || clean(e.name) || selectorOf(e)});
  }

  // Duplicate ids (inline SVG internals excepted: icons repeat them by design).
  const ids = {};
  for (const e of document.querySelectorAll('[id]')) {
    if (e.closest('svg') || !e.id) continue;
    ids[e.id] = (ids[e.id] || 0) + 1;
  }
  for (const [id, n] of Object.entries(ids)) if (n > 1) out.dupIds.push({id, n});

  // Links and buttons with no click handler on them or any parent.
  for (const e of document.querySelectorAll('a[href],button,[role=button]')) {
    if (!visible(e) || e.disabled || e.closest('[inert]')) continue;
    const link = e.tagName === 'A';
    if (link && !voidHref(e.getAttribute('href'))) continue;
    if (!link && e.tagName === 'BUTTON' && (e.form || e.type === 'submit' && e.closest('form') || e.type === 'reset')) continue;
    if (e.hasAttribute('popovertarget') || e.hasAttribute('commandfor') || e.closest('summary,label,dialog form[method=dialog]')) continue;
    if (handled(e)) continue;
    out.noHandler.push({sel: selectorOf(e), label: labelOf(e), link});
  }

  // Click candidates: anything that looks interactive or listens for clicks.
  const pattern = 'button,[role=button],[role=tab],[role=menuitem],[role=switch],summary,[aria-expanded],[aria-controls],[aria-haspopup],[data-toggle],[data-bs-toggle],[data-target],[data-bs-target],a[href^="#"],a[href^="javascript:" i],a:not([href])[onclick],[onclick]';
  const set = new Set(document.querySelectorAll(pattern));
  for (const e of document.querySelectorAll('body *')) if (e.__scL) set.add(e);
  const here = location.href.split('#')[0];
  const seen = new Set();
  for (const e of set) {
    if (out.cands.length >= 200) break;
    if (!(e instanceof HTMLElement) || !visible(e) || e.disabled || e.getAttribute('aria-disabled') === 'true') continue;
    // Off-screen controls (skip links, closed off-canvas menus) aren't
    // something a visitor can click as the page loads.
    const r = e.getBoundingClientRect(), cs = getComputedStyle(e);
    if (r.width < 1 || r.height < 1 || r.right + scrollX <= 0 || r.bottom + scrollY <= 0 || r.left + scrollX >= document.documentElement.scrollWidth ||
        cs.opacity === '0' || cs.pointerEvents === 'none') continue;
    if (e.matches('input,select,textarea,option,label,form,html,body') || e.closest('[inert]')) continue;
    if (e.type === 'reset') continue;
    // Form submits belong to level 3.
    if ((e.tagName === 'BUTTON' && e.type === 'submit' && e.form) || e.matches('input[type=submit],input[type=image]')) continue;
    // A parent that's also a candidate gets clicked instead of each child.
    let dup = false;
    for (let n = e.parentElement, i = 0; n && i < 3; n = n.parentElement, i++) if (set.has(n) && visible(n)) { dup = true; break; }
    if (dup) continue;
    const sel = selectorOf(e);
    if (seen.has(sel)) continue;
    seen.add(sel);
    const href = e.tagName === 'A' ? e.getAttribute('href') : null;
    let nav = false;
    if (href !== null && !voidHref(href) && !/^javascript:/i.test(href)) {
      if (href.startsWith('#')) {
        // In-page anchors just scroll, unless a script handles them.
        if (!e.__scL && !e.hasAttribute('aria-controls') && !e.hasAttribute('data-toggle') && !e.hasAttribute('data-bs-toggle') && e.getAttribute('role') !== 'tab') continue;
      } else if (e.href.split('#')[0] !== here || e.target === '_blank') nav = true;
    }
    const cls = typeof e.className === 'string' ? e.className : '';
    const words = [e.innerText, e.getAttribute('aria-label'), e.id, cls, href, e.title, e.value, e.name, e.getAttribute('data-action')].map(clean).join(' | ');
    const says = [e.innerText, e.getAttribute('aria-label'), e.title, e.value, e.name, e.getAttribute('data-action')].map(clean).join(' | ');
    let kind = 'button';
    const k = (cls + ' ' + (e.id || '') + ' ' + (e.getAttribute('role') || '')).toLowerCase();
    if (/menu|nav|hamburger|burger/.test(k) && (e.hasAttribute('aria-expanded') || /toggle/.test(k))) kind = 'menu';
    else if (e.getAttribute('role') === 'tab' || /\btabs?\b|tab-/.test(k)) kind = 'tab';
    else if (e.tagName === 'SUMMARY' || /accordion|collapse/.test(k)) kind = 'accordion';
    else if (/modal|popup|lightbox|dialog/.test(k) || e.getAttribute('aria-haspopup') === 'dialog') kind = 'modal';
    else if (/slick|swiper|carousel|slider|glide|splide|owl-|next|prev|arrow/.test(k)) kind = 'slider';
    else if (/load.?more|show.?more|more.?posts|view.?more/i.test(k + ' ' + clean(e.innerText))) kind = 'load-more';
    else if (/toggle/.test(k) || e.hasAttribute('aria-expanded')) kind = 'toggle';
    else if (e.tagName === 'A') kind = 'link';
    const prio = e.hasAttribute('aria-expanded') || e.hasAttribute('aria-controls') || e.tagName === 'SUMMARY' || kind !== 'button' && kind !== 'link' ? 0 : e.__scL ? 1 : 2;
    out.cands.push({sel, label: labelOf(e), kind, nav, void: href !== null && voidHref(href), words, says, allowed: allowed(e), prio, tag: e.tagName.toLowerCase()});
  }
  return out;
}`

type facts struct {
	WP        bool       `json:"wp"`
	Forms     []formInfo `json:"forms"`
	Unnamed   []field    `json:"unnamed"`
	Unlabeled []field    `json:"unlabeled"`
	DupIDs    []struct {
		ID string `json:"id"`
		N  int    `json:"n"`
	} `json:"dupIds"`
	NoHandler []struct {
		Sel   string `json:"sel"`
		Label string `json:"label"`
		Link  bool   `json:"link"`
	} `json:"noHandler"`
	Cands []candidate `json:"cands"`
}

type formInfo struct {
	Sel      string `json:"sel"`
	Name     string `json:"name"`
	Profile  string `json:"profile"`
	Method   string `json:"method"`
	Action   string `json:"action"`
	Nonce    bool   `json:"nonce"`
	Password bool   `json:"password"`
	Search   bool   `json:"search"`
	Comment  bool   `json:"comment"`
	CartForm bool   `json:"cartForm"`
	Required bool   `json:"required"`
	Fields   int    `json:"fields"`
	Visible  bool   `json:"visible"`
	Allowed  bool   `json:"allowed"`
}

type field struct {
	Sel   string `json:"sel"`
	Label string `json:"label"`
	Form  string `json:"form"`
}

type candidate struct {
	Sel     string `json:"sel"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Nav     bool   `json:"nav"`
	Void    bool   `json:"void"`  // a link to "#" or javascript:void(0)
	Words   string `json:"words"` // everything that names it, href, class and id included
	Says    string `json:"says"`  // what it says to a visitor (see acts)
	Allowed bool   `json:"allowed"`
	Prio    int    `json:"prio"`
	Tag     string `json:"tag"`
}

// collect marks listeners and reads the page's forms and controls.
func collect(tab *rod.Page, allow, forms []string) (*facts, error) {
	tab = tab.Timeout(20 * time.Second)
	if err := markListeners(tab); err != nil {
		return nil, err
	}
	if allow == nil {
		allow = []string{}
	}
	if forms == nil {
		forms = []string{}
	}
	res, err := tab.Eval(collectJS, allow, forms)
	if err != nil {
		return nil, err
	}
	var f facts
	if err := json.Unmarshal([]byte(res.Value.JSON("", "")), &f); err != nil {
		return nil, err
	}
	return &f, nil
}
