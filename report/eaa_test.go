// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

// eaaPaths are the 38 pages the newsletter dialog is on.
func eaaPaths(n int) []string {
	paths := []string{"/", "/shop/", "/about/"}
	for i := len(paths); i < n; i++ {
		paths = append(paths, "/p/"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+"/")
	}
	return paths
}

// eaaIssues are the problems of the shop's first --eaa snapshot: a
// keyboard trap in the payment field, a dialog that loses focus on 38
// pages, a missing label and a missing skip link.
func eaaIssues() (trap, focusLost, label, skip model.Issue) {
	trap = model.Issue{Check: "keyboard/trap", Level: model.Error, Title: "Keyboard focus is trapped: Card number",
		Source: "#card-element > div:nth-of-type(1)", Detail: "Tab and Shift+Tab kept focus here for 4 presses; 3 focusable elements after it can't be reached.",
		Fix: "Let Tab move on from the field.", WCAG: []string{"2.1.2"}, EN301549: []string{"9.2.1.2"}, Impact: "critical",
		FocusPath: []string{"#site-logo", "#menu", "#billing_email", "#billing_phone", "#card-element > div:nth-of-type(1)",
			"#card-element > div:nth-of-type(1)", "#card-element > div:nth-of-type(1)", "#card-element > div:nth-of-type(1)"}}
	focusLost = model.Issue{Check: "keyboard/focus-lost", Level: model.Warning, Title: "Focus is lost after closing: Newsletter",
		Source: "#newsletter-close", Fix: "When the dialog closes, move focus back to the button that opened it.",
		WCAG: []string{"2.4.3"}, EN301549: []string{"9.2.4.3"}, Impact: "serious", FocusPath: []string{"#newsletter-open", "#newsletter-close"}}
	label = model.Issue{Check: "a11y/label", Level: model.Warning, Title: "Form elements must have labels", Source: "#billing_phone",
		Detail: `<input id="billing_phone" name="billing_phone" type="tel">`, Fix: "See https://dequeuniversity.com/rules/axe/4.14/label for what the rule asks and how to fix it.",
		WCAG: []string{"1.3.1", "4.1.2"}, EN301549: []string{"9.1.3.1", "9.4.1.2"}, Impact: "critical"}
	skip = model.Issue{Check: "keyboard/skip-link-missing", Level: model.Notice, Title: "No skip link before a long run of stops",
		WCAG: []string{"2.4.1"}, EN301549: []string{"9.2.4.1"}, Impact: "minor"}
	for _, i := range []*model.Issue{&trap, &focusLost, &label, &skip} {
		i.Identify()
	}
	return
}

// eaaSummaryFor is the accessibility summary of a WCAG 2.1 run with
// failures of the given criteria; the rest are split between none found
// and needs a person as wcag.Summarize would.
func eaaSummaryFor(open int, failed ...model.CriterionCount) *model.EAASummary {
	e := &model.EAASummary{WCAG: "2.1", EN301549: "V3.2.1", Axe: "4.14.0", Open: open, EvidenceNote: model.EvidenceNote,
		Criteria: model.EAACriteria{Failed: failed, NoFailuresFound: []string{}, NotTested: []string{}}}
	for i, c := range wcag.Criteria("2.1") {
		hit := false
		for _, f := range failed {
			hit = hit || f.ID == c.ID
		}
		switch {
		case hit:
		case i%2 == 0:
			e.Criteria.NoFailuresFound = append(e.Criteria.NoFailuresFound, c.ID)
		default:
			e.Criteria.NotTested = append(e.Criteria.NotTested, c.ID)
		}
	}
	return e
}

