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
