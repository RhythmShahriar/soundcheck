package speed

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

func levels(p *model.Page) map[string]model.Level {
	m := map[string]model.Level{}
	for _, i := range p.Issues {
		m[i.Title] = i.Level
	}
	return m
}

func TestThresholds(t *testing.T) {
	cases := []struct {
		name string
		m    model.Metrics
		want map[string]model.Level
	}{
		{"good", model.Metrics{TTFB: 120, LCP: 2400, CLS: 0.1, TBT: 200, Bytes: 3 << 20, Requests: 100}, map[string]model.Level{}},
		{"needs improvement", model.Metrics{TTFB: 900, LCP: 2600, CLS: 0.11, TBT: 250, Bytes: 3<<20 + 1, Requests: 101}, map[string]model.Level{
			"Largest Contentful Paint over 2.5 s (lab)": model.Warning,
			"Layout shift (CLS) over 0.1 (lab)":         model.Warning,
			"Total Blocking Time over 200 ms (lab)":     model.Warning,
			"Server response (TTFB) over 0.8 s (lab)":   model.Notice,
			"Page weighs over 3 MB":                     model.Warning,
			"Page makes over 100 requests":              model.Notice,
		}},
		{"poor", model.Metrics{TTFB: 2000, LCP: 4100, CLS: 0.3, TBT: 700}, map[string]model.Level{
			"Largest Contentful Paint over 4 s (lab)": model.Error,
			"Layout shift (CLS) over 0.25 (lab)":      model.Error,
			"Total Blocking Time over 600 ms (lab)":   model.Error,
			"Server response (TTFB) over 1.8 s (lab)": model.Warning,
		}},
	}
	for _, c := range cases {
		p := &model.Page{Status: 200, Metrics: c.m}
		(&Module{}).CheckPage(nil, p)
		got := levels(p)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		for _, i := range p.Issues {
			if i.Key == "" || !strings.Contains(i.Detail+i.Title, "lab") && i.Check != "speed/weight" && i.Check != "speed/requests" {
				t.Errorf("%s: issue %+v needs a key and lab wording", c.name, i)
			}
		}
	}
	broken := &model.Page{Status: 500, Metrics: model.Metrics{LCP: 9000}}
	(&Module{}).CheckPage(nil, broken)
	if len(broken.Issues) != 0 {
		t.Errorf("an error page got speed issues: %v", broken.Issues)
	}
}

func TestUsedChars(t *testing.T) {
	r := func(s, e, c int) *proto.ProfilerCoverageRange {
		return &proto.ProfilerCoverageRange{StartOffset: s, EndOffset: e, Count: c}
	}
	// A 1000-char script: top level ran, a function at 100–600 never ran,
	// inside it a block (200–300) is also uncalled, and a called function
	// sits at 700–800 with an unexecuted branch 750–760.
	used, total := usedChars([]*proto.ProfilerCoverageRange{r(0, 1000, 1), r(100, 600, 0), r(200, 300, 0), r(700, 800, 1), r(750, 760, 0)})
	if total != 1000 || used != 1000-500-10 {
		t.Errorf("used %d of %d, want 490 of 1000", used, total)
	}
	// An uncalled function nested in another uncalled one, and a used
	// function inside an unused one.
	used, _ = usedChars([]*proto.ProfilerCoverageRange{r(0, 100, 1), r(10, 90, 0), r(20, 30, 1)})
	if used != 20+10 {
		t.Errorf("used %d, want 30", used)
	}
	js := jsUsage([]*proto.ProfilerScriptCoverage{
		{URL: "https://x/a.js", Functions: []*proto.ProfilerFunctionCoverage{{Ranges: []*proto.ProfilerCoverageRange{r(0, 400, 1)}}, {Ranges: []*proto.ProfilerCoverageRange{r(100, 400, 0)}}}},
		{URL: "", Functions: []*proto.ProfilerFunctionCoverage{{Ranges: []*proto.ProfilerCoverageRange{r(0, 10, 1)}}}},
	})
	if u := js["https://x/a.js"]; u.unusedShare() != 0.75 || len(js) != 1 {
		t.Errorf("js usage = %+v", js)
	}
	css := cssUsage(map[proto.CSSStyleSheetID]*proto.CSSCSSStyleSheetHeader{
		"1": {SourceURL: "https://x/a.css", Length: 1000},
		"2": {SourceURL: "https://x/", Length: 50, IsInline: true},
	}, []*proto.CSSRuleUsage{{StyleSheetID: "1", StartOffset: 0, EndOffset: 100, Used: true}, {StyleSheetID: "1", StartOffset: 100, EndOffset: 900}})
	if u := css["https://x/a.css"]; u.used != 100 || u.total != 1000 || len(css) != 1 {
		t.Errorf("css usage = %+v", css)
	}
	p := &model.Page{Resources: []model.Resource{{URL: "https://x/a.css", Type: "Stylesheet", Bytes: 400}, {URL: "https://x/i.png", Type: "Image", Bytes: 9}}}
	applyUnused(p, css)
	if p.Resources[0].Unused != 360 || p.Resources[1].Unused != 0 {
		t.Errorf("unused = %+v", p.Resources)
	}
}

