// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// sampleRun has pages with the given LCPs; a zero LCP isn't measured and
// a negative one is a page that failed to load.
func sampleRun(at time.Time, name string, lcps ...float64) *model.Run {
	r := &model.Run{Schema: model.Schema, Version: "1.2.0", Target: "http://brew.test/", Name: name, Started: at}
	for i, l := range lcps {
		p := model.Page{Path: fmt.Sprintf("/p%d/", i), Metrics: model.Metrics{LCP: l, TBT: float64(10 * i), TTFB: 100 + float64(i)}}
		if l < 0 {
			p.Error, p.Metrics = "net::ERR_CONNECTION_REFUSED", model.Metrics{}
		}
		r.Pages = append(r.Pages, p)
	}
	return r
}

func TestNew(t *testing.T) {
	run := sampleRun(t0, "good", 1000, 3000, 0, 2000, -1)
	run.Pages[0].Issues = []model.Issue{{Check: "health/php-error", Level: model.Warning, Title: "W", Key: "k1"}}
	run.Pages[1].Issues = []model.Issue{{Check: "health/php-error", Level: model.Warning, Title: "W", Key: "k1"},
		{Check: "health/status", Level: model.Error, Title: "500"}}
	e := New(run, nil)
	if e.Kind != KindSnapshot || e.Name != "good" || e.Pages != 5 || e.Verdict != Errors || e.Schema != Schema {
		t.Errorf("entry = %+v", e)
	}
	if e.Issues["warning"] != 1 || e.Issues["error"] != 1 {
		t.Errorf("issues = %v, want the warning on two pages counted once", e.Issues)
	}
	// LCP 1000, 2000, 3000 (0 unmeasured, failed page left out); TBT 0, 10, 20, 30.
	if e.LCP != 2000 || e.TBT != 15 || e.TTFB != 101.5 {
		t.Errorf("medians lcp %v tbt %v ttfb %v", e.LCP, e.TBT, e.TTFB)
	}
	if len(e.PageSet) != 4 || e.PageSet[3].Path != "/p3/" {
		t.Errorf("page metrics = %+v", e.PageSet)
	}

	c := &model.Comparison{Before: model.RunRef{Name: "good"}, Changes: []model.Change{{Class: model.Slower}, {Class: model.Fixed}}}
	after := sampleRun(t0.Add(time.Hour), "", 1500)
	if e := New(after, c); e.Kind != KindComparison || e.Name != "good" || e.Verdict != Changed || e.Changes[model.Slower] != 1 {
		t.Errorf("comparison entry = %+v", e)
	}
	c.Changes = append(c.Changes, model.Change{Class: model.Broke})
	if e := New(after, c); e.Verdict != Broke {
		t.Errorf("verdict = %s, want broke", e.Verdict)
	}
	if e := New(after, nil); e.Kind != KindRun || e.Verdict != Clean {
		t.Errorf("run entry = %+v", e)
	}

	lcps := make([]float64, 80)
	for i := range lcps {
		lcps[i] = 1000
	}
	if e := New(sampleRun(t0, "", lcps...), nil); len(e.PageSet) != MaxPages {
		t.Errorf("%d page metrics, want %d", len(e.PageSet), MaxPages)
	}
}

func TestAppendConcurrent(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".soundcheck")
	lcps := make([]float64, MaxPages) // long lines, several KB each
	for i := range lcps {
		lcps[i] = 1234.5
	}
	const each = 40
	var wg sync.WaitGroup
	errs := make(chan error, 2*each)
	for g := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				run := sampleRun(t0.Add(time.Duration(g*each+i)*time.Minute), fmt.Sprintf("g%d-%d", g, i), lcps...)
				if err := Append(root, run, nil); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(root, File))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	n := 0
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %d isn't JSON: %v", n+1, err)
		}
		n++
	}
	if n != 2*each {
		t.Errorf("%d lines, want %d", n, 2*each)
	}
	entries, err := Read(root)
	if err != nil || len(entries) != 2*each || !entries[0].At.Equal(t0) {
		t.Errorf("Read: %d entries, %v", len(entries), err)
	}
}

func TestReadSkipsTornLines(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, sampleRun(t0, "a", 1000), nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, File), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"schema":"rhythm-soundcheck/history/v1","at":"2026-` + "\n" + `{"schema":"other"}` + "\n")
	_ = f.Close()
	if entries, err := Read(root); err != nil || len(entries) != 1 {
		t.Errorf("Read = %d entries, %v; want the one good line", len(entries), err)
	}
	if entries, err := Read(filepath.Join(root, "nosuch")); err != nil || entries != nil {
		t.Errorf("missing history: %v, %v", entries, err)
	}
}

