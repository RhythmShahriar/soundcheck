// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/soundcheck/model"
)

func TestInvokedAs(t *testing.T) {
	none := func(string) string { return "" }
	forwarded := func(k string) string {
		if k == "RHYTHM_INVOKED_AS" {
			return "rhythm soundcheck"
		}
		return ""
	}
	for _, tc := range []struct {
		env   func(string) string
		argv0 string
		want  string
	}{
		{forwarded, "/usr/local/bin/rhythm-soundcheck", "rhythm soundcheck"},
		{none, "/usr/local/bin/rhythm-soundcheck", "rhythm-soundcheck"},
		{none, "/usr/local/bin/rhythm-soundcheck.exe", "rhythm-soundcheck"},
		{none, "rhythm-soundcheck.EXE", "rhythm-soundcheck"},
		{none, "./sc", "sc"},
		{none, "", "rhythm-soundcheck"},
	} {
		if got := invokedAs(tc.env, tc.argv0); got != tc.want {
			t.Errorf("invokedAs(argv0 %q) = %q, want %q", tc.argv0, got, tc.want)
		}
	}
}

// TestHeaderName renders the header with the name from each source: the
// rhythm CLI's RHYTHM_INVOKED_AS, the program's own file name and one the
// command passes.
func TestHeaderName(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	var out bytes.Buffer
	header := func(label string, o PrettyOptions) {
		for _, plain := range []bool{true, false} {
			o.Plain = plain
			fmt.Fprintf(&out, "== %s, plain %v\n", label, plain)
			NewPretty(&out, &out, o).Header("1.2.0", "http://brew.test/")
		}
	}
	t.Setenv("RHYTHM_INVOKED_AS", "rhythm soundcheck")
	header("RHYTHM_INVOKED_AS", PrettyOptions{})
	t.Setenv("RHYTHM_INVOKED_AS", "")
	header("argv0 /usr/local/bin/rhythm-soundcheck.exe",
		PrettyOptions{Name: invokedAs(func(string) string { return "" }, "/usr/local/bin/rhythm-soundcheck.exe")})
	header("Name set by the command", PrettyOptions{Name: "sc"})
	golden(t, "header.golden", out.String())
}

func verbosePage() *model.Page {
	return &model.Page{Path: "/shop/", Status: 200, Duration: 2100,
		Metrics: model.Metrics{TTFB: 120.4, Load: 840},
		Console: []string{"Uncaught TypeError: x is not a function", "Failed to load resource: 404"}}
}

func TestVerbosePlainPageDetail(t *testing.T) {
	var out bytes.Buffer
	p := NewPretty(&out, &out, PrettyOptions{Plain: true, Verbose: true, Name: "rhythm-soundcheck"})
	p.Page(verbosePage(), 1, 2)
	p.Page(&model.Page{Path: "/down/", Error: "net::ERR_CONNECTION_REFUSED"}, 2, 2)
	want := "[1/2] /shop/ 200 2.1s (ok)\n" +
		"  timing: ttfb 120ms, load 840ms, settle 1.26s\n" +
		"  console: Uncaught TypeError: x is not a function\n" +
		"  console: Failed to load resource: 404\n" +
		"[2/2] /down/ failed: net::ERR_CONNECTION_REFUSED 0s (error)\n"
	if out.String() != want {
		t.Errorf("verbose plain output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestPlainWithoutVerboseHasNoDetail(t *testing.T) {
	var out bytes.Buffer
	NewPretty(&out, &out, PrettyOptions{Plain: true, Name: "x"}).Page(verbosePage(), 1, 2)
	if s := out.String(); strings.Contains(s, "timing:") || strings.Contains(s, "console:") {
		t.Errorf("plain output without -v has page detail:\n%s", s)
	}
}

func TestVerboseLiveViewDetailGoesToErr(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	var out, errw bytes.Buffer
	NewPretty(&out, &errw, PrettyOptions{Verbose: true, Name: "x"}).Page(verbosePage(), 1, 2)
	if out.Len() != 0 {
		t.Errorf("live view wrote page detail to stdout: %q", out.String())
	}
	for _, want := range []string{"→ /shop/ 200 2.1s (ok)", "timing: ttfb 120ms, load 840ms, settle 1.26s", "console: Failed to load resource: 404"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, errw.String())
		}
	}
}

