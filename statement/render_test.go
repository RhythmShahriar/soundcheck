// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"bytes"
	"flag"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/net/html"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// golden compares got with testdata/name, or writes it with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs (run with -update to rewrite):\n%s", path, got)
	}
}

// fullOwner is an owner who filled in every key.
func fullOwner() Owner {
	o := owner()
	o.Contact.Phone = "+49 30 1234567"
	o.Contact.Address = "Example Str. 1, Berlin"
	o.ResponseDays = 10
	o.Country = "DE"
	o.Enforcement = "Marktüberwachungsstelle der Länder für die Barrierefreiheit von Produkten und Dienstleistungen (MLBF)\n"
	o.Scope = []string{"/", "/shop/*", "/checkout/*"}
	o.Service = "Online shop for coffee and equipment"
	return o
}

// fixtures are the drafts the goldens cover.
//
//   - clean: nothing found, the owner gave only the required keys.
//   - findings: a problem of every kind (axe, keyboard, reflow, a
//     label found twice), known issues, an ignored problem, manual
//     results, a skipped key flow and a -k run.
//   - gaps: pages not audited (a 500, a timeout, a frame from another
//     host) in WCAG 2.2.
//   - hostile: page paths, owner text and reasons written to break out
//     of Markdown, HTML and tables, an RTL override and CJK.
func fixtures(t *testing.T) map[string]Input {
	t.Helper()
	out := map[string]Input{}

	out["clean"] = input([]model.Page{
		{Path: "/", Audits: audited(), Keyboard: &model.KeyboardSummary{Stops: 12}},
		{Path: "/shop/", Audits: audited(), Keyboard: &model.KeyboardSummary{Stops: 30, Widgets: 1}},
	}, owner())

	findings := input([]model.Page{
		{Path: "/", Audits: audited(), Keyboard: &model.KeyboardSummary{Stops: 40, Widgets: 2},
			Issues: []model.Issue{issue("a11y/color-contrast", "footer a", "1.4.3"), issue("keyboard/focus-lost", "#newsletter"),
				issue("a11y/reflow", "body")}},
		{Path: "/shop/", Audits: audited(), Keyboard: &model.KeyboardSummary{Stops: 22},
			Issues: []model.Issue{issue("a11y/color-contrast", "footer a", "1.4.3"), issue("keyboard/focus-lost", "#newsletter")}},
		{Path: "/legal/", Audits: audited(), Keyboard: &model.KeyboardSummary{Stops: 9},
			Issues: []model.Issue{issue("a11y/color-contrast", "footer a", "1.4.3")}},
		{Path: "/about/", Audits: audited(), Keyboard: &model.KeyboardSummary{Stops: 9},
			Issues: []model.Issue{issue("a11y/color-contrast", "footer a", "1.4.3")}},
		{Path: "/checkout/", Audits: audited(), Keyboard: &model.KeyboardSummary{Stops: 5},
			Issues: []model.Issue{issue("keyboard/trap", "#card"), issue("a11y/label", "#billing_phone", "4.1.2"),
				{Check: "interact/unlabeled", Title: "Field without a label", Detail: "#billing_phone", WCAG: []string{"3.3.2", "4.1.2"}},
				issue("a11y/autocomplete-valid", "#billing_email", "1.3.5")}},
	}, fullOwner())
	findings.Run.KeyFlows = &model.KeyFlows{Run: 1, Skipped: []model.SkippedFlow{{File: "keyboard-contact.yaml", Reason: "needs --level flows"}}}
	findings.Summary = summarize(findings.Run)
	findings.Owner.KnownIssues = []KnownIssue{{Criterion: "1.2.2", Text: "Product videos have no captions yet.", Until: "2027-03-31"}}
	findings.Owner.Manual = []ManualCheck{
		{Criterion: "1.4.11", Result: ManualFailures, By: "Jane Doe", Date: "2026-10-20"},
		{Criterion: "2.4.3", Result: ManualNoFailures, By: "Jane Doe", Date: "2026-10-21"},
	}
	findings.Ignored = []model.Change{{Check: "a11y/color-contrast", WCAG: []string{"1.4.3"}, Pages: []string{"/legal/"}, Reason: "new theme in December"}}
	findings.Insecure = true
	out["findings"] = findings

	gaps := input([]model.Page{
		{Path: "/", Audits: audited(), Keyboard: &model.KeyboardSummary{Stops: 10}},
		{Path: "/broken/", Status: 500, Audits: map[string]model.Audit{
			"a11y": {Status: model.AuditError, Reason: "the page answered 500"}, "keyboard": {Status: model.AuditSkipped, Reason: "the page answered 500"}}},
		{Path: "/catalog/", Audits: map[string]model.Audit{
			"a11y": {Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}, "keyboard": {Status: model.AuditOK}}, Keyboard: &model.KeyboardSummary{Stops: 150, Capped: true}},
		{Path: "/pay/", Audits: map[string]model.Audit{
			"a11y": {Status: model.AuditOK, Frames: []string{"js.stripe.com"}}, "keyboard": {Status: model.AuditOK}}, Keyboard: &model.KeyboardSummary{Stops: 4}},
	}, owner())
	gaps.Run.Options.WCAG = "2.2"
	gaps.Run.Options.Modules = []string{"health", "a11y", "keyboard"}
	gaps.Summary = summarize(gaps.Run)
	out["gaps"] = gaps

	rlo := "\u202e"
	hostile := input([]model.Page{
		{Path: `/"><script>alert(1)</script>`, Audits: audited(), Issues: []model.Issue{issue("a11y/image-alt", "img", "1.1.1")}},
		{Path: "/a|b|c/" + rlo + "gnp.exe", Audits: audited(), Issues: []model.Issue{issue("a11y/image-alt", "img", "1.1.1")}},
		{Path: "/商品/コーヒー/", Audits: audited(), Issues: []model.Issue{issue("a11y/image-alt", "img", "1.1.1")}},
		{Path: "/[x](javascript:alert(1))/**b**/`c`/&#x202E;/www.evil.example/@team", Audits: map[string]model.Audit{
			"a11y": {Status: model.AuditError, Reason: "<img src=x onerror=alert(1)> | broke\nnext line"}}},
	}, Owner{Owner: "<img src=x onerror=alert(1)> Café 咖啡", Website: "https://shop.example", Contact: Contact{Email: "access@shop.example",
		Phone: "+1 <b>555</b>", Address: "| a | b |"}, Enforcement: "# heading\n- list\n" + rlo + "txet",
		KnownIssues: []KnownIssue{{Criterion: "1.2.2", Text: "</li><script>alert(1)</script> [TO DO: mine]"}}})
	hostile.Ignored = []model.Change{{Check: "a11y/image-alt", WCAG: []string{"1.1.1"}, Pages: []string{"/<svg onload=alert(1)>"}, Reason: "]]> -->" + rlo + " <!--"}}
	out["hostile"] = hostile
	return out
}

