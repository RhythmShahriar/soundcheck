// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/model"
)

// unstableResult is a compare where /down/ broke for real and /flaky/
// answered 500 once, then loaded fine when it was checked again.
func unstableResult(flakyOnly bool) Result {
	fatal := model.Issue{Check: "health/status", Level: model.Error, Title: "Server error 500", Fix: "Check the PHP error log."}
	fatal.Identify()
	ok := func(path string, issues ...model.Issue) model.Page {
		return model.Page{Path: path, URL: "http://brew.test" + path, Status: 200, Issues: issues}
	}
	before := &model.Run{Schema: model.Schema, Target: "http://brew.test/", Name: "good",
		Pages: []model.Page{ok("/"), ok("/flaky/"), ok("/down/")}}
	after := &model.Run{Schema: model.Schema, Tool: "rhythm-soundcheck", Version: "1.0.0", Target: "http://brew.test/",
		Started: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC), Duration: 6000,
		Pages: []model.Page{ok("/"), ok("/flaky/", fatal), ok("/down/", fatal)}}
	if flakyOnly {
		after.Pages[2] = ok("/down/")
	}
	c := compare.Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "1.0.0")
	compare.Confirm(c, before, &model.Run{Pages: []model.Page{ok("/flaky/"), ok("/down/", fatal)}})
	c.Created = time.Date(2026, 10, 23, 9, 0, 6, 0, time.UTC)
	return Result{Run: after, Comparison: c}
}

func TestUnstableNeverFails(t *testing.T) {
	r := unstableResult(true)
	if r.Comparison.Count(model.Unstable) != 1 || r.Comparison.Count(model.Broke) != 0 {
		t.Fatalf("want one UNSTABLE change: %+v", r.Comparison.Changes)
	}
	for _, threshold := range []string{"broke", "new", "changed", "slower"} {
		if FailOn(r, threshold) {
			t.Errorf("an UNSTABLE change fails at %s", threshold)
		}
	}
	if !FailOn(unstableResult(false), "broke") {
		t.Error("the BROKE that reproduced should fail at broke")
	}
}

func TestUnstableGolden(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	local := time.Local
	time.Local = time.UTC // the HTML report shows local time
	t.Cleanup(func() { time.Local = local })

	r := unstableResult(false)
	render := map[string]func(*bytes.Buffer) error{
		"unstable.pretty.golden": func(b *bytes.Buffer) error { NewPretty(b, b, PrettyOptions{}).Summary(r, ""); return nil },
		"unstable.plain.golden":  func(b *bytes.Buffer) error { NewPretty(b, b, PrettyOptions{Plain: true}).Summary(r, ""); return nil },
		"unstable.md.golden":     func(b *bytes.Buffer) error { return Markdown(b, r) },
		"unstable.xml.golden":    func(b *bytes.Buffer) error { return JUnit(b, r, "broke") },
		"unstable.html.golden":   func(b *bytes.Buffer) error { return HTML(b, r) },
	}
	for name, fn := range render {
		var b bytes.Buffer
		if err := fn(&b); err != nil {
			t.Fatal(name, err)
		}
		out := b.String()
		lower := strings.ToLower(out)
		if !strings.Contains(lower, "unstable") {
			t.Errorf("%s doesn't show UNSTABLE", name)
		}
		// UNSTABLE comes after everything else, BROKE first.
		if strings.Index(lower, "unstable") < strings.Index(lower, "broke") {
			t.Errorf("%s shows UNSTABLE before BROKE", name)
		}
		golden(t, name, out)
	}
}

func TestNoUnstableColumnWithoutUnstable(t *testing.T) {
	var md bytes.Buffer
	r := Result{Run: sampleRun(), Comparison: sampleComparison("")}
	if err := Markdown(&md, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), "| Broke | New | Changed | Slower | Fixed |\n|---:|---:|---:|---:|---:|\n") ||
		strings.Contains(md.String(), "Unstable") {
		t.Errorf("markdown table:\n%s", md.String())
	}
}
