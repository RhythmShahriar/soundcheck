package speed

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
)

func needBrowser(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
}

// noisyJPEG makes an image that doesn't compress well, like a photo.
func noisyJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // test pixels
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x + r.IntN(60)), uint8(y + r.IntN(60)), uint8(r.IntN(255)), 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// slowSite is an unoptimized WordPress-like page: a photo far larger than
// it's shown, an image below the fold without lazy-loading, a
// render-blocking stylesheet that's mostly unused, an uncompressed script
// and a heavy plugin script in the head. When slow is set, the page takes
// 900 ms to generate, the way a plugin update with a slow query would.
func slowSite(t *testing.T, slow *atomic.Bool) *httptest.Server {
	t.Helper()
	big, below := noisyJPEG(t, 1600, 1200), noisyJPEG(t, 300, 200)
	var css strings.Builder
	css.WriteString("body{margin:0;font-family:sans-serif}h1{font-size:2em}\n")
	for i := range 400 {
		fmt.Fprintf(&css, ".unused-%d{color:#%06x;padding:%dpx;margin:%dpx}\n", i, i*997, i%13, i%7)
	}
	var plugin strings.Builder
	plugin.WriteString("window.heavyReady = true;\n")
	for i := range 3000 {
		fmt.Fprintf(&plugin, "function heavyWidget%d(a, b) { var s = 0; for (var i = 0; i < a; i++) { s += i * b + %d; } return s; }\n", i, i)
	}
	app := "document.documentElement.dataset.app = 'ready';\n" + strings.Repeat("// padding so the file is over a kilobyte\n", 60)

	serve := func(typ string, body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", typ)
			_, _ = w.Write(body)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, _ *http.Request) {
		if slow.Load() {
			time.Sleep(900 * time.Millisecond)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width"><title>Slow shop</title>
<link rel="stylesheet" href="/style.css">
<script src="/wp-content/plugins/heavy/heavy.js?ver=1.0"></script>
</head><body><h1>Fresh coffee</h1>
<img src="/big.jpg" width="200" height="150" alt="Beans">
<div style="height:2400px">Scroll down</div>
<img src="/below.jpg" width="300" height="200" alt="Grinder">
<script src="/app.js" defer></script>
</body></html>`)
	})
	mux.HandleFunc("/style.css", serve("text/css", []byte(css.String())))
	mux.HandleFunc("/wp-content/plugins/heavy/heavy.js", serve("application/javascript", []byte(plugin.String())))
	mux.HandleFunc("/app.js", serve("application/javascript", []byte(app)))
	mux.HandleFunc("/big.jpg", serve("image/jpeg", big))
	mux.HandleFunc("/below.jpg", serve("image/jpeg", below))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSpeedInBrowser(t *testing.T) {
	needBrowser(t)
	var slow atomic.Bool
	srv := slowSite(t, &slow)
	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 1
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"speed"}
	ctx := context.Background()
	m := New()
	run := func(paths []string) *model.Run {
		t.Helper()
		r, err := runner.Run(ctx, runner.Options{Config: cfg, Target: srv.URL, Paths: paths, Dir: t.TempDir(), Version: "test",
			Modules: []check.Module{m}, Browser: browser.Options{NoDownload: true}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	before := run(nil)
	if len(before.Pages) != 1 || before.Pages[0].Error != "" {
		t.Fatalf("pages = %+v", before.Pages)
	}
	home := before.Pages[0]
	for _, r := range home.Resources {
		if strings.Contains(r.URL, "heavy.js") && (r.Unused == 0 || r.Unused*2 < r.Bytes) {
			t.Errorf("heavy.js coverage: %d of %d bytes unused", r.Unused, r.Bytes)
		}
		if strings.Contains(r.URL, "style.css") && r.Unused*2 < r.Bytes {
			t.Errorf("style.css coverage: %d of %d bytes unused", r.Unused, r.Bytes)
		}
	}
	got := map[string]model.Issue{}
	for _, i := range before.Issues {
		got[i.Title] = i
	}
	for title, want := range map[string]string{
		"Images are much larger than they're displayed": "/big.jpg (1600×1200 shown at 200×150",
		"Images below the fold aren't lazy-loaded":      "/below.jpg",
		"CSS and JavaScript block the first render":     "/wp-content/plugins/heavy/heavy.js",
		"CSS files are mostly unused":                   "/style.css",
		"JavaScript files are mostly unused":            "/wp-content/plugins/heavy/heavy.js",
		"Text files are sent without compression":       "/app.js",
		"Plugin heavy adds weight to pages":             "Plugin heavy adds",
	} {
		i, ok := got[title]
		if !ok {
			t.Errorf("missing %q", title)
			continue
		}
		if !strings.Contains(i.Detail, want) {
			t.Errorf("%s: %q lacks %q", title, i.Detail, want)
		}
	}
	if !strings.Contains(got["CSS and JavaScript block the first render"].Detail, "/style.css") {
		t.Errorf("render-blocking CSS missing: %s", got["CSS and JavaScript block the first render"].Detail)
	}
	for _, i := range before.Issues { // shown with -v or on failure
		t.Logf("%s %s: %s", i.Level, i.Title, i.Detail)
	}
	t.Logf("metrics %+v", home.Metrics)

	slow.Store(true)
	after := run([]string{"/"})
	cmp := compare.Compare(ctx, &check.CompareContext{Config: cfg, Before: before, After: after}, []check.Module{m}, "test")
	var ttfb *model.Change
	for i, c := range cmp.Changes {
		t.Logf("%s %s %v: %s → %s", c.Class, c.Title, c.Pages, c.Before, c.After)
		if c.Check == "speed/ttfb" {
			ttfb = &cmp.Changes[i]
		}
	}
	if ttfb == nil || ttfb.Class != model.Slower || strings.Join(ttfb.Pages, ",") != "/" || ttfb.Before == "" || ttfb.After == "" || ttfb.Score <= 25 {
		t.Errorf("ttfb change = %+v (all %+v)", ttfb, cmp.Changes)
	}
}

// TestMetricsInParallelTabs loads pages in four tabs at once, the way a
// real run does. Every page must report paint metrics (Chrome doesn't
// paint hidden tabs), the main document must count toward weight, and
// TBT must follow Lighthouse: long tasks after first paint count, a script
// that blocks before it delays FCP instead.
func TestMetricsInParallelTabs(t *testing.T) {
	needBrowser(t)
	const busy = `function busy(ms) { var t = Date.now(); while (Date.now() - t < ms) {} }`
	page := func(head, body string) string {
		return `<!doctype html><html><head><title>Page</title><script>` + busy + `</script>` + head + `</head><body><h1>Coffee</h1>
<p>` + strings.Repeat("Fresh beans roasted daily. ", 40) + `</p>` + body + `</body></html>`
	}
	afterPaint := `<script>requestAnimationFrame(function () { setTimeout(function () { busy(300); }, 0); });</script>`
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, _ *http.Request) {
		links := ""
		for i := range 6 {
			links += fmt.Sprintf(`<a href="/p%d/">Page %d</a> `, i, i)
		}
		fmt.Fprint(w, page("", links+`<a href="/blocking/">Blocking</a>`+afterPaint))
	})
	mux.HandleFunc("/{page}/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, page("", afterPaint)) })
	mux.HandleFunc("/blocking/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, page("<script>busy(400);</script>", ""))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.Rate, cfg.Tabs = 0, 4
	cfg.Viewports = []string{"desktop"}
	cfg.Modules = []string{"speed"}
	cfg.Pages.PerGroup = -1
	run, err := runner.Run(context.Background(), runner.Options{Config: cfg, Target: srv.URL, Dir: t.TempDir(), Version: "test",
		Modules: []check.Module{New()}, Browser: browser.Options{NoDownload: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Pages) != 8 {
		t.Fatalf("visited %d pages", len(run.Pages))
	}
	for _, p := range run.Pages {
		m := p.Metrics
		t.Logf("%s %+v", p.Path, m)
		if m.FCP <= 0 || m.LCP <= 0 {
			t.Errorf("%s: no paint metrics (FCP %v, LCP %v): was the tab hidden?", p.Path, m.FCP, m.LCP)
		}
		if m.Requests < 1 || m.Bytes < 1000 {
			t.Errorf("%s: the HTML isn't counted: %d bytes in %d requests", p.Path, m.Bytes, m.Requests)
		}
		if p.Path == "/blocking/" {
			if m.TBT > 100 || m.FCP < 350 {
				t.Errorf("%s: a script before first paint should delay FCP, not add TBT: FCP %v, TBT %v", p.Path, m.FCP, m.TBT)
			}
		} else if m.TBT < 100 || m.TBT > 500 {
			// About 250 ms, but loaded machines (CI runners) land anywhere in
			// 100–180 ms of that: the reason for the 250 ms noise floor.
			t.Errorf("%s: a 300 ms task after first paint gave TBT %v, want about 250", p.Path, m.TBT)
		}
	}
}
