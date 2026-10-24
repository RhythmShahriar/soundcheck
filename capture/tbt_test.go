// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package capture

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
)

// A 300 ms long task after first paint adds about 250 ms of TBT, also
// when the observer hasn't delivered it by the time the page is read and
// when it starts only after the page went idle. Run with -count=10: it
// must hold every time.
func TestLateLongTasksCountInTBT(t *testing.T) {
	b := launch(t)
	const busy = `function busy(ms) { var t = Date.now(); while (Date.now() - t < ms) {} }`
	page := func(script string) string {
		return `<!doctype html><html><head><title>Tasks</title><script>` + busy + `</script></head><body><h1>Coffee</h1><p>` +
			strings.Repeat("Fresh beans roasted daily. ", 40) + `</p><script>` + script + `</script></body></html>`
	}
	mux := http.NewServeMux()
	// Right after first paint, from the paint entry itself.
	mux.HandleFunc("/after-paint", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, page(`new PerformanceObserver(l => { if (l.getEntries().some(e => e.name === 'first-contentful-paint')) setTimeout(() => busy(300), 0); }).observe({type: 'paint', buffered: true});`))
	})
	// About when the page is read: load, 700 ms of network idle and a
	// 300 ms settle.
	for _, ms := range []int{900, 1100} {
		mux.HandleFunc(fmt.Sprintf("/late-%d", ms), func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, page(fmt.Sprintf(`addEventListener('load', () => setTimeout(() => busy(300), %d));`, ms)))
		})
	}
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Timeout: 20 * time.Second}
	for _, path := range []string{"/after-paint", "/late-900", "/late-1100"} {
		tab, err := browser.NewTab(sess)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := Start(context.Background(), tab, opts)
		if err != nil {
			t.Fatal(err)
		}
		p, err := Visit(context.Background(), rec, srv.URL+path, opts)
		rec.Stop()
		_ = tab.Close()
		if err != nil {
			t.Fatal(err)
		}
		if m := p.Metrics; m.TBT < 200 || m.TBT > 500 {
			t.Errorf("%s: a 300 ms task after first paint gave TBT %v, want about 250 (FCP %v)", path, m.TBT, m.FCP)
		}
	}
}
