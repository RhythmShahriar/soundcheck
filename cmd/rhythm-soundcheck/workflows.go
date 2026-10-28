// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/review"
	"github.com/RhythmShahriar/soundcheck/store"
)

// stored is the latest comparison with a snapshot, as accept and review
// work on it.
type stored struct {
	runDir, snapDir string
	run             *model.Run
	c               *model.Comparison
}

// latestComparison loads the newest result in the store, which must be a
// comparison with a snapshot: accept changes that snapshot.
func latestComparison(st store.Store, cmd string) (stored, error) {
	dir, err := st.LatestRun()
	if err != nil {
		return stored{}, cli.UsageError{Msg: named(err.Error())}
	}
	return comparisonIn(st, dir, cmd)
}

// comparisonIn loads the comparison stored in dir and the snapshot it
// compared with.
func comparisonIn(st store.Store, dir, cmd string) (stored, error) {
	c, err := store.LoadComparison(dir)
	if err != nil {
		return stored{}, cli.Usagef("%s works on a comparison and %s has none; run %s compare first", cmd, rel(dir), cmdName())
	}
	if c.Before.Name == "" {
		return stored{}, cli.Usagef("%s works on a comparison with a snapshot; %s compared two sites (--against)", cmd, rel(dir))
	}
	// The snapshot is found by its name in this store, never by the
	// folder comparison.json names: the file is in the project folder.
	snapDir, err := st.Snapshot(c.Before.Name)
	if err != nil {
		return stored{}, cli.UsageError{Msg: named(err.Error())}
	}
	run, err := store.LoadRun(dir)
	if err != nil {
		return stored{}, err
	}
	return stored{runDir: dir, snapDir: snapDir, run: run, c: c}, nil
}

// who names the person accepting a change, for snapshot.json: git's
// user.email, else $USER.
func who() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "config", "user.email").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	for _, v := range []string{"USER", "USERNAME"} {
		if s := os.Getenv(v); s != "" {
			return s
		}
	}
	return "unknown"
}

const acceptUsage = `Usage:
  rhythm soundcheck accept <id>...                    accept changes by id
  rhythm soundcheck accept --page <glob> --check <glob>
  rhythm soundcheck accept <id>... --ignore --reason <text> [--until <date>]

Accept makes changes of the latest comparison part of its snapshot, so the
next compare doesn't report them. Ids are shown next to each change (in
--json as "id"), BROKE and NEW ones too. --page /team/* --check visual/*
accepts every visual change on the team pages; either glob alone matches
everything else. --page '*' --check 'a11y/*' takes a whole accessibility
backlog and prints how many changes it accepted.

--ignore writes an ignore: rule into .soundcheck/config.yaml instead, with
the change's check, pages and source and your --reason: a known problem
kept in the committed config, where it is reviewed with the code. --until
2026-12-31 ends the rule after that day.
`

