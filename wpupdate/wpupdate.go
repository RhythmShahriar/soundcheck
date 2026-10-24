// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package wpupdate runs a WordPress update night on a staging site. It
// takes a snapshot, updates plugins through WP-CLI and compares the site
// with the snapshot after each update (or each half of them, with
// bisect), so the plugin whose update broke the site is named with the
// PHP file and line and can be rolled back. It writes a maintenance log
// in text and Markdown.
//
// WP-CLI and Soundcheck run as child processes from argv lists, never
// through a local shell. Run refuses any site the config marks as
// production: updating plugins on a live site is the host's job.
package wpupdate

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/internal/proc"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// Options say what to update and how.
type Options struct {
	// Target is the staging site, as Soundcheck takes it.
	Target string
	// WP is the WP-CLI command prefix, e.g. {"ssh", "deploy@staging",
	// "--", "wp", "--path=/srv/www"}; SplitCommand turns a --wp string
	// into it.
	WP []string
	// Plugins are the slugs to update; empty or "all" updates every
	// plugin with an update available.
	Plugins []string
	// OneByOne updates one plugin at a time and compares after each.
	// It's the default when Bisect isn't set.
	OneByOne bool
	// Bisect updates every plugin and compares once; on a break it rolls
	// halves back to find the culprit in about log2(n) compares.
	Bisect bool
	// StopOn is the change class that marks an update as bad: broke (the
	// default), new (broke or new problems) or changed (any difference).
	StopOn string
	// Rollback puts a bad update back to its old version and goes on.
	// Without it the loop stops at the first bad update and leaves it
	// installed.
	Rollback bool
	// FromTuner is a rhythm-tuner JSON report of the site: the plugins
	// it lists as vulnerable are updated first.
	FromTuner string
	// Bin is the Soundcheck binary.
	Bin string
	// Config is the site's Soundcheck config, for the production guard.
	Config config.Config
	// ConfigPath, when set, is passed to Soundcheck as --config.
	ConfigPath string
	// Args are extra flags for every snapshot and compare.
	Args []string
	// Dir is where Soundcheck runs and the logs go; empty is the current
	// folder.
	Dir string
	// Out gets a line per step while the loop runs; nil discards them.
	Out io.Writer
}

// Plugin statuses in the log.
const (
	Updated    = "updated"
	RolledBack = "rolled back"
	Kept       = "broke"       // the bad update is still installed
	Failed     = "failed"      // WP-CLI couldn't update it
	NotUpdated = "not updated" // the loop stopped before it
	Skipped    = "skipped"     // refused before the loop
)

// Plugin is one plugin's update and what it did to the site.
type Plugin struct {
	Slug       string `json:"slug"`
	Old        string `json:"old"`
	New        string `json:"new"`
	Vulnerable string `json:"vulnerable,omitempty"` // Tuner's worst severity for it
	Status     string `json:"status"`
	// Verdict is the worst change class its compare found: broke, new,
	// changed or clean. Empty when no compare checked it alone.
	Verdict string `json:"verdict,omitempty"`
	// Evidence says what broke: "Server error 500 on /contact/,
	// plugins/contact-form/includes/submit.php:412".
	Evidence string `json:"evidence,omitempty"`
	// Compares is how many compares bisect took to find this culprit
	// after the break showed.
	Compares int    `json:"compares,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Log is the maintenance log of one update night.
type Log struct {
	Target   string    `json:"target"`
	Mode     string    `json:"mode"` // one-by-one or bisect
	StopOn   string    `json:"stop_on"`
	Snapshot string    `json:"snapshot,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Plugins  []Plugin  `json:"plugins"`
	// Missing are slugs asked for that have no update available.
	Missing  []string `json:"missing,omitempty"`
	Compares int      `json:"compares"`
	// Works says the site's final state passed its compare: no change
	// at or above StopOn.
	Works bool `json:"works"`
	// WP is the WP-CLI command prefix the loop ran (Options.WP), for the
	// roll-back hint; empty reads as plain "wp".
	WP []string `json:"wp,omitempty"`
	// Note explains a loop that ended early, e.g. a break bisect
	// couldn't pin on one plugin.
	Note  string   `json:"note,omitempty"`
	Files []string `json:"files,omitempty"` // the text and Markdown logs
}

// Modes.
const (
	ModeOneByOne = "one-by-one"
	ModeBisect   = "bisect"
)

// fast are the flags of every snapshot and compare: one viewport and the
// modules that catch a broken update, so a compare per plugin stays quick.
var fast = []string{"--viewport", "desktop", "--modules", "health,interact"}

