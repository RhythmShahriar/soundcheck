// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package fleet

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/history"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/store"
)

// Exit codes, as the Soundcheck binary uses them.
const (
	exitError    = 3
	exitCanceled = 130
)

// Verdicts, worst first. A comparison is broke, changed or clean; a
// single run (a snapshot) has errors, warnings or is clean. Failed means
// Soundcheck couldn't check the site at all.
const (
	Failed      = "failed"
	Broke       = "broke"
	Interrupted = "interrupted"
	Errors      = "errors"
	Changed     = "changed"
	Warnings    = "warnings"
	Clean       = "clean"
)

var verdictOrder = []string{Failed, Broke, Interrupted, Errors, Changed, Warnings, Clean}

var verdictLabels = map[string]string{
	Failed: "Couldn't check", Broke: "Something broke", Interrupted: "Interrupted", Errors: "Has errors",
	Changed: "Changed", Warnings: "Warnings", Clean: "Clean",
}

// Result is how one site's run went.
type Result struct {
	Site     string         `json:"site"`
	URL      string         `json:"url"`
	Kind     string         `json:"kind,omitempty"` // run or comparison
	Verdict  string         `json:"verdict"`
	Counts   map[string]int `json:"counts"` // changes by class, or issues by level
	Pages    int            `json:"pages"`
	Exit     int            `json:"exit"`
	Error    string         `json:"error,omitempty"`  // why the site couldn't be checked
	Dir      string         `json:"dir"`              // the site's folder, relative to the fleet folder
	Report   string         `json:"report,omitempty"` // its HTML report, relative to the fleet folder
	Duration float64        `json:"duration_ms"`
	// Trend is the site's median LCP over its last runs, oldest first,
	// from its history.
	Trend []float64 `json:"trend_lcp_ms,omitempty"`
}

// Label is the verdict in words.
func (r Result) Label() string { return verdictLabels[r.Verdict] }

// countKeys lists the counts shown for a result, in order.
func (r Result) countKeys() []string {
	if r.Kind == "comparison" {
		return []string{model.Broke, model.New, model.Changed, model.Slower, model.Fixed}
	}
	return []string{string(model.Error), string(model.Warning), string(model.Notice)}
}

// worse orders results worst first: by verdict, then by the most
// serious counts, then by name.
func worse(a, b Result) int {
	if c := cmp.Compare(slices.Index(verdictOrder, a.Verdict), slices.Index(verdictOrder, b.Verdict)); c != 0 {
		return c
	}
	for _, k := range a.countKeys() {
		if k == model.Fixed {
			break
		}
		if c := cmp.Compare(b.Counts[k], a.Counts[k]); c != 0 {
			return c
		}
	}
	return cmp.Compare(a.Site, b.Site)
}

// Run checks every site in the sites file with the Soundcheck binary bin,
// mode "snapshot" or "compare", at most parallel at a time. It prints a
// line per finished site to out, writes fleet.html and fleet.json into
// the fleet folder and returns the worst exit code of all the sites.
func Run(ctx context.Context, bin, file, mode string, parallel int, out io.Writer) (int, error) {
	if mode != Snapshot && mode != Compare {
		return 0, fmt.Errorf("fleet: mode %q: want snapshot or compare", mode)
	}
	sites, err := Load(file)
	if err != nil {
		return 0, err
	}
	base, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		return 0, err
	}
	root := filepath.Join(base, Folder)
	parallel = max(1, min(parallel, len(sites)))
	fmt.Fprintf(out, "Fleet: %s %d sites, %d at a time\n", verb(mode), len(sites), parallel)

	results := make([]Result, len(sites))
	var (
		mu   sync.Mutex
		done int
		wg   sync.WaitGroup
	)
	sem := make(chan struct{}, parallel)
	for i, s := range sites {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := runSite(ctx, bin, base, root, mode, s)
			mu.Lock()
			defer mu.Unlock()
			results[i] = r
			done++
			fmt.Fprintf(out, "  [%d/%d] %s: %s\n", done, len(sites), s.Name, summaryLine(r))
		}()
	}
	wg.Wait()

	slices.SortStableFunc(results, worse)
	page := Page{Mode: mode, Created: time.Now(), Sites: results}
	if err := cache.WriteJSON(filepath.Join(root, "fleet.json"), page); err != nil {
		return 0, err
	}
	html := filepath.Join(root, "fleet.html")
	var buf bytes.Buffer
	if err := HTML(&buf, page); err != nil {
		return 0, err
	}
	if err := cache.WriteFile(html, buf.Bytes()); err != nil {
		return 0, err
	}
	worst := 0
	for _, r := range results {
		worst = max(worst, r.Exit)
	}
	fmt.Fprintf(out, "Worst: %s (%s)\nFleet report: %s\n", results[0].Site, results[0].Label(), html)
	return worst, nil
}

