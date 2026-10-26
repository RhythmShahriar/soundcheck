// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// account is a page whose hidden account link is labeled with the
// signed-in customer's email address and phone number, so the walk
// raises focus-invisible on it.
func account(email, phone string) string {
	return `<!doctype html><html lang="en"><head><title>Account</title></head><body><main>
<a href="#home" id="home">Home</a>
<a href="#me" id="me" style="opacity:0" aria-label="Signed in as ` + email + `, call ` + phone + `">Account</a>
<a href="#help" id="help">Help</a>
</main></body></html>`
}

// The walk keys its issues by check and element: when the customer in
// the label changes, the keys and Details stay the same and neither the
// issues, the warnings nor the trace on disk hold the email or phone.
func TestLiveKeyIgnoresPII(t *testing.T) {
	jane, max := "jane"+"@"+"example.test", "max.mustermann"+"@"+"example.test"
	srv := serve(t, &pages{html: map[string]string{
		"/a": account(jane, "+49 30 1234567"),
		"/b": account(max, "+49 40 7654321"),
	}})
	c := launch(t)
	run := func(path string) ([]model.Issue, string) {
		tab, p := c.open(srv.URL + path)
		cc := withSaver(runContext(t))
		live(t, New(), cc, tab, p)
		if p.Keyboard == nil {
			t.Fatalf("%s: no summary, audit %+v", path, p.Audits)
		}
		trace, err := os.ReadFile(filepath.Join(cc.Dir, filepath.FromSlash(p.Keyboard.Evidence)))
		if err != nil {
			t.Fatal(err)
		}
		out, _ := json.Marshal(struct {
			Issues   []model.Issue
			Warnings []string
			Summary  *model.KeyboardSummary
		}{p.Issues, cc.Run.Warnings, p.Keyboard})
		return p.Issues, string(out) + string(trace)
	}
	a, outA := run("/a")
	b, outB := run("/b")
	if !slices.ContainsFunc(a, func(i model.Issue) bool { return i.Key == "keyboard/focus-invisible|#me" }) {
		t.Fatalf("no focus-invisible on #me: %+v", a)
	}
	if len(a) != len(b) {
		t.Fatalf("%d and %d issues", len(a), len(b))
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Detail != b[i].Detail {
			t.Errorf("%q / %q, then %q / %q", a[i].Key, a[i].Detail, b[i].Key, b[i].Detail)
		}
	}
	for _, leak := range []string{"jane", "mustermann", "example.test", "1234567", "7654321"} {
		if strings.Contains(outA+outB, leak) {
			t.Errorf("%s kept in the output", leak)
		}
	}
}
