// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyflows

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Without key flows, Run leaves the run as it was.
func TestRunWithoutFlows(t *testing.T) {
	cc := &check.Context{Run: &model.Run{}}
	Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 150})
	if len(cc.Run.Issues) != 0 || len(cc.Run.Warnings) != 0 || cc.Run.KeyFlows != nil {
		t.Errorf("Run changed a run without key flows: %+v", cc.Run)
	}
	// A flows folder with only mouse flows has no key flows either.
	cc = fakeContext(t, map[string]string{"a.yaml": mouseFlow})
	calls := fake(t, func(flow.Flow, flow.Options) (flow.Result, string) { return flow.Result{}, "" })
	Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 150})
	if cc.Run.KeyFlows != nil || len(*calls) != 0 || len(cc.Run.Warnings) != 0 {
		t.Errorf("mouse flows only: %+v, calls %v", cc.Run, *calls)
	}
}

const (
	mouseFlow = "name: By mouse\nstart: /contact/\nsteps: [{click: '#send'}]\n"
	dryFlow   = "name: Checkout dry\nstart: /checkout/\nkeyboard: true\nsteps: [{click: '#a'}, {click: '#b'}, {click: '#c'}, {expect: Thanks}]\n"
	sendFlow  = "name: Contact sends\nstart: /contact/\nkeyboard: send\nsteps: [{click: '#send'}]\n"
)

func fakeContext(t *testing.T, files map[string]string) *check.Context {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Flows = dir
	cfg.Interactions.Levels = []string{"flows"}
	base, _ := url.Parse("http://127.0.0.1:37050/")
	return &check.Context{Config: cfg, Base: base, Run: &model.Run{Pages: []model.Page{{Path: "/checkout/"}}},
		NewSession: func() (*rod.Browser, error) { return nil, errors.New("no browser in a unit test") }}
}

type call struct {
	name string
	dry  bool
	tabs int
}

// fake swaps the player for one that records each call and fails the
// test when two run at once.
func fake(t *testing.T, fn func(flow.Flow, flow.Options) (flow.Result, string)) *[]call {
	t.Helper()
	var calls []call
	var running atomic.Int32
	old := play
	play = func(_ context.Context, _ *check.Context, f flow.Flow, o flow.Options) (flow.Result, string) {
		if running.Add(1) != 1 {
			t.Error("key flows ran at the same time")
		}
		defer running.Add(-1)
		calls = append(calls, call{f.Name, o.Dry, o.MaxTabs})
		return fn(f, o)
	}
	t.Cleanup(func() { play = old })
	return &calls
}

// Only key flows run, one after another, dry for keyboard: true, with
// the keyboard walk's max_tabs.
func TestRunPlaysKeyFlowsOnly(t *testing.T) {
	cc := fakeContext(t, map[string]string{"a-mouse.yaml": mouseFlow, "b-checkout.yaml": dryFlow, "c-contact.yaml": sendFlow,
		"d-broken.yaml": "name: x\nkeyboard: maybe\nsteps: [{wait: 1}]\n"})
	calls := fake(t, func(flow.Flow, flow.Options) (flow.Result, string) { return flow.Result{}, "" })
	Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 40})
	want := []call{{"Checkout dry", true, 40}, {"Contact sends", false, 40}}
	if !slices.Equal(*calls, want) {
		t.Errorf("calls %v, want %v", *calls, want)
	}
	if kf := cc.Run.KeyFlows; kf == nil || kf.Run != 2 || len(kf.Skipped) != 0 {
		t.Errorf("KeyFlows = %+v", kf)
	}
	if len(cc.Run.Warnings) != 0 || len(cc.Run.Issues) != 0 {
		t.Errorf("warnings %q, issues %+v", cc.Run.Warnings, cc.Run.Issues)
	}
}

