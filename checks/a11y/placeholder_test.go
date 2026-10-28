// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/model"
)

// form is a checkout page whose body is the fields given.
const form = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Checkout</title></head>
<body><main><h1>Checkout</h1>%s</main></body></html>`

func placeholderOn(p *model.Page) []model.Issue {
	var out []model.Issue
	for _, i := range p.Issues {
		if i.Check == "a11y/placeholder-label" {
			out = append(out, i)
		}
	}
	return out
}

// A theme update replaced the email field's label by a placeholder
// (s60). axe accepts the placeholder as a label; this check doesn't.
func TestPlaceholderLabel(t *testing.T) {
	needBrowser(t)
	srv := pages(t, map[string]string{
		"/only/": fmt.Sprintf(form, `<form action="/pay"><input type="email" name="email" placeholder="Email address">
<input name="city" aria-label="City" placeholder="City"><button>Pay</button></form>`),
		"/search/": fmt.Sprintf(form, `<form action="/find"><input type="text" name="q" placeholder="Search products"><button>Search</button></form>
<div role="search"><input name="s" placeholder="Search posts"></div><input type="search" name="k" placeholder="Search">`),
		"/label/": fmt.Sprintf(form, `<form><label for="em">Email</label> <input id="em" type="email" name="email" placeholder="you@example.org">
<label>Phone <input name="phone" placeholder="+44 20 ..."></label><button>Pay</button></form>`),
		"/labelledby/": fmt.Sprintf(form, `<form><span id="n">Name on card</span> <input aria-labelledby="n" name="holder" placeholder="As printed">
<span id="m" aria-label="Postcode"></span><input aria-labelledby="m" name="post" placeholder="AB1 2CD">
<input name="other" placeholder="Other"><button>Pay</button></form>`),
		"/other/": fmt.Sprintf(form, `<form><input type="checkbox" name="news" placeholder="news"><input type="hidden" name="t" placeholder="x">
