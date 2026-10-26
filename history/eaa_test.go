// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package history

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

// eaaRun is an --eaa run with the given open problems: label issues on
// many pages (counted once), a contrast problem per extra n and a
// keyboard trap.
func eaaRun(at time.Time, name string, contrast int) *model.Run {
	run := sampleRun(at, name, 1200, 1400)
	run.Options = model.Options{EAA: true, WCAG: "2.1"}
	label := model.Issue{Check: "a11y/label", Level: model.Warning, Title: "Form elements must have labels", Source: "#q",
		WCAG: []string{"1.3.1", "4.1.2"}}
	trap := model.Issue{Check: "keyboard/trap", Level: model.Error, Title: "Keyboard focus is trapped", Source: "#card",
		WCAG: []string{"2.1.2"}}
	for i := range run.Pages {
		run.Pages[i].Issues = append(run.Pages[i].Issues, label)
	}
	run.Pages[1].Issues = append(run.Pages[1].Issues, trap,
		model.Issue{Check: "health/console", Level: model.Warning, Title: "Console error", WCAG: []string{"9.9.9"}})
	for i := range contrast {
		run.Pages[0].Issues = append(run.Pages[0].Issues, model.Issue{Check: "a11y/color-contrast", Level: model.Warning,
			Title: "Elements must meet minimum color contrast ratio thresholds", Source: "#c" + string(rune('a'+i)), WCAG: []string{"1.4.3"}})
	}
	return run
}

// Append from an --eaa run records the a11y and keyboard counts and the
// criteria; a run without the modules leaves them out (not 0).
func TestAppendEAACounts(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, eaaRun(t0, "eaa-1", 3), nil); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, sampleRun(t0.Add(time.Hour), "", 1000), nil); err != nil {
		t.Fatal(err)
	}
	clean := eaaRun(t0.Add(2*time.Hour), "", 0)
	for i := range clean.Pages {
		clean.Pages[i].Issues = nil
	}
	if err := Append(root, clean, nil); err != nil {
		t.Fatal(err)
	}
	entries, err := Read(root)
	if err != nil || len(entries) != 3 {
		t.Fatalf("read %d entries: %v", len(entries), err)
	}
	e := entries[0]
	if e.A11yOpen == nil || *e.A11yOpen != 4 || e.KeyboardOpen == nil || *e.KeyboardOpen != 1 {
		t.Errorf("counts a11y %v keyboard %v, want 4 and 1", e.A11yOpen, e.KeyboardOpen)
	}
	want := map[string]int{"1.3.1": 1, "4.1.2": 1, "2.1.2": 1, "1.4.3": 3}
	if len(e.A11yByCriterion) != len(want) {
		t.Errorf("by criterion %v, want %v", e.A11yByCriterion, want)
	}
	for id, n := range want {
		if e.A11yByCriterion[id] != n {
			t.Errorf("by criterion %v, want %v", e.A11yByCriterion, want)
		}
	}
	if e := entries[1]; e.A11yOpen != nil || e.KeyboardOpen != nil || e.A11yByCriterion != nil {
		t.Errorf("a run without the modules has counts: %+v", e)
	}
	data, _ := os.ReadFile(filepath.Join(root, File))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if strings.Contains(lines[1], "a11y") || strings.Contains(lines[1], "keyboard_open") {
		t.Errorf("plain run line names the counts: %s", lines[1])
	}
	if !strings.Contains(lines[2], `"a11y_open":0`) || !strings.Contains(lines[2], `"keyboard_open":0`) || strings.Contains(lines[2], "a11y_by_criterion") {
		t.Errorf("clean --eaa run line: want 0 counts and no criteria: %s", lines[2])
	}
}

// --modules a11y alone records a11y_open and no keyboard count.
func TestEAACountsByModule(t *testing.T) {
	run := eaaRun(t0, "", 1)
	run.Options = model.Options{Modules: []string{"health", "a11y"}}
	e := New(run, nil)
	if e.A11yOpen == nil || *e.A11yOpen != 2 || e.KeyboardOpen != nil {
		t.Errorf("a11y only: a11y %v keyboard %v", e.A11yOpen, e.KeyboardOpen)
	}
}

