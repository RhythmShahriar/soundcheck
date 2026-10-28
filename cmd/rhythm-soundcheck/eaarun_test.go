// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/evidence"
	"github.com/RhythmShahriar/soundcheck/login"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/runner"
	"github.com/RhythmShahriar/soundcheck/statement"
)

// fakeEAA stands in for the browser and for the statement and evidence
// packages, which other parts of 1.3 build: each check returns pages
// (or what next returns for the check's number), the draft records the
// owner it got and the evidence pack records whether the run was
// interrupted.
type fakeEAA struct {
	pages  func(n int) []model.Page
	total  int // the pages the runner says it will visit, when more than it returns
	checks int
	owners []string
	prev   []string // targets evidence.Previous was asked about
}

func (f *fakeEAA) install(t *testing.T) {
	t.Helper()
	oldRun, oldBuild, oldMD, oldEv, oldPrev := runSite, buildStatement, statementMD, writeEvidence, previousEvidence
	t.Cleanup(func() {
		runSite, buildStatement, statementMD, writeEvidence, previousEvidence = oldRun, oldBuild, oldMD, oldEv, oldPrev
	})
	runSite = func(_ context.Context, opts runner.Options) (*model.Run, error) {
		f.checks++
		cfg := opts.Config
		run := &model.Run{Schema: model.Schema, Tool: "rhythm-soundcheck", Version: opts.Version, Target: opts.Target, Name: opts.Name,
			Options: model.Options{Viewports: cfg.Viewports, Levels: cfg.Interactions.Levels, Modules: cfg.Modules, Limit: cfg.Pages.Limit},
			Pages:   f.pages(f.checks)}
		if opts.Hooks.Discovered != nil {
			opts.Hooks.Discovered(max(f.total, len(run.Pages)), nil)
		}
		for i := range run.Pages {
			if opts.OnPage != nil {
				opts.OnPage(&run.Pages[i])
			}
		}
		return run, nil
	}
	buildStatement = func(in statement.Input) (*statement.Draft, error) {
		if !in.Run.Options.EAA || in.Summary.EvidenceNote != model.EvidenceNote {
			return nil, statement.ErrNoEAAData
		}
		f.owners = append(f.owners, in.Owner.Owner+" | "+in.Owner.Website+" | "+in.Owner.Contact.Email)
		return &statement.Draft{Layout: in.Layout, Todo: 1}, nil
	}
	statementMD = func(w io.Writer, d *statement.Draft) error {
		_, err := fmt.Fprintf(w, "# Accessibility statement draft (%s)\n", d.Layout)
		return err
	}
	writeEvidence = func(dir string, run *model.Run, prev *evidence.Ref, extra evidenceExtra) error {
		data, _ := json.Marshal(map[string]any{"interrupted": run.Interrupted, "insecure": extra.Insecure, "target": run.Target, "prev": prev != nil})
		return writePrivate(filepath.Join(dir, eaaDir, evidenceFile), func(w io.Writer) error { _, err := w.Write(data); return err })
	}
	previousEvidence = func(root, target string) (*evidence.Ref, error) {
		f.prev = append(f.prev, target)
		return nil, nil
	}
}

// audited is a page whose a11y and keyboard audits ended with status.
func audited(path, status string) model.Page {
	return model.Page{Path: path, URL: "http://127.0.0.1:20260" + path, Status: 200,
		Audits: map[string]model.Audit{"a11y": {Status: status}, "keyboard": {Status: model.AuditOK}}}
}

func clean(int) []model.Page {
	return []model.Page{audited("/", model.AuditOK), audited("/shop/", model.AuditCapped)}
}

// mode returns path's permission bits, or 0600 where Windows can't say.
func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return 0o600
	}
	return fi.Mode().Perm()
}