<input name="gone" placeholder="Hidden" style="display:none"><input type="submit" value="Go" placeholder="Go">
<input name="title" title="Coupon code" placeholder="SAVE10"><input name="a" placeholder="A"><button>Pay</button></form>`),
	})
	r := audited(t, srv, "2.1", "", "/only/", "/search/", "/label/", "/labelledby/", "/other/")

	got := placeholderOn(r.Page("/only/"))
	if len(got) != 1 {
		t.Fatalf("/only/: %+v", got)
	}
	want := model.Issue{Check: "a11y/placeholder-label", Level: model.Warning, Title: "Form field is labeled only by its placeholder",
		Detail: `The only name is the placeholder "Email address", which disappears when someone types.`,
		Source: `input[name="email"]`, Fix: "Add a visible <label for> or aria-labelledby that points at visible text.",
		WCAG: []string{"3.3.2"}, EN301549: []string{"9.3.3.2"}, Impact: "serious"}
	want.Identify()
	if g := got[0]; g.Check != want.Check || g.Level != want.Level || g.Title != want.Title || g.Detail != want.Detail ||
		g.Source != want.Source || g.Fix != want.Fix || !slices.Equal(g.WCAG, want.WCAG) || !slices.Equal(g.EN301549, want.EN301549) ||
		g.Impact != want.Impact || g.Key != want.Key {
		t.Errorf("issue = %+v\nwant    %+v", g, want)
	}
	if got := placeholderOn(r.Page("/search/")); len(got) > 0 {
		t.Errorf("search boxes: %+v", got)
	}
	if got := placeholderOn(r.Page("/label/")); len(got) > 0 {
		t.Errorf("fields with a label: %+v", got)
	}
	// The postcode's name comes from the aria-label of the element it
	// points at: only the browser's accessibility tree knows it.
	var sources []string
	for _, i := range placeholderOn(r.Page("/labelledby/")) {
		sources = append(sources, i.Source)
	}
	if !slices.Equal(sources, []string{`input[name="other"]`}) {
		t.Errorf("/labelledby/: %v", sources)
	}
	sources = nil
	for _, i := range placeholderOn(r.Page("/other/")) {
		sources = append(sources, i.Source)
	}
	if !slices.Equal(sources, []string{`input[name="a"]`}) {
		t.Errorf("/other/ (checkbox, hidden, submit, titled): %v", sources)
	}
	if a := r.Options.A11y; a == nil || !slices.Contains(a.Rules, "placeholder-label") {
		t.Errorf("rules don't list placeholder-label: %+v", a)
	}
	for _, w := range r.Warnings {
		if strings.Contains(w, "placeholder") {
			t.Errorf("warning %q", w)
		}
	}
}

// More fields than max_elements: the first ones are reported and the
// audit records the total, for compare's cap guard.
func TestPlaceholderLabelCapped(t *testing.T) {
	needBrowser(t)
	var b strings.Builder
	b.WriteString("<form>")
	for n := range 5 {
		fmt.Fprintf(&b, `<input name="f%d" placeholder="Field %d">`, n, n)
	}
	b.WriteString("<button>Send</button></form>")
	srv := pages(t, map[string]string{"/": fmt.Sprintf(form, b.String())})
	r := audited(t, srv, "2.1", "  max_elements: 2\n", "/")
	p := r.Page("/")
	if got := placeholderOn(p); len(got) != 2 || !strings.Contains(got[0].Detail, "The first 2 of 5 fields") {
		t.Errorf("issues = %+v", got)
	}
	if n := p.Audits["a11y"].CappedRules["placeholder-label"]; n != 5 {
		t.Errorf("capped rules = %v", p.Audits["a11y"].CappedRules)
	}
}

// More fields than the check looks at per document: the ones past
// maxFields still count toward the total the audit records.
func TestPlaceholderLabelCountsPastTheLimit(t *testing.T) {
	needBrowser(t)
	var b strings.Builder
	b.WriteString("<form>")
	const total = 210 // the check looks at 201 per document
	for n := range total {
		fmt.Fprintf(&b, `<input name="f%d" placeholder="Field %d">`, n, n)
	}
	// Fields named past the limit don't count.
	for n := range 3 {
		fmt.Fprintf(&b, `<input name="l%d" placeholder="Named %d" aria-label="Named %d">`, n, n, n)
	}
	b.WriteString("<button>Send</button></form>")
	srv := pages(t, map[string]string{"/": fmt.Sprintf(form, b.String())})
	r := audited(t, srv, "2.1", "  max_elements: 2\n", "/")
	p := r.Page("/")
	want := fmt.Sprintf("The first 2 of %d fields", total)
	if got := placeholderOn(p); len(got) != 2 || !strings.Contains(got[0].Detail, want) {
		t.Errorf("issues = %+v, want %q", got, want)
	}
	if n := p.Audits["a11y"].CappedRules["placeholder-label"]; n != total {
		t.Errorf("capped rules = %v, want %d", p.Audits["a11y"].CappedRules, total)
	}
}

// A placeholder can hold a customer's email address or phone number:
// the Detail masks them like any other text Soundcheck keeps.
func TestPlaceholderLabelMasksPersonalData(t *testing.T) {
	needBrowser(t)
	canary := "ph" + "canary7731"
	srv := pages(t, map[string]string{"/": fmt.Sprintf(form, `<form><input id="pl" placeholder="`+canary+`@example.org">
<input name="tel" placeholder="Call +49 30 1234567"><button>Send</button></form>`)})
	r := audited(t, srv, "2.1", "", "/")
	got := placeholderOn(r.Page("/"))
	if len(got) != 2 {
		t.Fatalf("issues = %+v", got)
	}
	for _, i := range got {
		if strings.Contains(i.Detail, canary) || strings.Contains(i.Detail, "example.org") || strings.Contains(i.Detail, "1234567") {
			t.Errorf("Detail keeps personal data: %q", i.Detail)
		}
		if !strings.Contains(i.Detail, "***") {
			t.Errorf("Detail not masked: %q", i.Detail)
		}
	}
}

func TestShortTextMasks(t *testing.T) {
	for _, c := range []struct{ in, out string }{
		{"Email address", "Email address"},
		{"  jane.doe@example.org  ", "***@***"},
		{"Call +49 30 1234567 now", "Call *** now"},
		{"Card 4111 1111 1111 1111", "Card ***"},
		{"IBAN DE89 3704 0044 0532 0130 00", "IBAN ***"},
	} {
		if got := a11y.ShortText(c.in, 60); got != c.out {
			t.Errorf("ShortText(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}
