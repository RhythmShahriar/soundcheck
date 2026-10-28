// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package clientreport

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const hostile = `<script>alert("x")</script><img src=x onerror=alert(1)>`

func writePNG(t *testing.T, path string, w, h int, c color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) report.Result {
	t.Helper()
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "before.png"), 40, 30, color.RGBA{200, 200, 200, 255})
	writePNG(t, filepath.Join(dir, "after.png"), 40, 30, color.RGBA{220, 80, 60, 255})
	run := &model.Run{Schema: model.Schema, Version: "1.2.0", Target: "https://www.brew.test/",
		Started: time.Date(2026, 10, 24, 9, 30, 0, 0, time.UTC),
		Options: model.Options{Viewports: []string{"desktop", "mobile"}, Modules: []string{"health", "visual", "interact"},
			Levels: []string{"inspect", "click"}, LoggedIn: true},
		Pages: []model.Page{{Path: "/"}, {Path: "/shop/"}, {Path: "/contact/"}}}
	c := &model.Comparison{Schema: model.Schema, Pages: 3,
		Before: model.RunRef{Name: "before-update", Started: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)},
		Changes: []model.Change{
			{Class: model.Broke, Check: "health/status", Title: "Shop page shows an error (HTTP 500)", Pages: []string{"/shop/"},
				Before: "200", After: "500", Source: "themes/brew/functions.php:212", Detail: "#0 wp-includes/class-wp-hook.php(324): brew_init()",
				Fix: "Check the PHP error log."},
			{Class: model.New, Check: "interact/dead-click", Title: hostile, Pages: []string{"/", "/shop/"}, Detail: "button.buy > span.label"},
			{Class: model.Changed, Check: "visual/page", Title: "Home page looks different", Pages: []string{"/"}, Score: 14,
				Files: map[string]string{"before": "before.png", "after": "after.png", "diff": "diff.png"}},
			{Class: model.Slower, Check: "speed/lcp", Title: "Main content shows later", Pages: []string{"/contact/"}, Before: "1.2 s", After: "2.6 s"},
			{Class: model.Fixed, Check: "health/js-error", Title: "Contact form script error", Pages: []string{"/contact/"}, Source: "plugins/form/app.js"},
		}}
	return report.Result{Run: run, Comparison: c, Dir: dir}
}

func brand(t *testing.T) Brand {
	t.Helper()
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "logo.png"), 4, 2, color.RGBA{15, 118, 110, 255})
	p := filepath.Join(dir, "agency.yaml")
	if err := os.WriteFile(p, []byte("name: Acme Web & Co\nlogo: logo.png\naccent: '#0F766E'\ncontact: hello@acme.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBrand(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func render(t *testing.T, res report.Result, b Brand) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, res, b); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestGoldenComparison(t *testing.T) {
	time.Local = time.UTC
	got := render(t, fixture(t), brand(t))
	golden := filepath.Join("testdata", "comparison.golden.html")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err, " (run go test -update to create it)")
	}
	if got != string(want) {
		t.Errorf("client report differs from %s; run go test ./clientreport -update and review the diff", golden)
	}
}

// The client report carries the HTML report's no-network policy (PRD
// L2, 9.3): it holds the --eaa accessibility section and must make no
// request when opened, so no script or remote image can run.
func TestClientReportCSP(t *testing.T) {
	const csp = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'">`
	got := render(t, fixture(t), brand(t))
	if !strings.Contains(got, csp) {
		t.Errorf("the client report has no CSP meta:\n%s", got[:min(len(got), 400)])
	}
	if strings.Contains(got, "<script") {
		t.Error("the client report has a script its CSP blocks")
	}
}

