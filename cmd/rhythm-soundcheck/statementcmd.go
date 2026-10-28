// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/update"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/statement"
	"github.com/RhythmShahriar/soundcheck/store"
	"github.com/RhythmShahriar/soundcheck/wcag"
)

const statementUsage = `Usage:
  rhythm soundcheck statement [run folder|snapshot] [flags]

Drafts an accessibility statement from an --eaa run (default: the latest
--eaa run or snapshot), with every place you have to fill in marked
[TO DO]. Who you are and how visitors reach you come from statement: in
.soundcheck/config.yaml (rhythm soundcheck init --eaa writes it).

      --md                 Markdown (default on a terminal and for .md)
      --html               standalone HTML, printable
      --json               JSON (schema rhythm-soundcheck/statement/v1)
      --earl               EARL JSON-LD for the W3C WCAG-EM Report Tool
                           (.jsonld)
  -o, --output <file>      write to a file (format from the extension);
                           repeatable
      --layout <name>      model (EU 2018/1523 model statement, default) or
                           service (EAA Annex V service information)
      --wcag <version>     2.1 or 2.2; a 2.2 run can be drawn as 2.1, not
                           the other way round
      --config <file>      read statement: (owner, contact, dates) from
                           this file
      --lang <code>        en (only en in 1.3)
      --force              replace a draft file you have edited
Exit codes: 0 written · 2 usage error · 3 no --eaa data, interrupted run,
or a file couldn't be read or written
`

// The statement formats, as variables so the tests can stand in for the
// statement package.
var (
	statementHTML = statement.HTML
	statementJSON = statement.JSON
	statementEARL = statement.EARL
)

// writtenFile records the SHA-256 of each draft `statement -o` wrote, by
// absolute path, so a later -o can tell a file the owner edited.
const writtenFile = "written.json"

// statementOut is one place the draft goes: a file, or stdout ("-").
type statementOut struct {
	path, format string
}

