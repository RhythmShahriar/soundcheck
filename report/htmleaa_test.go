// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

// eaaDir writes the files an --eaa run keeps beside run.json for the
// checkout page: its keyboard trace and the crop of the trapped field.
func eaaDir(t *testing.T, run *model.Run, stops int) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "keyboard"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "eaa", "focus"), 0o750); err != nil {
		t.Fatal(err)
	}
	type stop struct {
		Selector string `json:"selector"`
		IsBody   bool   `json:"is_body"`
		Name     string `json:"name"`
	}
	trace := struct {
		Schema  string `json:"schema"`
		Page    string `json:"page"`
		Forward []stop `json:"forward"`
	}{Schema: "rhythm-soundcheck/keyboard-trace/v1", Page: "/checkout/"}
	for i := range stops {
		switch i {
		case 0:
			trace.Forward = append(trace.Forward, stop{IsBody: true})
		case 1:
			// The trace keeps accessible names; the report never shows them.
			trace.Forward = append(trace.Forward, stop{Selector: "#billing_email", Name: "jane" + "@example.test"})
		default:
			trace.Forward = append(trace.Forward, stop{Selector: fmt.Sprintf("#stop-%d", i)})
		}
	}
	data, _ := json.Marshal(trace)
	if err := os.WriteFile(filepath.Join(dir, "keyboard", "checkout.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := range 40 {
		img.Set(x, 10, color.RGBA{200, 0, 0, 255})
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	if err := os.WriteFile(filepath.Join(dir, "eaa", "focus", "checkout-1.png"), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := range run.Pages {
		if pg := &run.Pages[i]; pg.Path == "/checkout/" {
			trap, _, _, _ := eaaIssues()
			pg.Keyboard.Evidence = "keyboard/checkout.json"
			pg.Keyboard.Crops = map[string]string{trap.Key: "eaa/focus/checkout-1.png"}
		}
	}
	return dir
}

// dataURL matches the embedded images, which golden files leave out.
var dataURL = regexp.MustCompile(`data:image/[a-z]+;base64,[A-Za-z0-9+/=]+`)

func TestEAAHTMLGolden(t *testing.T) {
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
	for name, r := range map[string]Result{"eaa-snapshot.html.golden": {Run: eaaSnapshot()}, "eaa-compare.html.golden": eaaComparison()} {
		r.Dir = eaaDir(t, r.Run, 4)
		var b bytes.Buffer
		if err := HTML(&b, r); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		for _, want := range []string{`role="tab" id="tab-a11y"`, "WCAG 2.1 A and AA criteria", model.EvidenceNote,
			`<li class="tile st-failed"><span class="num">2.1.2</span> No Keyboard Trap<span class="state"><span aria-hidden="true">✕</span> failures (1)</span></li>`,
			`<span aria-hidden="true">○</span> none found`, `<span aria-hidden="true">?</span> needs a person`,
			"<li><code>(page)</code></li><li><code>#billing_email</code></li>", `<img src="data:image/jpeg;base64,`,
			statementCommand, "Make the statement draft"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
		if strings.Contains(out, "jane") {
			t.Errorf("%s shows an accessible name from the trace", name)
		}
		golden(t, name, dataURL.ReplaceAllString(out, "data:…"))
	}
}

// The criteria grid has every criterion of the run's version, in four
// columns by principle; issues are listed under each criterion they
// fail, linking to their card.
func TestEAAHTMLTab(t *testing.T) {
	r := eaaComparison()
	var b bytes.Buffer
	if err := HTML(&b, r); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if n := strings.Count(out, `<li class="tile `); n != 50 {
		t.Errorf("%d tiles, want 50", n)
	}
	for _, p := range []string{"Perceivable", "Operable", "Understandable", "Robust"} {
		if !strings.Contains(out, `<div class="principle"><h3>`+p+`</h3>`) {
			t.Errorf("no %s column", p)
		}
	}
	// No trace and no Dir: the path isn't there to show.
	if !strings.Contains(out, "focus path not available") {
		t.Error("a missing trace isn't said")
	}
	for _, m := range regexp.MustCompile(`href="#(item-\d+)"`).FindAllStringSubmatch(out, -1) {
		if !strings.Contains(out, `id="`+m[1]+`"`) {
			t.Errorf("link to #%s has no target", m[1])
		}
	}
	if !strings.Contains(out, `<h3>2.1.2 No Keyboard Trap <span class="en">EN 301 549 9.2.1.2</span></h3>`) ||
		!strings.Contains(out, `<li class="k-broke"><span class="badge">BROKE</span><a href="#item-1" data-goto="results">Keyboard focus is trapped: Card number</a></li>`) {
		t.Errorf("trap not listed under its criterion:\n%s", out)
	}
	// FIXED changes keep their badge there too.
	if !strings.Contains(out, `<span class="badge">FIXED</span>`) {
		t.Error("the fixed dialog isn't under 2.4.3")
	}
	// Review mode pairs buttons with article.item; the tab adds none.
	if n := strings.Count(out, "<article "); n != len(r.Comparison.Changes) {
		t.Errorf("%d articles for %d changes", n, len(r.Comparison.Changes))
	}

	b.Reset()
	if err := HTML(&b, Result{Run: sampleRun()}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), `role="tablist"`) || strings.Contains(b.String(), `id="panel-a11y"`) {
		t.Error("a report without --eaa has an Accessibility tab")
	}
}

func TestFocusPath(t *testing.T) {
	run := eaaSnapshot()
	r := Result{Run: run, Dir: eaaDir(t, run, 160)}
	stops, more := r.focusPath("keyboard/checkout.json")
	if len(stops) != maxTraceStops || more != 10 || stops[0] != "(page)" || stops[1] != "#billing_email" {
		t.Errorf("stops %d (%q...), more %d", len(stops), stops[:2], more)
	}
	for _, rel := range []string{"", "keyboard/none.json", "../outside.json", "eaa/focus/checkout-1.png"} {
		if stops, _ := r.focusPath(rel); stops != nil {
			t.Errorf("focusPath(%q) = %q, want nil", rel, stops)
		}
	}
}

// The report's CSP names its script by hash: the hash must be the one
// of the script the report holds.
func TestReportCSP(t *testing.T) {
	for _, r := range []Result{{Run: sampleRun()}, eaaComparison()} {
		var b bytes.Buffer
		if err := HTML(&b, r); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		meta := regexp.MustCompile(`<meta http-equiv="Content-Security-Policy" content="([^"]+)">`).FindStringSubmatch(out)
		if meta == nil {
			t.Fatal("no CSP meta")
		}
		policy := html.UnescapeString(meta[1])
		scripts := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindAllStringSubmatch(out, -1)
		if len(scripts) != 1 {
			t.Fatalf("%d inline scripts, want 1", len(scripts))
		}
		want := "default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src '" + CSPHash(scripts[0][1]) + "'"
		if policy != want {
			t.Errorf("CSP = %q, want %q", policy, want)
		}
		if strings.Contains(out, "<script src") || strings.Contains(out, `<link `) || regexp.MustCompile(`src="https?:`).MatchString(out) {
			t.Error("the report loads something from the network")
		}
	}
}

// The tile states and the badges reach 4.5:1 in the light and the dark
// theme.
func TestReportColorsContrast(t *testing.T) {
	blocks := regexp.MustCompile(`(?s)(:root \{.*?\n\}|:root:not\(\[data-theme="light"\]\) \{.*?\n  \}|:root\[data-theme="dark"\] \{.*?\n\})`).FindAllString(htmlSource, -1)
	if len(blocks) != 3 {
		t.Fatalf("found %d theme blocks, want 3", len(blocks))
	}
	if bad := regexp.MustCompile(`#[0-9A-Fa-f]{6} +--`).FindString(htmlSource); bad != "" {
		t.Fatalf("a color token lacks its semicolon: %q", bad)
	}
	light := tokens(blocks[0])
	for i, b := range blocks {
		theme := tokens(b)
		for k, v := range light {
			if _, ok := theme[k]; !ok {
				theme[k] = v // dark blocks override the light one
			}
		}
		for _, fg := range []string{"st-fail", "st-none", "st-manual", "muted", "text"} {
			for _, bg := range []string{"card", "bg"} {
				if c := contrast(theme[fg], theme[bg]); c < 4.5 {
					t.Errorf("theme %d: --%s on --%s is %.2f:1", i, fg, bg, c)
				}
			}
		}
		for _, badge := range []string{"broke", "new", "changed", "slower", "fixed", "error", "warning", "notice", "muted"} {
			if c := contrast(theme["on-c"], theme[badge]); c < 4.5 {
				t.Errorf("theme %d: badge text on --%s is %.2f:1", i, badge, c)
			}
			if c := contrast(theme[badge], theme["card"]); c < 4.5 {
				t.Errorf("theme %d: --%s text on --card is %.2f:1", i, badge, c)
			}
		}
	}
}

// tokens reads the color custom properties of a CSS block.
func tokens(block string) map[string]string {
	m := map[string]string{}
	for _, t := range regexp.MustCompile(`--([a-z-]+): (#[0-9A-Fa-f]{6})`).FindAllStringSubmatch(block, -1) {
		m[t[1]] = t[2]
	}
	return m
}

// contrast is the WCAG contrast ratio of two #rrggbb colors.
func contrast(a, b string) float64 {
	lum := func(hex string) float64 {
		var c [3]float64
		for i := range c {
			v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
			s := float64(v) / 255
			if s <= 0.03928 {
				c[i] = s / 12.92
			} else {
				c[i] = math.Pow((s+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
	}
	if a == "" || b == "" {
		return 0
	}
	x, y := lum(a), lum(b)
	return (max(x, y) + 0.05) / (min(x, y) + 0.05)
}