// A clean --eaa snapshot exits 0, leaves a draft and an evidence pack
// only its owner can read and points at the draft before what's next.
func TestEAASnapshotWritesDraftAndEvidence(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	writeConfig(t, "")
	f := &fakeEAA{pages: clean}
	f.install(t)
	code, out, errOut := runCLI(t, "snapshot", "127.0.0.1:20260", "--eaa", "--name", "eaa-1", "--plain")
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	dir := filepath.Join(".soundcheck", "snapshots", "eaa-1", eaaDir)
	for _, name := range []string{draftFile, evidenceFile} {
		if m := mode(t, filepath.Join(dir, name)); m != 0o600 {
			t.Errorf("%s: mode %o", name, m)
		}
	}
	if m := mode(t, dir); m != 0o700 && runtime.GOOS != "windows" {
		t.Errorf("eaa folder: mode %o", m)
	}
	want := "statement: .soundcheck/snapshots/eaa-1/eaa/statement-draft.md (a draft for you to finish:\n" +
		"           rhythm-soundcheck statement -o accessibility-statement.md)\n" +
		"next: make your change (update a plugin, deploy…), then run: rhythm-soundcheck compare\n"
	if !strings.HasSuffix(slashes(out), want) {
		t.Errorf("end of the run:\n%s\nwant it to end with\n%s", out, want)
	}
	// No statement: block yet: the draft says what to fill in.
	if len(f.owners) != 1 || f.owners[0] != todoOwner+" | "+todoWebsite+" | "+todoEmail {
		t.Errorf("owners %q", f.owners)
	}
	run := readRunJSON(t, filepath.Join(".soundcheck", "snapshots", "eaa-1"))
	if !run.Options.EAA || run.Options.WCAG != "2.1" || run.Options.Keyboard == nil {
		t.Errorf("run.json options %+v", run.Options)
	}
	if len(f.prev) != 1 || f.prev[0] != "127.0.0.1:20260" {
		t.Errorf("evidence.Previous asked about %q", f.prev)
	}

	// The --json output carries the eaa block.
	code, out, errOut = runCLI(t, "snapshot", "127.0.0.1:20260", "--eaa", "--name", "eaa-2", "--json")
	var res struct {
		EAA *model.EAASummary `json:"eaa"`
	}
	if code != exitOK || json.Unmarshal([]byte(out), &res) != nil || res.EAA == nil || res.EAA.EvidenceNote != model.EvidenceNote {
		t.Errorf("--json: exit %d %+v\n%s\n%s", code, res.EAA, out, errOut)
	}

	// A compare and a run write both files into their own run folder (the
	// after run for a comparison), as does --against.
	for _, args := range [][]string{
		{"compare", "eaa-1", "--confirm=off", "--plain"},
		{"run", "127.0.0.1:20260", "--eaa", "--plain"},
		{"127.0.0.1:20260", "--against", "127.0.0.1:20261", "--eaa", "--confirm=off", "--plain"},
	} {
		before, _ := filepath.Glob(filepath.Join(".soundcheck", "runs", "*", eaaDir, draftFile))
		code, out, errOut := runCLI(t, args...)
		after, _ := filepath.Glob(filepath.Join(".soundcheck", "runs", "*", eaaDir, draftFile))
		if code != exitOK || len(after) != len(before)+1 {
			t.Errorf("%v: exit %d, drafts %d -> %d\n%s\n%s", args, code, len(before), len(after), out, errOut)
			continue
		}
		draft := after[len(after)-1]
		for _, path := range []string{draft, filepath.Join(filepath.Dir(draft), evidenceFile)} {
			if m := mode(t, path); m != 0o600 {
				t.Errorf("%v: %s mode %o", args, path, m)
			}
		}
		if !strings.Contains(slashes(out), "statement: "+slashes(draft)+" (a draft for you to finish:\n") {
			t.Errorf("%v: no statement line for %s\n%s", args, draft, out)
		}
	}

	// With statement: filled in, the draft uses it; a mistake in it is a
	// usage error before anything runs.
	old := decodeOwner
	t.Cleanup(func() { decodeOwner = old })
	decodeOwner = func(config.Config) (statement.Owner, error) {
		return statement.Owner{Owner: "Example Coffee Ltd", Website: "https://shop.example", Contact: statement.Contact{Email: "access@shop.example"}}, nil
	}
	f.owners = nil
	if code, _, errOut := runCLI(t, "snapshot", "127.0.0.1:20260", "--eaa", "--name", "eaa-3", "-q"); code != exitOK ||
		len(f.owners) != 1 || f.owners[0] != "Example Coffee Ltd | https://shop.example | access@shop.example" {
		t.Errorf("snapshot with statement: exit %d %q\n%s", code, f.owners, errOut)
	}
	decodeOwner = func(config.Config) (statement.Owner, error) {
		return statement.Owner{}, errors.New(".soundcheck/config.yaml: line 3: statement.country: \"XX\" isn't a country code")
	}
	checks := f.checks
	if code, _, errOut := runCLI(t, "snapshot", "127.0.0.1:20260", "--eaa", "--name", "eaa-4"); code != exitUsage ||
		!strings.Contains(errOut, "statement.country") || f.checks != checks {
		t.Errorf("bad statement: block: exit %d %q", code, errOut)
	}
}