// classRank orders change classes by how bad they are, for StopOn.
var classRank = map[string]int{model.Broke: 3, model.New: 2, model.Changed: 1, model.Slower: 1}

// stopOns are the values StopOn takes.
var stopOns = []string{model.Broke, model.New, model.Changed}

// Exit codes of the Soundcheck binary that still carry a result: 0, and
// 1 when a fail-on threshold in the config was reached.
const exitFindings = 1

// Run updates the target's plugins and returns the maintenance log, also
// written as text and Markdown under <Dir>/.soundcheck/wp-update/. An
// error after the loop started comes with the log so far.
func Run(ctx context.Context, o Options) (*Log, error) {
	if err := o.check(); err != nil {
		return nil, err
	}
	vuln := map[string]string{}
	if o.FromTuner != "" {
		var err error
		if vuln, err = readTuner(o.FromTuner); err != nil {
			return nil, fmt.Errorf("--from-tuner: %w", err)
		}
	}
	r := &runner{ctx: ctx, o: o, out: cleanWriter{cmp.Or[io.Writer](o.Out, io.Discard)}}
	r.log = &Log{Target: o.Target, WP: o.WP, Mode: ModeOneByOne, StopOn: cmp.Or(o.StopOn, model.Broke), Started: time.Now()}
	if o.Bisect {
		r.log.Mode = ModeBisect
	}
	err := r.run(vuln)
	for i := range r.log.Plugins {
		if p := &r.log.Plugins[i]; p.Status == "" {
			p.Status = NotUpdated // the loop ended before it
		}
	}
	r.log.Finished = time.Now()
	if werr := r.writeLogs(); werr != nil && err == nil {
		err = werr
	}
	return r.log, err
}

func (o Options) check() error {
	switch {
	case o.Target == "":
		return errors.New("wp-update: the site is missing")
	case len(o.WP) == 0:
		return errors.New("wp-update: --wp is missing: the WP-CLI command, e.g. \"wp --path=/srv/www\"")
	case o.Bin == "":
		return errors.New("wp-update: the Soundcheck binary is missing")
	case o.OneByOne && o.Bisect:
		return errors.New("wp-update: pick --one-by-one or --bisect, not both")
	case o.StopOn != "" && !slices.Contains(stopOns, o.StopOn):
		return fmt.Errorf("wp-update: --stop-on %q: want broke, new or changed", o.StopOn)
	}
	for _, u := range targetURLs(o.Target) {
		if o.Config.IsProduction(u) {
			return fmt.Errorf("wp-update: %s is a production site (listed under production: in the config); run updates on staging", o.Target)
		}
	}
	return nil
}

// targetURLs are the URLs a target may mean: itself and, for a bare host,
// both schemes, since Soundcheck tries https and falls back to http.
func targetURLs(t string) []string {
	if strings.Contains(t, "://") {
		return []string{t}
	}
	return []string{t, "https://" + t, "http://" + t}
}

type runner struct {
	ctx context.Context
	o   Options
	out io.Writer
	log *Log
}

func (r *runner) run(vuln map[string]string) error {
	plugins, err := r.list(vuln)
	if err != nil {
		return err
	}
	r.log.Plugins = plugins
	todo := r.todo()
	if len(todo) == 0 {
		fmt.Fprintln(r.out, "No plugin updates available.")
		r.log.Works = true
		return nil
	}
	r.log.Snapshot = "wp-update-" + store.Stamp(r.log.Started)
	fmt.Fprintf(r.out, "Snapshot %s of %s\n", r.log.Snapshot, r.o.Target)
	if _, err := r.soundcheck("snapshot", "--name", r.log.Snapshot); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if r.o.Bisect {
		return r.bisect(todo)
	}
	return r.oneByOne(todo)
}

// todo are the plugins the loop updates, in order.
func (r *runner) todo() []*Plugin {
	var out []*Plugin
	for i := range r.log.Plugins {
		if r.log.Plugins[i].Status == "" {
			out = append(out, &r.log.Plugins[i])
		}
	}
	return out
}

