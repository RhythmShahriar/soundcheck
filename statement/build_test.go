// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

func owner() Owner {
	return Owner{Owner: "Example Coffee Ltd", Website: "https://shop.example", Contact: Contact{Email: "access@shop.example"}}
}

func issue(check, source string, crit ...string) model.Issue {
	i := model.Issue{Check: check, Title: check, Source: source, WCAG: crit}
	i.Identify()
	return i
}

// input is a finished --eaa run of WCAG 2.1 with its summary.
func input(pages []model.Page, o Owner) Input {
	run := &model.Run{Target: "http://127.0.0.1:37060/", Pages: pages, Options: model.Options{EAA: true, WCAG: "2.1",
		Modules: []string{"health", "interact", "a11y", "keyboard"}, A11y: &model.A11yRules{Axe: "4.14.0", Rules: []string{"image-alt", "label"}}}}
	return Input{Run: run, Owner: o, Version: "1.3.0", Now: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC),
		Summary: wcag.Summarize(run, func(yield func(*model.Page) bool) {
			for i := range run.Pages {
				if !yield(&run.Pages[i]) {
					return
				}
			}
		}, nil, nil)}
}

func audited() map[string]model.Audit {
	return map[string]model.Audit{"a11y": {Status: model.AuditOK}, "keyboard": {Status: model.AuditOK}}
}

func row(d *Draft, id string) Row {
	for _, r := range d.Rows {
		if r.ID == id {
			return r
		}
	}
	return Row{}
}

func TestBuildRefuses(t *testing.T) {
	ok := input([]model.Page{{Path: "/", Audits: audited()}}, owner())
	for name, change := range map[string]func(*Input){
		"no owner":   func(in *Input) { in.Owner.Owner = " " },
		"no website": func(in *Input) { in.Owner.Website = "" },
		"no email":   func(in *Input) { in.Owner.Contact.Email = "" },
	} {
		in := ok
		change(&in)
		if _, err := Build(in); !errors.Is(err, ErrMissingOwner) {
			t.Errorf("%s: %v", name, err)
		}
	}
	in := ok
	in.Run = &model.Run{Options: model.Options{WCAG: "2.1"}}
	if _, err := Build(in); !errors.Is(err, ErrNoEAAData) {
		t.Errorf("no --eaa: %v", err)
	}
	in.Run = nil
	if _, err := Build(in); !errors.Is(err, ErrNoEAAData) {
		t.Errorf("no run: %v", err)
	}
	in = ok
	run := *ok.Run
	run.Interrupted = true
	in.Run = &run
	if _, err := Build(in); !errors.Is(err, ErrInterrupted) {
		t.Errorf("interrupted: %v", err)
	}
	in = ok
	in.Layout = "servise"
	if _, err := Build(in); err == nil || !strings.Contains(err.Error(), `--layout: "servise" isn't one of model, service (did you mean "service"?)`) {
		t.Errorf("layout: %v", err)
	}
	if d, err := Build(ok); err != nil || d.Layout != LayoutModel {
		t.Errorf("default layout: %v, %v", d, err)
	}
}