// Below --level flows no key flow runs: each is listed as skipped and
// the run says why.
func TestRunNeedsLevelFlows(t *testing.T) {
	for _, tc := range []struct {
		files   map[string]string
		skipped int
		note    string
	}{
		{map[string]string{"b-checkout.yaml": dryFlow, "c-contact.yaml": sendFlow, "a.yaml": mouseFlow}, 2,
			"keyboard: 2 key flows need --level flows (they type test data); not run"},
		{map[string]string{"b-checkout.yaml": dryFlow}, 1,
			"keyboard: 1 key flow needs --level flows (it types test data); not run"},
	} {
		cc := fakeContext(t, tc.files)
		cc.Config.Interactions.Levels = []string{"inspect", "click"}
		calls := fake(t, func(flow.Flow, flow.Options) (flow.Result, string) { return flow.Result{}, "" })
		Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 150})
		if len(*calls) != 0 {
			t.Errorf("flows ran: %v", *calls)
		}
		kf := cc.Run.KeyFlows
		if kf == nil || kf.Run != 0 || len(kf.Skipped) != tc.skipped {
			t.Fatalf("KeyFlows = %+v", kf)
		}
		for _, s := range kf.Skipped {
			if s.Reason != SkipLevel || strings.Contains(s.File, "/") {
				t.Errorf("skipped %+v", s)
			}
		}
		if !slices.Equal(cc.Run.Warnings, []string{tc.note}) {
			t.Errorf("warnings %q, want %q", cc.Run.Warnings, tc.note)
		}
	}
}

// A flow file that can't be read is interact's to report. When
// interact won't play flows, Run notes it so a broken key flow isn't
// dropped without a word; when interact will, Run stays quiet.
func TestRunNotesBrokenFlowFiles(t *testing.T) {
	const note = "keyboard: flow file broken.yaml can't be read"
	for _, tc := range []struct {
		name    string
		modules []string
		levels  []string
		want    bool
	}{
		{"interact off", []string{"keyboard"}, []string{"flows"}, true},
		{"below --level flows", []string{"interact", "keyboard"}, []string{"click"}, true},
		{"interact reports it", []string{"interact", "keyboard"}, []string{"flows"}, false},
	} {
		cc := fakeContext(t, map[string]string{"broken.yaml": "name: x\nkeyboard: yes\nsteps: [{wait: 1}]\n", "b.yaml": dryFlow})
		cc.Config.Modules = tc.modules
		cc.Config.Interactions.Levels = tc.levels
		fake(t, func(flow.Flow, flow.Options) (flow.Result, string) { return flow.Result{}, "" })
		Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 150})
		got := slices.ContainsFunc(cc.Run.Warnings, func(w string) bool { return strings.HasPrefix(w, note) })
		if got != tc.want {
			t.Errorf("%s: note %v, want %v; warnings %q", tc.name, got, tc.want, cc.Run.Warnings)
		}
		if cc.Run.KeyFlows == nil || len(cc.Run.KeyFlows.Skipped)+cc.Run.KeyFlows.Run != 1 {
			t.Errorf("%s: the readable key flow went missing: %+v", tc.name, cc.Run.KeyFlows)
		}
	}
	// A flows folder that can't be read at all is noted too.
	cc := fakeContext(t, nil)
	cc.Config.Flows = filepath.Join(cc.Config.Flows, "not-a-folder")
	if err := os.WriteFile(cc.Config.Flows, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cc.Config.Modules = []string{"keyboard"}
	Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 150})
	if len(cc.Run.Warnings) != 1 || !strings.HasPrefix(cc.Run.Warnings[0], "keyboard: flows folder ") || cc.Run.KeyFlows != nil {
		t.Errorf("unreadable folder: warnings %q, KeyFlows %+v", cc.Run.Warnings, cc.Run.KeyFlows)
	}
}

// keyboard: send sends only off public addresses, or with --staging. A
// public target without --staging runs it dry with a note. On a URL
// listed under production the flow runner's refusal applies, so it gets
// no note.
func TestSendNeedsPrivateOrStaging(t *testing.T) {
	note := "keyboard: send runs only on a loopback or private address, or with --staging; c-contact.yaml ran dry"
	for _, tc := range []struct {
		name            string
		public, staging bool
		production      []string
		dry             bool
		warnings        []string
	}{
		{"loopback", false, false, nil, false, nil},
		{"public", true, false, nil, true, []string{note}},
		{"public with --staging", true, true, nil, false, nil},
		{"public production", true, false, []string{"127.0.0.1"}, false, nil},
		// The runner left the target unclassified: the base host decides.
		{"unclassified public", false, false, nil, true, []string{note}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cc := fakeContext(t, map[string]string{"c-contact.yaml": sendFlow})
			if tc.name == "unclassified public" {
				cc.Base, _ = url.Parse("http://203.0.113.7/")
			}
			cc.TargetPublic = tc.public
			cc.Config.Trust.Staging = tc.staging
			cc.Config.Production = tc.production
			if err := cc.Config.Validate(); err != nil {
				t.Fatal(err)
			}
			calls := fake(t, func(flow.Flow, flow.Options) (flow.Result, string) { return flow.Result{}, "" })
			Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 150})
			if len(*calls) != 1 || (*calls)[0].dry != tc.dry {
				t.Errorf("calls %v, want dry %v", *calls, tc.dry)
			}
			if !slices.Equal(cc.Run.Warnings, tc.warnings) {
				t.Errorf("warnings %q, want %q", cc.Run.Warnings, tc.warnings)
			}
		})
	}
}