// eaaSnapshot is the first --eaa snapshot of a shop, with findings.
func eaaSnapshot() *model.Run {
	trap, focusLost, label, skip := eaaIssues()
	run := &model.Run{Schema: model.Schema, Tool: "rhythm-soundcheck", Version: "1.3.0", Target: "https://staging.shop.test/", Name: "eaa-1",
		Started: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), Duration: 161000,
		Options: model.Options{EAA: true, WCAG: "2.1", Modules: []string{"health", "a11y", "keyboard"}}}
	for _, path := range eaaPaths(38) {
		pg := model.Page{Path: path, URL: "https://staging.shop.test" + path, Status: 200, Issues: []model.Issue{focusLost, skip},
			Keyboard: &model.KeyboardSummary{Stops: 23, Wrapped: true, Widgets: 3, WidgetKinds: []string{"details", "dialog", "menu"}, MS: 900}}
		run.Pages = append(run.Pages, pg)
	}
	run.Pages = append(run.Pages, model.Page{Path: "/checkout/", URL: "https://staging.shop.test/checkout/", Status: 200,
		Issues:   []model.Issue{trap, label},
		Keyboard: &model.KeyboardSummary{Stops: 9, Capped: false, Widgets: 0, MS: 1340}})
	run.EAA = eaaSummaryFor(4,
		model.CriterionCount{ID: "1.3.1", Issues: 1, Pages: 1}, model.CriterionCount{ID: "2.1.2", Issues: 1, Pages: 1},
		model.CriterionCount{ID: "2.4.1", Issues: 1, Pages: 38}, model.CriterionCount{ID: "2.4.3", Issues: 1, Pages: 38},
		model.CriterionCount{ID: "4.1.2", Issues: 1, Pages: 1})
	return run
}

// eaaClean is an --eaa snapshot of a site the tests found nothing on.
func eaaClean() *model.Run {
	run := &model.Run{Schema: model.Schema, Tool: "rhythm-soundcheck", Version: "1.3.0", Target: "https://clean.test/", Name: "eaa-1",
		Started: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), Duration: 31000,
		Options: model.Options{EAA: true, WCAG: "2.1"}}
	for _, path := range eaaPaths(6) {
		run.Pages = append(run.Pages, model.Page{Path: path, URL: "https://clean.test" + path, Status: 200})
	}
	run.EAA = eaaSummaryFor(0)
	return run
}

// eaaNotAudited is an --eaa run whose catalog page timed out in axe-core.
func eaaNotAudited() *model.Run {
	run := eaaClean()
	run.Name = ""
	run.EAA.NotAudited = []model.NotAudited{{Path: "/catalog/", Module: "a11y", Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}}
	run.Pages = append(run.Pages, model.Page{Path: "/catalog/", URL: "https://clean.test/catalog/", Status: 200,
		Audits: map[string]model.Audit{"a11y": {Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}}})
	return run
}

// notCheckedWarning is the comparison warning lane axe writes for a page
// whose audit didn't finish.
const notCheckedWarning = "not checked: /catalog/ (a11y: axe-core timed out after 45s); its issues are kept from the snapshot, not counted as fixed"

// eaaComparison is the compare after a payment plugin update: the
// trap broke checkout, a new missing label and the dialog fixed.
func eaaComparison() Result {
	run := eaaSnapshot()
	run.Name = ""
	trap, focusLost, label, _ := eaaIssues()
	change := func(class string, i model.Issue, pages ...string) model.Change {
		return model.Change{Class: class, Check: i.Check, Title: i.Title, Source: i.Source, Detail: i.Detail, Fix: i.Fix, Pages: pages,
			WCAG: i.WCAG, EN301549: i.EN301549, Impact: i.Impact, FocusPath: i.FocusPath}
	}
	cvc := label
	cvc.Source, cvc.Detail = "#card_cvc", `<input id="card_cvc" name="card_cvc">`
	c := &model.Comparison{Schema: model.Schema, Version: "1.3.0", Pages: 39,
		Before: model.RunRef{Target: run.Target, Name: "eaa-1"},
		Changes: []model.Change{
			change(model.Broke, trap, "/checkout/"),
			change(model.New, cvc, "/checkout/"),
			change(model.Fixed, focusLost, eaaPaths(38)...),
		},
		Warnings: []string{notCheckedWarning}}
	c.Complete()
	// The label criteria had failures before, so only the trap is new.
	c.EAA = eaaSummaryFor(4,
		model.CriterionCount{ID: "1.3.1", Issues: 2, Pages: 1}, model.CriterionCount{ID: "2.1.2", Issues: 1, Pages: 1},
		model.CriterionCount{ID: "2.4.1", Issues: 1, Pages: 38}, model.CriterionCount{ID: "4.1.2", Issues: 2, Pages: 1})
	c.EAA.NotAudited = []model.NotAudited{{Path: "/catalog/", Module: "a11y", Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}}
	run.EAA = c.EAA
	return Result{Run: run, Comparison: c}
}