// A draft has a row per criterion, the run's facts and never a status.
func TestBuild(t *testing.T) {
	pages := []model.Page{
		{Path: "/", Audits: audited(), Keyboard: &model.KeyboardSummary{Widgets: 2},
			Issues: []model.Issue{issue("a11y/color-contrast", "footer a", "1.4.3"), issue("keyboard/trap", "#card")}},
		{Path: "/shop/", Audits: audited(), Issues: []model.Issue{issue("a11y/color-contrast", "footer a", "1.4.3")}},
		{Path: "/legal/", Audits: audited(), Issues: []model.Issue{issue("a11y/color-contrast", "footer a", "1.4.3")}},
		{Path: "/about/", Audits: audited(), Issues: []model.Issue{issue("a11y/color-contrast", "footer a", "1.4.3")}},
		{Path: "/big/", Audits: map[string]model.Audit{"a11y": {Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"},
			"keyboard": {Status: model.AuditOK, Frames: []string{"js.stripe.com"}}}},
	}
	in := input(pages, owner())
	in.Layout = LayoutService
	in.Ignored = []model.Change{
		{Check: "a11y/color-contrast", WCAG: []string{"1.4.3"}, Pages: []string{"/legal/?x=1"}, Reason: "new theme in December"},
		{Check: "a11y/image-alt", Pages: []string{"/big/"}, Reason: "not checked on this page: a11y timed out"},
		{Check: "health/php-error", Pages: []string{"/"}, Reason: "known"},
	}
	in.Owner.KnownIssues = []KnownIssue{{Criterion: "1.2.2", Text: "Product videos have no captions yet.", Until: "2027-03-31"}}
	d, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Rows) != 50 || d.Pages != 5 || d.Status != nil || d.Layout != LayoutService || d.Date != "2026-10-26" ||
		d.Standard != "WCAG 2.1 level AA / EN 301 549 V3.2.1" || d.Axe != "4.14.0" || d.AxeRules != 2 || d.Version != "1.3.0" ||
		d.EvidenceNote != model.EvidenceNote || d.Keyboard.WidgetsTested != 2 {
		t.Errorf("draft = %+v", d)
	}
	if r := row(d, "1.4.3"); r.Result != ResultFailures || r.Issues != 1 || r.Pages != 4 || !slices.Equal(r.Paths, []string{"/", "/legal/", "/shop/"}) {
		t.Errorf("1.4.3: %+v", r)
	}
	if r := row(d, "1.2.3"); r.Result != ResultNotTested || r.Tests != ResultNotTested {
		t.Errorf("1.2.3: %+v", r)
	}
	if r := row(d, "1.1.1"); r.Result != ResultNoFailures {
		t.Errorf("1.1.1: %+v", r)
	}
	if r := row(d, "4.1.1"); r.Result != ResultNotTested || r.HowTested() != r.Note || r.Note == "" {
		t.Errorf("4.1.1: %+v", r)
	}
	if d.Failed != 2 || d.NotTested != 23 {
		t.Errorf("%d failed, %d not tested", d.Failed, d.NotTested)
	}
	if !slices.Equal(d.Checked, []string{"/", "/about/", "/big/", "/legal/", "/shop/"}) || d.PrivatePages != 0 {
		t.Errorf("checked %v, private %d", d.Checked, d.PrivatePages)
	}
	if len(d.Ignored) != 1 || d.Ignored[0].Reason != "new theme in December" || !slices.Equal(d.Ignored[0].Pages, []string{"/legal/"}) ||
		!slices.Equal(d.Ignored[0].Criteria, []string{"1.4.3"}) {
		t.Errorf("ignored = %+v", d.Ignored)
	}
	if len(d.NotAudited) != 1 || d.NotAudited[0].Path != "/big/" || !slices.Equal(d.Frames, []string{"js.stripe.com"}) {
		t.Errorf("not audited %+v, frames %v", d.NotAudited, d.Frames)
	}
	if len(d.KnownIssues) != 1 || d.KnownIssues[0].Title != "Captions (Prerecorded)" {
		t.Errorf("known = %+v", d.KnownIssues)
	}
	if len(d.Notes) != 0 {
		t.Errorf("notes = %v", d.Notes)
	}
}