func pair(path, group string, b, a model.Metrics) (model.Page, model.Page) {
	return model.Page{Path: path, Group: group, Status: 200, Metrics: b}, model.Page{Path: path, Group: group, Status: 200, Metrics: a}
}

func compareRuns(slower float64, pairs ...[2]model.Page) []model.Change {
	before, after := &model.Run{}, &model.Run{}
	for _, p := range pairs {
		before.Pages = append(before.Pages, p[0])
		after.Pages = append(after.Pages, p[1])
	}
	cfg := config.Default()
	cfg.Speed.Slower = slower
	return (&Module{}).Compare(context.Background(), &check.CompareContext{Config: cfg, Before: before, After: after})
}

func pr(path, group string, b, a model.Metrics) [2]model.Page {
	x, y := pair(path, group, b, a)
	return [2]model.Page{x, y}
}

func TestRegressionNoiseFloors(t *testing.T) {
	base := model.Metrics{TTFB: 100, LCP: 1000, CLS: 0.01, TBT: 50, Bytes: 500 << 10, Requests: 20}
	cases := []struct {
		name  string
		after model.Metrics
		want  string // "check class" or ""
	}{
		{"LCP +30% but only +300ms floor met", model.Metrics{TTFB: 100, LCP: 1300, CLS: 0.01, TBT: 50, Bytes: 500 << 10, Requests: 20}, "speed/lcp slower"},
		{"LCP +29%", model.Metrics{TTFB: 100, LCP: 1290, CLS: 0.01, TBT: 50, Bytes: 500 << 10, Requests: 20}, ""},
		{"TTFB doubled but under +200ms", model.Metrics{TTFB: 290, LCP: 1000, CLS: 0.01, TBT: 50, Bytes: 500 << 10, Requests: 20}, ""},
		{"TTFB +200ms", model.Metrics{TTFB: 300, LCP: 1000, CLS: 0.01, TBT: 50, Bytes: 500 << 10, Requests: 20}, "speed/ttfb slower"},
		{"TBT +90ms", model.Metrics{TTFB: 100, LCP: 1000, CLS: 0.01, TBT: 140, Bytes: 500 << 10, Requests: 20}, ""},
		{"TBT +100ms", model.Metrics{TTFB: 100, LCP: 1000, CLS: 0.01, TBT: 150, Bytes: 500 << 10, Requests: 20}, "speed/tbt slower"},
		{"CLS +0.04", model.Metrics{TTFB: 100, LCP: 1000, CLS: 0.05, TBT: 50, Bytes: 500 << 10, Requests: 20}, ""},
		{"CLS +0.06", model.Metrics{TTFB: 100, LCP: 1000, CLS: 0.07, TBT: 50, Bytes: 500 << 10, Requests: 20}, "speed/cls slower"},
		{"weight +99KB", model.Metrics{TTFB: 100, LCP: 1000, CLS: 0.01, TBT: 50, Bytes: 599 << 10, Requests: 20}, ""},
		{"weight +150KB (30%)", model.Metrics{TTFB: 100, LCP: 1000, CLS: 0.01, TBT: 50, Bytes: 650 << 10, Requests: 20}, "speed/weight slower"},
		{"LCP much faster", model.Metrics{TTFB: 100, LCP: 600, CLS: 0.01, TBT: 50, Bytes: 500 << 10, Requests: 20}, "speed/lcp fixed"},
	}
	for _, c := range cases {
		got := compareRuns(25, pr("/", "/", base, c.after))
		var s []string
		for _, ch := range got {
			s = append(s, ch.Check+" "+ch.Class)
		}
		if strings.Join(s, ",") != c.want {
			t.Errorf("%s: got %v, want %q", c.name, s, c.want)
		}
	}

	// TBT from 0: there's no percentage, the floor decides.
	got := compareRuns(25, pr("/", "/", model.Metrics{TBT: 0, Requests: 1}, model.Metrics{TBT: 150, Requests: 1}))
	if len(got) != 1 || got[0].Title != "Total Blocking Time up from 0" || got[0].Before != "0 ms" || got[0].After != "150 ms" {
		t.Errorf("from zero: %+v", got)
	}

	// A stricter speed.slower setting.
	if got := compareRuns(50, pr("/", "/", base, cases[0].after)); len(got) != 0 {
		t.Errorf("30%% slower reported with speed.slower 50: %+v", got)
	}

	// Unmeasured LCP and failed pages are skipped.
	b, a := pair("/x", "", model.Metrics{LCP: 0}, model.Metrics{LCP: 5000})
	a2 := a
	a2.Status = 500
	if got := compareRuns(25, [2]model.Page{b, a}, [2]model.Page{b, a2}); len(got) != 0 {
		t.Errorf("unmeasured or failed: %+v", got)
	}
}