// cmdStatement writes the statement draft of an --eaa run.
func cmdStatement(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("statement", flag.ContinueOnError)
	formats := map[string]*bool{}
	for _, f := range []string{"md", "html", "json", "earl"} {
		formats[f] = fs.Bool(f, false, "")
	}
	var outputs cli.RepeatFlag
	fs.Var(&outputs, "o", "")
	fs.Var(&outputs, "output", "")
	layout := fs.String("layout", "model", "")
	drawAs := fs.String("wcag", "", "")
	configPath := fs.String("config", "", "")
	lang := fs.String("lang", "en", "")
	force := fs.Bool("force", false, "")
	pos, code, ok := subFlags(fs, args, statementUsage, stdout, stderr)
	if !ok {
		return code
	}
	quiet := boolFlag(fs, "quiet")
	if len(pos) > 1 {
		fmt.Fprint(stderr, named(statementUsage))
		return exitUsage
	}
	outs, err := statementOutputs(formats, outputs)
	if err == nil {
		err = statementFlags(*layout, *drawAs, *lang)
	}
	if err != nil {
		return failed(stderr, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	notice := update.StartCheck(ctx, &update.Updater{Repo: "RhythmShahriar/soundcheck", Binary: "rhythm-soundcheck",
		Current: version, UserAgent: "rhythm-soundcheck/" + version, API: os.Getenv("RHYTHM_GITHUB_API")})

	st := store.Store{Root: config.Dir}
	dir, err := statementSource(st, pos)
	if err != nil {
		return failed(stderr, err)
	}
	in, err := statementInput(dir, *drawAs)
	if err != nil {
		return failed(stderr, err)
	}
	path := cmpOr(*configPath, filepath.Join(config.Dir, "config.yaml"))
	if *configPath != "" {
		if _, err := os.Stat(path); err != nil {
			return failed(stderr, cli.Usagef("--config: %s: %v", path, errors.Unwrap(err)))
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return failed(stderr, cli.UsageError{Msg: err.Error()})
	}
	if in.Owner, err = decodeOwner(cfg); err != nil {
		return failed(stderr, cli.UsageError{Msg: err.Error()})
	}
	missing := cli.Usagef("statement needs statement.owner, statement.website and statement.contact.email in %s (%s init --eaa writes them); a statement without a way to report problems isn't one",
		path, cmdName())
	if in.Owner.Owner == "" || in.Owner.Website == "" || in.Owner.Contact.Email == "" {
		return failed(stderr, missing)
	}
	in.Layout = *layout
	d, err := buildStatement(in)
	switch {
	case errors.Is(err, statement.ErrMissingOwner):
		return failed(stderr, missing)
	case errors.Is(err, statement.ErrNoEAAData):
		return failed(stderr, noEAA(dir))
	case errors.Is(err, statement.ErrInterrupted):
		return failed(stderr, interruptedRun(dir, in.Run, in.Pages))
	case err != nil:
		return failed(stderr, err)
	}
	if err := writeStatement(d, dir, outs, *force, stdout, stderr, quiet); err != nil {
		return failed(stderr, err)
	}
	if !quiet {
		fmt.Fprintf(stderr, "%s left\n", plural(d.Todo, "[TO DO] marker"))
	}
	// The draft on stdout is for a program or a file; a prompt there
	// would end up in it.
	toStdout := slices.ContainsFunc(outs, func(o statementOut) bool { return o.path == "-" })
	update.PromptAtExit(ctx, update.PromptOptions{Check: notice, Quiet: quiet || toStdout || *formats["json"], Stdout: stdout, Out: stderr})
	return exitOK
}

// statementOutputs reads the format flags and -o: one format flag alone
// writes to stdout, -o files take their format from the extension (a
// single -o may take it from a format flag) and "-o -" is stdout.
// Markdown on stdout is the default.
func statementOutputs(formats map[string]*bool, outputs []string) ([]statementOut, error) {
	var chosen []string
	for name, on := range formats {
		if *on {
			chosen = append(chosen, name)
		}
	}
	slices.Sort(chosen)
	switch {
	case len(chosen) > 1:
		return nil, cli.Usagef("pick one format, not --%s", strings.Join(chosen, " and --"))
	case len(outputs) == 0:
		return []statementOut{{"-", cmpOr(strings.Join(chosen, ""), "md")}}, nil
	case len(outputs) == 1 && len(chosen) == 1:
		return []statementOut{{outputs[0], chosen[0]}}, nil
	case len(chosen) == 1:
		return nil, cli.Usagef("--%s with several -o: each file's format comes from its extension, so leave out --%[1]s", chosen[0])
	}
	var out []statementOut
	seen := map[string]bool{}
	for _, p := range outputs {
		f := statementFormat(p)
		switch {
		case p == "-":
			f = "md"
		case f == "":
			return nil, cli.Usagef("can't tell the format of %s from its extension; use .md, .html, .json or .jsonld", p)
		}
		key := absPath(p)
		if seen[key] {
			return nil, cli.Usagef("-o %s is given twice", p)
		}
		seen[key] = true
		out = append(out, statementOut{p, f})
	}
	return out, nil
}

// statementFormat is the format of a draft file by its extension.
func statementFormat(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return "md"
	case ".html", ".htm":
		return "html"
	case ".json":
		return "json"
	case ".jsonld":
		return "earl"
	}
	return ""
}

// statementFlags checks --layout, --wcag and --lang.
func statementFlags(layout, version, lang string) error {
	if err := config.OneOf(layout, []string{"model", "service"}); err != nil {
		return cli.Usagef("--layout: %v", err)
	}
	// No did-you-mean: one version is as near as another.
	if version != "" && version != "2.1" && version != "2.2" {
		return cli.Usagef("--wcag: %q isn't one of 2.1, 2.2", version)
	}
	if lang != "en" {
		return cli.Usagef("--lang: only en in this version")
	}
	return nil
}

// statementSource is the run folder to draft from: a folder or snapshot
// named, or the latest --eaa run or snapshot.
func statementSource(st store.Store, pos []string) (string, error) {
	if len(pos) == 0 {
		dir, err := latestEAA(st)
		if err != nil {
			return "", err
		}
		if dir == "" {
			return "", fmt.Errorf("no --eaa results in %s yet; run %s snapshot <url> --eaa first", st.Root, cmdName())
		}
		return dir, nil
	}
	// As with report, naming something that isn't there is a usage error.
	if fi, err := os.Stat(pos[0]); err == nil && fi.IsDir() { //nolint:gosec // the user names a run folder
		if _, err := os.Stat(filepath.Join(pos[0], "run.json")); errors.Is(err, fs.ErrNotExist) { //nolint:gosec // as above
			return "", cli.Usagef("no run in %s; name a run folder or a snapshot (see %s snapshots)", pos[0], cmdName())
		}
		return pos[0], nil
	}
	dir, err := st.Snapshot(pos[0])
	if err != nil {
		return "", cli.UsageError{Msg: named(err.Error())}
	}
	return dir, nil
}

// latestEAA is the newest run or snapshot made with --eaa, by its start
// time, or "" when there is none.
func latestEAA(st store.Store) (string, error) {
	var best string
	var bestAt time.Time
	for _, kind := range []string{"runs", "snapshots"} {
		entries, err := os.ReadDir(filepath.Join(st.Root, kind))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			if !e.IsDir() || !store.ValidName(e.Name()) {
				continue
			}
			dir := filepath.Join(st.Root, kind, e.Name())
			head, ok := runHead(dir)
			if ok && head.Options.EAA && (best == "" || head.Started.After(bestAt)) {
				best, bestAt = dir, head.Started
			}
		}
	}
	return best, nil
}

// runHead reads only what latestEAA needs from a run.json.
func runHead(dir string) (head struct {
	Started time.Time `json:"started"`
	Options struct {
		EAA bool `json:"eaa"`
	} `json:"options"`
}, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, "run.json")) //nolint:gosec // a run folder of the store
	if err != nil {
		return head, false
	}
	return head, json.Unmarshal(data, &head) == nil
}

