// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// An --eaa result shows a page path's token masked in every format; the
// stored result keeps it while a result without --eaa is left alone.
func TestMaskedPaths(t *testing.T) {
	token := strings.Repeat("ab12cd34ef", 4)
	path := "/reset/" + token + "/"
	newRun := func(eaa bool) *model.Run {
		run := eaaClean()
		run.Options.EAA = eaa
		if !eaa {
			run.EAA = nil
		}
		run.Pages = append(run.Pages, model.Page{Path: path, URL: "https://shop.test" + path, Group: "/reset/" + token, Status: 200,
			Issues: []model.Issue{{Check: "a11y/label", Level: model.Warning, Title: "Form elements must have labels", Source: "input"}}})
		run.Warnings = []string{"keyboard: " + path + ": stopped after 60s; the rest of the page wasn't walked"}
		return run
	}
	run := newRun(true)
	res := Result{Run: run, Comparison: &model.Comparison{Changes: []model.Change{{Class: model.New, Check: "a11y/label", Title: "x", Pages: []string{path}}},
		Warnings: []string{"not checked: " + path + " (keyboard: the walk took over 60 s)"}}}
	shown := Masked(res)
	var all bytes.Buffer
	NewPretty(&all, io.Discard, PrettyOptions{Plain: true, Verbose: true}).Summary(shown, "")
	NewPretty(&all, io.Discard, PrettyOptions{Plain: true, Verbose: true}).Page(&shown.Run.Pages[len(shown.Run.Pages)-1], 1, 1)
	for _, f := range []func(io.Writer, Result) error{Markdown, JSON, HTML, func(w io.Writer, r Result) error { return JUnit(w, r, "") }} {
		if err := f(&all, shown); err != nil {
			t.Fatal(err)
		}
	}
	all.WriteString(MaskedPage(&run.Pages[len(run.Pages)-1]).Path)
	if strings.Contains(all.String(), token) {
		t.Error("an --eaa output shows the token in a page path")
	}
	if !strings.Contains(all.String(), "/reset/•••••/") {
		t.Error("the masked path isn't shown")
	}
	if run.Pages[len(run.Pages)-1].Path != path || res.Comparison.Changes[0].Pages[0] != path {
		t.Error("Masked changed the stored result")
	}
	plain := Result{Run: newRun(false)}
	if got := Masked(plain); got.Run != plain.Run {
		t.Error("a result without --eaa was masked")
	}
}