func TestRegressionChangeValues(t *testing.T) {
	got := compareRuns(25, pr("/about/", "/about", model.Metrics{LCP: 1000, Requests: 1}, model.Metrics{LCP: 1500, Requests: 1}))
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	c := got[0]
	if c.Class != model.Slower || c.Title != "LCP 50% slower" || c.Before != "1.0 s" || c.After != "1.5 s" || c.Score != 50 ||
		strings.Join(c.Pages, ",") != "/about/" || !strings.Contains(c.Detail, "Lab") || c.Fix == "" {
		t.Errorf("change = %+v", c)
	}
	got = compareRuns(25, pr("/about/", "/about", model.Metrics{LCP: 2000, Requests: 1}, model.Metrics{LCP: 1000, Requests: 1}))
	if len(got) != 1 || got[0].Class != model.Fixed || got[0].Title != "LCP 50% faster" || got[0].Score != 50 {
		t.Errorf("faster = %+v", got)
	}
}

func TestRegressionGrouping(t *testing.T) {
	ok := model.Metrics{LCP: 1000, Requests: 1}
	slow := func(lcp float64) model.Metrics { return model.Metrics{LCP: lcp, Requests: 1} }

	// 2 of 6 pages slower, both product pages: one change for the template.
	got := compareRuns(25,
		pr("/product/a/", "/product/{slug}", ok, slow(1400)),
		pr("/product/b/", "/product/{slug}", ok, slow(1600)),
		pr("/product/c/", "/product/{slug}", ok, ok),
		pr("/", "/", ok, ok), pr("/about/", "/about", ok, ok), pr("/blog/", "/blog", ok, slow(1500)),
	)
	titles := map[string]model.Change{}
	for _, c := range got {
		titles[c.Title] = c
	}
	g, found := titles["LCP 50% slower on 2 /product/{slug} pages"]
	if !found || strings.Join(g.Pages, ",") != "/product/a/,/product/b/" || g.Before != "1.0 s" || g.After != "1.5 s" {
		t.Errorf("group change missing or wrong: %v", titles)
	}
	if b, found := titles["LCP 50% slower"]; !found || strings.Join(b.Pages, ",") != "/blog/" {
		t.Errorf("single page change missing: %v", titles)
	}
	if len(got) != 2 {
		t.Errorf("want 2 changes, got %d: %v", len(got), titles)
	}

	// 4 of 5 pages slower: one site-wide change.
	got = compareRuns(25,
		pr("/a/", "/a", ok, slow(1400)), pr("/b/", "/b", ok, slow(1400)), pr("/c/", "/c", ok, slow(1400)),
		pr("/d/", "/d", ok, slow(1400)), pr("/e/", "/e", ok, ok))
	if len(got) != 1 || got[0].Title != "LCP 40% slower on 4 pages" || len(got[0].Pages) != 4 || got[0].Score != 40 {
		t.Errorf("site-wide = %+v", got)
	}
}

