package report

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

func sampleRun() *model.Run {
	issue := func(check string, l model.Level, title, source string) model.Issue {
		i := model.Issue{Check: check, Level: l, Title: title, Source: source, Fix: "Fix it.\nMore detail."}
		i.Identify()
		return i
	}
	return &model.Run{
		Schema: model.Schema, Tool: "rhythm-soundcheck", Version: "1.0.0", Target: "http://brew.test/",
		Started: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC), Duration: 61000,
		Pages: []model.Page{
			{Path: "/", URL: "http://brew.test/", Status: 200, Issues: []model.Issue{
				issue("health/php-error", model.Warning, `PHP Warning: Undefined array key "id"`, "themes/brew/functions.php:212"),
				issue("health/favicon", model.Notice, "No favicon", ""),
			}, Metrics: model.Metrics{LCP: 1200, Load: 1800, Bytes: 512000}},
			{Path: "/shop/", URL: "http://brew.test/shop/", Status: 500, Issues: []model.Issue{
				issue("health/status", model.Error, "Page returns HTTP 500", ""),
				issue("health/php-error", model.Warning, `PHP Warning: Undefined array key "id"`, "themes/brew/functions.php:212"),
			}},
		},
		Warnings: []string{"robots.txt couldn't be read"},
	}
}

func sampleComparison(dir string) *model.Comparison {
	return &model.Comparison{
		Schema: model.Schema, Version: "1.0.0", Pages: 2,
		Before: model.RunRef{Target: "http://brew.test/", Name: "good", Started: time.Now().Add(-3 * time.Hour)},
		Changes: []model.Change{
			{Class: model.Broke, Check: "health/status", Title: "Page returns HTTP 500", Pages: []string{"/shop/"}, Before: "200", After: "500", Fix: "Check the PHP error log."},
			{Class: model.New, Check: "health/php-error", Title: `PHP Warning: Undefined array key "id"`, Source: "themes/brew/functions.php:212", Pages: []string{"/", "/shop/"}},
			{Class: model.Changed, Check: "visual/changed", Title: "Layout changed | header", Pages: []string{"/"}, Score: 12.5,
				Files: map[string]string{"before": "before.png", "after": "after.png", "diff": "diff.png"}},
			{Class: model.Slower, Check: "speed/lcp", Title: "LCP slower", Pages: []string{"/"}, Before: "1.2 s", After: "2.4 s", Score: 100},
			{Class: model.Fixed, Check: "health/js-error", Title: "JS error: jQuery is not defined", Pages: []string{"/shop/"}, Fix: "never shown"},
		},
		Warnings: []string{"the snapshot checked 3 pages, this run 2"},
	}
}

func TestFailOn(t *testing.T) {
	run := sampleRun()
	r := Result{Run: run, Comparison: sampleComparison("")}
	for threshold, want := range map[string]bool{"": false, "broke": true, "new": true, "slower": true} {
		if got := FailOn(r, threshold); got != want {
			t.Errorf("FailOn(%q) = %v", threshold, got)
		}
	}
	r.Comparison.Changes = r.Comparison.Changes[2:] // changed, slower, fixed
	if FailOn(r, "new") || !FailOn(r, "changed") {
		t.Error("only changed/slower left: new must pass, changed must fail")
	}
	r.Comparison.Changes = r.Comparison.Changes[2:] // fixed only
	if FailOn(r, "slower") {
		t.Error("FIXED must never fail a build")
	}

	single := Result{Run: run}
	if !FailOn(single, "error") || !FailOn(single, "notice") {
		t.Error("a run with an error must fail at error and below")
	}
	if err := ValidFailOn("fixed", true); err == nil {
		t.Error("fixed isn't a valid --fail-on")
	}
	if err := ValidFailOn("warning", false); err != nil {
		t.Error(err)
	}
}

func TestIssuesGroupsAcrossPages(t *testing.T) {
	is := Issues(sampleRun())
	if len(is) != 3 {
		t.Fatalf("got %d grouped issues, want 3: %+v", len(is), is)
	}
	if is[0].Level != model.Error || is[1].Level != model.Warning || len(is[1].Pages) != 2 {
		t.Errorf("want the error first, then the warning on 2 pages: %+v", is)
	}
}