// statementInput reads the run in dir (its pages one at a time), the
// changes its comparison left out and its EAA summary, drawn as the
// WCAG version drawAs when --wcag gives one.
func statementInput(dir, drawAs string) (statement.Input, error) {
	run, src, err := store.OpenRun(dir)
	if err != nil {
		return statement.Input{}, err
	}
	_ = src.Close()
	pages := func(yield func(*model.Page) bool) {
		_, src, err := store.OpenRun(dir)
		if err != nil {
			return
		}
		defer func() { _ = src.Close() }()
		for i := range src.Len() {
			p, err := src.Page(i)
			if err != nil || !yield(p) {
				return
			}
		}
	}
	if !run.Options.EAA {
		return statement.Input{}, noEAA(dir)
	}
	if run.Interrupted {
		return statement.Input{}, interruptedRun(dir, run, pages)
	}
	if drawAs != "" && drawAs != run.Options.WCAG {
		if wcag.Compare(drawAs, cmpOr(run.Options.WCAG, "2.0")) > 0 {
			return statement.Input{}, cli.Usagef("the run used the WCAG %s rules; --wcag %s needs a run made with --wcag %[2]s", cmpOr(run.Options.WCAG, "2.0"), drawAs)
		}
		// Every 2.1 criterion is in 2.2, so a 2.2 run answers for 2.1.
		run.Options.WCAG = drawAs
	}
	var ignored []model.Change
	if c, err := store.LoadComparison(dir); err == nil {
		ignored = c.Ignored
	}
	return statement.Input{Run: run, Pages: pages, Ignored: ignored, Summary: wcag.Summarize(run, pages, run.Issues, ignored),
		Version: version, Now: time.Now(), Insecure: insecureRun(dir), Hide: statementHide()}, nil
}

// insecureRun reads from a run's evidence pack whether it was made with
// -k: the run's options don't record it.
func insecureRun(dir string) bool {
	var ev struct {
		Insecure bool `json:"insecure"`
	}
	data, err := os.ReadFile(filepath.Join(dir, eaaDir, evidenceFile)) //nolint:gosec // a run folder of the store
	return err == nil && json.Unmarshal(data, &ev) == nil && ev.Insecure
}

// noEAA is the refusal of a run without --eaa results (exit 3).
func noEAA(dir string) error {
	return fmt.Errorf("%s has no --eaa results; run %s snapshot <url> --eaa first", rel(dir), cmdName())
}