// res builds a resource on shop.example.
func res(path, typ string, bytes int64) model.Resource {
	return model.Resource{URL: "https://shop.example" + path, Type: typ, Status: 200, Bytes: bytes, Protocol: "h2",
		Encoding: "br", Cache: "public, max-age=31536000", Mime: map[string]string{"Script": "application/javascript", "Stylesheet": "text/css", "Image": "image/jpeg"}[typ]}
}

func testSite(pages []model.Page, f map[string]*facts) site {
	u, _ := url.Parse("https://shop.example/")
	return site{base: u, pages: pages, facts: f}
}

func issues(s site) map[string]model.Issue {
	m := map[string]model.Issue{}
	for _, i := range s.suggestions() {
		m[i.Title] = i
	}
	return m
}

func TestPluginCost(t *testing.T) {
	var pages []model.Page
	for _, path := range []string{"/", "/shop/", "/contact/"} {
		p := model.Page{Path: path, Status: 200, Headers: map[string]string{"content-encoding": "br"}, Resources: []model.Resource{
			res("/wp-content/plugins/elementor/assets/frontend.js?ver=3.2", "Script", 300<<10),
			res("/wp-content/plugins/elementor/assets/frontend.css", "Stylesheet", 100<<10),
			res("/wp-content/plugins/tiny/x.js", "Script", 2<<10),
			res("/wp-content/themes/astra/style.css", "Stylesheet", 20<<10),
			res("/blog/wp-content/mu-plugins/sub%20dir/a.js", "Script", 1<<10),
		}}
		if path == "/shop/" {
			p.Resources = append(p.Resources, res("/wp-content/plugins/woocommerce/a.js", "Script", 600<<10))
		}
		pages = append(pages, p)
	}
	f := map[string]*facts{"/": {Blocking: []blocking{{URL: "https://shop.example/wp-content/plugins/elementor/assets/frontend.css", Duration: 90}}}}
	got := issues(testSite(pages, f))
	el, ok := got["Plugin elementor adds weight to pages"]
	if !ok {
		t.Fatalf("no elementor issue: %v", got)
	}
	if el.Level != model.Notice || el.Source != "plugins/elementor" || len(el.Pages) != 3 ||
		el.Detail != "Plugin elementor adds 400 KB and 2 requests to every page (3), with 30 ms of render-blocking loads (lab, per page)" {
		t.Errorf("elementor = %+v", el)
	}
	woo := got["Plugin woocommerce adds weight to pages"]
	if woo.Level != model.Warning || strings.Join(woo.Pages, ",") != "/shop/" || !strings.Contains(woo.Detail, "600 KB and 1 request to 1 of 3 pages") {
		t.Errorf("woocommerce = %+v", woo)
	}
	for title := range got {
		if strings.Contains(title, "tiny") || strings.Contains(title, "astra") || strings.Contains(title, "sub dir") {
			t.Errorf("light component reported: %s", title)
		}
	}
	if c := component("https://cdn.example/blog/wp-content/mu-plugins/sub%20dir/a.js"); c != "plugin sub dir" {
		t.Errorf("component = %q", c)
	}
	if c := component("https://shop.example/wp-content/themes/astra/style.css"); c != "theme astra" {
		t.Errorf("component = %q", c)
	}
	if c := component("https://shop.example/wp-content/uploads/a.jpg"); c != "" {
		t.Errorf("component = %q", c)
	}
}