// The terminal views of --eaa results match their golden files: the
// wcag and path lines, accept hints on BROKE and NEW, the counts line,
// the verdict and the evidence note, which -q keeps.
func TestEAATerminalGolden(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	results := map[string]Result{
		"snapshot":    {Run: eaaSnapshot()},
		"clean":       {Run: eaaClean()},
		"compare":     eaaComparison(),
		"not-audited": {Run: eaaNotAudited()},
	}
	views := map[string]PrettyOptions{
		"pretty": {Accept: "rhythm soundcheck accept"},
		"plain":  {Plain: true, Accept: "rhythm soundcheck accept"},
		"quiet":  {Plain: true, Quiet: true, Accept: "rhythm soundcheck accept"},
	}
	for rname, r := range results {
		for vname, o := range views {
			var b bytes.Buffer
			NewPretty(&b, &b, o).Summary(r, "full report: .soundcheck/snapshots/eaa-1/report.html")
			out := b.String()
			name := "eaa-" + rname + "." + vname + ".golden"
			if !strings.Contains(oneLine(out), model.EvidenceNote) {
				t.Errorf("%s lacks the evidence note:\n%s", name, out)
			}
			if strings.Contains(out, "all clear") {
				t.Errorf("%s says all clear:\n%s", name, out)
			}
			golden(t, name, out)
		}
	}
}

// Plain lines are what a script or screen reader gets: check them as
// text, not only as a golden file.
func TestEAAPlainLines(t *testing.T) {
	var b bytes.Buffer
	NewPretty(&b, io.Discard, PrettyOptions{Plain: true}).Summary(Result{Run: eaaSnapshot()}, "")
	out := b.String()
	for _, want := range []string{
		"[ERROR] Keyboard focus is trapped: Card number - /checkout/\n  source: #card-element > div:nth-of-type(1)\n  wcag: 2.1.2 · EN 301 549 9.2.1.2\n" +
			"  path: #billing_email then #billing_phone then #card-element > div:nth-of-type(1) (4 times)\n",
		"[WARNING] Focus is lost after closing: Newsletter - /, /shop/, /about/ +35 more (38 pages)\n",
		"  wcag: 1.3.1, 4.1.2 · EN 301 549 9.1.3.1, 9.4.1.2\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output lacks %q:\n%s", want, out)
		}
	}
	if flat := oneLine(out); !strings.Contains(flat, "Accessibility: 5 of 50 WCAG 2.1 A and AA criteria have failures · 21 had none found by these tests · 24 need a person "+
		`Snapshot "eaa-1" saved; the site already has errors`) {
		t.Errorf("no counts line before the verdict:\n%s", out)
	}
	if strings.Count(out, "path:") != 2 {
		t.Errorf("want path lines on the two keyboard issues with a path only:\n%s", out)
	}

	b.Reset()
	NewPretty(&b, io.Discard, PrettyOptions{Plain: true}).Summary(Result{Run: eaaClean()}, "")
	if !strings.Contains(b.String(), `Snapshot "eaa-1" saved; no failures found by these tests - 0 errors`) {
		t.Errorf("clean --eaa verdict:\n%s", b.String())
	}
	// Without --eaa a clean run is still all clear and has no note.
	plainRun := eaaClean()
	plainRun.EAA = nil
	b.Reset()
	NewPretty(&b, io.Discard, PrettyOptions{Plain: true}).Summary(Result{Run: plainRun}, "")
	if s := b.String(); !strings.Contains(s, "all clear") || strings.Contains(s, "Automated and keyboard") || strings.Contains(s, "Accessibility:") {
		t.Errorf("a run without --eaa changed:\n%s", s)
	}
}

