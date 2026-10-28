// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"strings"
	"testing"
)

// An id or a name can hold a token or an email address. The issue's
// source, detail and key mask them; two elements whose ids differ only
// in the value masked keep stable keys.
func TestIssuesMaskSelectors(t *testing.T) {
	token := "gh" + "p_" + strings.Repeat("Q7canary", 5)[:36]
	mail := "b-mark.roe" + "@" + "idcanary.test"
	found := []result{{ID: "select-name", Impact: "critical", Help: "Select element must have an accessible name", Tags: []string{"wcag412"},
		Nodes: []node{
			{Path: [][]Elem{{{Tag: "select", ID: token}}}, HTML: `<select id="` + token + `">`, Summary: "Fix any of the following:\n  Element has no title attribute " + mail},
			{Path: [][]Elem{{{Tag: "button", ID: mail}}}, HTML: `<button id="` + mail + `">`},
		}}}
	got := issues(found, 20, "2.1")
	if len(got) != 2 {
		t.Fatalf("%d issues: %+v", len(got), got)
	}
	want := []string{"#gh" + "p_•••••", `button[id="***@***"]`}
	for k, i := range got {
		if i.Source != want[k] || i.Key != "a11y/select-name|"+want[k] {
			t.Errorf("issue %d: source %q, key %q, want %q", k, i.Source, i.Key, want[k])
		}
		for _, leak := range []string{token, mail, "Q7canary", "idcanary"} {
			if strings.Contains(i.Source+i.Detail+i.Key, leak) {
				t.Errorf("issue %d keeps %q: %+v", k, leak, i)
			}
		}
	}
	fields := []field{{Path: [][]Elem{{{Tag: "input", Name: mail}}}, Placeholder: "Email"}}
	ph, _ := placeholderIssues(fields, beyond{}, nil, 20)
	if len(ph) != 1 || ph[0].Source != `input[name="***@***"]` {
		t.Errorf("placeholder issues = %+v", ph)
	}
}