func TestPluginRegression(t *testing.T) {
	page := func(path string, kb ...int64) model.Page {
		p := model.Page{Path: path, Status: 200}
		for i, k := range kb {
			p.Resources = append(p.Resources, res(fmt.Sprintf("/wp-content/plugins/%s/f.js", []string{"slider", "forms", "old"}[i]), "Script", k<<10))
		}
		return p
	}
	before, after := &model.Run{}, &model.Run{}
	for _, path := range []string{"/", "/a/"} {
		before.Pages = append(before.Pages, page(path, 200, 0, 300))
		after.Pages = append(after.Pages, page(path, 500, 150, 0))
	}
	// forms (0 → 150 KB) appears; old disappears.
	for i := range after.Pages {
		after.Pages[i].Resources = after.Pages[i].Resources[:2]
		before.Pages[i].Resources = append(before.Pages[i].Resources[:1], before.Pages[i].Resources[2])
	}
	got := map[string]model.Change{}
	for _, c := range (&Module{}).Compare(context.Background(), &check.CompareContext{Config: config.Default(), Before: before, After: after}) {
		got[c.Title] = c
	}
	if s := got["Plugin slider got 2.5× heavier per page"]; s.Class != model.Slower || s.Before != "200 KB" || s.After != "500 KB" || s.Source != "plugins/slider" {
		t.Errorf("slider = %+v (all %v)", s, got)
	}
	if s := got["Plugin forms now loads assets on pages"]; s.Class != model.Slower {
		t.Errorf("forms = %+v (all %v)", s, got)
	}
	if s := got["Plugin old no longer loads assets on pages"]; s.Class != model.Fixed {
		t.Errorf("old = %+v (all %v)", s, got)
	}
}

func TestSuggestions(t *testing.T) {
	home := model.Page{Path: "/", Status: 200, Headers: map[string]string{"content-length": "20480"}, Resources: []model.Resource{
		res("/wp-content/uploads/hero.jpg", "Image", 900<<10),
		res("/wp-content/uploads/below.jpg", "Image", 80<<10),
		res("/wp-content/uploads/icon.png", "Image", 1<<10),
		{URL: "https://shop.example/app.js", Type: "Script", Status: 200, Bytes: 40 << 10, Mime: "text/javascript", Protocol: "http/1.1", Cache: "max-age=600"},
		{URL: "https://shop.example/tiny.js", Type: "Script", Status: 200, Bytes: 900, Mime: "text/javascript", Protocol: "h2", Cache: "no-cache"},
		{URL: "https://shop.example/style.css", Type: "Stylesheet", Status: 200, Bytes: 50 << 10, Unused: 40 << 10, Encoding: "gzip", Mime: "text/css", Protocol: "h2", Cache: "max-age=31536000"},
		{URL: "https://shop.example/used.css", Type: "Stylesheet", Status: 200, Bytes: 50 << 10, Unused: 10 << 10, Encoding: "gzip", Mime: "text/css", Protocol: "h2", Cache: "max-age=31536000"},
		{URL: "https://cdn.tracker.example/t.js?id=1", Type: "Script", Status: 200, Bytes: 300 << 10},
		{URL: "https://fonts.example/f.woff2", Type: "Font", Status: 200, Bytes: 20 << 10},
	}}
	f := map[string]*facts{"/": {Viewport: 900, DPR: 1,
		Images: []picture{
			{Src: "https://shop.example/wp-content/uploads/hero.jpg", Natural: 3000, NaturalH: 2000, Width: 600, Height: 400, Top: 100},
			{Src: "https://shop.example/wp-content/uploads/below.jpg", Natural: 800, NaturalH: 600, Width: 800, Height: 600, Top: 2000},
			{Src: "https://shop.example/wp-content/uploads/lazy.jpg", Natural: 800, NaturalH: 600, Width: 800, Height: 600, Top: 2000, Loading: "lazy"},
			{Src: "https://shop.example/wp-content/uploads/icon.png", Natural: 32, NaturalH: 32, Width: 16, Height: 16, Top: 3000},
		},
		Blocking: []blocking{{URL: "https://shop.example/style.css", Duration: 120}}}}
	got := issues(testSite([]model.Page{home}, f))
	want := map[string]string{
		"Images are much larger than they're displayed":   "/wp-content/uploads/hero.jpg (3000×2000 shown at 600×400",
		"Images below the fold aren't lazy-loaded":        "/wp-content/uploads/below.jpg (80 KB)",
		"CSS and JavaScript block the first render":       "/style.css (50 KB, 120 ms)",
		"CSS files are mostly unused":                     "/style.css (80% of 50 KB)",
		"Text files are sent without compression":         "/app.js (40 KB)",
		"Site isn't served over HTTP/2 or HTTP/3":         "1 of 7 requests",
		"Static files aren't cached by browsers for long": "/app.js (max-age 10m)",
		"Heavy third party: cdn.tracker.example":          "cdn.tracker.example adds 300 KB and 1 request to 1 of 1 pages",
	}
	for title, detail := range want {
		i, ok := got[title]
		if !ok {
			t.Errorf("missing %q", title)
			continue
		}
		if !strings.Contains(i.Detail, detail) {
			t.Errorf("%s: detail %q lacks %q", title, i.Detail, detail)
		}
		if i.Fix == "" || len(i.Pages) == 0 || (i.Level != model.Notice && i.Level != model.Warning) {
			t.Errorf("%s: %+v", title, i)
		}
	}
	for title, i := range got {
		if _, ok := want[title]; !ok {
			t.Errorf("unexpected %q: %s", title, i.Detail)
		}
		for _, bad := range []string{"lazy.jpg", "icon.png", "used.css", "tiny.js", "fonts.example"} {
			if strings.Contains(i.Detail, bad) && (bad != "tiny.js" || title != "Static files aren't cached by browsers for long") {
				t.Errorf("%s mentions %s: %s", title, bad, i.Detail)
			}
		}
	}
	// The uncompressed HTML (20 KB, no content-encoding) is reported too.
	if !strings.Contains(got["Text files are sent without compression"].Detail, "/ (HTML)") {
		t.Errorf("html compression: %s", got["Text files are sent without compression"].Detail)
	}
	// One issue per cause, not per page: a second page doubles the counts,
	// not the issues.
	other := home
	other.Path = "/b/"
	f["/b/"] = f["/"]
	got2 := testSite([]model.Page{home, other}, f).suggestions()
	if len(got2) != len(got) {
		t.Errorf("%d issues for 2 pages, %d for 1", len(got2), len(got))
	}
	for _, i := range got2 {
		if len(i.Pages) != 2 {
			t.Errorf("%s: pages %v", i.Title, i.Pages)
		}
	}
	// Plain http sites aren't told to use HTTP/2.
	s := testSite([]model.Page{home}, f)
	s.base, _ = url.Parse("http://shop.example/")
	if _, ok := issues(s)["Site isn't served over HTTP/2 or HTTP/3"]; ok {
		t.Error("http site told to use HTTP/2")
	}
}