func TestPlainLanguage(t *testing.T) {
	got := render(t, fixture(t), brand(t))
	for _, want := range []string{
		"Something stopped working", "We checked 3 pages on 24 October 2026 and compared them with how they worked on 1 October 2026.",
		"3 pages of the site, on desktop and phone screens", "Pages as a signed-in user", "Buttons and menus respond when clicked",
		"Stopped working", "On /shop/ · 200 → 500", "On 2 pages", "Fixed since last time", "Contact form script error",
		"Prepared by Acme Web &amp; Co · hello@acme.test", "--accent: #0F766E", `<img src="data:image/png;base64,`,
		`<img src="data:image/jpeg;base64,`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	// Nothing only a developer needs: sources, stack traces, selectors,
	// check ids or fix hints.
	for _, bad := range []string{"source:", "Source", "functions.php", "class-wp-hook", "button.buy", "health/status",
		"interact/dead-click", "PHP error log", "app.js", "before-update"} {
		if strings.Contains(got, bad) {
			t.Errorf("report contains %q", bad)
		}
	}
}

func TestEscapesHostileTitle(t *testing.T) {
	got := render(t, fixture(t), brand(t))
	if strings.Contains(got, "<script>alert") || strings.Contains(got, "<img src=x") {
		t.Error("a title from the site was written unescaped")
	}
	if !strings.Contains(got, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;") {
		t.Error("the escaped title is missing")
	}
}

func TestRunReport(t *testing.T) {
	res := fixture(t)
	res.Comparison = nil
	res.Run.Pages[1].Issues = []model.Issue{
		{Check: "health/status", Level: model.Error, Title: "Shop page shows an error", Source: "x.php:1"},
		{Check: "health/favicon", Level: model.Notice, Title: "No favicon"},
	}
	got := render(t, res, Brand{Name: "Acme"})
	for _, want := range []string{"We found errors on the site", "We checked 3 pages on 24 October 2026.", "Shop page shows an error", "On /shop/", "Errors"} {
		if !strings.Contains(got, want) {
			t.Errorf("run report lacks %q", want)
		}
	}
	if strings.Contains(got, "No favicon") || strings.Contains(got, "x.php") {
		t.Error("run report shows a notice or a source")
	}
	if !strings.Contains(got, "--accent: "+DefaultAccent) {
		t.Error("no default accent")
	}
}

func TestMoreChangesAreCounted(t *testing.T) {
	res := fixture(t)
	for range 12 {
		res.Comparison.Changes = append(res.Comparison.Changes, model.Change{Class: model.New, Title: "Another"})
	}
	if got := render(t, res, Brand{Name: "Acme"}); !strings.Contains(got, "And 8 more") {
		t.Error("changes past the first 8 aren't counted")
	}
}

func TestLoadBrandRefuses(t *testing.T) {
	outside := t.TempDir()
	writePNG(t, filepath.Join(outside, "secret.png"), 2, 2, color.Black)
	svg := func(body string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10">` + body + `</svg>`
	}
	for name, tc := range map[string]struct{ yaml, file, data, want string }{
		"dot dot":        {"name: A\nlogo: ../secret.png\n", "", "", "inside the folder"},
		"absolute":       {"name: A\nlogo: " + filepath.Join(outside, "secret.png") + "\n", "", "", "inside the folder"},
		"missing":        {"name: A\nlogo: nosuch.png\n", "", "", "nosuch.png"},
		"text file":      {"name: A\nlogo: logo.png\n", "logo.png", "not an image", "PNG, JPEG or SVG"},
		"script":         {"name: A\nlogo: logo.svg\n", "logo.svg", svg(`<script>alert(1)</script>`), "<script>"},
		"onload":         {"name: A\nlogo: logo.svg\n", "logo.svg", svg(`<rect onload="alert(1)" width="1" height="1"/>`), "event handlers"},
		"foreign object": {"name: A\nlogo: logo.svg\n", "logo.svg", svg(`<foreignObject><p>x</p></foreignObject>`), "foreignObject"},
		"image":          {"name: A\nlogo: logo.svg\n", "logo.svg", svg(`<image href="https://evil.test/x.png"/>`), "<image>"},
		"external use":   {"name: A\nlogo: logo.svg\n", "logo.svg", svg(`<use xlink:href="https://evil.test/a.svg#x"/>`), "link inside"},
		"js href":        {"name: A\nlogo: logo.svg\n", "logo.svg", svg(`<use href="javascript:alert(1)"/>`), "link inside"},
		"css url":        {"name: A\nlogo: logo.svg\n", "logo.svg", svg(`<rect style="fill: url(#a); stroke: url( 'https://evil.test/t' )"/>`), "own parts"},
		"doctype":        {"name: A\nlogo: logo.svg\n", "logo.svg", `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + svg(`<text>&x;</text>`), "DOCTYPE"},
		"style element":  {"name: A\nlogo: logo.svg\n", "logo.svg", svg(`<style>@import url(https://evil.test/a.css);</style>`), "<style>"},
		"no name":        {"logo: logo.png\n", "", "", "name"},
		"bad accent":     {"name: A\naccent: 'red; background: url(x)'\n", "", "", "accent"},
		"unknown key":    {"name: A\nlogoo: x.png\n", "", "", `line 2: unknown key "logoo" (did you mean "logo"?); agency.yaml takes name, logo, accent and contact`},
		"color twice":    {"name: A\naccent: '#fff'\ncolor: '#000'\n", "", "", "line 3: accent is already set on line 2"},
		"not keys":       {"- name\n", "", "", "want keys like"},
	} {
		dir := t.TempDir()
		if tc.file != "" {
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		p := filepath.Join(dir, "agency.yaml")
		if err := os.WriteFile(p, []byte(tc.yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadBrand(p)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

// help calls accent the agency's color, so color: is taken for it.
func TestLoadBrandTakesColorForAccent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agency.yaml")
	if err := os.WriteFile(p, []byte("name: Acme\ncolor: '#0F766E'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBrand(p)
	if err != nil || b.Accent != "#0F766E" {
		t.Errorf("LoadBrand = %+v, %v", b, err)
	}
}

func TestLoadBrandSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	writePNG(t, filepath.Join(outside, "secret.png"), 2, 2, color.Black)
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(dir, "logo.png")); err != nil {
		t.Skip("no symlinks here: ", err)
	}
	p := filepath.Join(dir, "agency.yaml")
	if err := os.WriteFile(p, []byte("name: A\nlogo: logo.png\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBrand(p); err == nil {
		t.Error("a symlink out of the brand folder was followed")
	}
}

func TestLoadBrandSVG(t *testing.T) {
	dir := t.TempDir()
	logo := `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><defs><linearGradient id="a"><stop offset="0" stop-color="#0F766E"/></linearGradient></defs><title>Acme</title><rect width="10" height="10" fill="url(#a)"/><use href="#a"/></svg>`
	if err := os.MkdirAll(filepath.Join(dir, "img"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "img", "logo.svg"), []byte(logo), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "agency.yaml")
	if err := os.WriteFile(p, []byte("name: Acme\nlogo: img/logo.svg\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBrand(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b.logo), "data:image/svg+xml;base64,") {
		t.Errorf("logo = %.40s", b.logo)
	}
}

// A screenshot path that leaves the result's folder is not embedded.
func TestPictureOutsideResultDir(t *testing.T) {
	res := fixture(t)
	outside := t.TempDir()
	writePNG(t, filepath.Join(outside, "private.png"), 40, 30, color.RGBA{1, 2, 3, 255})
	rel, err := filepath.Rel(res.Dir, filepath.Join(outside, "private.png"))
	if err != nil {
		t.Fatal(err)
	}
	if got := render(t, res, brand(t)); !strings.Contains(got, "data:image/jpeg") {
		t.Fatal("fixture report has no pictures")
	}
	for i := range res.Comparison.Changes {
		if res.Comparison.Changes[i].Files != nil {
			res.Comparison.Changes[i].Files = map[string]string{"before": rel, "after": "after.png"}
		}
	}
	if got := render(t, res, brand(t)); strings.Contains(got, "data:image/jpeg") {
		t.Error("report embeds a screenshot from outside the result folder")
	}
}

// An UNSTABLE change is not counted, so the client never sees it.
func TestLeavesOutUnstable(t *testing.T) {
	res := fixture(t)
	res.Comparison.Changes = append(res.Comparison.Changes, model.Change{Class: model.Unstable, Check: "health/js-error",
		Title: "Flaky script error", Pages: []string{"/"}})
	got := render(t, res, brand(t))
	if strings.Contains(got, "Flaky script error") || strings.Contains(got, `<b>0</b><span></span>`) {
		t.Error("client report shows the UNSTABLE change or an unnamed count")
	}
	if strings.Count(got, `<div class="count `) != 5 {
		t.Errorf("want 5 counts, one per counted class:\n%s", got)
	}
}
