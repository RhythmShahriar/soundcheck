// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"

	"github.com/RhythmShahriar/soundcheck/model"
)

// axeSHA256 is the SHA-256 of axe.min.js from the axe-core 4.14.0 npm
// package. axe-core publishes the same hash in its sri-history.json
// (sha256-IMCf4Veoo0ow4kGqofza3mV3NPCKs3ns++t9RcxG6Hg=). Updating axe
// means changing axeVersion, this hash and the vendored files together.
const axeSHA256 = "20c09fe157a8a34a30e241aaa1fcdade657734f08ab379ecfbeb7d45cc46e878"

func TestVendoredAxe(t *testing.T) {
	sum := sha256.Sum256([]byte(axeSource))
	if got := hex.EncodeToString(sum[:]); got != axeSHA256 {
		t.Errorf("vendor/axe.min.js SHA-256 = %s, want %s", got, axeSHA256)
	}
	if !strings.HasPrefix(axeSource, "/*! axe v"+axeVersion+"\n") {
		t.Errorf("vendor/axe.min.js isn't axe %s: %.40q", axeVersion, axeSource)
	}
	lic, err := os.ReadFile("vendor/LICENSE")
	if err != nil || !strings.Contains(string(lic), "Mozilla Public License, version 2.0") {
		t.Errorf("vendor/LICENSE must hold axe-core's MPL-2.0 license (err %v)", err)
	}
	if _, err := os.Stat("vendor/LICENSE-3RD-PARTY.txt"); err != nil {
		t.Errorf("vendor/LICENSE-3RD-PARTY.txt: %v", err)
	}
}

// The MPL-2.0 asks that people who get the binary can find axe-core's
// license and source: the release archives carry the license and the
// README names the source and the version embedded.
func TestAxeShipsItsLicense(t *testing.T) {
	rel, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rel), "src: checks/a11y/vendor/LICENSE*") || !strings.Contains(string(rel), "dst: THIRD_PARTY/axe-core") {
		t.Error(".goreleaser.yaml doesn't put checks/a11y/vendor/LICENSE* in the archives")
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"### Third-party", "axe-core](https://github.com/dequelabs/axe-core) " + axeVersion, "Mozilla Public License 2.0"} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README.md has no %q", want)
		}
	}
}

// el parses "tag#id[name]:n" shorthand into an Elem.
func el(s string) Elem {
	var e Elem
	if i := strings.LastIndex(s, ":"); i > 0 {
		e.Nth, _ = strconv.Atoi(s[i+1:])
		s = s[:i]
	}
	if i := strings.Index(s, "["); i >= 0 {
		e.Name = strings.TrimSuffix(s[i+1:], "]")
		s = s[:i]
	}
	e.Tag, e.ID, _ = strings.Cut(s, "#")
	return e
}

func tree(steps ...string) []Elem {
	var out []Elem
	for _, s := range steps {
		out = append(out, el(s))
	}
	return out
}

