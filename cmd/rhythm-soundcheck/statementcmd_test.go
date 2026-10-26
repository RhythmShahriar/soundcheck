// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/statement"
	"github.com/RhythmShahriar/soundcheck/store"
)

// fakeStatement stands in for the statement package: the draft records
// its layout and WCAG version and each format writes its name.
func fakeStatement(t *testing.T, owner statement.Owner) *[]statement.Input {
	t.Helper()
	oldBuild, oldMD, oldHTML, oldJSON, oldEARL, oldOwner := buildStatement, statementMD, statementHTML, statementJSON, statementEARL, decodeOwner
	t.Cleanup(func() {
		buildStatement, statementMD, statementHTML, statementJSON, statementEARL, decodeOwner = oldBuild, oldMD, oldHTML, oldJSON, oldEARL, oldOwner
	})
	var inputs []statement.Input
	buildStatement = func(in statement.Input) (*statement.Draft, error) {
		inputs = append(inputs, in)
		return &statement.Draft{Layout: in.Layout, Standard: in.Run.Options.WCAG, Todo: 2}, nil
	}
	render := func(name string) func(io.Writer, *statement.Draft) error {
		return func(w io.Writer, d *statement.Draft) error {
			_, err := fmt.Fprintf(w, "%s %s %s\n", name, d.Layout, d.Standard)
			return err
		}
	}
	statementMD, statementHTML, statementJSON, statementEARL = render("md"), render("html"), render("json"), render("earl")
	decodeOwner = func(config.Config) (statement.Owner, error) { return owner, nil }
	return &inputs
}

var shopOwner = statement.Owner{Owner: "Example Coffee Ltd", Website: "https://shop.example", Contact: statement.Contact{Email: "access@shop.example"}}

