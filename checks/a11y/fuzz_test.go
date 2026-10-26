// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// The fuzz targets check what the parsers promise whatever axe or a page
// sends: no panic, nothing a visitor typed in an issue, keys that stay
// unique and selectors that stay one line. New failing inputs land in
// testdata/fuzz/<target>/; commit them with the fix.

// fakeKey is a credential-shaped value for the seeds, assembled at run
// time so the source never holds one.
func fakeKey() string { return "sk_" + "live_" + "4eC39HqLyjWDarjtT1zdp7dc" }

// planted is what a visitor typed into a field. It's no credential
// the kit knows, so only the module's own masking can hide it.
const planted = "ana.visitor.planted@example.org"

func FuzzIssues(f *testing.F) {
	f.Add(`{"violations":[{"id":"label","impact":"critical","help":"Form elements must have labels","tags":["wcag2a","wcag412","EN-9.4.1.2"],"total":2,"nodes":[{"path":[[{"tag":"html"},{"tag":"body"},{"tag":"input","id":"email"}]],"html":"<input id=\"email\">","failureSummary":"Fix any of the following:\n  no label","impact":"critical"},{"path":[[{"tag":"input","name":"q"}]],"html":"<input name=q value=`+fakeKey()+`>"}]}]}`, uint8(20), uint8(1))
	f.Add(`{"violations":[{"id":"x","nodes":[{"path":[[{"tag":"a"}]]}]},{"id":"x","nodes":[{"path":[[{"tag":"a"}]]},{"path":[[{"tag":"b"}]]}]}]}`, uint8(1), uint8(0))
	f.Add(`{"violations":[{"id":"html-has-lang","impact":"serious","nodes":[{"path":[[{"tag":"html"}]],"html":"<html data-user='4471'>"}]}]}`, uint8(5), uint8(2))
	f.Add(`{"violations":null,"frames":[{"host":"maps.example.org"}]}`, uint8(0), uint8(0))
	f.Add(`{"violations":[{"id":"y","total":-3,"nodes":[{"path":[],"html":"<a href=\"/r?t=1\" value=\"`+planted+`\">"}]}]}`, uint8(3), uint8(1))
	f.Fuzz(func(t *testing.T, raw string, keep, v uint8) {
		doc, err := parse(raw)
		if err != nil {
			return
		}
		max := int(keep)%maxRules + 1
		version := []string{"2.0", "2.1", "2.2"}[int(v)%3]
		for i := range doc.Violations {
			for j := range doc.Violations[i].Nodes {
				n := &doc.Violations[i].Nodes[j]
				// The element's first attribute is what the visitor typed.
				n.HTML = `<input value="` + planted + `" ` + without(n.HTML, planted)
				// axe quotes an attribute in some summaries; the value is
				// masked there too.
				n.Summary = `value="` + planted + `" ` + without(n.Summary, planted)
			}
		}
		got := issues(doc.Violations, max, version)
		perRule := map[string]int{}
		keys := map[string]bool{}
		for _, i := range got {
			perRule[i.Check]++
			if perRule[i.Check] > max {
				t.Fatalf("more than %d issues of %s", max, i.Check)
			}
			if keys[i.Key] {
				t.Fatalf("key %q twice", i.Key)
			}
			keys[i.Key] = true
			if strings.Contains(i.Detail, planted) {
				t.Fatalf("detail shows the typed value: %q", i.Detail)
			}
			if i.Source == "" {
				t.Fatalf("issue without a selector: %+v", i)
			}
		}
	})
}

// without removes every s from text, also those removing one makes.
func without(text, s string) string {
	for strings.Contains(text, s) {
		text = strings.ReplaceAll(text, s, "")
	}
	return text
}

func FuzzSnippet(f *testing.F) {
	for _, s := range []string{
		`<input id="email" value="ana@example.com" data-user-id='4471'>`,
		`<input name=acct VALUE=12345678 type="text">`,
		`<a href="/reset?token=9f8e7d6c5b4a&email=ana@example.com"></a>`,
		`<a href='/password/reset/Zx81kQpL0aTnB4vRu7cWmE2s'></a>`,
		`<form action=/subscribe?key=abc123>`,
		`<input value="` + fakeKey() + `">`,
		`<input data-key=` + fakeKey() + ` value="cut short by ax`,
		`<input/value="slash"><a id="x"href="/r?q=after-quote">`,
		`<input data-a= value="spaced" data-x@y="odd-name">`,
		`<button title="value=fake">` + strings.Repeat("long text ", 30) + `</button>`,
		"<input value=\"a\nb\" \xff>",
		`<input type="email" value="ana@example.com" data-user-id="4471" disabled="">`,
		`<a class="reset" href="/reset?token=9f8e7d6c5b4a&amp;email=ana%40example.com">Reset</a>`,
		`<input title="say &quot;hi&quot;" value="typed`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := snippet(in)
		if !utf8.ValidString(out) {
			t.Fatalf("snippet(%q) = %q: not UTF-8", in, out)
		}
		if n := utf8.RuneCountInString(out); n > maxSnippet {
			t.Fatalf("snippet(%q) has %d runes", in, n)
		}
		if again := snippet(out); again != out {
			t.Fatalf("snippet(%q) = %q, then %q", in, out, again)
		}
		seen := strings.TrimSpace(spaces.ReplaceAllString(strings.ToValidUTF8(in, "�"), " "))
		if !serialized(seen) {
			return
		}
		for _, s := range hidden(seen) {
			if strings.Count(seen, s) == 1 && strings.Contains(out, s) {
				t.Fatalf("snippet(%q) = %q shows %q", in, out, s)
			}
		}
	})
}