func TestVerboseCapsConsole(t *testing.T) {
	pg := verbosePage()
	pg.Console = nil
	for i := range maxConsole + 3 {
		pg.Console = append(pg.Console, fmt.Sprintf("log %d", i))
	}
	var out bytes.Buffer
	NewPretty(&out, &out, PrettyOptions{Plain: true, Verbose: true, Name: "x"}).Page(pg, 1, 1)
	s := out.String()
	if n := strings.Count(s, "  console: log "); n != maxConsole {
		t.Errorf("%d console lines, want %d", n, maxConsole)
	}
	if !strings.Contains(s, "  console: …and 3 more\n") {
		t.Errorf("no count of the rest:\n%s", s)
	}
}

// Plain mode prints a long step's progress at the start and at every
// tenth, not once per link. A short step's is printed once, when done.
func TestProgressPlain(t *testing.T) {
	var out bytes.Buffer
	p := NewPretty(&out, io.Discard, PrettyOptions{Plain: true})
	for i := 0; i <= 200; i++ {
		p.Progress("Checking 200 links", i, 200)
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(got) != 11 || got[0] != "Checking 200 links: 0/200" || got[1] != "Checking 200 links: 20/200" || got[10] != "Checking 200 links: 200/200" {
		t.Errorf("progress lines:\n%s", out.String())
	}
	out.Reset()
	for i := 0; i <= 7; i++ {
		p.Progress("Checking 7 links", i, 7)
	}
	if out.String() != "Checking 7 links: 7/7\n" {
		t.Errorf("short step:\n%s", out.String())
	}
	out.Reset()
	q := NewPretty(&out, io.Discard, PrettyOptions{Plain: true, Quiet: true})
	q.Progress("Checking 50 links", 10, 50)
	if out.Len() != 0 {
		t.Errorf("quiet printed %q", out.String())
	}
}

// BROKE, NEW and CHANGED lines of a comparison with a snapshot say how
// to accept them; FIXED ones and comparisons with another site don't.
func TestAcceptHint(t *testing.T) {
	c := &model.Comparison{Before: model.RunRef{Name: "good"}, Changes: []model.Change{
		{ID: "c1", Class: model.Changed, Check: "visual/page", Title: "Page looks different", Pages: []string{"/"}},
		{ID: "b1", Class: model.Broke, Check: "health/status", Title: "Page not found (404)", Pages: []string{"/x/"}},
		{ID: "n1", Class: model.New, Check: "health/js-error", Title: "JS error", Pages: []string{"/y/"}},
		{ID: "f1", Class: model.Fixed, Check: "health/favicon", Title: "No favicon", Pages: []string{"/"}},
	}}
	res := Result{Run: &model.Run{}, Comparison: c}
	var out bytes.Buffer
	NewPretty(&out, io.Discard, PrettyOptions{Plain: true, Accept: "rhythm-soundcheck accept"}).Summary(res, "")
	for _, want := range []string{
		"[CHANGED] Page looks different - /\n  rhythm-soundcheck accept c1 if this is expected\n",
		"[BROKE] Page not found (404) - /x/\n  rhythm-soundcheck accept b1 if this is expected\n",
		"[NEW] JS error - /y/\n  rhythm-soundcheck accept n1 if this is expected\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no hint %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "accept f1") {
		t.Errorf("FIXED got a hint:\n%s", out.String())
	}
	out.Reset()
	lipgloss.SetColorProfile(termenv.Ascii)
	NewPretty(&out, io.Discard, PrettyOptions{Accept: "rhythm-soundcheck accept"}).Summary(res, "")
	for _, id := range []string{"b1", "n1", "c1"} {
		if !strings.Contains(out.String(), "rhythm-soundcheck accept "+id+" if this is expected") {
			t.Errorf("live view has no hint for %s:\n%s", id, out.String())
		}
	}
	out.Reset()
	c.Before.Name = ""
	NewPretty(&out, io.Discard, PrettyOptions{Plain: true, Accept: "rhythm-soundcheck accept"}).Summary(res, "")
	if strings.Contains(out.String(), "if this is expected") {
		t.Errorf("--against got a hint:\n%s", out.String())
	}
}
