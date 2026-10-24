// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package importer

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func has(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// loads checks the YAML the import writes is a config Soundcheck accepts.
func loads(t *testing.T, r *Result) config.Config {
	t.Helper()
	data, err := r.YAML("test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	return cfg
}

// The sample config `backstop init` writes, from BackstopJS's docs.
func TestBackstopSample(t *testing.T) {
	r, err := Backstop(read(t, "backstop.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Site != "https://garris.github.io" || strings.Join(r.Pages, ",") != "/BackstopJS/" ||
		strings.Join(r.Viewports, ",") != "desktop,mobile" || len(r.Ignore) != 0 {
		t.Errorf("result = %+v", r)
	}
	for _, want := range []string{"onBeforeScript puppet/onBefore.js", "onReadyScript puppet/onReady.js",
		`cookiePath on scenario "BackstopJS Homepage"`, `misMatchThreshold on scenario "BackstopJS Homepage"`} {
		if !has(r.Unmapped, want) {
			t.Errorf("unmapped has no %q:\n%s", want, strings.Join(r.Unmapped, "\n"))
		}
	}
	// Empty defaults (delay 0, readySelector "") aren't settings anyone made.
	for _, not := range []string{"delay", "readySelector", "hoverSelector", "referenceUrl"} {
		if has(r.Unmapped, not+" on") {
			t.Errorf("%s listed though it's empty:\n%s", not, strings.Join(r.Unmapped, "\n"))
		}
	}
	if !has(r.Mapped, "phone 320 px as mobile") || !has(r.Mapped, "tablet 1024 px as desktop") {
		t.Errorf("mapped:\n%s", strings.Join(r.Mapped, "\n"))
	}
	cfg := loads(t, r)
	if cfg.Site != "https://garris.github.io" || strings.Join(cfg.Pages.Extra, ",") != "/BackstopJS/" {
		t.Errorf("config = %+v", cfg)
	}
	if cfg.Pages.Limit != config.Default().Pages.Limit {
		t.Error("settings the import doesn't set must keep their defaults")
	}
}

func TestBackstopMapping(t *testing.T) {
	r, err := Backstop(read(t, "backstop-site.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	// Pages: www and the port don't matter, duplicates go, query kept,
	// fragment dropped; other hosts and file URLs aren't pages.
	if got := strings.Join(r.Pages, " "); got != "/ /cart/?step=1" {
		t.Errorf("pages = %q", got)
	}
	if got := strings.Join(r.Ignore, " "); got != ".cookie-banner #live-chat .ad" {
		t.Errorf("ignore = %q", got)
	}
	// 600 px is still mobile; a viewport with no width can't be mapped.
	if strings.Join(r.Viewports, ",") != "desktop,mobile" || !has(r.Mapped, "small 600 px") || !has(r.Unmapped, `viewport "broken" has no width`) {
		t.Errorf("viewports %v\n%s\n%s", r.Viewports, strings.Join(r.Mapped, "\n"), strings.Join(r.Unmapped, "\n"))
	}
	for _, want := range []string{
		`scenario "Docs": https://docs.shop.test/help/ isn't on https://staging.shop.test`,
		`scenario "Local file": file:///tmp/page.html isn't a web page`,
		`onReadyScript on scenario "Docs"`,
		"delay on 5 scenarios", // from scenarioDefaults
	} {
		if !has(r.Unmapped, want) {
			t.Errorf("unmapped has no %q:\n%s", want, strings.Join(r.Unmapped, "\n"))
		}
	}
	if !has(r.Mapped, "not removed from the page") || !has(r.Mapped, "they now apply to every page") {
		t.Errorf("mapped:\n%s", strings.Join(r.Mapped, "\n"))
	}
	cfg := loads(t, r)
	if strings.Join(cfg.Visual.Ignore, " ") != ".cookie-banner #live-chat .ad" || strings.Join(cfg.Viewports, ",") != "desktop,mobile" {
		t.Errorf("config = %+v", cfg)
	}
}

// Negative: every scenario on another site. Nothing becomes a page, each
// URL is named and the site still gets crawled.
func TestBackstopForeignHosts(t *testing.T) {
	r, err := Backstop(read(t, "foreign.json"), "staging.shop.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Pages) != 0 || r.Site != "staging.shop.test" {
		t.Errorf("result = %+v", r)
	}
	for _, want := range []string{"https://blog.other.test/ isn't on staging.shop.test", "http://status.other.test/now isn't on staging.shop.test"} {
		if !has(r.Unmapped, want) {
			t.Errorf("unmapped has no %q:\n%s", want, strings.Join(r.Unmapped, "\n"))
		}
	}
	if cfg := loads(t, r); len(cfg.Pages.Extra) != 0 || strings.Join(cfg.Viewports, ",") != "desktop" {
		t.Errorf("config = %+v", cfg)
	}
	if _, err := Backstop(read(t, "foreign.json"), "://"); err == nil {
		t.Error("a bad --site must fail")
	}
}

func TestBackstopErrors(t *testing.T) {
	_, err := Backstop(read(t, "malformed.json"), "")
	if err == nil || !strings.HasPrefix(err.Error(), "line 4: ") {
		t.Errorf("malformed: %v", err)
	}
	for _, body := range []string{`{}`, `{"scenarios": []}`, `[]`, `{"id": "x"}`} {
		if _, err := Backstop([]byte(body), ""); err == nil {
			t.Errorf("%s: no error", body)
		}
	}
}

func TestLostPixelPages(t *testing.T) {
	r, err := LostPixelPages(read(t, "lostpixel.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	// The commented-out path isn't read, relative paths get their slash.
	if got := strings.Join(r.Pages, " "); got != "/app /next-app /pricing" {
		t.Errorf("pages = %q", got)
	}
	for _, want := range []string{"path `/blog/${slug}` is computed", "path routes.account is computed", "--pages-only"} {
		if !has(r.Unmapped, want) {
			t.Errorf("unmapped has no %q:\n%s", want, strings.Join(r.Unmapped, "\n"))
		}
	}
	if cfg := loads(t, r); strings.Join(cfg.Pages.Extra, " ") != "/app /next-app /pricing" || cfg.Site != "" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestLostPixelErrors(t *testing.T) {
	for body, want := range map[string]string{
		`export default { storybookShots: {} }`:                       "no pageShots",
		`export default { pageShots: { baseUrl: 'x' } }`:              "pageShots has no pages",
		`export default { pageShots: { pages: getPages() } }`:         "isn't a list written in the file",
		`export default { pageShots: { pages: [ { path: '/a' } `:      "never closes",
		`export default { pageShots: { pages: [{ name: 'x' }] } }`:    "no page paths",
		`const s = "pageShots: { pages: ['/x'] }"; export default {}`: "no pageShots",
	} {
		if _, err := LostPixelPages([]byte(body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", body, err, want)
		}
	}
}

// The import only reads: no package that could start a process or load
// code is imported here.
func TestReadsOnly(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			switch path {
			case "os/exec", "syscall", "plugin", "os", "unsafe", "net/http":
				t.Errorf("%s imports %s", f, path)
			}
		}
	}
}

func TestYAMLEmpty(t *testing.T) {
	data, err := (&Result{}).YAML("x.json")
	if err != nil || strings.Contains(string(data), "{}") || !strings.HasPrefix(string(data), "# Imported from x.json.") {
		t.Errorf("%v\n%s", err, data)
	}
}
