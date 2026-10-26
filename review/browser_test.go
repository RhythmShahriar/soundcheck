// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package review

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
)

// TestReviewPageInChrome clicks the injected buttons in a real browser:
// each bar must sit on its own change and post to the server.
func TestReviewPageInChrome(t *testing.T) {
	if testing.Short() {
		t.Skip("needs Chrome")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	rec := &recorder{}
	s, _ := startServer(t, sampleResult(sampleChanges()), rec)
	page := b.Timeout(testenv.Timeout(20 * time.Second)).MustPage(s.URL())
	page.MustWaitLoad()
	bars := page.MustElements(".rv-bar")
	if len(bars) != 3 {
		t.Fatalf("%d review bars, want 3 (none on the fixed change)", len(bars))
	}
	if txt := page.MustElement("#rv-banner").MustText(); strings.Contains(txt, "off") {
		t.Fatalf("banner: %s", txt)
	}
	// The second bar belongs to the NEW php-error change.
	bars[1].MustElement(".rv-ignore").MustClick()
	bars[1].MustElement(".rv-state").MustWait(`() => this.textContent === "Ignored from now on"`)
	bars[0].MustElement(".rv-accept").MustClick()
	bars[0].MustElement(".rv-state").MustWait(`() => this.textContent === "Accepted"`)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	want := `ignore PHP Warning: Undefined array key "id"|accept Page returns HTTP 500`
	if got := strings.Join(rec.calls, "|"); got != want {
		t.Errorf("decide calls = %q, want %q", got, want)
	}
}

// TestReviewPageRefusesMismatch keeps the buttons off when the page and
// the changes on record don't line up.
func TestReviewPageRefusesMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("needs Chrome")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	s, _ := startServer(t, sampleResult(sampleChanges()), &recorder{})
	s.page = []byte(strings.Replace(string(s.page), "Page returns HTTP 500</h3>", "Something else</h3>", 1))
	page := b.Timeout(testenv.Timeout(20 * time.Second)).MustPage(s.URL())
	page.MustWaitLoad()
	if n := len(page.MustElements(".rv-bar")); n != 0 {
		t.Errorf("%d review bars on a mismatched page, want 0", n)
	}
	if txt := page.MustElement("#rv-banner").MustText(); !strings.Contains(txt, "Review buttons are off") {
		t.Errorf("banner: %s", txt)
	}
}

// TestReviewPagePairsByID puts each bar on the article whose data-id is
// its change's id, so a page that lists the changes differently (here:
// without the fixed one) still gets every button on the right change.
func TestReviewPagePairsByID(t *testing.T) {
	if testing.Short() {
		t.Skip("needs Chrome")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	changes := sampleChanges()
	for i := range changes {
		changes[i].ID = fmt.Sprintf("c%d", i+1)
	}
	rec := &recorder{}
	s, _ := startServer(t, sampleResult(changes), rec)
	if !strings.Contains(string(s.page), `data-id="c3"`) {
		t.Fatal("the report's articles have no data-id")
	}
	s.page = regexp.MustCompile(`(?s)<article[^>]*data-kind="fixed".*?</article>`).ReplaceAll(s.page, nil)
	page := b.Timeout(testenv.Timeout(20 * time.Second)).MustPage(s.URL())
	page.MustWaitLoad()
	if txt := page.MustElement("#rv-banner").MustText(); strings.Contains(txt, "off") {
		t.Fatalf("banner: %s", txt)
	}
	for _, art := range page.MustElements("article.item") {
		id := *art.MustAttribute("data-id")
		if got := art.MustElement(".rv-id").MustText(); got != id {
			t.Errorf("article %s carries the bar of %s", id, got)
		}
	}
	page.MustElement(`article[data-id="c3"] .rv-accept`).MustClick()
	page.MustElement(`article[data-id="c3"] .rv-state`).MustWait(`() => this.textContent === "Accepted"`)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if got := strings.Join(rec.calls, "|"); got != "accept Page returns HTTP 500" {
		t.Errorf("decide calls = %q", got)
	}
}

// startServerOn is startServer on the first free port of lo to hi,
// the range these fixtures may use.
func startServerOn(t *testing.T, res report.Result, rec *recorder, lo, hi int) *server {
	t.Helper()
	s, err := newServer(res, rec.decide)
	if err != nil {
		t.Fatal(err)
	}
	for port := lo; port <= hi; port++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		ts := httptest.NewUnstartedServer(s)
		_ = ts.Listener.Close()
		ts.Listener = l
		ts.Start()
		t.Cleanup(ts.Close)
		s.host = l.Addr().String()
		return s
	}
	t.Fatalf("no free port in %d-%d", lo, hi)
	return nil
}

// TestReviewEAAPageInChrome reviews an --eaa comparison under the
// review page's CSP: the report's script runs the tabs, the toolbar's
// adds its buttons only to the result cards (not to the Accessibility
// tab's list) and a decision still reaches the server.
func TestReviewEAAPageInChrome(t *testing.T) {
	if testing.Short() {
		t.Skip("needs Chrome")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	b, err := browser.Launch(context.Background(), browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	changes := append(sampleChanges(), model.Change{Class: model.Broke, Check: "keyboard/trap", Title: "Keyboard focus is trapped",
		Source: "#card", Pages: []string{"/checkout/"}, WCAG: []string{"2.1.2"}, EN301549: []string{"9.2.1.2"}})
	res := sampleResult(changes)
	res.Comparison.Complete()
	res.Comparison.EAA = &model.EAASummary{WCAG: "2.1", EvidenceNote: model.EvidenceNote, Open: 1,
		Criteria: model.EAACriteria{Failed: []model.CriterionCount{{ID: "2.1.2", Issues: 1, Pages: 1}}}}
	rec := &recorder{}
	s := startServerOn(t, res, rec, 37090, 37099)
	page := b.Timeout(testenv.Timeout(20 * time.Second)).MustPage(s.URL())
	page.MustWaitLoad()
	if txt := page.MustElement("#rv-banner").MustText(); strings.Contains(txt, "off") {
		t.Fatalf("banner: %s", txt)
	}
	if n := len(page.MustElements(".rv-bar")); n != 4 {
		t.Fatalf("%d review bars, want 4", n)
	}
	page.MustElement("#tab-a11y").MustClick()
	if page.MustElement("#panel-a11y").MustAttribute("hidden") != nil || page.MustElement("#panel-results").MustAttribute("hidden") == nil {
		t.Fatal("the Accessibility tab didn't open: the report's script was blocked")
	}
	page.MustElement(`#panel-a11y a[data-goto]`).MustClick()
	if page.MustElement("#panel-results").MustAttribute("hidden") != nil {
		t.Fatal("the link to the result didn't show the Results tab")
	}
	var trap string
	for _, c := range res.Comparison.Changes {
		if c.Check == "keyboard/trap" {
			trap = c.ID
		}
	}
	page.MustElement(`article[data-id="` + trap + `"] .rv-accept`).MustClick()
	page.MustElement(`article[data-id="` + trap + `"] .rv-state`).MustWait(`() => this.textContent === "Accepted"`)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if got := strings.Join(rec.calls, "|"); got != "accept Keyboard focus is trapped" {
		t.Errorf("decide calls = %q", got)
	}
}