// readRunJSON reads the head of a stored run.
func readRunJSON(t *testing.T, dir string) model.Run {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r model.Run
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// A page whose keyboard or a11y audit didn't finish exits 3, whatever
// --fail-on says: the evidence is incomplete.
func TestEAANotAuditedExits3(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	writeConfig(t, "")
	broken := false
	f := &fakeEAA{pages: func(int) []model.Page {
		if !broken {
			return clean(0)
		}
		pages := clean(0)
		pages[1].Audits["a11y"] = model.Audit{Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}
		pages[1].Issues = []model.Issue{{Check: "a11y/label", Level: model.Warning, Title: "Form field without a label", Source: "#q"}}
		return pages
	}}
	f.install(t)
	if code, out, errOut := runCLI(t, "snapshot", "127.0.0.1:20260", "--eaa", "--name", "base", "--plain"); code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	broken = true
	want := "rhythm-soundcheck: the accessibility evidence is incomplete: the a11y or keyboard tests didn't finish on 1 page (see \"not checked\" above), so the statement marks them not tested\n"
	for _, failOn := range []string{"new", "none"} {
		code, out, errOut := runCLI(t, "compare", "--confirm=off", "--fail-on", failOn, "--plain")
		if code != exitError || !strings.HasSuffix(errOut, want) {
			t.Errorf("compare --fail-on %s: exit %d\n%s\n%s", failOn, code, out, errOut)
		}
	}
	code, out, errOut := runCLI(t, "run", "127.0.0.1:20260", "--eaa", "--fail-on", "warning", "--plain")
	if code != exitError || !strings.HasSuffix(errOut, want) {
		t.Errorf("run --fail-on warning: exit %d\n%s\n%s", code, out, errOut)
	}
	// Without --eaa an unfinished audit doesn't change the exit code.
	code, _, errOut = runCLI(t, "run", "127.0.0.1:20260", "--modules", "health,a11y,keyboard", "--plain")
	if code != exitOK || strings.Contains(errOut, "incomplete") {
		t.Errorf("run without --eaa: exit %d\n%s", code, errOut)
	}
	// A page that answered 404 isn't evidence that went missing.
	f.pages = func(int) []model.Page {
		pages := clean(0)
		pages[1].Status = 404
		pages[1].Audits["a11y"] = model.Audit{Status: model.AuditSkipped, Reason: "status 404"}
		pages[1].Audits["keyboard"] = model.Audit{Status: model.AuditSkipped, Reason: "HTTP 404"}
		return pages
	}
	if code, out, errOut := runCLI(t, "run", "127.0.0.1:20260", "--eaa", "--fail-on", "none", "--plain"); code != exitOK || strings.Contains(errOut, "incomplete") {
		t.Errorf("run with a 404 page: exit %d\n%s\n%s", code, out, errOut)
	}
}

// An interrupted --eaa run exits 130 and writes the evidence pack,
// marked interrupted. It writes no draft: that would leave problems out.
func TestEAAInterrupted(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	writeConfig(t, "")
	f := &fakeEAA{pages: func(int) []model.Page {
		pages := clean(0)
		pages[1].Error = model.CanceledError
		return pages
	}, total: 5}
	f.install(t)
	code, out, errOut := runCLI(t, "run", "127.0.0.1:20260", "--eaa", "--plain")
	if code != exitCanceled {
		t.Fatalf("exit %d, want 130\n%s\n%s", code, out, errOut)
	}
	evs, _ := filepath.Glob(filepath.Join(".soundcheck", "runs", "*", eaaDir, evidenceFile))
	if len(evs) != 1 {
		t.Fatalf("evidence packs: %v", evs)
	}
	data, err := os.ReadFile(evs[0])
	if err != nil || !strings.Contains(string(data), `"interrupted":true`) {
		t.Errorf("evidence: %s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(evs[0]), draftFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an interrupted run left a draft: %v", err)
	}
	if strings.Contains(out, "statement:") {
		t.Errorf("an interrupted run points at a draft:\n%s", out)
	}
	// It records the pages it set out to visit, for the statement
	// command's refusal: "after 1 of 5 pages", not "1 of 2".
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(evs[0]), progressFile)); err != nil || string(data) != `{"pages":5}` {
		t.Errorf("progress: %s %v", data, err)
	}
	code, _, errOut = runCLI(t, "statement")
	if !strings.Contains(errOut, " was interrupted after 1 of 5 pages;") || code != exitError {
		t.Errorf("statement: exit %d %q", code, errOut)
	}
}