// The owner's manual result wins over the tests' row and records who
// and when; the tests' counts stay.
func TestBuildManual(t *testing.T) {
	pages := []model.Page{{Path: "/", Audits: audited(), Issues: []model.Issue{issue("keyboard/trap", "#card")}}}
	o := owner()
	o.Manual = []ManualCheck{
		{Criterion: "1.4.3", Result: ManualFailures, By: "Jane Doe", Date: "2026-10-20"},
		{Criterion: "2.1.2", Result: ManualNoFailures, By: "Jane Doe", Date: "2026-10-21"},
		{Criterion: "1.4.11", Result: ManualNotChecked, By: "Jane Doe", Date: "2026-10-21"},
		{Criterion: "2.4.11", Result: ManualNoFailures, By: "Jane Doe", Date: "2026-10-21"}, // 2.2 only
	}
	d, err := Build(input(pages, o))
	if err != nil {
		t.Fatal(err)
	}
	if r := row(d, "1.4.3"); r.Result != ResultManual || r.Tests != ResultNoFailures || *r.Manual != (Manual{ManualFailures, "Jane Doe", "2026-10-20"}) {
		t.Errorf("1.4.3: %+v", r)
	}
	if r := row(d, "2.1.2"); r.Result != ResultManual || r.Tests != ResultFailures || r.Issues != 1 || r.Manual.Date != "2026-10-21" {
		t.Errorf("2.1.2: %+v", r)
	}
	if r := row(d, "1.4.11"); r.Result != ResultManual || r.Tests != ResultNotTested {
		t.Errorf("1.4.11: %+v", r)
	}
	// The status sentence counts what the tests did.
	if d.Failed != 1 || d.NotTested != 23 {
		t.Errorf("%d failed, %d not tested", d.Failed, d.NotTested)
	}
	if !slices.Equal(d.Notes, []string{"Your manual result for 2.4.11 is left out: WCAG 2.1 has no criterion 2.4.11."}) {
		t.Errorf("notes = %q", d.Notes)
	}
}

// V2: a run against 127.0.0.1 with a login names the site by
// statement.website only. Neither the run's host nor the account nor a
// token reaches the draft.
func TestBuildHidesTheRun(t *testing.T) {
	token := strings.Repeat("Ab9", 14)
	pages := []model.Page{
		{Path: "/", Audits: audited()},
		{Path: "/users/jane.doe/orders/", Audits: audited(), Issues: []model.Issue{issue("a11y/image-alt", "img", "1.1.1")}},
		{Path: "/password/reset/" + token + "?key=" + token, Audits: audited(), Issues: []model.Issue{issue("a11y/image-alt", "img", "1.1.1")}},
		{Path: "http://127.0.0.1:37060/go/127.0.0.1/?session=abc#top", Audits: audited()},
		{Path: "/big/", Audits: map[string]model.Audit{
			"a11y":     {Status: model.AuditError, Reason: "GET http://127.0.0.1:37060/big/?sid=1 failed for jane.doe"},
			"keyboard": {Status: model.AuditOK, Frames: []string{"127.0.0.1", "127.0.0.1:37060", "js.stripe.com"}}}},
	}
	in := input(pages, owner())
	in.Hide = []string{"jane.doe", "staging.shop.test"}
	in.Insecure = true
	in.Ignored = []model.Change{{Check: "a11y/image-alt", WCAG: []string{"1.1.1"}, Pages: []string{"/users/jane.doe/"}, Reason: "seen on staging.shop.test"}}
	skipped := model.SkippedFlow{File: "flows/staging.shop.test-checkout.yaml", Reason: "needs the login of jane.doe on 127.0.0.1:37060"}
	in.Summary.Keyboard.FlowsSkipped = []model.SkippedFlow{skipped}
	d, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if in.Summary.Keyboard.FlowsSkipped[0] != skipped {
		t.Errorf("Build changed the run's summary: %+v", in.Summary.Keyboard.FlowsSkipped)
	}
	if len(d.Keyboard.FlowsSkipped) != 1 || !strings.Contains(d.Keyboard.FlowsSkipped[0].File, "checkout.yaml") {
		t.Errorf("skipped flows = %+v", d.Keyboard.FlowsSkipped)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]string{"draft": string(b)}
	for name, render := range map[string]func(io.Writer, *Draft) error{"md": Markdown, "html": HTML, "json": JSON, "earl": EARL} {
		for _, layout := range Layouts {
			d.Layout = layout
			var buf bytes.Buffer
			if err := render(&buf, d); err != nil {
				t.Fatalf("%s %s: %v", name, layout, err)
			}
			outputs[name+" "+layout] = buf.String()
		}
	}
	for name, out := range outputs {
		for _, leak := range []string{"127.0.0.1", "37060", "jane.doe", "staging.shop.test", token, "session=", "abc", "sid=", "#top"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s holds %q:\n%s", name, leak, out)
			}
		}
	}
	out := string(b)
	if !strings.Contains(out, "https://shop.example") || !slices.Contains(d.Checked, "/password/reset/"+masked) || !slices.Equal(d.Frames, []string{"js.stripe.com"}) {
		t.Errorf("checked %v, frames %v", d.Checked, d.Frames)
	}
	if !slices.Equal(d.Notes, []string{"TLS certificate not verified for this run"}) {
		t.Errorf("notes = %q", d.Notes)
	}
}

