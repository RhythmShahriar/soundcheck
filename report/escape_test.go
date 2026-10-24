// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/rhythm/kit/term"

	"github.com/RhythmShahriar/soundcheck/model"
)

// The hostile title from the QA report (#13) and the OSC 52 payload from
// the security report (#10): clipboard write, cursor up, erase line.
const (
	hostileTitle = "Sale | 50% `off` **now** [click](http://127.0.0.1:20300/x) <img src=x onerror=alert(1)> \x01\x02\x1b[31mRED\x1b[0m \u202egnp.exe"
	osc52        = "JS error: boom \x1b]52;c;Y3VybCBldmlsLnRlc3QgfCBzaA==\x07\x1b[1A\x1b[2Kall good"
)

func TestTerm(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":                  "plain text",
		"tab\tstays":                  "tab\tstays",
		"a\x1b[2Jb":                   `a\x1b[2Jb`,
		"bell\x07 nl\n del\x7f":       `bell\x07 nl\x0a del\x7f`,
		"c1 \u009b31m":                `c1 \x9b31m`,
		"raw \x9b byte":               `raw \x9b byte`,
		"\u202aa\u202eb\u2066c\u2069": "abc",
		"café ♪ 日本":                   "café ♪ 日本",
	} {
		if got := Term(in); got != want {
			t.Errorf("Term(%q) = %q, want %q", in, got, want)
		}
	}
}

// Term is the kit's term.Sanitize wherever the text has no newline or C1
// character, and xmlText keeps the newlines Sanitize keeps.
func TestTermIsKitSanitize(t *testing.T) {
	for _, in := range []string{"plain", "a\x1b[2Jb", "del\x7f", "raw \x9b byte", "\u202egnp.exe", "tab\tstays", osc52} {
		if got, want := Term(in), term.Sanitize(in); got != want {
			t.Errorf("Term(%q) = %q, kit %q", in, got, want)
		}
	}
	if got := xmlText("a\nb\u009b\x1b\uFFFE"); got != `a`+"\n"+`b\x9b\x1b` {
		t.Errorf("xmlText = %q", got)
	}
}

func TestMarkdownText(t *testing.T) {
	got := MarkdownText(hostileTitle + "\nline two")
	want := `Sale \| 50% \` + "`" + `off\` + "`" + ` \*\*now\*\* \[click\]\(http\://127.0.0.1:20300/x\) \<img src=x onerror=alert\(1\)\> \\x01\\x02\\x1b\[31mRED\\x1b\[0m gnp.exe line two`
	if got != want {
		t.Errorf("MarkdownText =\n%s\nwant\n%s", got, want)
	}
	if got := MarkdownText(`# h ~~s~~ ![i](u) _e_ a\b`); got != `\# h \~\~s\~\~ \!\[i\]\(u\) \_e\_ a\\b` {
		t.Errorf("MarkdownText = %s", got)
	}
	// GitHub links these without any Markdown around them.
	for in, want := range map[string]string{
		"see http://127.0.0.1:20300/x": `see http\://127.0.0.1:20300/x`,
		"or ftp://host":                `or ftp\://host`,
		"WWW.example.test now":         `WWW\.example.test now`,
		"mail ops@example.test":        `mail ops\@example.test`,
		"ping @owner":                  `ping \@owner`,
		"time 10:30 and a.b":           "time 10:30 and a.b",
	} {
		if got := MarkdownText(in); got != want {
			t.Errorf("MarkdownText(%q) = %s, want %s", in, got, want)
		}
	}
}

// hostileResult puts site text in every field a report shows.
func hostileResult() Result {
	run := &model.Run{Schema: model.Schema, Tool: "rhythm-soundcheck", Version: "1.0.0", Target: "http://127.0.0.1:20300/\u202e",
		Started: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC), Duration: 2000,
		Pages:    []model.Page{{Path: "/b\x1b[2K", URL: "http://127.0.0.1:20300/b", Status: 200, Title: hostileTitle}},
		Warnings: []string{"flow \x1b]52;c;eA==\x07 unreadable"}}
	c := &model.Comparison{Schema: model.Schema, Version: "1.0.0", Pages: 1,
		Before: model.RunRef{Target: run.Target, Name: "t"},
		Changes: []model.Change{
			{Class: model.Broke, Check: "health/js-error", Title: osc52, Source: "inline \x1b[1Ascript", Detail: osc52,
				Fix: "fix \x1b[2J it", Pages: []string{"/b\x1b[2K"}},
			{Class: model.Changed, Check: "health/title", Title: "Page title changed", Pages: []string{"/b`|"},
				Before: "B", After: hostileTitle + "\uFFFE"},
		},
		Warnings: []string{"note \x1b[1A\u202e"}}
	return Result{Run: run, Comparison: c}
}