// cmdAccept accepts changes of the latest comparison into its snapshot.
func cmdAccept(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("accept", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	page := fs.String("page", "", "")
	check := fs.String("check", "", "")
	ignore := fs.Bool("ignore", false, "")
	reason := fs.String("reason", "", "")
	until := fs.String("until", "", "")
	apply := colorFlags(fs)
	ids, err := cli.ParseInterspersed(fs, args)
	apply()
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, named(acceptUsage))
		return exitOK
	}
	switch {
	case err != nil:
		fmt.Fprintln(stderr, prefix(), flagError(fs, err))
		return exitUsage
	case len(ids) == 0 && *page == "" && *check == "":
		fmt.Fprint(stderr, named(acceptUsage))
		return exitUsage
	case len(ids) > 0 && (*page != "" || *check != ""):
		fmt.Fprintln(stderr, prefix(), "give change ids or --page and --check, not both")
		return exitUsage
	}
	if err := ignoreFlags(*ignore, *reason, *until, len(ids) > 0); err != nil {
		return failed(stderr, err)
	}
	st := store.Store{Root: config.Dir}
	sc, err := latestComparison(st, "accept")
	if err != nil {
		return failed(stderr, err)
	}
	if *ignore {
		return ignoreChanges(sc, ids, strings.TrimSpace(*reason), *until, stdout, stderr)
	}
	accepted, pack, err := acceptInto(sc, store.Selector{IDs: ids, Page: *page, Check: *check})
	if err != nil {
		return failed(stderr, err)
	}
	// By id each change is named; a --page and --check backlog may be
	// hundreds, so it is counted.
	if len(ids) == 0 {
		fmt.Fprintf(stdout, "Accepted %s\n", plural(distinctIDs(accepted), "change"))
	}
	for _, a := range accepted {
		if len(ids) == 0 {
			break
		}
		where := a.Page
		if where == "" {
			where = "site-wide"
		}
		fmt.Fprintf(stdout, "Accepted %s %s (%s)\n", a.ID, report.Term(a.Title), report.Term(where))
	}
	// The snapshot's whole record counts, not only this call's.
	held := distinctIDs(accepted)
	if info, err := store.LoadSnapshotInfo(sc.snapDir); err == nil {
		held = distinctIDs(info.Accepted)
	}
	fmt.Fprintf(stdout, "Snapshot %q now holds %s. Run %s compare to check again.\n", sc.c.Before.Name, plural(held, "accepted change"), cmdName())
	evidenceNote(pack, sc.c.Before.Name, stderr)
	return exitOK
}

// acceptedPack is what an accept did to the snapshot's evidence pack.
type acceptedPack struct {
	kept   string // the old pack's folder in the store, "" when it had none
	signed bool   // the old pack was signed; the new one isn't yet
	err    error  // recording the pack again failed after the accept
	draft  error  // writing the statement draft again failed; the stale one is removed
}

// acceptInto accepts the selected changes into the snapshot. A snapshot
// with an evidence pack has the pack kept first (evidence.Keep), as
// later --eaa runs are chained to it as it was. It is recorded again for
// the accepted state after.
func acceptInto(sc stored, sel store.Selector) ([]store.Accepted, acceptedPack, error) {
	var pack acceptedPack
	kept, err := keepEvidence(sc.snapDir)
	if err != nil {
		return nil, pack, err
	}
	pack.kept = kept
	accepted, err := store.Accept(sc.snapDir, sc.runDir, sc.c, sel, who())
	if err != nil {
		return nil, pack, cli.UsageError{Msg: err.Error()}
	}
	if kept != "" {
		pack.signed, pack.err = rerecordEvidence(sc.snapDir, "accept")
	}
	// The draft isn't in the pack; written again it lists what was
	// accepted, as statement of the snapshot does.
	pack.draft = refreshDraft(sc.snapDir)
	return accepted, pack, nil
}

// distinctIDs counts the changes among accepted entries, which are one
// per page.
func distinctIDs(accepted []store.Accepted) int {
	seen := map[string]bool{}
	for _, a := range accepted {
		seen[a.ID] = true
	}
	return len(seen)
}

// ignoreFlags checks --ignore, --reason and --until: --ignore needs a
// reason and change ids, the other two need --ignore.
func ignoreFlags(ignore bool, reason, until string, byID bool) error {
	switch {
	case !ignore && (reason != "" || until != ""):
		return cli.Usagef("--reason and --until go with --ignore")
	case !ignore:
		return nil
	case strings.TrimSpace(reason) == "":
		return cli.Usagef("--ignore needs --reason: say why this is known")
	case !byID:
		return cli.Usagef("--ignore takes change ids, not --page and --check")
	}
	if until != "" {
		if _, err := time.Parse("2006-01-02", until); err != nil {
			return cli.Usagef("--until: %q isn't a date like 2026-12-31", until)
		}
	}
	return nil
}