// eaaEntries are four --eaa entries with a plain run between them.
func eaaEntries() []Entry {
	var out []Entry
	for i, n := range []int{30, 20, -1, 22, 3} {
		at := t0.Add(time.Duration(i) * 7 * 24 * time.Hour)
		if n < 0 {
			out = append(out, New(sampleRun(at, "", 1300, 1500), nil))
			continue
		}
		var c *model.Comparison
		name := "eaa-1"
		if i > 0 {
			c = &model.Comparison{Before: model.RunRef{Name: "eaa-1"}}
			name = ""
		}
		out = append(out, New(eaaRun(at, name, n), c))
	}
	return out
}

// history --metric a11y and keyboard match their golden files, pretty
// and plain; so do the entries' JSON lines.
func TestRenderA11yGolden(t *testing.T) {
	time.Local = time.UTC
	for _, tc := range []struct {
		name, metric string
		plain        bool
	}{
		{"render-a11y.golden.txt", "a11y", false},
		{"render-a11y-plain.golden.txt", "a11y", true},
		{"render-keyboard.golden.txt", "keyboard", false},
	} {
		var b bytes.Buffer
		if err := Render(&b, eaaEntries(), "", tc.metric, tc.plain); err != nil {
			t.Fatal(err)
		}
		golden(t, tc.name, b.String())
		if tc.plain {
			for _, r := range b.String() {
				if r > 127 {
					t.Fatalf("plain output has %q:\n%s", r, b.String())
				}
			}
		}
	}
	var b bytes.Buffer
	for _, e := range eaaEntries() {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(append(line, '\n'))
	}
	golden(t, "history-eaa.golden.jsonl", b.String())
	var out bytes.Buffer
	_ = Render(&out, eaaEntries(), "", "a11y", true)
	if !strings.Contains(out.String(), "A11y open  31 to 4 (-87%)\n") ||
		!strings.Contains(out.String(), "Worst criteria now: 1.4.3 Contrast (3), 1.3.1 Info and Relationships (1), 2.1.2 No Keyboard Trap (1)\n") {
		t.Errorf("footer:\n%s", out.String())
	}
}

// --page is for timings: the counts are for the whole site.
func TestRenderA11yPage(t *testing.T) {
	err := Render(&bytes.Buffer{}, eaaEntries(), "/p0/", "a11y", false)
	if err == nil || !strings.Contains(err.Error(), "--page works with lcp, tbt and ttfb") {
		t.Errorf("a11y with --page: %v", err)
	}
	if v := eaaEntries()[0].Value("a11y", "/p0/"); !math.IsNaN(v) {
		t.Errorf("per-page a11y value = %v, want NaN", v)
	}
}

// A criterion id that isn't numbers and dots never reaches the
// terminal.
func TestWorstCriteriaHostile(t *testing.T) {
	n := 2
	e := Entry{A11yOpen: &n, A11yByCriterion: map[string]int{"\x1b[2J": 9, "1.4.3": 2}}
	if got := worstCriteria([]Entry{e}, 3); got != "1.4.3 Contrast (2)" {
		t.Errorf("worst = %q", got)
	}
}

// A history written by 1.2 loads and renders with every metric.
func TestReadHistory12(t *testing.T) {
	time.Local = time.UTC
	root := t.TempDir()
	data, err := os.ReadFile(filepath.Join("testdata", "history-1.2.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, File), data, 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := Read(root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("read %d entries: %v", len(entries), err)
	}
	for _, m := range Metrics {
		var b bytes.Buffer
		if err := Render(&b, entries, "", m, true); err != nil {
			t.Errorf("%s: %v", m, err)
		}
	}
	var b bytes.Buffer
	_ = Render(&b, entries, "", "lcp", true)
	if !strings.Contains(b.String(), "94 ms to 120 ms") {
		t.Errorf("1.2 lcp:\n%s", b.String())
	}
}