func TestPrettyPlainComparison(t *testing.T) {
	var buf bytes.Buffer
	p := NewPretty(&buf, &buf, PrettyOptions{Plain: true})
	p.Header("1.0.0", "http://brew.test/")
	p.Summary(Result{Run: sampleRun(), Comparison: sampleComparison("")}, "report: .soundcheck/runs/x/report.html")
	out := buf.String()
	for _, want := range []string{
		"[BROKE] Page returns HTTP 500 - /shop/", "200 → 500", "fix: Check the PHP error log.",
		"[NEW] PHP Warning", "source: themes/brew/functions.php:212",
		"[FIXED] JS error", "Something broke - 1 broke, 1 new, 1 changed, 1 slower, 1 fixed - compared with",
		`snapshot "good" (3h ago)`, "note: the snapshot checked 3 pages",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "never shown") {
		t.Error("fixed changes shouldn't print a fix")
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("plain output has escape codes")
	}
}

func TestPrettyRun(t *testing.T) {
	var buf bytes.Buffer
	NewPretty(&buf, &buf, PrettyOptions{}).Summary(Result{Run: sampleRun()}, "")
	out := buf.String()
	for _, want := range []string{"ERRORS", "Page returns HTTP 500", "WARNINGS", "2 pages", "No favicon"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, Result{Run: sampleRun(), Comparison: sampleComparison("")}); err != nil {
		t.Fatal(err)
	}
	var c model.Comparison
	if err := json.Unmarshal(buf.Bytes(), &c); err != nil || c.Schema != model.Schema || len(c.Changes) != 5 {
		t.Fatalf("round trip: %v %+v", err, c)
	}
}

func TestMarkdown(t *testing.T) {
	var buf bytes.Buffer
	if err := Markdown(&buf, Result{Run: sampleRun(), Comparison: sampleComparison("")}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"**Something broke.**", "| 1 | 1 | 1 | 1 | 1 |", "### ✖ BROKE (1)", `Layout changed \| header`, "`/` `/shop/`", "<details>"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown lacks %q:\n%s", want, out)
		}
	}
}

func TestJUnit(t *testing.T) {
	var buf bytes.Buffer
	r := Result{Run: sampleRun(), Comparison: sampleComparison("")}
	if err := JUnit(&buf, r, ""); err != nil {
		t.Fatal(err)
	}
	var s junitSuites
	if err := xml.Unmarshal(buf.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Failures != 2 || s.Tests != 6 {
		t.Errorf("default: want broke+new failing (2 of 6), got %d of %d", s.Failures, s.Tests)
	}
	buf.Reset()
	_ = JUnit(&buf, r, "slower")
	_ = xml.Unmarshal(buf.Bytes(), &s)
	if s.Failures != 4 {
		t.Errorf("--fail-on slower: want 4 failures, got %d", s.Failures)
	}
}

func TestHTMLEmbedsShots(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]color.Color{"before.png": color.White, "after.png": color.Black, "diff.png": color.RGBA{255, 0, 0, 255}} {
		img := image.NewRGBA(image.Rect(0, 0, 1366, 2000))
		for y := range 2000 {
			for x := range 1366 {
				img.Set(x, y, c)
			}
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	var buf bytes.Buffer
	if err := HTML(&buf, Result{Run: sampleRun(), Comparison: sampleComparison(dir), Dir: dir}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if n := strings.Count(out, "data:image/jpeg;base64,"); n < 3 {
		t.Errorf("want before, after and diff embedded, found %d images", n)
	}
	for _, want := range []string{"Something broke", "Page returns HTTP 500", "200 → 500", "Slider", `data-filter="broke"`, "/shop/"} {
		if !strings.Contains(out, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Contains(out, "ZgotmplZ") {
		t.Error("a value was rejected by html/template")
	}
	if len(out) > 400_000 {
		t.Errorf("report is %d bytes; images should be scaled down", len(out))
	}

	// A single run renders its issues instead.
	buf.Reset()
	if err := HTML(&buf, Result{Run: sampleRun()}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "ERRORS") || !strings.Contains(buf.String(), "already has errors") {
		t.Error("run report lacks its issues")
	}
}