func TestEscapedInEveryFormat(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	r := hostileResult()
	runOnly := Result{Run: r.Run}
	runOnly.Run.Pages[0].Issues = []model.Issue{{Check: "health/js-error", Level: model.Warning, Title: osc52, Source: "\x1b[1A", Detail: osc52, Key: "k"}}

	terminal := map[string]func(io.Writer){
		"pretty":     func(w io.Writer) { NewPretty(w, w, PrettyOptions{}).Summary(r, "") },
		"plain":      func(w io.Writer) { NewPretty(w, w, PrettyOptions{Plain: true}).Summary(r, "") },
		"run pretty": func(w io.Writer) { NewPretty(w, w, PrettyOptions{}).Summary(runOnly, "") },
		"run plain":  func(w io.Writer) { NewPretty(w, w, PrettyOptions{Plain: true}).Summary(runOnly, "") },
		"live": func(w io.Writer) {
			p := NewPretty(w, w, PrettyOptions{Plain: true, Verbose: true})
			p.Header("1.0.0", r.Run.Target)
			p.Step("Site", "\x1b]0;title\x07")
			pg := model.Page{Path: "/b\x1b[2K", Error: "net::\x1b[1A"}
			p.Page(&pg, 1, 1)
			p.Log("console: " + osc52)
		},
	}
	for name, fn := range terminal {
		var b bytes.Buffer
		fn(&b)
		out := b.String()
		if strings.ContainsAny(out, "\x1b\x07\u202e") {
			t.Errorf("%s output holds ESC, BEL or U+202E:\n%q", name, out)
		}
		if !strings.Contains(out, `\x1b`) {
			t.Errorf("%s output doesn't show the escape as text:\n%s", name, out)
		}
	}

	var md bytes.Buffer
	if err := Markdown(&md, r); err != nil {
		t.Fatal(err)
	}
	golden(t, "hostile.md.golden", md.String())
	if s := md.String(); strings.Contains(s, "](http") || strings.Contains(s, "**now") || strings.Contains(s, " <img") || strings.ContainsAny(s, "\x1b\x07\u202e") {
		t.Errorf("Markdown has a live link, formatting, HTML or control characters:\n%s", s)
	}

	for _, fail := range []string{"", "changed"} {
		var x bytes.Buffer
		if err := JUnit(&x, r, fail); err != nil {
			t.Fatal(err)
		}
		var parsed junitSuites
		if err := xml.Unmarshal(x.Bytes(), &parsed); err != nil {
			t.Fatalf("JUnit doesn't parse: %v\n%s", err, x.String())
		}
		if s := x.String(); strings.ContainsAny(s, "\x1b\x07\u202e\uFFFE") || strings.Contains(s, "&#x1B;") {
			t.Errorf("JUnit holds control characters:\n%s", s)
		}
	}

	var h bytes.Buffer
	if err := HTML(&h, r); err != nil {
		t.Fatal(err)
	}
	if s := h.String(); strings.Contains(s, "\u202e") || strings.Contains(s, "<img src=x") {
		t.Error("HTML holds U+202E or live markup")
	}
}
