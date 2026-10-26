// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y_test

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/checks/a11y"
	"github.com/RhythmShahriar/soundcheck/model"
)

// big is a page of n paragraphs of pale text, each a color-contrast
// violation: enough work for axe to take seconds.
func big(n int) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Catalog</title></head><body><main><h1>Catalog</h1>`)
	for i := range n {
		fmt.Fprintf(&b, `<p style="color:#ccc;background:#fff">Item %d</p>`, i)
	}
	b.WriteString(`</main></body></html>`)
	return b.String()
}

// A page whose audit runs out of time isn't a clean page. It has no
// issues; its audit says it timed out and a warning says the results
// are missing.
func TestAuditTimeout(t *testing.T) {
	needBrowser(t)
	defer a11y.SetTimeout(time.Second)()
	srv := pages(t, map[string]string{"/catalog/": big(30000)})
	r := audited(t, srv, "2.1", "", "/catalog/")
	p := r.Page("/catalog/")
	want := model.Audit{Status: model.AuditTimeout, Reason: "axe-core timed out after 1s"}
	if !reflect.DeepEqual(p.Audits["a11y"], want) || len(p.Issues) > 0 {
		t.Errorf("audit %+v, %d issues", p.Audits["a11y"], len(p.Issues))
	}
	w := "a11y: /catalog/: axe-core: timed out after 1s; the page's a11y results are missing; the statement marks it"
	if !slices.Contains(r.Warnings, w) {
		t.Errorf("warnings = %q", r.Warnings)
	}
}

// A page with 20,000 failing elements is trimmed in the browser: the
// run keeps max_elements of them, records the rule as capped and
// Soundcheck's memory doesn't grow with the page.
func TestAuditBigPageMemory(t *testing.T) {
	needBrowser(t)
	srv := pages(t, map[string]string{"/catalog/": big(20000)})
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	r := audited(t, srv, "2.1", "", "/catalog/")
	runtime.ReadMemStats(&after)
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 50<<20 {
		t.Errorf("the heap grew by %d MB", grew>>20)
	}
	p := r.Page("/catalog/")
	a := p.Audits["a11y"]
	switch a.Status {
	case model.AuditTimeout:
		t.Logf("the audit timed out: %+v", a)
		return
	case model.AuditOK:
	default:
		t.Fatalf("audit = %+v", a)
	}
	if a.CappedRules["color-contrast"] != 20000 {
		t.Errorf("capped rules = %v", a.CappedRules)
	}
	n := 0
	for _, i := range p.Issues {
		if i.Check == "a11y/color-contrast" {
			n++
			if !strings.HasSuffix(i.Detail, "The first 20 of 20000 elements that fail this rule on the page are reported.") {
				t.Errorf("detail = %q", i.Detail)
			}
		}
	}
	if n != 20 {
		t.Errorf("%d color-contrast issues, want 20", n)
	}
}

// A 404 or 500 page is the server's template: it isn't audited and
// says so.
func TestAuditSkipsErrorPages(t *testing.T) {
	needBrowser(t)
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, page(true))
	}))
	r := audited(t, srv, "2.1", "", "/")
	p := r.Page("/")
	if want := (model.Audit{Status: model.AuditSkipped, Reason: "status 500"}); !reflect.DeepEqual(p.Audits["a11y"], want) || len(p.Issues) > 0 {
		t.Errorf("audit %+v, issues %+v", p.Audits["a11y"], p.Issues)
	}
	if len(r.Warnings) > 0 {
		t.Errorf("warnings = %q", r.Warnings)
	}
}

// framed is a page with a same-origin frame (/inner/) and one from
// another origin, both with a field without a label. The other origin's
// field carries canary text that must not reach the run.
func framed(t *testing.T) (other string, html map[string]string) {
	t.Helper()
	canary := "canary-" + "frame-" + "7f3a"
	o := pages(t, map[string]string{"/widget/": `<!doctype html><html lang="en"><head><title>Widget</title></head><body><main>` +
		`<input type="text" name="` + canary + `" value="` + canary + `"></main></body></html>`})
	u, _ := url.Parse(o.URL)
	html = map[string]string{
		"/": `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Shop</title></head><body><main><h1>Shop</h1>` +
			`<iframe id="inner" title="Newsletter" src="/inner/"></iframe>` +
			`<iframe id="map" title="Map" src="` + o.URL + `/widget/"></iframe></main></body></html>`,
		"/inner/": `<!doctype html><html lang="en"><head><title>Newsletter</title></head><body><main>` +
			`<form id="news"><input type="email" name="email"></form></main></body></html>`,
	}
	return u.Host, html
}

func TestAuditFrames(t *testing.T) {
	needBrowser(t)
	other, html := framed(t)
	srv := pages(t, html)
	own, _ := url.Parse(srv.URL)
	canary := "canary-" + "frame-" + "7f3a"

	t.Run("iframes off", func(t *testing.T) {
		r := audited(t, srv, "2.1", "", "/")
		p := r.Page("/")
		a := p.Audits["a11y"]
		want := []string{own.Host, other}
		slices.Sort(want)
		if a.Status != model.AuditOK || !slices.Equal(a.Frames, want) {
			t.Errorf("audit = %+v, want frames %v", a, want)
		}
		if len(p.Issues) > 0 {
			t.Errorf("issues from frames that weren't audited: %+v", p.Issues)
		}
	})
	t.Run("iframes on", func(t *testing.T) {
		r := audited(t, srv, "2.1", "  iframes: true\n", "/")
		p := r.Page("/")
		a := p.Audits["a11y"]
		if a.Status != model.AuditOK || !slices.Equal(a.Frames, []string{other}) {
			t.Errorf("audit = %+v, want frames [%s]", a, other)
		}
		var keys []string
		for _, i := range p.Issues {
			keys = append(keys, i.Key)
			if strings.Contains(fmt.Sprintf("%+v", i), canary) {
				t.Errorf("the other origin's frame reached the issue: %+v", i)
			}
		}
		if want := []string{`a11y/label|#inner >>> input[name="email"]`}; !slices.Equal(keys, want) {
			t.Errorf("keys = %v, want %v", keys, want)
		}
	})
}
