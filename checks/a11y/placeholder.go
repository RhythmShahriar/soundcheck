// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
	"github.com/RhythmShahriar/soundcheck/model"
)

// maxFields bounds the fields with a placeholder looked at per document.
// Each costs a call for its accessible name.
const maxFields = maxElements + 1

// field is a form field with a placeholder that fieldsJS found, with its
// own name as the script worked it out. The browser's accessibility
// tree has the last word on the name (see named).
type field struct {
	Path        [][]Elem `json:"path"` // see Selector
	Placeholder string   `json:"placeholder"`
	Named       bool     `json:"named"` // a name other than the placeholder, as the script sees it
}

// fieldsGlobal holds the elements fieldsJS found in the isolated world,
// where the page can't see or change it, so their accessible names can
// be read one by one.
const fieldsGlobal = "__soundcheckFields"

// fieldsJS lists the visible fields with a non-empty placeholder that
// could be named by it alone: inputs that take text (not hidden,
// submit, button, image, reset, checkbox or radio), textareas and
// selects. Search boxes are left out: a search field in [role=search]
// or a form whose only text field it is, sent with a visible button
// that says what it does, is understood without a label.
var fieldsJS = strings.NewReplacer("MAX_FIELDS", fmt.Sprint(maxFields), "GLOBAL", fieldsGlobal).Replace(`() => {
  ` + pathJS + `
  const skip = new Set(['hidden', 'submit', 'button', 'image', 'reset', 'checkbox', 'radio']);
  const shown = el => {
    if (el.closest('[aria-hidden="true"]')) return false;
    if (el.checkVisibility && !el.checkVisibility({opacityProperty: true, visibilityProperty: true})) return false;
    const s = getComputedStyle(el);
    if (s.visibility === 'hidden' || s.display === 'none') return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  };
  const words = s => (s || '').replace(/\s+/g, ' ').trim();
  const textOf = el => words(el.innerText || el.textContent);
  const named = el => {
    const by = (el.getAttribute('aria-labelledby') || '').split(/\s+/).filter(Boolean)
      .map(id => document.getElementById(id)).filter(Boolean).map(textOf).join(' ');
    if (words(by)) return true;
    if (words(el.getAttribute('aria-label'))) return true;
    for (const l of el.labels || []) if (textOf(l)) return true;
    return words(el.getAttribute('title')) !== '';
  };
  const takesText = el => el.localName === 'textarea' || el.localName === 'select' ||
    (el.localName === 'input' && !skip.has(el.type));
  const buttonText = b => b.localName === 'input' ? words(b.value || b.getAttribute('alt') || (b.type === 'submit' ? 'Submit' : '')) :
    words(textOf(b) || b.getAttribute('aria-label'));
  const searchLike = el => {
    if (el.type === 'search' || el.closest('[role="search"], search')) return true;
    const form = el.form || el.closest('form');
    if (!form) return false;
    const fields = Array.from(form.querySelectorAll('input, textarea, select')).filter(f => takesText(f) && shown(f));
    if (fields.length !== 1 || fields[0] !== el) return false;
    return Array.from(form.querySelectorAll('button, input[type="submit"], input[type="image"], [role="button"]'))
      .some(b => shown(b) && buttonText(b) !== '');
  };
  const found = [], els = [];
  for (const el of document.querySelectorAll('input, textarea, select')) {
    if (found.length >= MAX_FIELDS) break;
    if (!takesText(el)) continue;
    const ph = words(el.getAttribute('placeholder'));
    if (!ph || !shown(el) || searchLike(el)) continue;
    els.push(el);
    found.push({path: path(el), placeholder: ph.slice(0, 200), named: named(el)});
  }
  globalThis.GLOBAL = els;
  return JSON.stringify(found);
}`)

// axGroup is the object group of the elements fields resolves, released
// when it's done.
const axGroup = "soundcheck-a11y-fields"