// list asks WP-CLI for the plugins with updates, keeps the ones asked
// for and puts vulnerable ones first, worst first.
func (r *runner) list(vuln map[string]string) ([]Plugin, error) {
	out, err := r.wp("plugin", "list", "--update=available", "--format=json", "--fields=name,version,update_version")
	if err != nil {
		return nil, fmt.Errorf("listing plugin updates: %w", err)
	}
	// PHP notices may come before the JSON on stdout.
	if i := bytes.IndexByte(out, '['); i > 0 {
		out = out[i:]
	}
	var rows []struct {
		Name          string `json:"name"`
		Version       string `json:"version"`
		UpdateVersion string `json:"update_version"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &rows); err != nil {
		return nil, fmt.Errorf("listing plugin updates: WP-CLI's answer isn't JSON: %w", err)
	}
	all := len(r.o.Plugins) == 0 || slices.Contains(r.o.Plugins, "all")
	var plugins []Plugin
	for _, row := range rows {
		if !all && !slices.Contains(r.o.Plugins, row.Name) {
			continue
		}
		p := Plugin{Slug: row.Name, Old: row.Version, New: row.UpdateVersion, Vulnerable: vuln[strings.ToLower(row.Name)]}
		switch {
		case strings.HasPrefix(p.Slug, "-") || p.Slug == "":
			// WP-CLI would read it as an option.
			p.Status, p.Error = Skipped, "the name starts with \"-\""
		case p.Old == "":
			// Without the old version there's nothing to roll back to.
			p.Status, p.Error = Skipped, "WP-CLI didn't say which version is installed"
		}
		plugins = append(plugins, p)
	}
	if !all {
		for _, s := range r.o.Plugins {
			if !slices.ContainsFunc(plugins, func(p Plugin) bool { return p.Slug == s }) {
				r.log.Missing = append(r.log.Missing, s)
			}
		}
	}
	slices.SortStableFunc(plugins, func(a, b Plugin) int {
		if (a.Vulnerable != "") != (b.Vulnerable != "") {
			if a.Vulnerable != "" {
				return -1
			}
			return 1
		}
		if a.Vulnerable != "" {
			return cmp.Compare(rank(a.Vulnerable), rank(b.Vulnerable))
		}
		return 0
	})
	return plugins, nil
}

// oneByOne updates each plugin and compares after it.
func (r *runner) oneByOne(todo []*Plugin) error {
	stopped := ""
	for i, p := range todo {
		if stopped != "" {
			p.Status, p.Error = NotUpdated, "stopped at "+stopped
			continue
		}
		if err := r.ctx.Err(); err != nil {
			return err
		}
		if _, err := r.wp("plugin", "update", p.Slug); err != nil {
			p.Status, p.Error = Failed, err.Error()
			fmt.Fprintf(r.out, "[%d/%d] %s: update failed: %s\n", i+1, len(todo), p.Slug, p.Error)
			continue
		}
		res, err := r.compare()
		if err != nil {
			p.Status, p.Error = Updated, "not checked: "+err.Error()
			return err
		}
		p.Verdict = res.verdict()
		r.log.Works = !res.reaches(r.log.StopOn)
		if r.log.Works {
			p.Status = Updated
			fmt.Fprintf(r.out, "[%d/%d] %s %s → %s: %s\n", i+1, len(todo), p.Slug, p.Old, p.New, p.Verdict)
			continue
		}
		p.Evidence = res.evidence(p.Slug, nil, r.log.StopOn)
		fmt.Fprintf(r.out, "[%d/%d] %s %s → %s: %s: %s\n", i+1, len(todo), p.Slug, p.Old, p.New, p.Verdict, p.Evidence)
		if !r.o.Rollback {
			p.Status, stopped = Kept, p.Slug
			continue
		}
		if err := r.rollback(p); err != nil {
			p.Status, p.Error = Kept, "couldn't roll back: "+err.Error()
			return err
		}
		p.Status = RolledBack
		// A failed or partial reinstall can leave the site broken, so
		// the rollback gets a compare of its own before the loop goes on.
		res, err = r.compare()
		if err != nil {
			r.log.Works = false
			p.Error = "the rollback wasn't checked: " + err.Error()
			return err
		}
		r.log.Works = !res.reaches(r.log.StopOn)
		fmt.Fprintf(r.out, "      after the rollback: %s\n", res.verdict())
		if !r.log.Works {
			p.Error = "the site still breaks after the rollback"
			r.log.Note = fmt.Sprintf("the site still breaks after rolling %s back to %s; check it by hand", p.Slug, p.Old)
			stopped = p.Slug
		}
	}
	return nil
}

// bisect updates every plugin and compares once. On a break it finds the
// culprit by halves: with the plugins known to be good updated, it
// updates half the suspects, rolls the other half back and keeps the
// half the break follows. With Rollback it then checks the rest without
// the culprit and repeats while the site still breaks.
func (r *runner) bisect(todo []*Plugin) error {
	updated := map[*Plugin]bool{}
	slugs := make([]string, len(todo))
	for i, p := range todo {
		slugs[i] = p.Slug
	}
	fmt.Fprintf(r.out, "Updating %d plugins\n", len(todo))
	if _, err := r.wp(append([]string{"plugin", "update"}, slugs...)...); err != nil {
		return fmt.Errorf("updating plugins: %w", err)
	}
	for _, p := range todo {
		updated[p], p.Status = true, Updated
	}
	// set updates the plugins in want and rolls every other one back.
	set := func(want []*Plugin) error {
		var up []string
		for _, p := range todo {
			in := slices.Contains(want, p)
			switch {
			case in && !updated[p]:
				up = append(up, p.Slug)
			case !in && updated[p]:
				if err := r.rollback(p); err != nil {
					return err
				}
				updated[p] = false
			}
		}
		if len(up) > 0 {
			if _, err := r.wp(append([]string{"plugin", "update"}, up...)...); err != nil {
				return err
			}
			for _, p := range want {
				updated[p] = true
			}
		}
		return nil
	}
	test := func(want []*Plugin) (result, error) {
		if err := r.ctx.Err(); err != nil {
			return result{}, err
		}
		if err := set(want); err != nil {
			return result{}, err
		}
		return r.compare()
	}

	res, err := r.compare()
	if err != nil {
		return err
	}
	fmt.Fprintf(r.out, "All %d updated: %s\n", len(todo), res.verdict())
	var culprits, good []*Plugin
	suspects := todo
	// inRes are the plugins updated in res's compare, the narrowest one
	// that broke: its evidence must not describe another one's change.
	inRes := todo
	for res.reaches(r.log.StopOn) {
		before := r.log.Compares
		for len(suspects) > 1 {
			half := suspects[:len(suspects)/2]
			try, err := test(slices.Concat(good, half))
			if err != nil {
				return err
			}
			fmt.Fprintf(r.out, "  %s: %s\n", names(half), try.verdict())
			if try.reaches(r.log.StopOn) {
				suspects, res, inRes = half, try, slices.Concat(good, half)
			} else {
				good, suspects = append(good, half...), suspects[len(suspects)/2:]
			}
		}
		c := suspects[0]
		var others []string
		for _, p := range inRes {
			if p != c {
				others = append(others, p.Slug)
			}
		}
		c.Verdict, c.Evidence, c.Compares = res.verdict(), res.evidence(c.Slug, others, r.log.StopOn), r.log.Compares-before
		culprits = append(culprits, c)
		fmt.Fprintf(r.out, "Culprit: %s %s → %s (%s), found in %d compares\n", c.Slug, c.Old, c.New, c.Evidence, c.Compares)
		if !r.o.Rollback {
			c.Status = Kept
			// Leave every update in place, as asked for.
			if err := set(todo); err != nil {
				return err
			}
			return nil
		}
		c.Status = RolledBack
		rest := slices.DeleteFunc(slices.Clone(todo), func(p *Plugin) bool { return slices.Contains(culprits, p) })
		if res, err = test(rest); err != nil {
			return err
		}
		inRes = rest
		fmt.Fprintf(r.out, "Without %s: %s\n", names(culprits), res.verdict())
		suspects = slices.DeleteFunc(rest, func(p *Plugin) bool { return slices.Contains(good, p) })
		if res.reaches(r.log.StopOn) && len(suspects) == 0 {
			// The plugins that passed together break now: a flaky
			// site or an update that only breaks with another one.
			r.log.Note = "the site still breaks with only updates that passed before; check it by hand"
			return nil
		}
	}
	r.log.Works = true
	return nil
}

// rollback installs a plugin's old version again.
func (r *runner) rollback(p *Plugin) error {
	fmt.Fprintf(r.out, "Rolling back %s to %s\n", p.Slug, p.Old)
	_, err := r.wp("plugin", "install", p.Slug, "--version="+p.Old, "--force")
	return err
}

func names(ps []*Plugin) string {
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = p.Slug
	}
	return strings.Join(s, ", ")
}

// wp runs one WP-CLI command and returns its stdout.
func (r *runner) wp(args ...string) ([]byte, error) {
	argv := wpArgv(r.o.WP, args...)
	var stdout bytes.Buffer
	stderr := &tail{max: 64 << 10}
	cmd := exec.CommandContext(r.ctx, argv[0], argv[1:]...) //nolint:gosec // the user's WP-CLI command with argv and no shell
	cmd.Stdout, cmd.Stderr = &stdout, stderr
	// Interrupt rather than kill, so WP-CLI can finish writing a plugin's
	// files; one that hangs is killed after WaitDelay.
	proc.Interrupt(cmd)
	cmd.WaitDelay = 30 * time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("wp %s: %s", strings.Join(args[:min(2, len(args))], " "), lastLine(stderr.String(), err))
	}
	return stdout.Bytes(), nil
}

// soundcheck runs the Soundcheck binary with the fast flags and returns
// its JSON.
func (r *runner) soundcheck(cmdArgs ...string) ([]byte, error) {
	args := append(append([]string(nil), cmdArgs...), "--json")
	args = append(args, fast...)
	if r.o.ConfigPath != "" {
		args = append(args, "--config", r.o.ConfigPath)
	}
	args = append(append(args, r.o.Args...), r.o.Target)
	var stdout bytes.Buffer
	stderr := &tail{max: 64 << 10}
	cmd := exec.CommandContext(r.ctx, r.o.Bin, args...) //nolint:gosec // the Soundcheck binary, with argv and no shell
	cmd.Dir, cmd.Stdout, cmd.Stderr = r.o.Dir, &stdout, stderr
	// On cancel, interrupt: Soundcheck then closes its browser.
	proc.Interrupt(cmd)
	cmd.WaitDelay = 10 * time.Second
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != exitFindings) {
		if r.ctx.Err() != nil {
			return nil, r.ctx.Err()
		}
		return nil, errors.New(lastLine(stderr.String(), err))
	}
	return stdout.Bytes(), nil
}

// compare compares the site with the snapshot.
func (r *runner) compare() (result, error) {
	out, err := r.soundcheck("compare", "--snapshot", r.log.Snapshot)
	if err != nil {
		return result{}, fmt.Errorf("compare: %w", err)
	}
	r.log.Compares++
	var res result
	if err := json.Unmarshal(out, &res); err != nil {
		return result{}, fmt.Errorf("compare: Soundcheck's answer isn't JSON: %w", err)
	}
	return res, nil
}

// result is the part of a comparison wp-update reads.
type result struct {
	Changes []model.Change `json:"changes"`
}

// verdict is the worst class among the changes, or clean.
func (c result) verdict() string {
	v, best := "clean", 0
	for _, ch := range c.Changes {
		if n := classRank[ch.Class]; n > best {
			v, best = ch.Class, n
		}
	}
	if v == model.Slower {
		v = model.Changed
	}
	return v
}

// reaches reports whether a change is at or above stopOn.
func (c result) reaches(stopOn string) bool {
	return slices.ContainsFunc(c.Changes, func(ch model.Change) bool { return classRank[ch.Class] >= classRank[stopOn] })
}

// evidence describes the change that best explains a bad update: one
// whose source is in the plugin's folder, else one in a PHP file, else
// the worst one. Changes in the folder of one of others, the plugins
// also updated in that compare, are left out while another is there:
// they're those plugins' news (a bisect can name a culprit from a
// compare that had more updates than its own).
func (c result) evidence(slug string, others []string, stopOn string) string {
	var bad, theirs []model.Change
	for _, ch := range c.Changes {
		if classRank[ch.Class] < classRank[stopOn] {
			continue
		}
		if slices.ContainsFunc(others, func(o string) bool { return strings.Contains(ch.Source, "plugins/"+o+"/") }) {
			theirs = append(theirs, ch)
			continue
		}
		bad = append(bad, ch)
	}
	if len(bad) == 0 {
		bad = theirs
	}
	if len(bad) == 0 {
		return ""
	}
	slices.SortStableFunc(bad, func(a, b model.Change) int { return cmp.Compare(classRank[b.Class], classRank[a.Class]) })
	pick := bad[0]
	inPlugin := func(ch model.Change) bool { return strings.Contains(ch.Source, "plugins/"+slug+"/") }
	inPHP := func(ch model.Change) bool { return strings.Contains(ch.Source, ".php:") }
	if i := slices.IndexFunc(bad, inPlugin); i >= 0 {
		pick = bad[i]
	} else if i := slices.IndexFunc(bad, inPHP); i >= 0 {
		pick = bad[i]
	}
	s := pick.Title
	switch n := len(pick.Pages); {
	case n == 1:
		s += " on " + pick.Pages[0]
	case n > 1:
		s += fmt.Sprintf(" on %d pages", n)
	}
	if pick.Source != "" {
		s += ", " + pick.Source
	}
	return s
}

// lastLine is the last non-empty line a command printed to stderr, or
// err when it printed nothing.
func lastLine(stderr string, err error) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return err.Error()
}

// tail keeps the last max bytes written to it, so a chatty child can't
// fill memory and its last line, usually the error, survives.
type tail struct {
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.buf) }