func TestCacheLifetime(t *testing.T) {
	for in, want := range map[string]int{
		"":                                  0,
		"public, max-age=31536000":          31536000,
		"max-age=60, s-maxage=86400":        86400,
		"no-cache, max-age=3600":            0,
		"private, no-store":                 0,
		`max-age="600"`:                     600,
		"public, max-age=604800, immutable": 604800,
	} {
		if got := cacheLifetime(in); got != want {
			t.Errorf("%q: %d, want %d", in, got, want)
		}
	}
}

func TestCheckSiteUsesLiveFacts(t *testing.T) {
	m := &Module{}
	run := &model.Run{Pages: []model.Page{{Path: "/", Status: 200, Resources: []model.Resource{res("/big.jpg", "Image", 500<<10)}}}}
	m.facts = map[*model.Run]map[string]*facts{run: {"/": {Viewport: 900, DPR: 1, Images: []picture{
		{Src: "https://shop.example/big.jpg", Natural: 4000, NaturalH: 3000, Width: 400, Height: 300},
	}}}}
	u, _ := url.Parse("https://shop.example/")
	cc := &check.Context{Base: u, Run: run}
	m.CheckSite(context.Background(), cc)
	if len(run.Issues) != 1 || run.Issues[0].Title != "Images are much larger than they're displayed" || run.Issues[0].Key == "" {
		t.Errorf("site issues = %+v", run.Issues)
	}
	if m.facts[run] != nil {
		t.Error("facts kept after the run")
	}
}

func TestAmount(t *testing.T) {
	for _, c := range []struct {
		pct   float64
		worse bool
		want  string
	}{{40, true, "40%"}, {99.6, true, "100%"}, {100, true, "2×"}, {150, true, "2.5×"}, {1394, true, "15×"}, {75, false, "75%"}} {
		if got := amount(c.pct, c.worse); got != c.want {
			t.Errorf("amount(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}