func TestSelector(t *testing.T) {
	for _, c := range []struct {
		path [][]Elem
		want string
	}{
		{[][]Elem{tree("html", "body", "main", "input#email")}, "#email"},
		// No id or name: the path below <body>, with positions only where
		// the parent has siblings of the same tag.
		{[][]Elem{tree("html", "body", "main", "form", "p:2", "button")}, "main > form > p:nth-of-type(2) > button"},
		// The last id or name anchors the selector; what's above it doesn't count.
		{[][]Elem{tree("html", "body", "div:3", "form#signup", "p:2", "button")}, "#signup > p:nth-of-type(2) > button"},
		{[][]Elem{tree("html", "body", "footer", "form:4", "input[email]:2")}, `input[name="email"]`},
		{[][]Elem{tree("html", "body", "nav#menu", "ul", "li:5", "a")}, "#menu > ul > li:nth-of-type(5) > a"},
		{[][]Elem{tree("html", "body", "nav#menu", "div#search", "input[q]")}, `input[name="q"]`},
		// Generated ids change between runs, so they don't anchor or show.
		{[][]Elem{tree("html", "body", "article#post-12345", "a:2")}, "article > a:nth-of-type(2)"},
		{[][]Elem{tree("html", "body", "div#el-4f9a2b7c1d", "button")}, "div > button"},
		{[][]Elem{tree("html", "body", "div#:r1:", "button")}, "div > button"},
		// An id that isn't a plain CSS name is quoted.
		{[][]Elem{tree("html", "body", "div#2col", "a")}, `div[id="2col"] > a`},
		{[][]Elem{{{Tag: "input", Name: `a"b`}}}, `input[name="a\"b"]`},
		// Shadow DOM: the host's path, then the path inside its root.
		{[][]Elem{tree("html", "body", "div:2", "my-widget#cart"), tree("div", "button:2")}, "#cart >>> div > button:nth-of-type(2)"},
		{nil, ""},
	} {
		if got := Selector(c.path); got != c.want {
			t.Errorf("Selector(%+v) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestIssues(t *testing.T) {
	found := []result{
		{ID: "label", Impact: "critical", Help: "Form elements must have labels",
			HelpURL: "https://dequeuniversity.com/rules/axe/4.14/label?application=axeAPI",
			Nodes: []node{
				{Path: [][]Elem{tree("html", "body", "main", "input#email")}, HTML: "<input id=\"email\">", Summary: "Fix any of the following:\n  Element does not have an implicit (wrapped) <label>\n  Element has no title attribute", Impact: "critical"},
				{Path: [][]Elem{tree("html", "body", "main", "input#email")}}, // the same element again
			}},
		{ID: "color-contrast", Impact: "serious", Help: "Elements must meet minimum color contrast ratio thresholds",
			Nodes: []node{{Path: [][]Elem{tree("html", "body", "button")}, Impact: "moderate"}}},
		{ID: "region", Impact: "minor", Help: "All page content should be contained by landmarks",
			Nodes: []node{{Path: [][]Elem{tree("html", "body", "aside#promo")}}}},
	}
	got := issues(found, 20, "2.1")
	if len(got) != 3 {
		t.Fatalf("issues = %+v", got)
	}
	want := model.Issue{Check: "a11y/label", Level: model.Warning, Title: "Form elements must have labels", Source: "#email",
		Detail: "<input id=\"email\">\nFix any of the following:\nElement does not have an implicit (wrapped) <label>\nElement has no title attribute",
		Fix:    "See https://dequeuniversity.com/rules/axe/4.14/label for what the rule asks and how to fix it.",
		Key:    "a11y/label|#email", Impact: "critical"}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("label issue =\n%+v\nwant\n%+v", got[0], want)
	}
	// The element's own impact wins over the rule's.
	if got[1].Level != model.Notice || got[1].Fix != "" {
		t.Errorf("contrast issue = %+v", got[1])
	}
	if got[2].Level != model.Notice || got[2].Key != "a11y/region|#promo" {
		t.Errorf("region issue = %+v", got[2])
	}
}

// A change knows only its check, title and source: the confirm pass and
// accept find the issues it comes from by the key those give. An a11y
// issue's own key must be that one, or a violation the second visit saw
// again would look gone and a real one would be UNSTABLE.
func TestIssueKeyIsTheChangeKey(t *testing.T) {
	found := []result{{ID: "label", Impact: "critical", Help: "Form elements must have labels",
		Nodes: []node{{Path: [][]Elem{tree("html", "body", "form", "input#email")}}}}}
	for _, i := range issues(found, 20, "2.1") {
		if want := model.IssueKey(i.Check, i.Title, i.Source); i.Key != want {
			t.Errorf("key %q, a change of it looks for %q", i.Key, want)
		}
		// axe rewording the rule's help doesn't make it a new violation.
		reworded := i
		reworded.Title = "Form fields need labels"
		if reworded.Identify(); reworded.Key != i.Key {
			t.Errorf("a reworded title changed the key: %q, was %q", reworded.Key, i.Key)
		}
	}
}

// A field prefilled after login must not put the visitor's details into
// run.json; empty values and other attributes stay as they are.
func TestSnippetMasksFilledValues(t *testing.T) {
	for in, want := range map[string]string{
		`<input id="email" value="ana@example.com" data-user-id='4471'>`: `<input id="email" value="***" data-user-id="***">`,
		`<input name=acct VALUE=12345678 type="text">`:                   `<input name=acct VALUE="***" type="text">`,
		`<input id="q" value="" placeholder="Search">`:                   `<input id="q" value="" placeholder="Search">`,
		`<button data-x = "a b">Go</button>`:                             `<button data-x = "***">Go</button>`,
		`<li aria-valuenow="5">`:                                         `<li aria-valuenow="5">`,
	} {
		if got := snippet(in); got != want {
			t.Errorf("snippet(%s) = %s, want %s", in, got, want)
		}
	}
}

// A link's query (a reset token and email) and a token in its path never
// reach run.json; the path the link goes to stays readable.
func TestSnippetMasksLinks(t *testing.T) {
	for in, want := range map[string]string{
		`<a href="/reset?token=9f8e7d6c5b4a&email=ana@example.com"></a>`: `<a href="/reset?***"></a>`,
		`<a href='/password/reset/Zx81kQpL0aTnB4vRu7cWmE2s'></a>`:        `<a href="/password/reset/***"></a>`,
		`<form action=/subscribe?key=abc123>`:                            `<form action="/subscribe?***">`,
		`<img src="/logo.png">`:                                          `<img src="/logo.png">`,
		`<a href="/shop/">`:                                              `<a href="/shop/">`,
	} {
		if got := snippet(in); got != want {
			t.Errorf("snippet(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestIssuesCapsElements(t *testing.T) {
	r := result{ID: "link-name", Impact: "serious", Help: "Links must have discernible text"}
	for i := range 50 {
		r.Nodes = append(r.Nodes, node{Path: [][]Elem{tree("html", "body", "ul", "li:"+strconv.Itoa(i+1), "a")}})
	}
	got := issues([]result{r}, 20, "2.1")
	if len(got) != 20 {
		t.Fatalf("%d issues, want 20", len(got))
	}
	if !strings.HasSuffix(got[0].Detail, "The first 20 of 50 elements that fail this rule on the page are reported.") {
		t.Errorf("detail = %q", got[0].Detail)
	}
}

func TestLevel(t *testing.T) {
	for impact, want := range map[string]model.Level{
		"critical": model.Warning, "serious": model.Warning, "moderate": model.Notice, "minor": model.Notice, "": model.Notice,
	} {
		if got := level(impact); got != want {
			t.Errorf("level(%q) = %s, want %s", impact, got, want)
		}
	}
}

func TestCriteria(t *testing.T) {
	for _, c := range []struct {
		name     string
		tags     []string
		version  string
		wcag, en []string
	}{
		{name: "two-digit criterion", tags: []string{"cat.text-spacing", "wcag21aa", "wcag1412", "EN-301-549", "EN-9.1.4.12"}, version: "2.1",
			wcag: []string{"1.4.12"}, en: []string{"9.1.4.12"}},
		{name: "one-digit criterion", tags: []string{"cat.forms", "wcag21aa", "wcag135", "EN-9.1.3.5"}, version: "2.1",
			wcag: []string{"1.3.5"}, en: []string{"9.1.3.5"}},
		{name: "sorted by number, no repeats", tags: []string{"wcag412", "wcag131", "wcag1412", "wcag143", "wcag131", "EN-9.4.1.2", "EN-9.1.3.1"}, version: "2.2",
			wcag: []string{"1.3.1", "1.4.3", "1.4.12", "4.1.2"}, en: []string{"9.1.3.1", "9.4.1.2"}},
		{name: "2.2 criterion under 2.2", tags: []string{"wcag22aa", "wcag2411", "wcag258"}, version: "2.2",
			wcag: []string{"2.4.11", "2.5.8"}},
		{name: "2.2 criterion under 2.1", tags: []string{"wcag22aa", "wcag2411", "wcag258", "wcag111"}, version: "2.1",
			wcag: []string{"1.1.1"}},
		{name: "2.2 criterion under 2.0", tags: []string{"wcag258", "EN-9.2.5.8"}, version: "2.0"},
		{name: "unrelated tags", tags: []string{"cat.forms", "wcag2a", "wcag21aa", "best-practice", "ACT", "TTv5", "section508", "EN-301-549", "wcag", "wcag1x2"}, version: "2.1"},
		{name: "no tags", version: "2.1"},
	} {
		wcag, en := criteria(c.tags, c.version)
		if !slices.Equal(wcag, c.wcag) || !slices.Equal(en, c.en) {
			t.Errorf("%s: criteria(%v, %s) = %v, %v; want %v, %v", c.name, c.tags, c.version, wcag, en, c.wcag, c.en)
		}
	}
}

// Every issue of a rule names its criteria and clauses. It has axe's
// impact of the element.
func TestIssuesCarryCriteria(t *testing.T) {
	found := []result{{ID: "label", Impact: "critical", Help: "Form elements must have labels",
		Tags: []string{"cat.forms", "wcag2a", "wcag412", "section508", "EN-301-549", "EN-9.4.1.2", "wcag131", "EN-9.1.3.1"},
		Nodes: []node{
			{Path: [][]Elem{tree("html", "body", "input#a")}, Impact: "serious"},
			{Path: [][]Elem{tree("html", "body", "input#b")}},
		}}}
	got := issues(found, 20, "2.1")
	if len(got) != 2 {
		t.Fatalf("issues = %+v", got)
	}
	for n, i := range got {
		if !slices.Equal(i.WCAG, []string{"1.3.1", "4.1.2"}) || !slices.Equal(i.EN301549, []string{"9.1.3.1", "9.4.1.2"}) {
			t.Errorf("issue %d: wcag %v, en %v", n, i.WCAG, i.EN301549)
		}
	}
	if got[0].Impact != "serious" || got[1].Impact != "critical" {
		t.Errorf("impacts %q and %q, want the element's then the rule's", got[0].Impact, got[1].Impact)
	}
	// Two issues don't share their lists: a later change of one stays
	// its own.
	got[0].WCAG[0] = "x"
	if got[1].WCAG[0] != "1.3.1" {
		t.Error("issues share their WCAG list")
	}
}

func TestParse(t *testing.T) {
	doc, err := parse(`{"violations":[{"id":"label","impact":"critical","tags":["wcag412"],"total":3,"nodes":[{"path":[[{"tag":"input","id":"a"}]]}]}],"frames":[{"host":"maps.example","same":false}],"rules":["label"]}`)
	if err != nil || len(doc.Violations) != 1 || doc.Violations[0].Total != 3 || doc.Frames[0] != (frameRef{Host: "maps.example"}) ||
		!slices.Equal(doc.Rules, []string{"label"}) {
		t.Errorf("parse = %+v, %v", doc, err)
	}
	// runJS trims what it sends; a result past 2 MB isn't axe's.
	huge := `{"violations":[{"id":"x","help":"` + strings.Repeat("a", maxResult) + `"}]}`
	if _, err := parse(huge); err == nil || err.Error() != "axe-core result too large" {
		t.Errorf("parse(%d bytes) = %v", len(huge), err)
	}
	if _, err := parse(`{"violations":`); err == nil || !strings.HasPrefix(err.Error(), "axe-core's result: ") {
		t.Errorf("parse(truncated) = %v", err)
	}
	var many strings.Builder
	many.WriteString(`{"violations":[`)
	for i := range maxRules + 5 {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`{"id":"r` + strconv.Itoa(i) + `"}`)
	}
	many.WriteString(`]}`)
	if doc, err := parse(many.String()); err != nil || len(doc.Violations) != maxRules {
		t.Errorf("parse(%d rules) = %d rules, %v", maxRules+5, len(doc.Violations), err)
	}
}

// A failed audit says why in at most 200 characters, with credentials
// the kit knows masked; only the audit's own time running out is a
// timeout.
func TestFailed(t *testing.T) {
	if a := failed(context.Background(), errTimedOut); !reflect.DeepEqual(a, model.Audit{Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}) {
		t.Errorf("timeout = %+v", a)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if a := failed(canceled, errTimedOut); a.Status != model.AuditError {
		t.Errorf("a stopped run's audit = %+v", a)
	}
	token := "ghp_" + strings.Repeat("A1b2C3d4", 5)
	a := failed(context.Background(), errors.New("TypeError: cannot read "+token+" "+strings.Repeat("x", 300)))
	if a.Status != model.AuditError || strings.Contains(a.Reason, token) || len([]rune(a.Reason)) != maxReason ||
		!strings.HasPrefix(a.Reason, "TypeError: cannot read ") {
		t.Errorf("error = %+v", a)
	}
}

func TestMerge(t *testing.T) {
	page := []result{{ID: "label", Total: 1, Nodes: []node{{HTML: "a"}}}}
	frame := []result{{ID: "label", Total: 2, Nodes: []node{{HTML: "b"}, {HTML: "c"}}}, {ID: "image-alt", Total: 1, Nodes: []node{{HTML: "d"}}}}
	got := merge(page, frame)
	if len(got) != 2 || got[0].Total != 3 || len(got[0].Nodes) != 3 || got[0].Nodes[0].HTML != "a" || got[1].ID != "image-alt" {
		t.Errorf("merge = %+v", got)
	}
}

// A field axe already reported as a11y/label on the page isn't reported
// twice; the issues are capped at keep.
func TestPlaceholderIssues(t *testing.T) {
	f := func(name, ph string) field {
		return field{Path: [][]Elem{{{Tag: "html"}, {Tag: "body"}, {Tag: "input", Name: name}}}, Placeholder: ph}
	}
	label := model.Issue{Check: "a11y/label", Source: `input[name="email"]`}
	got, capped := placeholderIssues([]field{f("email", "Email"), f("city", "City"), f("city", "City")}, []model.Issue{label}, 20)
	if len(got) != 1 || got[0].Source != `input[name="city"]` || capped != 0 {
		t.Errorf("issues = %+v, capped %d", got, capped)
	}
	got, capped = placeholderIssues([]field{f("a", "A"), f("b", "B"), f("c", "C")}, nil, 2)
	if len(got) != 2 || capped != 3 || !strings.HasSuffix(got[1].Detail, "The first 2 of 3 fields named only by their placeholder on the page are reported.") {
		t.Errorf("capped: %+v, %d", got, capped)
	}
	if got, _ := placeholderIssues(nil, nil, 20); len(got) != 0 {
		t.Errorf("no fields: %+v", got)
	}
}

// The placeholder in a Detail is on one line and at most 60 runes; a
// credential in it is redacted.
func TestPlaceholderDetail(t *testing.T) {
	key := "sk_" + "live_" + strings.Repeat("4eC39HqLyjWDarjtT1zdp7dc", 1)
	long := strings.Repeat("é", 80)
	got, _ := placeholderIssues([]field{
		{Path: [][]Elem{{{Tag: "input", Name: "k"}}}, Placeholder: "e.g.\n" + key},
		{Path: [][]Elem{{{Tag: "input", Name: "l"}}}, Placeholder: long},
	}, nil, 20)
	if len(got) != 2 {
		t.Fatal(got)
	}
	if strings.Contains(got[0].Detail, key) || strings.Contains(got[0].Detail, "\n") {
		t.Errorf("detail = %q", got[0].Detail)
	}
	if !strings.Contains(got[1].Detail, `"`+strings.Repeat("é", 59)+`…"`) {
		t.Errorf("detail = %q", got[1].Detail)
	}
}

// Every name source but the placeholder names a field.
func TestNamedBy(t *testing.T) {
	src := func(typ proto.AccessibilityAXValueSourceType, attr, v string) *proto.AccessibilityAXValueSource {
		s := &proto.AccessibilityAXValueSource{Type: typ, Attribute: attr}
		if v != "-" {
			s.Value = &proto.AccessibilityAXValue{Value: gson.New(v)}
		}
		return s
	}
	cases := []struct {
		sources []*proto.AccessibilityAXValueSource
		want    bool
	}{
		{[]*proto.AccessibilityAXValueSource{src(proto.AccessibilityAXValueSourceTypePlaceholder, "placeholder", "Email")}, false},
		{[]*proto.AccessibilityAXValueSource{src(proto.AccessibilityAXValueSourceTypeAttribute, "aria-label", "  "),
			src(proto.AccessibilityAXValueSourceTypeAttribute, "aria-placeholder", "Email"), src(proto.AccessibilityAXValueSourceTypeAttribute, "title", "-")}, false},
		{[]*proto.AccessibilityAXValueSource{src(proto.AccessibilityAXValueSourceTypeRelatedElement, "", "Email"),
			src(proto.AccessibilityAXValueSourceTypePlaceholder, "placeholder", "Email")}, true},
		{[]*proto.AccessibilityAXValueSource{src(proto.AccessibilityAXValueSourceTypeAttribute, "title", "Coupon")}, true},
		{nil, false},
	}
	for n, c := range cases {
		if got := namedBy(c.sources); got != c.want {
			t.Errorf("case %d: %v, want %v", n, got, c.want)
		}
	}
}

// The reflow issue names up to three offenders and the one that reaches
// furthest right as its source.
func TestReflowIssue(t *testing.T) {
	off := func(name string, right int) offender {
		return offender{Path: [][]Elem{{{Tag: "div", ID: name}}}, Right: right}
	}
	if i := reflowIssue(reflowReport{Width: 400}); i != nil {
		t.Errorf("no offenders: %+v", i)
	}
	rep := reflowReport{Width: 900}
	rep.Offenders = append(rep.Offenders, off("a", 400), off("b", 900), off("a", 400), off("c", 500), off("d", 600))
	i := reflowIssue(rep)
	if i == nil || i.Source != "#b" || i.Detail != "At 320 CSS px the page is 900 px wide; #a, #b, #c stick out." || i.Key == "" {
		t.Errorf("issue = %+v", i)
	}
}