// storeRun saves a run into the store's runs folder under a name made
// from at, as a check would leave it. It returns the run's folder.
func storeRun(t *testing.T, at time.Time, o model.Options, pages ...model.Page) string {
	t.Helper()
	dir := filepath.Join(config.Dir, "runs", store.Stamp(at))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		pages = []model.Page{audited("/", model.AuditOK)}
	}
	run := &model.Run{Schema: model.Schema, Tool: "rhythm-soundcheck", Version: "1.0.0", Target: "http://127.0.0.1:20260/",
		Started: at, Options: o, Pages: pages}
	if err := store.SaveRun(dir, run); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStatementCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "")
	inputs := fakeStatement(t, shopOwner)
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	eaaRun := storeRun(t, now, model.Options{EAA: true, WCAG: "2.1"})
	// A newer run without --eaa isn't the default source.
	plain := storeRun(t, now.Add(time.Hour), model.Options{WCAG: "2.1"})

	code, out, errOut := runCLI(t, "statement")
	if code != exitOK || out != "md model 2.1\n" || errOut != "2 [TO DO] markers left\n" {
		t.Fatalf("default: exit %d %q %q", code, out, errOut)
	}
	if in := (*inputs)[0]; in.Run.Target != "http://127.0.0.1:20260/" || in.Owner.Owner != "Example Coffee Ltd" || in.Version != version ||
		in.Summary.EvidenceNote != model.EvidenceNote || in.Pages == nil {
		t.Errorf("input %+v", in)
	}
	for _, args := range [][]string{{"--html"}, {"--json"}, {"--earl"}, {"-o", "-"}} {
		code, out, _ := runCLI(t, append([]string{"statement", eaaRun}, args...)...)
		want := map[string]string{"--html": "html", "--json": "json", "--earl": "earl", "-o": "md"}[args[0]] + " model 2.1\n"
		if code != exitOK || out != want {
			t.Errorf("%v: exit %d %q", args, code, out)
		}
	}

	// Files, each in the format of its extension, only for the owner.
	code, out, errOut = runCLI(t, "statement", "-o", "out/s.md", "-o", "out/s.html", "-o", "out/s.json", "-o", "out/s.jsonld", "--layout", "service")
	if code != exitOK || out != "" {
		t.Fatalf("-o: exit %d %q %q", code, out, errOut)
	}
	for file, want := range map[string]string{"s.md": "md", "s.html": "html", "s.json": "json", "s.jsonld": "earl"} {
		path := filepath.Join("out", file)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want+" service 2.1\n" {
			t.Errorf("%s: %q %v", file, data, err)
		}
		if m := mode(t, path); m != 0o600 {
			t.Errorf("%s: mode %o", file, m)
		}
		if !strings.Contains(errOut, "Statement draft written to "+path+"\n") {
			t.Errorf("no line for %s:\n%s", path, errOut)
		}
	}
	if w := readWritten(eaaRun); len(w) != 4 {
		t.Errorf("written.json: %v", w)
	}

	// Writing again replaces a draft nobody touched; an edited one needs
	// --force, which leaves the other files as they were until then.
	if code, _, errOut := runCLI(t, "statement", "-o", "out/s.md"); code != exitOK {
		t.Errorf("again: exit %d %s", code, errOut)
	}
	if err := os.WriteFile(filepath.Join("out", "s.md"), []byte("md service 2.1\nOur own words.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runCLI(t, "statement", "-o", "out/s.html", "-o", "out/s.md")
	if want := "rhythm-soundcheck: out/s.md was edited since Soundcheck wrote it; write to another file with -o, or add --force to replace your edits\n"; code != exitUsage || slashes(errOut) != want {
		t.Errorf("edited: exit %d %q", code, errOut)
	}
	if data, _ := os.ReadFile(filepath.Join("out", "s.html")); string(data) != "html service 2.1\n" {
		t.Errorf("a refused write changed s.html: %q", data)
	}
	if code, _, errOut := runCLI(t, "statement", "-o", "out/s.md", "--force"); code != exitOK {
		t.Errorf("--force: exit %d %s", code, errOut)
	}
	if data, _ := os.ReadFile(filepath.Join("out", "s.md")); string(data) != "md model 2.1\n" {
		t.Errorf("after --force: %q", data)
	}
	// A file Soundcheck never wrote counts as edited.
	if err := os.WriteFile("mine.md", []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runCLI(t, "statement", "-o", "mine.md"); code != exitUsage {
		t.Errorf("someone else's file: exit %d", code)
	}

	// A symlink at -o is never followed, even with --force.
	if err := os.Symlink("mine.md", "link.md"); err == nil {
		code, _, errOut := runCLI(t, "statement", "-o", "link.md", "--force")
		if code != exitError || errOut != "rhythm-soundcheck: refusing to write through a symlink: link.md\n" {
			t.Errorf("symlink: exit %d %q", code, errOut)
		}
		if data, _ := os.ReadFile("mine.md"); string(data) != "mine" {
			t.Errorf("wrote through the symlink: %q", data)
		}
	}

	// A run folder whose eaa/ links elsewhere is refused before any -o
	// file is written: written.json would land at the link's target.
	linked := filepath.Join(eaaRun, eaaDir)
	if err := os.Rename(linked, "elsewhere"); err != nil {
		t.Fatal(err)
	}
	if abs, _ := filepath.Abs("elsewhere"); os.Symlink(abs, linked) == nil {
		code, _, errOut := runCLI(t, "statement", eaaRun, "-o", "linked.md")
		if want := "rhythm-soundcheck: " + linked + " is a symbolic link; not writing the EAA files through it\n"; code != exitError || slashes(errOut) != slashes(want) {
			t.Errorf("linked eaa/: exit %d %q\nwant %q", code, errOut, want)
		}
		if _, err := os.Stat("linked.md"); err == nil {
			t.Error("linked eaa/: wrote linked.md")
		}
		if err := os.Remove(linked); err != nil {
			t.Fatal(err)
		}
	}

	// A run without --eaa results.
	code, _, errOut = runCLI(t, "statement", plain)
	if want := "rhythm-soundcheck: " + plain + " has no --eaa results; run rhythm-soundcheck snapshot <url> --eaa first\n"; code != exitError || slashes(errOut) != slashes(want) {
		t.Errorf("no --eaa: exit %d %q\nwant %q", code, errOut, want)
	}
}

func TestStatementRefusals(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "")
	fakeStatement(t, statement.Owner{Owner: "Example Coffee Ltd"})
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

	if code, _, errOut := runCLI(t, "statement"); code != exitError ||
		errOut != "rhythm-soundcheck: no --eaa results in .soundcheck yet; run rhythm-soundcheck snapshot <url> --eaa first\n" {
		t.Errorf("nothing stored: exit %d %q", code, errOut)
	}
	run21 := storeRun(t, now, model.Options{EAA: true, WCAG: "2.1"})
	code, _, errOut := runCLI(t, "statement")
	want := "rhythm-soundcheck: statement needs statement.owner, statement.website and statement.contact.email in .soundcheck/config.yaml (rhythm-soundcheck init --eaa writes them); a statement without a way to report problems isn't one\n"
	if code != exitUsage || slashes(errOut) != want {
		t.Errorf("missing owner: exit %d %q", code, errOut)
	}
	inputs := fakeStatement(t, shopOwner)
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"--wcag", "2.2"}, exitUsage, "the run used the WCAG 2.1 rules; --wcag 2.2 needs a run made with --wcag 2.2"},
		{[]string{"--wcag", "2.0"}, exitUsage, `--wcag: "2.0" isn't one of 2.1, 2.2`},
		{[]string{"--lang", "de"}, exitUsage, "--lang: only en in this version"},
		{[]string{"--layout", "servce"}, exitUsage, `--layout: "servce" isn't one of model, service (did you mean "service"?)`},
		{[]string{"--md", "--html"}, exitUsage, "pick one format, not --html and --md"},
		{[]string{"-o", "s.txt"}, exitUsage, "can't tell the format of s.txt from its extension; use .md, .html, .json or .jsonld"},
		{[]string{"nosuch"}, exitUsage, `no snapshot named "nosuch"; see rhythm-soundcheck snapshots`},
		{[]string{"--config", "nosuch.yaml"}, exitUsage, "--config: nosuch.yaml: no such file or directory"},
	} {
		code, _, errOut := runCLI(t, append([]string{"statement"}, tc.args...)...)
		if code != tc.code || errOut != "rhythm-soundcheck: "+tc.want+"\n" {
			t.Errorf("%v: exit %d %q\nwant %q", tc.args, code, errOut, tc.want)
		}
	}
	// A 2.2 run can be drawn as 2.1.
	run22 := storeRun(t, now.Add(time.Minute), model.Options{EAA: true, WCAG: "2.2"})
	if code, out, _ := runCLI(t, "statement", "--wcag", "2.1"); code != exitOK || out != "md model 2.1\n" {
		t.Errorf("2.2 as 2.1: exit %d %q", code, out)
	}
	if in := (*inputs)[len(*inputs)-1]; in.Summary.WCAG != "2.1" {
		t.Errorf("summary drawn as %q", in.Summary.WCAG)
	}
	if code, out, _ := runCLI(t, "statement", run21); code != exitOK || out != "md model 2.1\n" {
		t.Errorf("named folder: exit %d %q", code, out)
	}
	_ = run22

	// An interrupted run.
	stopped := audited("/shop/", model.AuditOK)
	stopped.Error = model.CanceledError
	dir := storeRun(t, now.Add(2*time.Minute), model.Options{EAA: true, WCAG: "2.1"}, audited("/", model.AuditOK), stopped, audited("/about/", model.AuditOK))
	code, _, errOut = runCLI(t, "statement")
	if want := "rhythm-soundcheck: " + dir + " was interrupted after 2 of 3 pages; a statement from part of the site would leave problems out. Run it again\n"; code != exitError || slashes(errOut) != slashes(want) {
		t.Errorf("interrupted: exit %d %q\nwant %q", code, errOut, want)
	}
	// The pages it never reached count too, from eaa/progress.json.
	if err := writePrivate(filepath.Join(dir, eaaDir, progressFile), func(w io.Writer) error { _, err := io.WriteString(w, `{"pages":42}`); return err }); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runCLI(t, "statement")
	if want := "rhythm-soundcheck: " + dir + " was interrupted after 2 of 42 pages; a statement from part of the site would leave problems out. Run it again\n"; code != exitError || slashes(errOut) != slashes(want) {
		t.Errorf("interrupted with progress: exit %d %q\nwant %q", code, errOut, want)
	}

	// The statement package's own refusals map the same way.
	_ = os.RemoveAll(dir)
	buildStatement = func(statement.Input) (*statement.Draft, error) { return nil, statement.ErrMissingOwner }
	if code, _, errOut := runCLI(t, "statement"); code != exitUsage || !strings.Contains(errOut, "a statement without a way to report problems isn't one") {
		t.Errorf("ErrMissingOwner: exit %d %q", code, errOut)
	}
	buildStatement = func(statement.Input) (*statement.Draft, error) { return nil, statement.ErrNoEAAData }
	if code, _, errOut := runCLI(t, "statement"); code != exitError || !strings.Contains(errOut, "has no --eaa results") {
		t.Errorf("ErrNoEAAData: exit %d %q", code, errOut)
	}
	buildStatement = func(statement.Input) (*statement.Draft, error) { return nil, statement.ErrInterrupted }
	if code, _, errOut := runCLI(t, "statement"); code != exitError ||
		!strings.Contains(errOut, " was interrupted after 1 of 1 page; a statement from part of the site would leave problems out. Run it again\n") {
		t.Errorf("ErrInterrupted: exit %d %q", code, errOut)
	}
	buildStatement = func(statement.Input) (*statement.Draft, error) { return nil, errors.New("boom") }
	if code, _, _ := runCLI(t, "statement"); code != exitError {
		t.Errorf("another error: exit %d", code)
	}
}

// With the statement package itself (pending until it is built).
func TestStatementCommandReal(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfig(t, "")
	if _, err := statement.Build(statement.Input{}); errors.Is(err, statement.ErrNotBuilt) {
		t.Skip("pending: statement")
	}
	storeRun(t, time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), model.Options{EAA: true, WCAG: "2.1"})
	writeConfig(t, "statement:\n  owner: Example Coffee Ltd\n  website: https://shop.example\n  contact:\n    email: access@shop.example\n")
	for _, f := range []string{"s.md", "s.html", "s.json", "s.jsonld"} {
		if code, _, errOut := runCLI(t, "statement", "-o", f); code != exitOK {
			t.Errorf("%s: exit %d %s", f, code, errOut)
		}
	}
}