// ignoreChanges writes an ignore rule for each change id into the config
// file: the change's check, pages and source (its title when it has no
// source), with the reason and end date given.
func ignoreChanges(sc stored, ids []string, reason, until string, stdout, stderr io.Writer) int {
	path := filepath.Join(config.Dir, "config.yaml")
	var rules []config.IgnoreRule
	for _, id := range ids {
		i := slices.IndexFunc(sc.c.Changes, func(ch model.Change) bool { return ch.ID == id })
		if i < 0 {
			return failed(stderr, cli.Usagef("no change %q in this comparison", id))
		}
		r, err := ignoreRule(sc.c.Changes[i], reason, until)
		if err != nil {
			return failed(stderr, err)
		}
		rules = append(rules, r)
	}
	for i, r := range rules {
		if err := appendIgnore(path, r); err != nil {
			return failed(stderr, err)
		}
		where := strings.Join(r.Pages, ", ")
		if where == "" {
			where = "site-wide"
		}
		fmt.Fprintf(stdout, "Ignored %s %s (%s): rule added to %s\n", ids[i], report.Term(r.Check), report.Term(where), path)
	}
	fmt.Fprintf(stdout, "Commit %s to share the rules; the snapshot is unchanged.\n", path)
	return exitOK
}

// evidenceNote tells, after an accept into a snapshot with an evidence
// pack, what happened to the pack: accept rewrote the snapshot's
// run.json, its pages file and snapshot.json (and the screenshots of
// accepted visual changes), so the pack was recorded again for the
// accepted state. The pack as it was stays in the store with its files:
// later --eaa runs are chained to it. Evidence verify of them checks
// that copy.
func evidenceNote(pack acceptedPack, name string, stderr io.Writer) {
	if pack.draft != nil {
		fmt.Fprintf(stderr, "%s the statement draft of snapshot %q couldn't be written again: %v; it was removed. Draft one with %s statement %[2]s\n",
			prefix(), name, pack.draft, cmdName())
	}
	if pack.kept == "" {
		return
	}
	where := rel(filepath.Join(config.Dir, filepath.FromSlash(pack.kept)))
	if pack.err != nil {
		fmt.Fprintf(stderr, "%s accepted. The evidence pack of snapshot %q couldn't be recorded again: %v; evidence verify of it will report run.json and the pages file as changed. The pack from before is kept in %s, so later --eaa runs still verify\n",
			prefix(), name, pack.err, where)
		return
	}
	fmt.Fprintf(stderr, "note: accept changed run.json, the pages file and snapshot.json of snapshot %q, so its evidence pack was recorded again. The pack from before is kept in %s with its files: evidence verify of later --eaa runs, which are chained to it, checks that copy and notes it\n",
		name, where)
	if pack.signed {
		fmt.Fprintf(stderr, "note: the old pack was signed; sign the new one with %s evidence sign %s --key <file>\n", cmdName(), name)
	}
}

// ignoreRule is the rule that ignores ch from now on. Its source and
// pages are globs (model.Glob), where only * is special and can't be
// escaped. A selector's brackets match themselves, so the source is
// written as it is; a * in it (a[href*="x"]) matches itself and, rarely,
// a sibling selector of the same check on the same pages. A change
// without a source is matched by its title instead, so the rule doesn't
// cover the whole check. A page with a * in its path would ignore more
// pages than the change, so it is refused and the rule is left to be
// written by hand.
func ignoreRule(ch model.Change, reason, until string) (config.IgnoreRule, error) {
	for _, p := range ch.Pages {
		if strings.Contains(p, "*") {
			return config.IgnoreRule{}, fmt.Errorf("can't ignore %s: page %q has a *, which an ignore rule reads as any text; add the rule to config.yaml by hand", ch.ID, p)
		}
	}
	r := config.IgnoreRule{Check: ch.Check, Pages: slices.Clone(ch.Pages), Source: ch.Source, Until: until, Reason: reason}
	if r.Source == "" {
		r.Title = ch.Title
	}
	return r, nil
}

// failed prints err and returns its exit code: 2 for a usage error.
func failed(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, prefix(), named(err.Error()))
	var ue cli.UsageError
	if errors.As(err, &ue) {
		return exitUsage
	}
	return exitError
}