// A comparison says which criteria are new and how the open count moved.
// Its not-checked line comes from the comparison's warnings, once.
func TestEAACompareLine(t *testing.T) {
	r := eaaComparison()
	var b bytes.Buffer
	NewPretty(&b, io.Discard, PrettyOptions{Plain: true, Accept: "rhythm soundcheck accept"}).Summary(r, "")
	out := b.String()
	want := "Accessibility: 4 of 50 WCAG 2.1 A and AA criteria have failures (2.1.2 new) · 22 had none found by these tests · 24 need a person · open issues 3 to 4 Something broke"
	if !strings.Contains(oneLine(out), want) {
		t.Errorf("lacks %q:\n%s", want, out)
	}
	if n := strings.Count(out, "not checked: /catalog/"); n != 1 {
		t.Errorf("not-checked line printed %d times:\n%s", n, out)
	}
	for _, class := range []string{"BROKE", "NEW"} {
		ch := r.Comparison.Changes[0]
		if class == "NEW" {
			ch = r.Comparison.Changes[1]
		}
		if !strings.Contains(out, "rhythm soundcheck accept "+ch.ID+" if this is expected") {
			t.Errorf("%s has no accept hint:\n%s", class, out)
		}
	}
}

// oneLine joins wrapped output back into one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestPathLine(t *testing.T) {
	for _, tc := range []struct {
		path        []string
		plain, want string
	}{
		{nil, "", ""},
		{[]string{"#a"}, "path: #a", "path: #a"},
		{[]string{"main > ul > li", "main > ul > li", "#news"}, "path: main > ul > li (2 times) then #news", "path: main > ul > li ×2 → #news"},
		{[]string{"#a", "#b", "#b", "#c"}, "path: #a then #b (2 times) then #c", "path: #a → #b ×2 → #c"},
		{[]string{"1", "2", "3", "4", "5", "6", "7", "8"}, "path: 3 then 4 then 5 then 6 then 7 then 8", "path: 3 → 4 → 5 → 6 → 7 → 8"},
		{[]string{"#x", "#x", "#x", "#x", "#x", "#x", "#x"}, "path: #x (6 times)", "path: #x ×6"},
		{[]string{"#a\x1b[2J", "#b"}, `path: #a\x1b[2J then #b`, `path: #a\x1b[2J → #b`},
	} {
		if got := pathLine(tc.path, true); got != tc.plain {
			t.Errorf("pathLine(%q, plain) = %q, want %q", tc.path, got, tc.plain)
		}
		if got := pathLine(tc.path, false); got != tc.want {
			t.Errorf("pathLine(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// Only keyboard issues get a path line and only issues with criteria a
// wcag line.
func TestIssueExtras(t *testing.T) {
	if got := issueExtras("a11y/label", []string{"1.3.1"}, nil, []string{"#a"}, true); len(got) != 1 || got[0] != "wcag: 1.3.1" {
		t.Errorf("a11y issue extras = %q", got)
	}
	if got := issueExtras("health/status", nil, nil, nil, true); len(got) != 0 {
		t.Errorf("health issue extras = %q", got)
	}
	if got := issueExtras("keyboard/trap", nil, nil, []string{"#a"}, true); len(got) != 1 || got[0] != "path: #a" {
		t.Errorf("keyboard issue extras = %q", got)
	}
}

// Past three pages an accessibility issue also says how many; other
// checks keep today's label.
func TestWhereLabel(t *testing.T) {
	five := eaaPaths(5)
	if got := whereLabel("a11y/color-contrast", five); got != "/, /shop/, /about/ +2 more (5 pages)" {
		t.Errorf("a11y label = %q", got)
	}
	if got := whereLabel("health/php-error", five); got != "/, /shop/, /about/ +2 more" {
		t.Errorf("health label = %q", got)
	}
	if got := whereLabel("keyboard/trap", five[:3]); got != "/, /shop/, /about/" {
		t.Errorf("three pages = %q", got)
	}
}

func TestVerboseKeyboardLine(t *testing.T) {
	var out bytes.Buffer
	pg := verbosePage()
	pg.Keyboard = &model.KeyboardSummary{Stops: 23, Widgets: 3, WidgetKinds: []string{"menu", "dialog", "details"}, MS: 900}
	NewPretty(&out, &out, PrettyOptions{Plain: true, Verbose: true, Name: "x"}).Page(pg, 1, 1)
	if !strings.Contains(out.String(), "\n  keyboard: /shop/ 23 stops, 3 widgets (menu, dialog, details), 0.9s\n") {
		t.Errorf("no keyboard line:\n%s", out.String())
	}
	pg.Keyboard = &model.KeyboardSummary{Stops: 1, Widgets: 1, MS: 1250}
	out.Reset()
	NewPretty(&out, &out, PrettyOptions{Plain: true, Verbose: true, Name: "x"}).Page(pg, 1, 1)
	if !strings.Contains(out.String(), "keyboard: /shop/ 1 stop, 1 widget, 1.2s\n") {
		t.Errorf("keyboard line without kinds:\n%s", out.String())
	}
	out.Reset()
	NewPretty(&out, &out, PrettyOptions{Plain: true, Name: "x"}).Page(pg, 1, 1)
	if strings.Contains(out.String(), "keyboard:") {
		t.Errorf("keyboard line without -v:\n%s", out.String())
	}
}

func TestWrap(t *testing.T) {
	lines := wrap(model.EvidenceNote, 80)
	for _, l := range lines {
		if len([]rune(l)) > 80 {
			t.Errorf("line over 80: %q", l)
		}
	}
	if strings.Join(lines, " ") != model.EvidenceNote {
		t.Errorf("wrap lost words: %q", lines)
	}
	for _, l := range wrap(model.EvidenceNote, 72) {
		if strings.HasSuffix(l, "EN") || strings.HasSuffix(l, "301") {
			t.Errorf("wrap broke EN 301 549: %q", l)
		}
	}
	if got := wrap("a "+strings.Repeat("x", 30)+" b", 20); len(got) != 3 {
		t.Errorf("long word: %q", got)
	}
	var b bytes.Buffer
	NewPretty(&b, io.Discard, PrettyOptions{Plain: true, Quiet: true, Columns: 40}).Summary(Result{Run: eaaClean()}, "")
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if len([]rune(l)) > 40 {
			t.Errorf("-q line over 40 columns: %q", l)
		}
	}
}

// The Markdown of an --eaa snapshot and comparison matches its golden
// file: criteria after accessibility titles and the Accessibility
// section with its table, not-checked list and evidence note.
func TestEAAMarkdownGolden(t *testing.T) {
	snap := eaaSnapshot()
	snap.EAA.NotAudited = []model.NotAudited{{Path: "/catalog/", Module: "a11y", Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}}
	for name, r := range map[string]Result{"eaa-snapshot.md.golden": {Run: snap}, "eaa-compare.md.golden": eaaComparison()} {
		var b bytes.Buffer
		if err := Markdown(&b, r); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		for _, want := range []string{"### Accessibility (WCAG 2.1 A and AA)\n", "<details><summary>Criteria with failures</summary>",
			"| 2.1.2 No Keyboard Trap | 9.2.1.2 |", "> " + model.EvidenceNote, "- `/catalog/` (a11y: timeout)",
			"Keyboard focus is trapped: Card number (2.1.2)"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s lacks %q:\n%s", name, want, out)
			}
		}
		golden(t, name, out)
	}
	var b bytes.Buffer
	if err := Markdown(&b, eaaComparison()); err != nil {
		t.Fatal(err)
	}
	if out := b.String(); !strings.Contains(out, "| 2.1.2 No Keyboard Trap | 9.2.1.2 | 1 (new) | 1 |") || strings.Contains(out, "| 1.3.1 Info and Relationships | 9.1.3.1 | 2 (new)") {
		t.Errorf("only the trap's criterion is new:\n%s", out)
	}
	b.Reset()
	if err := Markdown(&b, Result{Run: sampleRun()}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "Accessibility") {
		t.Errorf("a run without --eaa has an Accessibility section:\n%s", b.String())
	}
}

// The Accessibility section is what a PR comment shows everyone with
// access to the repository: what a page showed (titles with names,
// details, selectors, reasons) never reaches it, only counts, check
// ids, criteria and paths.
func TestEAAMarkdownSectionHasNoPageText(t *testing.T) {
	canary := "canary-" + "7f3a"
	r := eaaComparison()
	for i := range r.Comparison.Changes {
		ch := &r.Comparison.Changes[i]
		ch.Title += " " + canary + "-title"
		ch.Detail = canary + "-detail jane@example.test"
		ch.Source = "#" + canary + "-source"
		ch.Files = map[string]string{"after": canary + "-shot.png"}
	}
	r.Comparison.EAA.NotAudited[0].Reason = canary + "-reason"
	for i := range r.Run.Pages {
		for j := range r.Run.Pages[i].Issues {
			r.Run.Pages[i].Issues[j].Detail = canary + "-issue"
		}
	}
	snap := Result{Run: eaaSnapshot()}
	for i := range snap.Run.Pages {
		for j := range snap.Run.Pages[i].Issues {
			is := &snap.Run.Pages[i].Issues[j]
			is.Title, is.Detail, is.Source = canary+"-title", canary+"-detail", canary+"-source"
		}
	}
	for name, res := range map[string]Result{"comparison": r, "snapshot": snap} {
		var b bytes.Buffer
		if err := Markdown(&b, res); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		start := strings.Index(out, "### Accessibility")
		if start < 0 {
			t.Fatalf("%s: no Accessibility section:\n%s", name, out)
		}
		end := strings.Index(out[start:], "<sub>rhythm-soundcheck")
		section := out[start : start+end]
		if strings.Contains(section, "canary") || strings.Contains(section, "jane") || strings.Contains(section, ".png") {
			t.Errorf("%s: the Accessibility section holds page text:\n%s", name, section)
		}
	}
}

// A clean --eaa comparison says what the tests found; with problems
// still open, or without --eaa, it says only that nothing changed.
func TestEAACleanCompareVerdict(t *testing.T) {
	for _, c := range []struct {
		name string
		eaa  *model.EAASummary
		want string
	}{
		{"eaa, none open", &model.EAASummary{}, "Nothing changed; no failures found by these tests"},
		{"eaa, one open", &model.EAASummary{Open: 1}, "Nothing changed"},
		{"eaa, set aside", &model.EAASummary{Criteria: model.EAACriteria{Failed: []model.CriterionCount{{ID: "1.4.3", Issues: 20, Pages: 1}}}},
			"Nothing changed; 1 WCAG criterion has failures"},
		{"no eaa", nil, "Nothing changed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := verdict(&model.Comparison{EAA: c.eaa}); got != c.want {
				t.Errorf("verdict = %q, want %q", got, c.want)
			}
		})
	}
}

