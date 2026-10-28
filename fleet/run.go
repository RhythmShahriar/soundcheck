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
	"github.com/RhythmShahriar/soundcheck/internal/proc"
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
	// EAAOpen and CriteriaFailed are the open accessibility and
	// keyboard issues together and the WCAG criteria with failures of an
	// --eaa run, from its JSON eaa block (history.jsonl splits the open
	// issues into a11y_open and keyboard_open). A site checked without
	// --eaa leaves them out (not 0).
	EAAOpen        *int `json:"eaa_open,omitempty"`
	CriteriaFailed *int `json:"criteria_failed,omitempty"`
	// EAATrend says how EAAOpen moved since the site's previous fleet
	// run: up, down or same. It is absent when either run had no eaa
	// block.
	EAATrend string `json:"eaa_trend,omitempty"`
	// EAABefore is EAAOpen of that previous fleet run, for the page's
	// trend label.
	EAABefore *int `json:"eaa_before,omitempty"`
	// Statement is the site's accessibility statement draft, relative
	// to the fleet folder, when its --eaa run wrote one.
	Statement string `json:"statement,omitempty"`
	// VerdictLabel is Label at the time the fleet ran, so a reader of
	// fleet.json sees that a clean --eaa result isn't an all-clear.
	VerdictLabel string `json:"label,omitempty"`
}

// Trends of EAAOpen against the previous fleet run.
const (
	TrendUp   = "up"
	TrendDown = "down"
	TrendSame = "same"
)

// Label is the verdict in words. A clean --eaa result never reads as an
// all-clear, as the single-site verdicts don't: a comparison says
// "Nothing changed" and adds what the tests found only when no
// accessibility problem is open; a snapshot says what the tests found.
func (r Result) Label() string {
	if r.Verdict != Clean || r.EAAOpen == nil {
		return verdictLabels[r.Verdict]
	}
	found := "no failures found by these tests"
	if r.CriteriaFailed != nil && *r.CriteriaFailed > 0 {
		found = fmt.Sprintf("%d WCAG criteria have failures", *r.CriteriaFailed)
		if *r.CriteriaFailed == 1 {
			found = "1 WCAG criterion has failures"
		}
	}
	switch {
	case r.Kind != "comparison":
		return strings.ToUpper(found[:1]) + found[1:]
	case *r.EAAOpen > 0:
		return "Nothing changed"
	}
	return "Nothing changed; " + found
}

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
// mode "snapshot" or "compare", at most parallel at a time. extra are
// flags for every site, such as --eaa, added after the site's own args;
// like a site's args they can't set a flag the fleet sets itself. It
// prints a line per finished site to out, writes fleet.html and
// fleet.json into the fleet folder and returns the worst exit code of
// all the sites.
func Run(ctx context.Context, bin, file, mode string, parallel int, extra []string, out io.Writer) (int, error) {
	if mode != Snapshot && mode != Compare {
		return 0, fmt.Errorf("fleet: mode %q: want snapshot or compare", mode)
	}
	if a, ok := reservedArg(extra); ok {
		return 0, fmt.Errorf("fleet: can't set %s for every site; the fleet sets it itself", a)
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
			r := runSite(ctx, bin, base, root, mode, s, extra)
			mu.Lock()
			defer mu.Unlock()
			results[i] = r
			done++
			fmt.Fprintf(out, "  [%d/%d] %s: %s\n", done, len(sites), s.Name, summaryLine(r))
		}()
	}
	wg.Wait()

	slices.SortStableFunc(results, worse)
	a11yTrends(results, previous(filepath.Join(root, "fleet.json")))
	for i := range results {
		results[i].VerdictLabel = results[i].Label()
	}
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

// previous reads the last fleet run's page, or nil when there is none or
// it can't be read: the trend is then left out, never guessed.
func previous(path string) *Page {
	data, err := os.ReadFile(path) //nolint:gosec // our own fleet folder
	if err != nil {
		return nil
	}
	var p Page
	if json.Unmarshal(data, &p) != nil {
		return nil
	}
	return &p
}

// a11yTrends sets each --eaa result's EAATrend against the same site in
// the previous fleet run, when that run had an eaa block for it too.
func a11yTrends(results []Result, prev *Page) {
	if prev == nil {
		return
	}
	before := map[string]int{}
	for _, r := range prev.Sites {
		if r.EAAOpen != nil {
			before[r.Site] = *r.EAAOpen
		}
	}
	for i := range results {
		r := &results[i]
		b, ok := before[r.Site]
		if r.EAAOpen == nil || !ok {
			continue
		}
		r.EAABefore = &b
		switch now := *r.EAAOpen; {
		case now > b:
			r.EAATrend = TrendUp
		case now < b:
			r.EAATrend = TrendDown
		default:
			r.EAATrend = TrendSame
		}
	}
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
func runSite(ctx context.Context, bin, base, root, mode string, s Site, extra []string) Result {
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
	args = append(args, s.Args...)
	// eaa: true in sites.yaml adds --eaa, unless the site's args or the
	// fleet's own (fleet ... --eaa) already have it.
	if s.EAA && !hasFlag(s.Args, "eaa") && !hasFlag(extra, "eaa") {
		args = append(args, "--eaa")
	}
	args = append(append(args, extra...), s.URL)
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
	proc.Interrupt(cmd)
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
	runDir := resultDir(dir, r.Kind, stdout.Bytes())
	r.Report = inFleet(root, runDir, "report.html")
	if r.EAAOpen != nil {
		r.Statement = inFleet(root, runDir, filepath.Join("eaa", "statement-draft.md"))
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
// summary when the binary writes one, or the changes or issues to
// count when it doesn't.
type child struct {
	Kind        string `json:"kind"`
	Interrupted bool   `json:"interrupted"`
	Summary     *struct {
		Broke, New, Changed, Slower, Fixed int
		Verdict                            string
	} `json:"summary"`
	Changes []model.Change `json:"changes"`
	Before  *model.RunRef  `json:"before"`
	Pages   json.RawMessage
	// EAA is the accessibility summary of an --eaa run or comparison.
	EAA *struct {
		Open     int `json:"open"`
		Criteria struct {
			Failed []json.RawMessage `json:"failed"`
		} `json:"criteria"`
	} `json:"eaa"`
}

func (r *Result) parse(data []byte) error {
	var c child
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	// A summary always has a verdict. One without (a model.Comparison
	// marshaled before Complete) holds zero counts that say nothing, so
	// count the changes instead.
	if c.Summary != nil && c.Summary.Verdict == "" {
		c.Summary = nil
	}
	if c.Kind == "" { // older binaries: a comparison is the one with a snapshot
		c.Kind = "run"
		if c.Before != nil {
			c.Kind = "comparison"
		}
	}
	r.Kind = c.Kind
	if c.EAA != nil {
		open, failed := c.EAA.Open, len(c.EAA.Criteria.Failed)
		r.EAAOpen, r.CriteriaFailed = &open, &failed
	}
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
		if c.Interrupted {
			r.Verdict = Interrupted
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
	if c.Interrupted || (c.Summary != nil && c.Summary.Verdict == Interrupted) {
		r.Verdict = Interrupted
	}
	return nil
}

// inFleet is the file name in runDir relative to the fleet folder, or ""
// when there's no such file.
func inFleet(root, runDir, name string) string {
	if runDir == "" {
		return ""
	}
	p := filepath.Join(runDir, name)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// resultDir finds the folder Soundcheck wrote the result's files (the
// HTML report, the statement draft) in: the snapshot's folder, or the
// newest run's.
func resultDir(dir, kind string, data []byte) string {
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
	return d
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