func verb(mode string) string {
	if mode == Snapshot {
		return "taking a snapshot of"
	}
	return "comparing"
}

// summaryLine is "Something broke · 2 broke, 1 new · 41s".
func summaryLine(r Result) string {
	parts := []string{r.Label()}
	if r.Error != "" {
		parts = append(parts, r.Error)
	}
	var counts []string
	for _, k := range r.countKeys() {
		if n := r.Counts[k]; n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", n, countName(k, n)))
		}
	}
	if len(counts) > 0 {
		parts = append(parts, strings.Join(counts, ", "))
	}
	parts = append(parts, (time.Duration(r.Duration) * time.Millisecond).Round(time.Second).String())
	return strings.Join(parts, " · ")
}

// countName is "broke", "new" or "errors" as a count's noun.
func countName(k string, n int) string {
	switch k {
	case string(model.Error), string(model.Warning), string(model.Notice):
		if n == 1 {
			return k
		}
		return k + "s"
	}
	return k
}

// runSite runs Soundcheck for one site in its own folder.
func runSite(ctx context.Context, bin, base, root, mode string, s Site) Result {
	dir := filepath.Join(root, s.Name)
	r := Result{Site: s.Name, URL: s.URL, Dir: s.Name, Counts: map[string]int{}}
	start := time.Now()
	fail := func(exit int, msg string) Result {
		r.Verdict, r.Exit, r.Error = Failed, exit, msg
		if ctx.Err() != nil {
			r.Verdict, r.Exit, r.Error = Interrupted, exitCanceled, ""
		}
		r.Duration = float64(time.Since(start).Milliseconds())
		return r
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fail(exitError, err.Error())
	}
	args := []string{mode, "--json"}
	if s.Config != "" {
		args = append(args, "--config", filepath.Join(base, s.Config))
	}
	args = append(append(args, s.Args...), s.URL)
	logf, err := os.Create(filepath.Join(dir, "soundcheck.log")) //nolint:gosec // inside our own fleet folder
	if err != nil {
		return fail(exitError, err.Error())
	}
	defer func() { _ = logf.Close() }()
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // the Soundcheck binary, with argv from sites.yaml and no shell
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, childEnv(os.Environ(), s.EnvPrefix), &stdout, logf
	// On cancel, interrupt rather than kill: Soundcheck then closes its
	// browser and drops the half-made snapshot. A run that doesn't stop
	// within WaitDelay is killed.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	runErr := cmd.Run()
	r.Exit = cmd.ProcessState.ExitCode()
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return fail(exitError, "couldn't start Soundcheck: "+runErr.Error())
	}
	if err := cache.WriteFile(filepath.Join(dir, "result.json"), stdout.Bytes()); err != nil {
		return fail(exitError, err.Error())
	}
	if err := r.parse(stdout.Bytes()); err != nil {
		msg := "no result (see " + filepath.Join(s.Name, "soundcheck.log") + ")"
		if r.Exit == exitCanceled {
			return fail(exitCanceled, msg)
		}
		return fail(max(r.Exit, exitError), msg)
	}
	if rep := reportPath(dir, r.Kind, stdout.Bytes()); rep != "" {
		if rel, err := filepath.Rel(root, rep); err == nil {
			r.Report = filepath.ToSlash(rel)
		}
	}
	r.Trend = trend(filepath.Join(dir, config.Dir))
	r.Duration = float64(time.Since(start).Milliseconds())
	return r
}

// trendRuns is how many of a site's latest entries its trend shows.
const trendRuns = 12