// A run made with -k says so in its evidence pack, so evidence sign
// refuses it; a run that verified TLS can be signed.
func TestEAAInsecureEvidence(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	writeConfig(t, "")
	f := &fakeEAA{pages: clean}
	f.install(t)
	fakeEvidence(t)
	if err := os.WriteFile("sign.key", []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		flags []string
		code  int
		err   string
	}{
		{"eaa-k", []string{"-k"}, exitUsage, "rhythm-soundcheck: can't sign a run made with -k: TLS wasn't verified\n"},
		{"eaa-tls", nil, exitOK, ""},
	} {
		args := append([]string{"snapshot", "127.0.0.1:20260", "--eaa", "--name", tc.name, "--plain"}, tc.flags...)
		if code, out, errOut := runCLI(t, args...); code != exitOK {
			t.Fatalf("%s: exit %d\n%s\n%s", tc.name, code, out, errOut)
		}
		data, err := os.ReadFile(filepath.Join(".soundcheck", "snapshots", tc.name, eaaDir, evidenceFile))
		want := fmt.Sprintf(`"insecure":%t`, tc.flags != nil)
		if err != nil || !strings.Contains(string(data), want) {
			t.Errorf("%s: evidence %s %v, want %s", tc.name, data, err, want)
		}
		code, _, errOut := runCLI(t, "evidence", "sign", tc.name, "--key", "sign.key")
		if code != tc.code || errOut != tc.err {
			t.Errorf("%s: sign exit %d %q, want %d %q", tc.name, code, errOut, tc.code, tc.err)
		}
	}
}