// fields finds the fields of the document whose isolated world is world
// that have a placeholder and no other name. prefix is the document's
// frame path, as in auditFrame.
func fields(page *rod.Page, world proto.RuntimeExecutionContextID, prefix [][]Elem) ([]field, error) {
	raw, err := eval(page, world, "("+fieldsJS+")()", false)
	if err != nil {
		return nil, err
	}
	var found []field
	if err := json.Unmarshal([]byte(raw), &found); err != nil {
		return nil, fmt.Errorf("the placeholder check's result: %w", err)
	}
	defer func() {
		_ = proto.RuntimeReleaseObjectGroup{ObjectGroup: axGroup}.Call(page)
		_, _ = eval(page, world, "delete globalThis."+fieldsGlobal, false)
	}()
	var out []field
	for n, f := range found {
		if page.GetContext().Err() != nil {
			return nil, page.GetContext().Err()
		}
		if ok, err := named(page, world, n); err == nil {
			f.Named = ok
		}
		if f.Named {
			continue
		}
		if len(prefix) > 0 {
			f.Path = append(append([][]Elem(nil), prefix...), f.Path...)
		}
		out = append(out, f)
	}
	return out, nil
}

// named reads whether the n-th field fieldsJS found has an accessible
// name from anything but its placeholder, from the browser's own
// accessibility tree: the same name sources axe and screen readers use
// (aria-labelledby, aria-label, <label>, title). An error leaves the
// script's answer.
func named(page *rod.Page, world proto.RuntimeExecutionContextID, n int) (bool, error) {
	res, err := proto.RuntimeEvaluate{Expression: fmt.Sprintf("globalThis.%s[%d]", fieldsGlobal, n), ContextID: world,
		ObjectGroup: axGroup}.Call(page)
	if err != nil {
		return false, err
	}
	if res.ExceptionDetails != nil || res.Result == nil || res.Result.ObjectID == "" {
		return false, errors.New("the field is gone")
	}
	tree, err := proto.AccessibilityGetPartialAXTree{ObjectID: res.Result.ObjectID}.Call(page)
	if err != nil {
		return false, err
	}
	for _, node := range tree.Nodes {
		if node.Name == nil {
			continue
		}
		return namedBy(node.Name.Sources), nil
	}
	return false, errors.New("the field has no accessibility node")
}

// namedBy reports whether any name source but the placeholder gives a
// name.
func namedBy(sources []*proto.AccessibilityAXValueSource) bool {
	for _, s := range sources {
		if s == nil || s.Type == proto.AccessibilityAXValueSourceTypePlaceholder ||
			s.Attribute == "placeholder" || s.Attribute == "aria-placeholder" {
			continue
		}
		if s.Value == nil {
			continue
		}
		if v, ok := s.Value.Value.Val().(string); ok && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// placeholderIssues turns the fields with only a placeholder into
// a11y/placeholder-label issues, at most keep of them. axe's label rule
// accepts a placeholder as a label, so it never reports one: the
// placeholder disappears as soon as someone types, which leaves them
// guessing what the field was for. A field axe already reported as
// a11y/label on the page is left out: one problem, one issue. capped is
// the number of fields found when it's more than keep, for the page's
// Audit.CappedRules, else 0.
func placeholderIssues(found []field, have []model.Issue, keep int) (out []model.Issue, capped int) {
	hasLabel := map[string]bool{}
	for _, i := range have {
		if i.Check == "a11y/label" {
			hasLabel[i.Source] = true
		}
	}
	seen := map[string]bool{}
	var all []model.Issue
	for _, f := range found {
		sel := Selector(f.Path)
		if sel == "" || seen[sel] || hasLabel[sel] {
			continue
		}
		seen[sel] = true
		i := model.Issue{Check: "a11y/placeholder-label", Level: model.Warning, Title: "Form field is labelled only by its placeholder", //nolint:misspell // British English, like the rest of the copy
			Detail: fmt.Sprintf("The only name is the placeholder %q, which disappears when someone types.", shortText(f.Placeholder, 60)),
			Source: sel, Fix: "Add a visible <label for> or aria-labelledby that points at visible text.",
			WCAG: []string{"3.3.2"}, EN301549: []string{"9.3.3.2"}, Impact: "serious"}
		i.Identify()
		all = append(all, i)
	}
	if len(all) > keep {
		total := len(all)
		all = all[:keep]
		for n := range all {
			all[n].Detail += fmt.Sprintf("\nThe first %d of %d fields named only by their placeholder on the page are reported.", keep, total)
		}
		return all, total
	}
	return all, 0
}

// shortText is site text for a Detail: on one line, credentials the kit
// knows redacted and at most n runes.
func shortText(s string, n int) string {
	s = secrets.Redact(strings.TrimSpace(spaces.ReplaceAllString(s, " ")))
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}