// trend reads the median LCP of a site's latest runs from its history.
func trend(storeRoot string) []float64 {
	entries, err := history.Read(storeRoot)
	if err != nil {
		return nil
	}
	var out []float64
	for _, e := range entries[max(0, len(entries)-trendRuns):] {
		if e.LCP > 0 {
			out = append(out, e.LCP)
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// child is the part of Soundcheck's --json output the fleet reads: the
// summary when the binary writes one, and the changes or issues to
// count when it doesn't.
type child struct {
	Kind    string `json:"kind"`
	Summary *struct {
		Broke, New, Changed, Slower, Fixed int
		Verdict                            string
	} `json:"summary"`
	Changes []model.Change `json:"changes"`
	Before  *model.RunRef  `json:"before"`
	Pages   json.RawMessage
}

func (r *Result) parse(data []byte) error {
	var c child
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.Kind == "" { // older binaries: a comparison is the one with a snapshot
		c.Kind = "run"
		if c.Before != nil {
			c.Kind = "comparison"
		}
	}
	r.Kind = c.Kind
	if c.Kind == "comparison" {
		_ = json.Unmarshal(c.Pages, &r.Pages)
		for _, ch := range c.Changes {
			r.Counts[ch.Class]++
		}
		if s := c.Summary; s != nil {
			r.Counts = map[string]int{model.Broke: s.Broke, model.New: s.New, model.Changed: s.Changed, model.Slower: s.Slower, model.Fixed: s.Fixed}
		}
		r.Verdict = Clean
		switch {
		case r.Counts[model.Broke] > 0:
			r.Verdict = Broke
		case r.Counts[model.New]+r.Counts[model.Changed]+r.Counts[model.Slower] > 0:
			r.Verdict = Changed
		}
		if c.Summary != nil && slices.Contains(verdictOrder, c.Summary.Verdict) {
			r.Verdict = c.Summary.Verdict
		}
		return nil
	}
	var run model.Run
	if err := json.Unmarshal(data, &run); err != nil {
		return err
	}
	r.Pages = len(run.Pages)
	for l, n := range report.Counts(report.Issues(&run)) {
		r.Counts[string(l)] = n
	}
	r.Verdict = Clean
	switch {
	case r.Counts[string(model.Error)] > 0:
		r.Verdict = Errors
	case r.Counts[string(model.Warning)] > 0:
		r.Verdict = Warnings
	}
	if c.Summary != nil && c.Summary.Verdict == Interrupted {
		r.Verdict = Interrupted
	}
	return nil
}

// reportPath finds the HTML report Soundcheck wrote next to the result:
// in the snapshot's folder, or in the newest run's.
func reportPath(dir, kind string, data []byte) string {
	st := store.Store{Root: filepath.Join(dir, config.Dir)}
	var d string
	if kind == "comparison" {
		d, _ = st.LatestRun()
	} else {
		var run struct{ Name string }
		if json.Unmarshal(data, &run) == nil && run.Name != "" {
			d, _ = st.Snapshot(run.Name)
		}
	}
	if d == "" {
		return ""
	}
	p := filepath.Join(d, "report.html")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// passEnv are the variables every site's run gets from the fleet's own
// environment: what a program needs to run and find Chrome. Credentials
// never come from here; see childEnv.
var passEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "TZ", "TMPDIR", "TMP", "TEMP",
	"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"CI", "GITHUB_ACTIONS",
	// Windows
	"SystemRoot", "SYSTEMROOT", "windir", "ComSpec", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
	"ProgramFiles", "ProgramFiles(x86)", "PROGRAMFILES", "PATHEXT",
}

// siteFamilies are the variable families a site gets from its prefixed
// copies: ACME_SOUNDCHECK_PASSWORD becomes SOUNDCHECK_PASSWORD.
var siteFamilies = []string{"SOUNDCHECK_", "RHYTHM_ECHO_"}

// childEnv builds a site's environment from an allowlist, so one site's
// password never reaches another site's run. The fleet's own
// SOUNDCHECK_* variables aren't passed either: they would reach every
// site.
func childEnv(environ []string, prefix string) []string {
	out := []string{"NO_COLOR=1"}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if slices.Contains(passEnv, k) || strings.HasPrefix(k, "LC_") {
			out = append(out, kv)
			continue
		}
		rest, ok := strings.CutPrefix(k, prefix+"_")
		if !ok {
			continue
		}
		for _, fam := range siteFamilies {
			if strings.HasPrefix(rest, fam) && len(rest) > len(fam) {
				out = append(out, rest+"="+v)
			}
		}
	}
	return out
}