// The header says what the accessibility and keyboard tests do, how
// many key flows there are and, under --eaa, what the run never does.
func TestEAAHeader(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	writeConfig(t, "")
	flows := map[string]string{
		"keyboard-checkout.yaml": "name: Checkout by keyboard\nstart: /checkout/\nkeyboard: true\nsteps: []\n",
		"keyboard-contact.yml":   "name: Contact\nstart: /contact/\nkeyboard: send\nsteps: []\n",
		"example.yaml":           "name: Example\nstart: /\nsteps: []\n",
		"off.yaml":               "name: Off\nstart: /\nkeyboard: false\nsteps: []\n",
	}
	for name, body := range flows {
		if err := os.WriteFile(filepath.Join(".soundcheck", "flows", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := runSite
	t.Cleanup(func() { runSite = old })
	runSite = func(context.Context, runner.Options) (*model.Run, error) { return nil, errors.New("site down") }
	_, out, _ := runCLI(t, "run", "127.0.0.1:20260", "--eaa", "--plain")
	want := `rhythm-soundcheck 1.0.0-test - 127.0.0.1:20260

Preset: --eaa: modules health, a11y, keyboard; WCAG 2.1
Modules: health, a11y, keyboard
Accessibility: axe-core 4.14.0, WCAG 2.1 A and AA rules, reflow at 320 px
Keyboard: Tab walk on every page (up to 150 stops), menus and dialogs; 2 key flows (keyboard-checkout.yaml, keyboard-contact.yml) need --level flows, not run
Never: submits a form, places an order or types into fields outside key flows
Levels: none: the interact module is off
Viewports: desktop, mobile
Production: none listed: nothing in this run submits forms
`
	if out != want {
		t.Errorf("header:\n%s\nwant\n%s", out, want)
	}
	// a11y alone: only its line; interact's submit level drops "Never".
	_, out, _ = runCLI(t, "run", "127.0.0.1:20260", "--modules", "+a11y", "--plain")
	if !strings.Contains(out, "Accessibility: axe-core 4.14.0, WCAG 2.1 A and AA rules") || strings.Contains(out, "Keyboard:") || strings.Contains(out, "Never:") {
		t.Errorf("+a11y header:\n%s", out)
	}
	_, out, _ = runCLI(t, "run", "127.0.0.1:20260", "--eaa", "--modules", "+interact", "--level", "flows", "--plain")
	if strings.Contains(out, "Never:") || !strings.Contains(out, "2 key flows (keyboard-checkout.yaml, keyboard-contact.yml) run") {
		t.Errorf("--eaa with --level flows:\n%s", out)
	}
	// --level flows without the interact module: key flows run and the
	// Levels line says so, rather than "none".
	_, out, _ = runCLI(t, "run", "127.0.0.1:20260", "--eaa", "--level", "flows", "--plain")
	if !strings.Contains(out, "2 key flows (keyboard-checkout.yaml, keyboard-contact.yml) run") ||
		!strings.Contains(out, "\nLevels: flows (key flows only; the interact module is off)\n") {
		t.Errorf("--eaa --level flows without interact:\n%s", out)
	}
}

// Under --eaa with key flows the header says what the levels do: the
// interact module is off, so nothing is clicked or submitted outside key
// flows. A snapshot's stored submit level isn't listed.
func TestEAALevelsHeader(t *testing.T) {
	cfg := config.Default()
	cfg.Modules = []string{"health", "a11y", "keyboard"}
	cfg.Interactions.Levels = config.Cumulative([]string{"inspect", "click", "flows"})
	if got := levels(cfg); got != "flows (key flows only; the interact module is off)" {
		t.Errorf("levels: %s", got)
	}
	if got := production(cfg); got != "none listed: only key flows with keyboard: send submit, on a local or private address or with --staging" {
		t.Errorf("production: %s", got)
	}
	snap := model.Options{Modules: cfg.Modules, Levels: cfg.Interactions.Levels, Viewports: []string{"desktop"}}
	if got := snapshotSettings("before", snap); got != `from snapshot "before" (modules health,a11y,keyboard; levels flows; viewports desktop)` {
		t.Errorf("settings: %s", got)
	}
	snap.Modules = append(snap.Modules, "interact")
	if got := snapshotSettings("before", snap); !strings.Contains(got, "levels inspect,click,submit,flows") {
		t.Errorf("settings with interact: %s", got)
	}
}

func TestKeyFlowsLine(t *testing.T) {
	for _, tc := range []struct {
		files []string
		flows bool
		want  string
	}{
		{nil, false, "no key flows"},
		{[]string{"a.yaml"}, false, "1 key flow (a.yaml) needs --level flows, not run"},
		{[]string{"a.yaml"}, true, "1 key flow (a.yaml) runs"},
		{[]string{"a.yaml", "b.yaml"}, false, "2 key flows (a.yaml, b.yaml) need --level flows, not run"},
		{[]string{"a.yaml", "b.yaml"}, true, "2 key flows (a.yaml, b.yaml) run"},
	} {
		if got := keyFlowsLine(tc.files, tc.flows); got != tc.want {
			t.Errorf("%v %v: %q", tc.files, tc.flows, got)
		}
	}
}

// writePrivate replaces a symlink at the path instead of writing
// through it.
func TestWritePrivateReplacesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "eaa", "draft.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks here: ", err)
	}
	if err := writePrivate(link, func(w io.Writer) error { _, err := io.WriteString(w, "new"); return err }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "keep" {
		t.Errorf("wrote through the symlink: %q", data)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("the link is still there: %v", err)
	}
}

// eaaFolder refuses a run folder whose eaa/ links elsewhere and lets a
// real folder or a missing one through.
func TestEAAFolderRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := eaaFolder(dir); err != nil {
		t.Errorf("no eaa folder: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, eaaDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := eaaFolder(dir); err != nil {
		t.Errorf("a real eaa folder: %v", err)
	}
	run, elsewhere := t.TempDir(), t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(run, eaaDir)); err != nil {
		t.Skip("no symlinks here: ", err)
	}
	if err := eaaFolder(run); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a linked eaa folder: %v", err)
	}
	if err := recordWritten(run, filepath.Join(run, "statement.md"), []byte("x")); err == nil {
		t.Error("recordWritten wrote through the link")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote into the link's target: %v", entries)
	}
}