// What a key flow ran into becomes issues on its start page (or site
// issues when the run didn't visit it) and notes.
func TestReport(t *testing.T) {
	cc := fakeContext(t, map[string]string{"b-checkout.yaml": dryFlow, "c-contact.yaml": sendFlow})
	cc.Run.Options.WCAG = "2.2"
	fake(t, func(f flow.Flow, _ flow.Options) (flow.Result, string) {
		if f.Keyboard == flow.KeyboardDry {
			return flow.Result{Held: &flow.Held{Step: 2, Method: "POST", Path: "/checkout/"},
				Failures: []flow.Failure{{Step: 1, Desc: "click #a", Err: errors.New("not reached after 150 Tab stops; focus stayed on #menu"),
					Key: &flow.KeyFailure{Kind: "click", Target: "#a", Reason: "not reached after 150 Tab stops; focus stayed on #menu",
						Path: []string{"a.home", "#menu"}}}}}, ""
		}
		return flow.Result{Failures: []flow.Failure{
			{Step: 1, Desc: "click #send", Err: flow.ErrProduction, Refused: true},
			{Step: 1, Desc: "click #send", Err: errors.New("#send not found")},
		}}, ""
	})
	Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 150})
	if !slices.Equal(cc.Run.Warnings, []string{"keyboard: b-checkout.yaml stopped at step 2 (would have sent POST /checkout/); steps 3-4 not run"}) {
		t.Errorf("warnings %q", cc.Run.Warnings)
	}
	page := cc.Run.Page("/checkout/")
	if len(page.Issues) != 1 {
		t.Fatalf("page issues %+v", page.Issues)
	}
	i := page.Issues[0]
	want := model.Issue{Check: ids.Flow, Level: model.Error, Title: "Key flow can't be finished by keyboard",
		Detail: `Checkout dry: step 1 click "#a": not reached after 150 Tab stops; focus stayed on #menu`,
		Source: "b-checkout.yaml: click #a", WCAG: []string{"2.1.1"}, EN301549: []string{"9.2.1.1"}, Impact: "critical",
		FocusPath: []string{"a.home", "#menu"}}
	if i.Check != want.Check || i.Level != want.Level || i.Title != want.Title || i.Detail != want.Detail || i.Source != want.Source ||
		!slices.Equal(i.WCAG, want.WCAG) || !slices.Equal(i.EN301549, want.EN301549) || i.Impact != want.Impact ||
		!slices.Equal(i.FocusPath, want.FocusPath) || i.Key != "keyboard/flow|b-checkout.yaml: click #a" || len(i.Pages) != 0 {
		t.Errorf("issue\n got %+v\nwant %+v", i, want)
	}
	// /contact/ wasn't visited: its issues are the site's.
	var got []string
	for _, i := range cc.Run.Issues {
		got = append(got, i.Check+": "+i.Title+": "+i.Detail+" @"+strings.Join(i.Pages, ","))
	}
	wantSite := []string{
		`interact/flow-production: Flow "Contact sends" step 1 refused on production: click #send: ` + flow.ErrProduction.Error() + " @/contact/",
		`interact/flow: Flow "Contact sends" step 1 failed: click #send: #send not found @/contact/`,
	}
	if !slices.Equal(got, wantSite) {
		t.Errorf("site issues\n got %q\nwant %q", got, wantSite)
	}
}