// summarize sums up run again after a fixture changed it.
func summarize(run *model.Run) model.EAASummary {
	return wcag.Summarize(run, func(yield func(*model.Page) bool) {
		for i := range run.Pages {
			if !yield(&run.Pages[i]) {
				return
			}
		}
	}, nil, nil)
}

// render writes d in every format.
func render(t testing.TB, d *Draft) map[string]string {
	t.Helper()
	out := map[string]string{}
	for ext, write := range map[string]func(*bytes.Buffer, *Draft) error{
		"md":   func(b *bytes.Buffer, d *Draft) error { return Markdown(b, d) },
		"html": func(b *bytes.Buffer, d *Draft) error { return HTML(b, d) },
		"json": func(b *bytes.Buffer, d *Draft) error { return JSON(b, d) },
	} {
		var b bytes.Buffer
		if err := write(&b, d); err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		out[ext] = b.String()
	}
	return out
}

func TestGoldens(t *testing.T) {
	for name, in := range fixtures(t) {
		for _, layout := range Layouts {
			in.Layout = layout
			d, err := Build(in)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			for ext, got := range render(t, d) {
				if strings.Contains(got, "\r") {
					t.Errorf("%s.%s has CR", name, ext)
				}
				golden(t, name+"-"+layout+"."+ext, got)
			}
		}
	}
}