// A run made logged in lists no paths unless the owner asks.
func TestBuildPrivatePaths(t *testing.T) {
	pages := []model.Page{
		{Path: "/account/orders/1234/", Audits: audited(), Issues: []model.Issue{issue("a11y/image-alt", "img", "1.1.1")}},
		{Path: "/account/", Audits: map[string]model.Audit{"a11y": {Status: model.AuditTimeout}}},
	}
	in := input(pages, owner())
	in.Run.Options.LoggedIn = true
	in.Ignored = []model.Change{{Check: "a11y/image-alt", WCAG: []string{"1.1.1"}, Pages: []string{"/account/orders/1234/"}, Reason: "r"}}
	d, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), "/account") || d.PrivatePages != 2 || d.Checked != nil || d.NotAudited[0].Path != "" || row(d, "1.1.1").Pages != 1 {
		t.Errorf("private paths listed: %s", b)
	}
	in.Owner.ListPrivatePaths = true
	d, _ = Build(in)
	if d.PrivatePages != 0 || len(d.Checked) != 2 || !slices.Equal(row(d, "1.1.1").Paths, []string{"/account/orders/1234/"}) || d.NotAudited[0].Path != "/account/" {
		t.Errorf("list_private_paths: %+v", d)
	}
}

func TestCleanPath(t *testing.T) {
	for in, want := range map[string]string{
		"/shop/?utm=x":                       "/shop/",
		"/a#b":                               "/a",
		"":                                   "/",
		"?only=query":                        "/",
		"https://host.test/x/y?z":            "/x/y",
		"/verify/" + strings.Repeat("a", 30): "/verify/" + masked,
		"/login/short":                       "/login/short",
		"/HOST.test/x":                       "/" + masked + "/x",
	} {
		if got := cleanPath(in, []string{"host.test", "xy"}); got != want {
			t.Errorf("cleanPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// A two-character hide string, such as a short login name, is masked
// where it stands alone and left inside longer words.
func TestHideShort(t *testing.T) {
	for in, want := range map[string]string{
		"/users/bo/":         "/users/" + masked + "/",
		"/users/BO/bo/":      "/users/" + masked + "/" + masked + "/",
		"signed in as bo.":   "signed in as " + masked + ".",
		"bo":                 masked,
		"/books/robot/":      "/books/robot/",
		"bob logged in":      "bob logged in",
		"a link has no name": "a link has no name",
	} {
		if got := hideAll(in, []string{"bo", "a"}); got != want {
			t.Errorf("hideAll(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanText(t *testing.T) {
	got := cleanText("GET http://host.test:8080/a?sid=1&x=2 failed; see https://x.example/b#frag", []string{"host.test"})
	if want := "GET http://" + masked + ":8080/a failed; see https://x.example/b"; got != want {
		t.Errorf("cleanText = %q, want %q", got, want)
	}
}
