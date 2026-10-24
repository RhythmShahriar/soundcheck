// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

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
	got := issues(found)
	if len(got) != 3 {
		t.Fatalf("issues = %+v", got)
	}
	want := model.Issue{Check: "a11y/label", Level: model.Warning, Title: "Form elements must have labels", Source: "#email",
		Detail: "<input id=\"email\">\nFix any of the following:\nElement does not have an implicit (wrapped) <label>\nElement has no title attribute",
		Fix:    "See https://dequeuniversity.com/rules/axe/4.14/label for what the rule asks and how to fix it.",
		Key:    "a11y/label|#email"}
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

func TestIssuesCapsElements(t *testing.T) {
	r := result{ID: "link-name", Impact: "serious", Help: "Links must have discernible text"}
	for i := range 50 {
		r.Nodes = append(r.Nodes, node{Path: [][]Elem{tree("html", "body", "ul", "li:"+strconv.Itoa(i+1), "a")}})
	}
	got := issues([]result{r})
	if len(got) != maxNodes {
		t.Fatalf("%d issues, want %d", len(got), maxNodes)
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