// A run or snapshot with no open problem says no failures only when no
// criterion failed: findings an ignore rule set aside still fail theirs.
func TestEAARunVerdictWithFailedCriteria(t *testing.T) {
	run := eaaClean()
	if got := runVerdict(run, nil); got != `Snapshot "eaa-1" saved; no failures found by these tests` {
		t.Errorf("clean: %q", got)
	}
	run.EAA.Criteria.Failed = []model.CriterionCount{{ID: "1.4.3", Issues: 2, Pages: 1}, {ID: "2.4.7", Issues: 1, Pages: 1}}
	if got := runVerdict(run, nil); got != `Snapshot "eaa-1" saved; 2 WCAG criteria have failures` {
		t.Errorf("set aside: %q", got)
	}
	if got := runPanel(run, map[model.Level]int{}); strings.Contains(got, noFailuresVerdict) || !strings.Contains(got, "2 WCAG criteria have failures") {
		t.Errorf("panel:\n%s", got)
	}
}

// An --eaa run whose only findings are notices never says "all clear":
// not in the terminal, the Markdown or the HTML report head. With an
// a11y notice that fails 1.4.4 it counts the failed criterion; with a
// health notice alone it says no failures were found by these tests.
func TestEAARunWithOnlyNoticesIsNotAllClear(t *testing.T) {
	a11y := model.Issue{Check: "a11y/meta-viewport", Level: model.Notice, Title: "Zooming and scaling are disabled",
		Key: "a11y/meta-viewport", WCAG: []string{"1.4.4"}}
	favicon := model.Issue{Check: "health/favicon", Level: model.Notice, Title: "No favicon", Key: "health/favicon"}
	for _, c := range []struct {
		name   string
		issues []model.Issue
		failed []model.CriterionCount
		want   string
	}{
		{"a11y and health notices", []model.Issue{a11y, favicon}, []model.CriterionCount{{ID: "1.4.4", Issues: 1, Pages: 1}}, "1 WCAG criterion has failures"},
		{"health notice", []model.Issue{favicon}, nil, noFailuresVerdict},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := eaaClean()
			run.Pages[0].Issues = c.issues
			run.EAA.Criteria.Failed = c.failed
			counts := map[model.Level]int{model.Notice: len(c.issues)}
			if got := runVerdict(run, counts); got != `Snapshot "eaa-1" saved; `+c.want {
				t.Errorf("verdict = %q", got)
			}
			if got := runPanel(run, counts); strings.Contains(strings.ToLower(got), "all clear") || !strings.Contains(got, c.want) {
				t.Errorf("panel:\n%s", got)
			}
			var md, page bytes.Buffer
			if err := Markdown(&md, Result{Run: run}); err != nil {
				t.Fatal(err)
			}
			if err := HTML(&page, Result{Run: run}); err != nil {
				t.Fatal(err)
			}
			for name, out := range map[string]string{"markdown": md.String(), "html": page.String()} {
				if strings.Contains(strings.ToLower(out), "all clear") || !strings.Contains(out, c.want) {
					t.Errorf("%s says all clear or lacks %q", name, c.want)
				}
			}
		})
	}
}