// decider makes review's decisions: accept into the snapshot, or ignore
// from now on with a rule in the config file.
func decider(sc stored, configPath string) review.Decide {
	return func(ch model.Change, a review.Action) error {
		switch a {
		case review.Accept:
			_, pack, err := acceptInto(sc, store.Selector{IDs: []string{ch.ID}})
			if err == nil {
				err = pack.err
			}
			return err
		case review.Ignore:
			r := config.IgnoreRule{Check: ch.Check, Title: ch.Title, Pages: ch.Pages,
				Reason: fmt.Sprintf("ignored in review by %s on %s", who(), time.Now().Format("2006-01-02"))}
			return appendIgnore(configPath, r)
		}
		return nil
	}
}

// appendIgnore adds an ignore rule to the config file, keeping the rest
// of the file and its comments. The file is created when missing.
func appendIgnore(path string, r config.IgnoreRule) error {
	data, err := os.ReadFile(path) //nolint:gosec // the project's own config file
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: the file must be a list of settings", path)
	}
	var list *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "ignore" {
			list = root.Content[i+1]
		}
	}
	if list == nil || list.Kind != yaml.SequenceNode {
		if list == nil {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "ignore"}, &yaml.Node{})
			list = root.Content[len(root.Content)-1]
		}
		*list = yaml.Node{Kind: yaml.SequenceNode} // an empty "ignore:" or "ignore: []"
	}
	var item yaml.Node
	if err := item.Encode(r); err != nil {
		return err
	}
	// Leave out the fields the rule doesn't set.
	var kept []*yaml.Node
	for i := 0; i+1 < len(item.Content); i += 2 {
		v := item.Content[i+1]
		if (v.Kind == yaml.ScalarNode && v.Value == "") || (v.Kind == yaml.SequenceNode && len(v.Content) == 0) {
			continue
		}
		if v.Kind == yaml.SequenceNode {
			v.Style = yaml.FlowStyle
		}
		kept = append(kept, item.Content[i], v)
	}
	item.Content = kept
	list.Content = append(list.Content, &item)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	// The file must still load: a rule that breaks it would stop every
	// later run.
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { //nolint:gosec // the project's config file, named by the command
		return err
	}
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil { //nolint:gosec // a project file meant to be committed
		return err
	}
	if _, err := config.Load(tmp); err != nil {
		_ = os.Remove(tmp) //nolint:gosec // the temporary file written just above
		return err
	}
	return os.Rename(tmp, path) //nolint:gosec // as above
}

const reviewUsage = `Usage:
  rhythm soundcheck review [run]

Steps through the changes of the latest comparison (or the run folder
given): a accepts a change into the snapshot, i ignores it from now on (a
rule in .soundcheck/config.yaml), s skips it and q quits. It needs a
terminal; in scripts use accept, or report --serve for the browser.
`

// cmdReview reviews the latest comparison's changes in the terminal.
func cmdReview(args []string, stdout, stderr io.Writer) int {
	pos, code, ok := subFlags(flag.NewFlagSet("review", flag.ContinueOnError), args, reviewUsage, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) > 1 {
		fmt.Fprint(stderr, named(reviewUsage))
		return exitUsage
	}
	st := store.Store{Root: config.Dir}
	var sc stored
	var err error
	if len(pos) == 1 {
		sc, err = comparisonIn(st, pos[0], "review")
	} else {
		sc, err = latestComparison(st, "review")
	}
	if err != nil {
		return failed(stderr, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	err = review.Terminal(ctx, os.Stdin, stdout, review.Reviewable(sc.c.Changes), decider(sc, filepath.Join(config.Dir, "config.yaml")))
	switch {
	case errors.Is(err, review.ErrNoTTY):
		return failed(stderr, cli.UsageError{Msg: err.Error()})
	case err != nil:
		return failed(stderr, err)
	}
	return exitOK
}