func TestHeldNotes(t *testing.T) {
	for _, tc := range []struct {
		step int
		want string
	}{
		{3, "keyboard: b-checkout.yaml stopped at step 3 (would have sent PUT /cart/); step 4 not run"},
		{4, "keyboard: b-checkout.yaml stopped at step 4 (would have sent PUT /cart/)"},
	} {
		cc := fakeContext(t, map[string]string{"b-checkout.yaml": dryFlow})
		fake(t, func(flow.Flow, flow.Options) (flow.Result, string) {
			return flow.Result{Held: &flow.Held{Step: tc.step, Method: "PUT", Path: "/cart/"}}, ""
		})
		Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 150})
		if !slices.Equal(cc.Run.Warnings, []string{tc.want}) {
			t.Errorf("step %d: %q", tc.step, cc.Run.Warnings)
		}
	}
}

// A flow the player skips, such as one needing a login without
// credentials, is listed with its reason; a stopped run skips the rest.
func TestSkips(t *testing.T) {
	cc := fakeContext(t, map[string]string{"b-checkout.yaml": dryFlow, "c-contact.yaml": sendFlow})
	ctx, cancel := context.WithCancel(context.Background())
	fake(t, func(flow.Flow, flow.Options) (flow.Result, string) { cancel(); return flow.Result{}, SkipLogin })
	Run(ctx, cc, model.KeyboardOptions{MaxTabs: 150})
	want := []model.SkippedFlow{{File: "b-checkout.yaml", Reason: SkipLogin}, {File: "c-contact.yaml", Reason: SkipStopped}}
	if kf := cc.Run.KeyFlows; kf == nil || kf.Run != 0 || !slices.Equal(kf.Skipped, want) {
		t.Errorf("KeyFlows = %+v", kf)
	}
	if !slices.Equal(cc.Run.Warnings, []string{"keyboard: b-checkout.yaml not run: " + SkipLogin}) {
		t.Errorf("warnings %q", cc.Run.Warnings)
	}
}

// Form errors a key flow saw that aren't tied to their fields are
// keyboard/error-not-linked on its start page, keyed by the form.
func TestReportUnlinked(t *testing.T) {
	cc := fakeContext(t, map[string]string{"b-checkout.yaml": dryFlow})
	fake(t, func(flow.Flow, flow.Options) (flow.Result, string) {
		return flow.Result{Unlinked: []flow.Unlinked{
			{Step: 2, Form: "#checkout", Fields: 3, Focus: "#place_order"},
			{Step: 3, Form: "form.cart", Fields: 1, Focus: "body"},
		}}, ""
	})
	Run(context.Background(), cc, model.KeyboardOptions{MaxTabs: 150})
	page := cc.Run.Page("/checkout/")
	if len(page.Issues) != 2 {
		t.Fatalf("issues %+v", page.Issues)
	}
	i := page.Issues[0]
	if i.Check != ids.ErrorNotLinked || i.Level != model.Warning || i.Title != "Form errors aren't tied to their fields" ||
		i.Source != "#checkout" || i.Impact != "serious" || !slices.Equal(i.WCAG, []string{"3.3.1"}) ||
		!slices.Equal(i.EN301549, []string{"9.3.3.1"}) || i.Key != "keyboard/error-not-linked|#checkout" ||
		i.Detail != "After Checkout dry step 2, 3 fields show errors but focus stayed on #place_order and the errors aren't linked with aria-describedby or aria-errormessage." {
		t.Errorf("issue %+v", i)
	}
	if d := page.Issues[1].Detail; d != "After Checkout dry step 3, 1 field shows errors but focus stayed on body and the errors aren't linked with aria-describedby or aria-errormessage." {
		t.Errorf("detail %q", d)
	}
}

// A target counts as local only when its host is localhost or a
// loopback, private or link-local address.
func TestLocalHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "shop.localhost": true, "127.0.0.1": true, "::1": true,
		"10.0.0.5": true, "192.168.1.20": true, "172.16.0.1": true, "169.254.1.1": true, "fd00::1": true,
		"203.0.113.7": false, "8.8.8.8": false, "2001:db8::1": false, "": false,
	} {
		if got := localHost(context.Background(), host); got != want {
			t.Errorf("localHost(%q) = %v, want %v", host, got, want)
		}
	}
}