// A run's not-checked lines never say its issues are kept from a
// snapshot, which it has none of; a comparison's do. -q keeps them, as
// the exit message of an incomplete audit points at them.
func TestNotCheckedWording(t *testing.T) {
	const kept = "kept from the snapshot"
	run := Result{Run: eaaNotAudited()}
	for name, o := range map[string]PrettyOptions{"plain": {Plain: true}, "quiet": {Plain: true, Quiet: true}, "pretty": {}} {
		var b bytes.Buffer
		NewPretty(&b, io.Discard, o).Summary(run, "")
		out := b.String()
		if !strings.Contains(out, "not checked: /catalog/ (a11y: axe-core timed out after 45s)") {
			t.Errorf("%s: no not-checked line:\n%s", name, out)
		}
		if strings.Contains(out, kept) {
			t.Errorf("%s: a run says %q:\n%s", name, kept, out)
		}
	}
	var b bytes.Buffer
	NewPretty(&b, io.Discard, PrettyOptions{Plain: true, Quiet: true}).Summary(eaaComparison(), "")
	if !strings.Contains(b.String(), notCheckedWarning) {
		t.Errorf("quiet comparison lacks the not-checked line:\n%s", b.String())
	}
	var html bytes.Buffer
	if err := HTML(&html, run); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), kept) || !strings.Contains(html.String(), "missing from these results") {
		t.Errorf("run report.html words the not-checked pages as a comparison's")
	}
	html.Reset()
	if err := HTML(&html, eaaComparison()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html.String(), kept) {
		t.Errorf("comparison report.html lacks %q", kept)
	}
}
