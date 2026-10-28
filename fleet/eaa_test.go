// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// golden compares got with testdata/name, or rewrites it under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err, " (run go test ./fleet -update to create it)")
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file (go test ./fleet -update rewrites it):\n%s", name, got)
	}
}

// eaa: true is a site key; anything else close to it is still refused.
func TestLoadEAA(t *testing.T) {
	sites, err := Load(writeSites(t, "- {name: shop, url: shop.test, eaa: true}\n- {name: blog, url: blog.test}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !sites[0].EAA || sites[1].EAA {
		t.Errorf("eaa = %v, %v", sites[0].EAA, sites[1].EAA)
	}
	for _, bad := range []string{"- {name: shop, url: shop.test, eaa: yes please}\n", "- {name: shop, url: shop.test, eea: true}\n"} {
		if _, err := Load(writeSites(t, bad)); err == nil {
			t.Errorf("%q loaded", bad)
		}
	}
}

// A site with eaa: true gets --eaa once, whether or not its args or the
// fleet's extra args already carry it; a plain site gets the fleet's.
func TestRunEAAArgs(t *testing.T) {
	for name, tc := range map[string]struct {
		sites string
		extra []string
		want  map[string][]string
	}{
		"site key": {
			"- {name: shop, url: http://ok.test/, eaa: true, args: [--limit, '5']}\n- {name: blog, url: http://ok.test/}\n", nil,
			map[string][]string{"shop": {"snapshot", "--json", "--limit", "5", "--eaa", "http://ok.test/"}, "blog": {"snapshot", "--json", "http://ok.test/"}},
		},
		"fleet flag": {
			"- {name: shop, url: http://ok.test/, eaa: true}\n- {name: blog, url: http://ok.test/}\n", []string{"--eaa", "--wcag", "2.2"},
			map[string][]string{"shop": {"snapshot", "--json", "--eaa", "--wcag", "2.2", "http://ok.test/"}, "blog": {"snapshot", "--json", "--eaa", "--wcag", "2.2", "http://ok.test/"}},
		},
		"site args": {
			"- {name: shop, url: http://ok.test/, eaa: true, args: [--eaa=true]}\n", nil,
			map[string][]string{"shop": {"snapshot", "--json", "--eaa=true", "http://ok.test/"}},
		},
	} {
		p := writeSites(t, tc.sites)
		if _, err := Run(context.Background(), os.Args[0], p, Snapshot, 2, tc.extra, &bytes.Buffer{}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for site, want := range tc.want {
			if got := readFile(t, filepath.Join(filepath.Dir(p), Folder, site, "args.txt")); got != strings.Join(want, "\n") {
				t.Errorf("%s: %s args =\n%s\nwant\n%s", name, site, got, strings.Join(want, "\n"))
			}
		}
	}
}

// fleet.json carries eaa_open and criteria_failed from each --eaa
// site's eaa block and a trend from the second fleet run on; a plain
// site has none of them. Each --eaa site links its statement draft.
func TestRunEAAColumns(t *testing.T) {
	p := writeSites(t, "- {name: shop, url: http://ok.test/, eaa: true}\n- {name: blog, url: http://ok.test/}\n")
	read := func() Page {
		var page Page
		if err := json.Unmarshal([]byte(readFile(t, filepath.Join(filepath.Dir(p), Folder, "fleet.json"))), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	site := func(page Page, name string) Result {
		for _, r := range page.Sites {
			if r.Site == name {
				return r
			}
		}
		t.Fatalf("no site %s", name)
		return Result{}
	}
	if _, err := Run(context.Background(), os.Args[0], p, Snapshot, 2, nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	shop := site(read(), "shop")
	if shop.EAAOpen == nil || *shop.EAAOpen != 9 || shop.CriteriaFailed == nil || *shop.CriteriaFailed != 2 || shop.EAATrend != "" {
		t.Errorf("first run shop = %+v", shop)
	}
	if shop.Statement != "shop/.soundcheck/snapshots/2026-10-24T10-00-00/eaa/statement-draft.md" {
		t.Errorf("statement = %q", shop.Statement)
	}
	if _, err := Run(context.Background(), os.Args[0], p, Snapshot, 2, nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	page := read()
	shop, blog := site(page, "shop"), site(page, "blog")
	if *shop.EAAOpen != 7 || shop.EAATrend != TrendDown || shop.EAABefore == nil || *shop.EAABefore != 9 {
		t.Errorf("second run shop = %+v", shop)
	}
	if blog.EAAOpen != nil || blog.CriteriaFailed != nil || blog.EAATrend != "" || blog.Statement != "" {
		t.Errorf("plain site has a11y fields: %+v", blog)
	}
	raw := readFile(t, filepath.Join(filepath.Dir(p), Folder, "fleet.json"))
	// eaa_open counts keyboard issues too, so it isn't named like
	// history.jsonl's a11y_open, which doesn't.
	if strings.Count(raw, `"eaa_open"`) != 1 || !strings.Contains(raw, `"eaa_trend":"down"`) || strings.Contains(raw, "a11y_") {
		t.Errorf("fleet.json:\n%s", raw)
	}
	html := readFile(t, filepath.Join(filepath.Dir(p), Folder, "fleet.html"))
	if !strings.Contains(html, "Open accessibility issues") || !strings.Contains(html, "down from 9 at the last fleet run") {
		t.Errorf("fleet.html lacks the a11y columns or trend")
	}
}

// A fleet without --eaa sites has no accessibility columns.
func TestHTMLWithoutEAA(t *testing.T) {
	var b bytes.Buffer
	if err := HTML(&b, Page{Mode: Snapshot, Sites: []Result{{Site: "blog", Verdict: Clean, Counts: map[string]int{}}}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "a11y") || strings.Contains(b.String(), "Criteria with failures") {
		t.Errorf("plain fleet has a11y columns:\n%s", b.String())
	}
}

// fleet.html and fleet.json match their golden files for two sites: an
// --eaa site whose open issues went up since the last fleet run and a
// plain site, whose cells are empty, not 0.
func TestEAAGolden(t *testing.T) {
	time.Local = time.UTC
	open, failed, before := 12, 4, 9
	page := Page{Mode: Compare, Created: time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC), Sites: []Result{
		{Site: "shop", URL: "https://shop.test/", Kind: "comparison", Verdict: Broke, Counts: map[string]int{"broke": 1, "new": 2},
			Pages: 40, Dir: "shop", Report: "shop/.soundcheck/runs/r1/report.html", Duration: 161000,
			EAAOpen: &open, CriteriaFailed: &failed, EAATrend: TrendUp, EAABefore: &before,
			Statement: "shop/.soundcheck/runs/r1/eaa/statement-draft.md"},
		{Site: "blog", URL: "https://blog.test/", Kind: "comparison", Verdict: Clean, Counts: map[string]int{}, Pages: 12, Dir: "blog", Duration: 30000},
	}}
	var h bytes.Buffer
	if err := HTML(&h, page); err != nil {
		t.Fatal(err)
	}
	golden(t, "eaa-fleet.html.golden", h.String())
	j, err := json.MarshalIndent(page, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "eaa-fleet.json.golden", string(j)+"\n")
	row := h.String()[strings.Index(h.String(), ">blog<"):]
	row = row[:strings.Index(row, "</tr>")]
	if !strings.Contains(row, `<td class="num"></td>`) || strings.Contains(row, `<td class="num">0</td>`) {
		t.Errorf("plain site row: want empty a11y cells:\n%s", row)
	}
}

// A clean --eaa site never reads "Clean": a comparison with open
// accessibility problems says only "Nothing changed", one without says
// what the tests found. A snapshot says what the tests found. A
// plain site still says "Clean".
func TestLabelEAAClean(t *testing.T) {
	n := func(v int) *int { return &v }
	for _, tc := range []struct {
		r    Result
		want string
	}{
		{Result{Kind: "comparison", Verdict: Clean, EAAOpen: n(1), CriteriaFailed: n(1)}, "Nothing changed"},
		{Result{Kind: "comparison", Verdict: Clean, EAAOpen: n(0), CriteriaFailed: n(0)}, "Nothing changed; no failures found by these tests"},
		{Result{Kind: "comparison", Verdict: Clean, EAAOpen: n(0), CriteriaFailed: n(2)}, "Nothing changed; 2 WCAG criteria have failures"},
		{Result{Kind: "run", Verdict: Clean, EAAOpen: n(0), CriteriaFailed: n(0)}, "No failures found by these tests"},
		{Result{Kind: "run", Verdict: Clean, EAAOpen: n(1), CriteriaFailed: n(1)}, "1 WCAG criterion has failures"},
		{Result{Kind: "comparison", Verdict: Clean}, "Clean"},
		{Result{Kind: "comparison", Verdict: Changed, EAAOpen: n(1)}, "Changed"},
	} {
		if got := tc.r.Label(); got != tc.want {
			t.Errorf("%+v: label %q, want %q", tc.r, got, tc.want)
		}
	}
	var buf bytes.Buffer
	page := Page{Mode: Compare, Created: time.Unix(0, 0), Sites: []Result{
		{Site: "ledger", Kind: "comparison", Verdict: Clean, EAAOpen: n(1), CriteriaFailed: n(1)},
		{Site: "blog", Kind: "comparison", Verdict: Clean},
	}}
	if err := HTML(&buf, page); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{"<b>1</b> Nothing changed</span>", "<b>1</b> Clean</span>", `<span class="badge k-clean">Nothing changed</span>`} {
		if !strings.Contains(html, want) {
			t.Errorf("fleet.html has no %q", want)
		}
	}
	if strings.Contains(html, "<b>2</b> Clean") {
		t.Errorf("fleet.html counts the --eaa site as Clean")
	}
}

// Two clean --eaa sites: the one with open accessibility problems ranks
// worse than the one without, whatever their names.
func TestWorseRanksOpenEAAProblems(t *testing.T) {
	n := func(v int) *int { return &v }
	results := []Result{
		{Site: "good", Kind: "comparison", Verdict: Clean, EAAOpen: n(0), CriteriaFailed: n(0)},
		{Site: "zoom", Kind: "comparison", Verdict: Clean, EAAOpen: n(1), CriteriaFailed: n(1)},
		{Site: "blog", Kind: "comparison", Verdict: Clean},
		{Site: "alpha", Kind: "comparison", Verdict: Clean, EAAOpen: n(1), CriteriaFailed: n(3)},
		{Site: "shop", Kind: "comparison", Verdict: Broke, Counts: map[string]int{model.Broke: 1}, EAAOpen: n(0), CriteriaFailed: n(0)},
	}
	slices.SortStableFunc(results, worse)
	var got []string
	for _, r := range results {
		got = append(got, r.Site)
	}
	if want := []string{"shop", "alpha", "zoom", "blog", "good"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}