// The [TO DO] count is what the Markdown and HTML hold, the same for
// both formats and both layouts.
func TestTodoCount(t *testing.T) {
	for name, in := range fixtures(t) {
		for _, layout := range Layouts {
			in.Layout = layout
			d, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			out := render(t, d)
			md, page := strings.Count(out["md"], "[TO DO:"), strings.Count(out["html"], "[TO DO:")
			if d.Todo == 0 || md != d.Todo || page != d.Todo {
				t.Errorf("%s %s: Todo %d, Markdown %d, HTML %d", name, layout, d.Todo, md, page)
			}
		}
	}
	// A placeholder the CLI fills in for a missing owner counts.
	in := fixtures(t)["clean"]
	d, _ := Build(in)
	_ = Markdown(&bytes.Buffer{}, d)
	before := d.Todo
	in.Owner.Owner = "[TO DO: who runs the website; set statement.owner]"
	d, _ = Build(in)
	if err := Markdown(&bytes.Buffer{}, d); err != nil || d.Todo != before+2 {
		t.Errorf("placeholder owner: %d markers, want %d", d.Todo, before+2)
	}
}

// The status is never chosen: the only status words are the options of
// the [TO DO] that asks the owner; the counts in it are the tests'.
func TestStatusIsTheOwners(t *testing.T) {
	d, err := Build(fixtures(t)["findings"])
	if err != nil {
		t.Fatal(err)
	}
	out := render(t, d)
	want := `[TO DO: choose one after your review: "fully compliant", "partially compliant" or "not compliant" with WCAG 2.1 level AA / EN 301 549 V3.2.1. Soundcheck does not choose this for you. Its tests found failures of 7 of 50 criteria and could not test 23 of them.]`
	if !strings.Contains(out["md"], want) {
		t.Errorf("Markdown lacks the status [TO DO]:\n%s", want)
	}
	for _, ext := range []string{"md", "html"} {
		if n := strings.Count(strings.ToLower(out[ext]), "complian"); n != 3 {
			t.Errorf("%s says complian %d times, want the 3 options only", ext, n)
		}
	}
	for _, s := range []string{BannerTitle, markdownText(BannerEnd), model.EvidenceNote, "Rhythm Soundcheck 1.3.0 wrote this on 2026-10-26 from a check of 5 pages of https\\://shop.example."} {
		if !strings.Contains(out["md"], s) {
			t.Errorf("banner lacks %q", s)
		}
	}
	for _, l := range Limits {
		if !strings.Contains(out["md"], strings.ReplaceAll(l, "_", "\\_")) || !strings.Contains(out["html"], strings.ReplaceAll(l, "'", "&#39;")) {
			t.Errorf("method section lacks %q", l)
		}
	}
}

// allowedTags are the elements the HTML template writes; anything else
// came from the input.
var allowedTags = map[string]bool{"html": true, "head": true, "meta": true, "title": true, "style": true, "body": true, "main": true,
	"aside": true, "p": true, "h1": true, "h2": true, "h3": true, "ul": true, "li": true, "strong": true, "mark": true, "div": true,
	"table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true}

var allowedAttrs = map[string]bool{"lang": true, "charset": true, "http-equiv": true, "content": true, "name": true, "class": true, "aria-label": true, "scope": true}

// checkSafe fails when hostile input got through: a tag or attribute
// the template doesn't write, a bidirectional or control character, an
// unescaped "<" or "[" in Markdown or a table row with more cells than
// the table.
func checkSafe(t testing.TB, out map[string]string) {
	t.Helper()
	for ext, s := range out {
		if i := strings.IndexFunc(s, func(r rune) bool { return bidi(r) || (unicode.IsControl(r) && r != '\n') }); i >= 0 {
			t.Fatalf("%s holds %U at %d", ext, []rune(s[i:])[0], i)
		}
	}
	md := out["md"]
	for i := 0; i < len(md); i++ {
		if (md[i] == '<' || md[i] == '[') && !escaped(md, i) && !strings.HasPrefix(md[i:], "[TO DO: ") {
			t.Fatalf("unescaped %q in Markdown at %q", md[i], md[max(0, i-20):min(len(md), i+20)])
		}
	}
	for _, l := range strings.Split(md, "\n") {
		if !strings.HasPrefix(l, "| ") {
			continue
		}
		cells := 0
		for i := range len(l) {
			if l[i] == '|' && !escaped(l, i) {
				cells++
			}
		}
		if cells != 6 {
			t.Fatalf("table row with %d pipes: %q", cells, l)
		}
	}
	z := html.NewTokenizer(strings.NewReader(out["html"]))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken && tt != html.EndTagToken {
			continue
		}
		tok := z.Token()
		if !allowedTags[tok.Data] {
			t.Fatalf("HTML has a <%s> element", tok.Data)
		}
		for _, a := range tok.Attr {
			if !allowedAttrs[a.Key] {
				t.Fatalf("HTML <%s> has a %s attribute", tok.Data, a.Key)
			}
		}
	}
	if strings.Count(out["html"], "<html") != 1 || strings.Count(out["html"], "<script") != 0 {
		t.Fatal("HTML isn't one page without scripts")
	}
}