// A page path's query value is masked in every --eaa output however
// short it is and whatever its name: a session id like ?sid=9f3ac2e81b7d
// looks like neither a credential nor a long token. The live page line
// masks it too. The stored result keeps it.
func TestMaskedShortQuery(t *testing.T) {
	sid := "9f3ac2" + "e81b7d"
	path := "/a11y/embed.php?email=jane@example.org&sid=" + sid
	run := eaaClean()
	run.Target = "https://shop.test" + path
	run.Pages = append(run.Pages, model.Page{Path: path, URL: run.Target, FinalURL: run.Target, Group: path, Status: 200,
		Issues: []model.Issue{{Check: "a11y/label", Level: model.Warning, Title: "Form elements must have labels", Source: "input", Pages: []string{path}}}})
	res := Result{Run: run, Comparison: &model.Comparison{Changes: []model.Change{{Class: model.New, Check: "a11y/label", Title: "x", Pages: []string{path}}}}}
	shown := Masked(res)
	var all bytes.Buffer
	pretty := NewPretty(&all, io.Discard, PrettyOptions{Plain: true, Verbose: true})
	pretty.Summary(shown, "")
	pretty.Page(MaskedPage(&run.Pages[len(run.Pages)-1]), 1, 1)
	for _, f := range []func(io.Writer, Result) error{Markdown, JSON, HTML, func(w io.Writer, r Result) error { return JUnit(w, r, "") }} {
		if err := f(&all, shown); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(all.String(), sid) {
		t.Error("an --eaa output shows a short query value of a page path")
	}
	if !strings.Contains(all.String(), "/a11y/embed.php?email=•••••&amp;sid=•••••") && !strings.Contains(all.String(), "/a11y/embed.php?email=•••••&sid=•••••") {
		t.Error("the masked path isn't shown")
	}
	if run.Pages[len(run.Pages)-1].Path != path || res.Comparison.Changes[0].Pages[0] != path {
		t.Error("Masked changed the stored result")
	}
}

// An --eaa result's start URL is masked like its page paths, in every
// format and in the comparison's run references: the target heads the
// Markdown (posted as a PR comment), the HTML meta and the JSON. Its
// query string is dropped. The stored run keeps the URL as typed.
func TestMaskedTarget(t *testing.T) {
	token := strings.Repeat("ab12cd34ef", 4)
	canary := "cnry" + strings.Repeat("9f", 10)
	target := "https://shop.test/reset/" + token + "/?utm=" + canary
	for _, compared := range []bool{false, true} {
		run := eaaClean()
		run.Target = target
		res := Result{Run: run}
		if compared {
			res.Comparison = &model.Comparison{Before: model.RunRef{Target: target}, After: model.RunRef{Target: target}, Pages: 1}
		}
		shown := Masked(res)
		var all bytes.Buffer
		NewPretty(&all, io.Discard, PrettyOptions{Plain: true, Verbose: true}).Summary(shown, "")
		for _, f := range []func(io.Writer, Result) error{Markdown, JSON, HTML, func(w io.Writer, r Result) error { return JUnit(w, r, "") }} {
			if err := f(&all, shown); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Contains(all.String(), token) || strings.Contains(all.String(), canary) {
			t.Errorf("compared %t: an --eaa output shows the target's token or query", compared)
		}
		if !strings.Contains(all.String(), "https://shop.test/reset/•••••/") {
			t.Errorf("compared %t: the masked target isn't shown", compared)
		}
		if run.Target != target || (compared && res.Comparison.Before.Target != target) {
			t.Errorf("compared %t: Masked changed the stored result", compared)
		}
		if got := ShownTarget(run); got != "https://shop.test/reset/•••••/" {
			t.Errorf("ShownTarget: %s", got)
		}
	}
	plain := eaaClean()
	plain.Options.EAA, plain.EAA, plain.Target = false, nil, target
	if got := ShownTarget(plain); got != target {
		t.Errorf("ShownTarget without --eaa: %s", got)
	}
}

// An issue's key embeds its title, so a token the title masks must not
// come back through the key (issues[].key, pages[].issues[].key and
// groups[].key in JSON). A URL a page sends to its console is masked
// too, as is the JS error title made from it: no page path names them.
func TestMaskedKeyAndConsole(t *testing.T) {
	token := strings.Repeat("Kq4", 13) + "z"
	link := "/password/reset/" + token
	console := "reset failed for http://127.0.0.1:40422/account/reset/" + token + "?key=" + token
	jsTitle := "JS error: reset failed for http://N.0.0.1:N/account/reset/" + token + "?key=" + token
	broken := model.Issue{Check: "health/broken-link", Level: model.Warning, Title: "Broken link to " + link + " (404)", Pages: []string{link}}
	broken.Identify()
	jsErr := model.Issue{Check: "health/js-error", Level: model.Warning, Title: jsTitle, Detail: console}
	jsErr.Identify()
	run := eaaClean()
	run.Pages = append(run.Pages, model.Page{Path: "/two.html", URL: "http://127.0.0.1:40422/two.html", Group: "/two.html", Status: 200,
		Console: []string{console}, Issues: []model.Issue{jsErr}})
	run.Issues = append(run.Issues, broken)
	res := Result{Run: run, Comparison: &model.Comparison{Changes: []model.Change{{Class: model.New, Check: jsErr.Check, Title: jsTitle, Detail: console, Pages: []string{"/two.html"}}}}}
	shown := Masked(res)
	var all bytes.Buffer
	pretty := NewPretty(&all, io.Discard, PrettyOptions{Plain: true, Verbose: true})
	pretty.Summary(shown, "")
	pretty.Page(MaskedPage(&run.Pages[len(run.Pages)-1]), 1, 1)
	for _, f := range []func(io.Writer, Result) error{Markdown, JSON, HTML, func(w io.Writer, r Result) error { return JUnit(w, r, "") }} {
		if err := f(&all, shown); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(all.String(), token) {
		t.Error("an --eaa output shows the token through an issue key or a console line")
	}
	if !strings.Contains(all.String(), "/account/reset/•••••?key=•••••") {
		t.Error("the masked console URL isn't shown")
	}
	if run.Pages[len(run.Pages)-1].Console[0] != console || run.Issues[len(run.Issues)-1].Key != broken.Key {
		t.Error("Masked changed the stored result")
	}
}