// The draft never shows the test account's user name.
func TestStatementHidesTheLoginUser(t *testing.T) {
	t.Setenv(login.EnvUser, "")
	if got := statementHide(); got != nil {
		t.Errorf("no user: %q", got)
	}
	t.Setenv(login.EnvUser, "shopper")
	if got := statementHide(); !slices.Equal(got, []string{"shopper"}) {
		t.Errorf("user: %q", got)
	}
}

// accept says what it did to the snapshot's evidence pack. It stays
// quiet for a snapshot without one.
func TestAcceptEvidenceNote(t *testing.T) {
	var b strings.Builder
	evidenceNote(acceptedPack{}, "base", &b)
	if b.Len() != 0 {
		t.Errorf("no pack: %q", b.String())
	}
	evidenceNote(acceptedPack{kept: "evidence/ab12", signed: true}, "base", &b)
	for _, want := range []string{`note: accept changed run.json, the pages file and snapshot.json of snapshot "base"`,
		"kept in .soundcheck/evidence/ab12", "evidence verify of later --eaa runs", "evidence sign base --key"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in %q", want, b.String())
		}
	}
	b.Reset()
	evidenceNote(acceptedPack{kept: "evidence/ab12", err: errors.New("disk full")}, "base", &b)
	if !strings.Contains(b.String(), "couldn't be recorded again: disk full") {
		t.Errorf("failed: %q", b.String())
	}
}

// A writer that isn't a terminal keeps the report's default width.
func TestColumnsNotATerminal(t *testing.T) {
	var b strings.Builder
	if got := columns(&b); got != 0 {
		t.Errorf("a buffer: %d", got)
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if got := columns(f); got != 0 {
		t.Errorf("a file: %d", got)
	}
}

// report --client of a stored --eaa snapshot or comparison carries the
// accessibility evidence and its note: the summary isn't in run.json,
// so report has to make it again before the client page is written.
func TestReportClientEAA(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("SOUNDCHECK_ECHO", "")
	t.Setenv("SOUNDCHECK_NO_SANDBOX", "")
	writeConfig(t, "")
	f := &fakeEAA{pages: clean}
	f.install(t)
	if code, out, errOut := runCLI(t, "snapshot", "127.0.0.1:20260", "--eaa", "--name", "eaa-1", "-q"); code != exitOK {
		t.Fatalf("snapshot: exit %d\n%s\n%s", code, out, errOut)
	}
	if err := os.WriteFile("b.yaml", []byte("name: Acme Web\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	note := []byte("Automated and keyboard tests")
	for _, args := range [][]string{
		{"report", "eaa-1", "--client", "--brand", "b.yaml", "-o", "snap.html"},
		{"compare", "eaa-1", "--confirm=off", "-q"},
		{"report", "--client", "--brand", "b.yaml", "-o", "cmp.html"},
	} {
		if code, out, errOut := runCLI(t, args...); code != exitOK {
			t.Fatalf("%v: exit %d\n%s\n%s", args, code, out, errOut)
		}
	}
	for _, name := range []string{"snap.html", "cmp.html"} {
		data, err := os.ReadFile(name)
		if err != nil || !bytes.Contains(data, note) {
			t.Errorf("%s: no evidence note (%v)\n%.600s", name, err, data)
		}
	}
	// Negative: a run made without --eaa gets no note.
	if code, _, errOut := runCLI(t, "snapshot", "127.0.0.1:20260", "--name", "plain", "-q"); code != exitOK {
		t.Fatalf("plain snapshot: exit %d %s", code, errOut)
	}
	if code, _, errOut := runCLI(t, "report", "plain", "--client", "--brand", "b.yaml", "-o", "plain.html"); code != exitOK {
		t.Fatalf("plain report: exit %d %s", code, errOut)
	}
	if data, _ := os.ReadFile("plain.html"); bytes.Contains(data, note) {
		t.Error("a run without --eaa got the evidence note")
	}
}