// escaped reports whether the byte at i follows an odd number of
// backslashes.
func escaped(s string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

func TestHostileInput(t *testing.T) {
	for _, layout := range Layouts {
		in := fixtures(t)["hostile"]
		in.Layout = layout
		d, err := Build(in)
		if err != nil {
			t.Fatal(err)
		}
		checkSafe(t, render(t, d))
	}
}

// The checker itself must catch what it looks for.
func TestCheckSafeCatches(t *testing.T) {
	clean := render(t, mustBuild(t, fixtures(t)["clean"]))
	for name, change := range map[string]func(map[string]string){
		"tag": func(o map[string]string) { o["html"] = strings.Replace(o["html"], "<main>", "<main><img src=x>", 1) },
		"event": func(o map[string]string) {
			o["html"] = strings.Replace(o["html"], "<main>", `<main class="a" onclick="x">`, 1)
		},
		"bidi": func(o map[string]string) { o["json"] += "\u202e" },
		"md":   func(o map[string]string) { o["md"] += "<b>" },
		"link": func(o map[string]string) { o["md"] += "[a](b)" },
		"row":  func(o map[string]string) { o["md"] += "\n| a | b | c | d | e | f |\n" },
	} {
		out := maps.Clone(clean)
		change(out)
		ft := &fakeT{}
		func() {
			defer func() { _ = recover() }()
			checkSafe(ft, out)
		}()
		if !ft.failed {
			t.Errorf("%s: not caught", name)
		}
	}
}

// fakeT records a failure instead of failing the test.
type fakeT struct {
	testing.TB
	failed bool
}

func (f *fakeT) Helper() {}
func (f *fakeT) Fatalf(string, ...any) {
	f.failed = true
	panic("fatal")
}
func (f *fakeT) Fatal(...any) {
	f.failed = true
	panic("fatal")
}

func mustBuild(t testing.TB, in Input) *Draft {
	t.Helper()
	d, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// FuzzStatement: hostile strings in a page path, the owner's text and a
// reason never produce an unescaped tag, a broken table row or a raw
// bidirectional override in any format.
func FuzzStatement(f *testing.F) {
	for _, s := range []string{`"><script>alert(1)</script>`, "<img src=x onerror=alert(1)>", "a|b", "\u202egnp.exe", "商品", "[x](javascript:1)",
		"&#x202E;", "- item", "1. one", "\\|", "\\", "`code`", "www.example.com", "http://x.test/?q=1", "\x1b[31m", "\xff\xfe", "[TO DO: x]"} {
		f.Add(s, s, s)
	}
	f.Fuzz(func(t *testing.T, path, text, reason string) {
		pages := []model.Page{{Path: "/" + path, Audits: audited(), Issues: []model.Issue{issue("a11y/image-alt", text, "1.1.1")}},
			{Path: path, Audits: map[string]model.Audit{"a11y": {Status: model.AuditTimeout, Reason: reason}}}}
		o := owner()
		o.Owner, o.Contact.Phone, o.Contact.Address, o.Enforcement, o.Service = text, text, reason, text, reason
		o.Scope = []string{path}
		o.KnownIssues = []KnownIssue{{Criterion: "1.2.2", Text: text, Until: reason}}
		o.Manual = []ManualCheck{{Criterion: "1.4.11", Result: reason, By: text, Date: path}}
		in := input(pages, o)
		in.Ignored = []model.Change{{Check: "a11y/image-alt", WCAG: []string{"1.1.1"}, Pages: []string{path}, Reason: reason}}
		in.Run.KeyFlows = &model.KeyFlows{Skipped: []model.SkippedFlow{{File: text, Reason: reason}}}
		in.Summary = summarize(in.Run)
		for _, layout := range Layouts {
			in.Layout = layout
			d, err := Build(in)
			if err != nil {
				return // an owner of only spaces, say
			}
			checkSafe(t, render(t, d))
		}
	})
}