func TestBackfill(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".soundcheck")
	save := func(dir string, run *model.Run, c *model.Comparison) {
		t.Helper()
		if err := store.SaveRun(dir, run); err != nil {
			t.Fatal(err)
		}
		if c != nil {
			if err := store.SaveComparison(dir, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	good := sampleRun(t0, "good", 1000)
	save(filepath.Join(root, "snapshots", "good"), good, nil)
	save(filepath.Join(root, "runs", "2026-10-02T09-00-00"), sampleRun(t0.Add(24*time.Hour), "", 1500),
		&model.Comparison{Before: model.RunRef{Name: "good"}, Changes: []model.Change{{Class: model.Slower}}})
	save(filepath.Join(root, "runs", "2026-10-03T09-00-00"), sampleRun(t0.Add(48*time.Hour), "", 1100), nil)
	old := sampleRun(t0.Add(72*time.Hour), "", 900)
	old.Schema = "rhythm-soundcheck/v0"
	save(filepath.Join(root, "runs", "2026-10-04T09-00-00"), old, nil)
	// Already in the history: the snapshot, and a run since pruned.
	if err := Append(root, good, nil); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, sampleRun(t0.Add(-24*time.Hour), "", 800), nil); err != nil {
		t.Fatal(err)
	}

	n, err := Backfill(root)
	if err != nil || n != 2 {
		t.Fatalf("Backfill = %d, %v; want 2 added", n, err)
	}
	entries, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, fmt.Sprintf("%s:%s:%.0f", e.At.Format("01-02"), e.Kind, e.LCP))
	}
	want := "09-30:run:800 10-01:snapshot:1000 10-02:comparison:1500 10-03:run:1100"
	if strings.Join(got, " ") != want {
		t.Errorf("history = %s\nwant      %s", strings.Join(got, " "), want)
	}
	if n, err := Backfill(root); err != nil || n != 0 {
		t.Errorf("second Backfill = %d, %v; want nothing new", n, err)
	}
	if n, err := Backfill(filepath.Join(t.TempDir(), "empty")); err != nil || n != 0 {
		t.Errorf("Backfill of an empty store = %d, %v", n, err)
	}
}

func sampleEntries() []Entry {
	var out []Entry
	for i, l := range []float64{1200, 1250, 0, 1600, 1900} {
		second := l + 400
		if l == 0 { // a week where LCP wasn't measured
			second = 0
		}
		run := sampleRun(t0.Add(time.Duration(i)*7*24*time.Hour), "", l, second)
		for j := range run.Pages {
			run.Pages[j].Metrics.TBT += float64(i * 40)
		}
		var c *model.Comparison
		switch i {
		case 0:
			run.Name = "before-october-updates"
		case 3:
			c = &model.Comparison{Before: model.RunRef{Name: "before-october-updates"}, Changes: []model.Change{{Class: model.Slower}}}
		}
		if i == 4 {
			run.Pages[0].Issues = []model.Issue{{Check: "health/status", Level: model.Error, Title: "500"}}
		}
		out = append(out, New(run, c))
	}
	return out
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err, " (run go test -update to create it)")
	}
	if got != string(want) {
		t.Errorf("%s differs:\n%s\nwant:\n%s", name, got, want)
	}
}

func TestRenderGolden(t *testing.T) {
	time.Local = time.UTC
	var site, page, tbt bytes.Buffer
	if err := Render(&site, sampleEntries(), "", ""); err != nil {
		t.Fatal(err)
	}
	golden(t, "render.golden.txt", site.String())
	if err := Render(&page, sampleEntries(), "/p1/", "lcp"); err != nil {
		t.Fatal(err)
	}
	golden(t, "render-page.golden.txt", page.String())
	if err := Render(&tbt, sampleEntries(), "", "tbt"); err != nil {
		t.Fatal(err)
	}
	golden(t, "render-tbt.golden.txt", tbt.String())
}

func TestRenderErrors(t *testing.T) {
	if err := Render(io.Discard, sampleEntries(), "", "cls"); err == nil || !strings.Contains(err.Error(), "want lcp, tbt or ttfb") {
		t.Errorf("unknown metric: %v", err)
	}
	if err := Render(io.Discard, sampleEntries(), "/nosuch/", "lcp"); err == nil || !strings.Contains(err.Error(), "/nosuch/") {
		t.Errorf("unknown page: %v", err)
	}
	var b bytes.Buffer
	if err := Render(&b, nil, "", ""); err != nil || !strings.Contains(b.String(), "No history yet") {
		t.Errorf("empty history: %q, %v", b.String(), err)
	}
}

func TestSparkline(t *testing.T) {
	nan := math.NaN()
	for _, tc := range []struct {
		in   []float64
		want string
	}{
		{[]float64{1, 2, 3, 4, 5, 6, 7, 8}, "▁▂▃▄▅▆▇█"},
		{[]float64{5, 5, 5}, "▅▅▅"},
		{[]float64{0, nan, 10}, "▁ █"},
		{nil, ""},
		{[]float64{nan}, " "},
	} {
		if got := Sparkline(tc.in); got != tc.want {
			t.Errorf("Sparkline(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSVG(t *testing.T) {
	nan := math.NaN()
	for _, in := range [][]float64{{1200, 1300, nan, 1250, 1900}, {5}, {}, {nan, nan}, {3, 3}, {math.Inf(1), 2, 4}} {
		s := string(SVG(in))
		d := xml.NewDecoder(strings.NewReader(s))
		d.Strict = true
		elems := map[string]int{}
		for {
			tok, err := d.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("SVG(%v) isn't well-formed XML: %v\n%s", in, err, s)
			}
			if se, ok := tok.(xml.StartElement); ok {
				elems[se.Name.Local]++
			}
		}
		if elems["svg"] != 1 || strings.Contains(s, "NaN") || strings.Contains(s, "Inf") {
			t.Errorf("SVG(%v) = %s", in, s)
		}
		if len(in) == 5 && (elems["polyline"] != 2 || elems["circle"] != 1) {
			t.Errorf("SVG with a gap: %v elements, want 2 polylines and a dot", elems)
		}
	}
}