// serialized reports whether a browser could have written html as an
// element's outerHTML, maybe cut short as axe does with a long one: html
// is the start of what the HTML tokenizer reads from it written out
// again, with every attribute value in double quotes and its quotes
// escaped. Text the tokenizer reads another way than a serializer writes
// it (an unquoted value after a bare "name=", a quote in a tag name
// further on) never comes from axe, so the leak check skips it; the
// other properties hold for any text.
func serialized(in string) bool {
	for _, end := range []string{"", ">", `">`} {
		var b strings.Builder
		z := html.NewTokenizer(strings.NewReader(in + end))
		for z.Next() != html.ErrorToken {
			b.WriteString(z.Token().String())
		}
		if strings.HasPrefix(spaces.ReplaceAllString(b.String(), " "), in) {
			return true
		}
	}
	return false
}

// hidden lists the texts snippet must hide in html as the HTML
// tokenizer reads it: the value of value and data-* attributes and the
// query of a link. HTML cut short ends inside a tag, which the tokenizer
// drops, so the tag is also read closed each way it could be. Texts
// masking could make up (short ones, "*", quotes, entities) are left
// out: they say nothing about a leak.
func hidden(in string) []string {
	var out []string
	for _, end := range []string{"", ">", `">`, `'>`} {
		z := html.NewTokenizer(strings.NewReader(in + end))
		later := false // a token was read before this one
		for {
			tt := z.Next()
			if tt == html.ErrorToken {
				break
			}
			first := !later
			later = true
			if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
				continue
			}
			// outerHTML never makes a tag name with a quote or "=".
			// snippet reads the name of the tag axe's HTML opens with like
			// the tokenizer; one further on, made up by the fuzzer, it may
			// not.
			if name, _ := z.TagName(); !first && strings.ContainsAny(string(name), `"'=`) {
				continue
			}
			for {
				k, v, more := z.TagAttr()
				name := string(k)
				var s string
				switch {
				case name == "value" || strings.HasPrefix(name, "data-"):
					s = string(v)
				case name == "href" || name == "src" || name == "action" || name == "formaction":
					if q := strings.IndexAny(string(v), "?#"); q >= 0 {
						s = string(v)[q+1:]
					}
				}
				if utf8.RuneCountInString(s) >= 3 && !strings.ContainsAny(s, "*\"'&�…") {
					out = append(out, s)
				}
				if !more {
					break
				}
			}
		}
	}
	return out
}

// nameHolds reports whether an element of path has a name holding s.
func nameHolds(path [][]Elem, s string) bool {
	for _, tree := range path {
		for _, e := range tree {
			if strings.Contains(e.Name, s) {
				return true
			}
		}
	}
	return false
}

func FuzzSelector(f *testing.F) {
	for _, s := range []string{
		`[[{"tag":"html"},{"tag":"body"},{"tag":"main"},{"tag":"input","id":"email"}]]`,
		`[[{"tag":"html"}]]`,
		`[[{"tag":"html"},{"tag":"body"}]]`,
		`[[{"tag":"html"},{"tag":"body"},{"tag":"article","id":"post-12345"},{"tag":"a","nth":2}]]`,
		`[[{"tag":"div","id":":r1:"}],[{"tag":"button","nth":2}]]`,
		`[[{"tag":"input","name":"a\"b\nc\u0000"}]]`,
		`[[{"tag":""}],[]]`,
		`[[{"tag":"x\ny","id":"2col"}]]`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		var path [][]Elem
		if json.Unmarshal([]byte(raw), &path) != nil {
			return
		}
		got := Selector(path)
		if again := Selector(path); again != got {
			t.Fatalf("Selector(%s) = %q, then %q", raw, got, again)
		}
		if strings.ContainsAny(got, "\n\r\x00") {
			t.Fatalf("Selector(%s) = %q: not one line", raw, got)
		}
		elems := 0
		for _, tree := range path {
			elems += len(tree)
			for _, e := range tree {
				if e.ID == "" || !generated.MatchString(e.ID) {
					continue
				}
				if strings.Contains(got, `[id="`+quote(e.ID)+`"]`) {
					t.Fatalf("Selector(%s) = %q keeps the generated id %q", raw, got, e.ID)
				}
				// A name may hold the same text after a space; only a step
				// of its own is the id.
				if slices.Contains(strings.Split(got, " "), "#"+e.ID) && !nameHolds(path, e.ID) {
					t.Fatalf("Selector(%s) = %q keeps the generated id %q", raw, got, e.ID)
				}
			}
		}
		if elems > 0 && got == "" {
			t.Fatalf("Selector(%s) is empty", raw)
		}
	})
}