// interruptedRun is the refusal of an interrupted run (exit 3). The
// total is the pages the run set out to visit, from eaa/progress.json,
// as pages it never reached aren't stored.
func interruptedRun(dir string, run *model.Run, pages func(func(*model.Page) bool)) error {
	total, done := 0, 0
	for p := range pages {
		total++
		if !p.Canceled() {
			done++
		}
	}
	var pr progress
	data, err := os.ReadFile(filepath.Join(dir, eaaDir, progressFile)) //nolint:gosec // a run folder of the store
	if err == nil && json.Unmarshal(data, &pr) == nil {
		total = max(total, pr.Pages)
	}
	return fmt.Errorf("%s was interrupted after %d of %s; a statement from part of the site would leave problems out. Run it again",
		rel(dir), done, plural(total, "page"))
}

// writeStatement renders d to each output. A file the owner edited since
// Soundcheck wrote it (its SHA-256 isn't one written.json recorded) is
// kept unless force. A symlink is never written through.
func writeStatement(d *statement.Draft, dir string, outs []statementOut, force bool, stdout, stderr io.Writer, quiet bool) error {
	// Every file is checked before any is written, so a refusal leaves
	// them all as they were.
	for _, o := range outs {
		if o.path == "-" {
			continue
		}
		// written.json goes into eaa/: refuse a linked one before any
		// file is written, not after.
		if err := eaaFolder(dir); err != nil {
			return err
		}
		if err := writable(o.path, dir, force); err != nil {
			return err
		}
	}
	for _, o := range outs {
		var buf bytes.Buffer
		if err := renderStatement(&buf, d, o.format); err != nil {
			return err
		}
		if o.path == "-" {
			if _, err := stdout.Write(buf.Bytes()); err != nil {
				return err
			}
			continue
		}
		if err := writePrivate(o.path, func(w io.Writer) error { _, err := w.Write(buf.Bytes()); return err }); err != nil {
			return fmt.Errorf("writing %s: %w", o.path, err)
		}
		if err := recordWritten(dir, o.path, buf.Bytes()); err != nil {
			return fmt.Errorf("recording %s: %w", o.path, err)
		}
		if !quiet {
			fmt.Fprintf(stderr, "Statement draft written to %s\n", o.path)
		}
	}
	return nil
}

// renderStatement writes d in one format.
func renderStatement(w io.Writer, d *statement.Draft, format string) error {
	switch format {
	case "html":
		return statementHTML(w, d)
	case "json":
		return statementJSON(w, d)
	case "earl":
		return statementEARL(w, d)
	}
	return statementMD(w, d)
}

// writable refuses a symlink at path and, without force, a file there
// that Soundcheck didn't write as it is now.
func writable(path, dir string, force bool) error {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case fi.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("refusing to write through a symlink: %s", path)
	case force:
		return nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // the file -o names
	if err != nil {
		return err
	}
	if wroteIt(dir, path, data) {
		return nil
	}
	return cli.Usagef("%s was edited since Soundcheck wrote it; write to another file with -o, or add --force to replace your edits", path)
}

// written is eaa/written.json: SHA-256 by absolute path.
type written map[string]string

// wroteIt reports whether a draft Soundcheck wrote to path had exactly
// data: written.json of the run in dir, or of any other run or snapshot
// in the store, records its SHA-256. A draft made from last week's run
// may then be replaced by this week's.
func wroteIt(dir, path string, data []byte) bool {
	key, sum := absPath(path), sha(data)
	folders := []string{dir}
	for _, kind := range []string{"runs", "snapshots"} {
		entries, _ := os.ReadDir(filepath.Join(config.Dir, kind))
		for _, e := range entries {
			folders = append(folders, filepath.Join(config.Dir, kind, e.Name()))
		}
	}
	for _, f := range folders {
		if readWritten(f)[key] == sum {
			return true
		}
	}
	return false
}

func readWritten(dir string) written {
	w := written{}
	data, err := os.ReadFile(filepath.Join(dir, eaaDir, writtenFile)) //nolint:gosec // a run folder of the store
	if err == nil {
		_ = json.Unmarshal(data, &w)
	}
	return w
}

// recordWritten adds path's SHA-256 to written.json of the run in dir.
func recordWritten(dir, path string, data []byte) error {
	if err := eaaFolder(dir); err != nil {
		return err
	}
	w := readWritten(dir)
	w[absPath(path)] = sha(data)
	out, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(dir, eaaDir, writtenFile), func(wr io.Writer) error { _, err := wr.Write(append(out, '\n')); return err })
}

func sha(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// absPath is path made absolute and clean, or as given when it can't be.
func absPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}
