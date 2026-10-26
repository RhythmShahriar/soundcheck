// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

// Command bench runs Soundcheck's public benchmark against the testbed:
// for each scenario in testbed/scenarios.yaml it sets the good state,
// takes a snapshot, breaks the scenario, compares and checks the
// comparison against the scenario's expectation. Third-party tools
// (BackstopJS, lychee, Unlighthouse) run on the same states when they're
// available. A control pass then snapshots the sites with nothing broken
// and compares twice, counting every change at or above NEW as a false
// alarm. The wp-update scenarios run the binary's wp-update on the
// testbed's ten plugin updates instead and score the culprit it names.
// The scoreboard is written to docs/benchmark.md.
//
// Run from the module root with the testbed up (make -C testbed up):
//
//	make build
//	go run -tags testbed ./testbed/bench [-bin bin/rhythm-soundcheck] [-only s01,s05] [-tools auto|none|all|lychee,…] [-control 10]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/model"
)

// The testbed's own Soundcheck account (created by wp/seed.sh), used for
// scenarios that need a login when SOUNDCHECK_USER isn't set.
const (
	testbedUser     = "soundcheck"
	testbedPassword = "soundcheck-testbed"
)

type options struct {
	bin, out, work string
	only           []string
	tools          string
	timeout        time.Duration
	sites          map[string]string
	echo           string   // Echo's URL for scenarios that need it
	control        int      // control pass rounds
	compose        []string // the testbed's docker compose command, for wp-update
}

func main() {
	var o options
	var only, wp, php, phpHTTPS, compose string
	flag.StringVar(&o.bin, "bin", "bin/rhythm-soundcheck", "the rhythm-soundcheck binary to benchmark")
	flag.StringVar(&o.out, "out", "docs/benchmark.md", "where to write the scoreboard")
	flag.StringVar(&o.work, "work", "", "working folder for runs (default testbed/bench/work)")
	flag.StringVar(&only, "only", "", "comma-separated scenario IDs to run (default all)")
	flag.StringVar(&o.tools, "tools", "auto", "third-party tools: auto (those installed), none, all, or a list: backstop,lychee,unlighthouse")
	flag.DurationVar(&o.timeout, "timeout", 5*time.Minute, "time limit for each command")
	flag.StringVar(&wp, "wp", "", "WordPress testbed URL (default from scenarios.yaml)")
	flag.StringVar(&php, "php", "", "plain-PHP testbed URL (default from scenarios.yaml)")
	flag.StringVar(&phpHTTPS, "php-https", "", "plain-PHP testbed https URL (default from scenarios.yaml)")
	flag.StringVar(&o.echo, "echo", "", "Echo's URL (e.g. http://127.0.0.1:8025) for scenarios that need it; without it they're not run")
	flag.IntVar(&o.control, "control", 10, "control pass rounds (snapshot, then compare twice); 0 skips it. Default 0 with -only")
	flag.StringVar(&compose, "compose", "", "docker compose command for the testbed's WP-CLI, split at spaces (default: docker compose -f testbed/docker-compose.yml)")
	flag.Parse()
	if only != "" {
		o.only = strings.Split(only, ",")
		// A partial run skips the control pass unless -control was given.
		set := false
		flag.Visit(func(f *flag.Flag) { set = set || f.Name == "control" })
		if !set {
			o.control = 0
		}
	}
	o.sites = map[string]string{"wp": wp, "php": php, "php-https": phpHTTPS}
	o.compose = strings.Fields(compose)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

// Results is results.json.
type Results struct {
	Scenarios []Outcome      `json:"scenarios"`
	Control   []ControlRound `json:"control,omitempty"`
}

// Outcome is everything recorded for one scenario.
type Outcome struct {
	Scenario   Scenario          `json:"scenario"`
	Soundcheck Result            `json:"soundcheck"`
	Tools      map[string]Result `json:"tools"`
	Seconds    float64           `json:"seconds"`
}

func run(ctx context.Context, o options) error {
	dir, err := Testbed()
	if err != nil {
		return err
	}
	m, err := LoadManifest(filepath.Join(dir, "scenarios.yaml"))
	if err != nil {
		return err
	}
	sitesFromEnv(m)
	for k, v := range o.sites {
		if v != "" {
			m.Sites[k] = v
		}
	}
	scenarios, err := m.Pick(o.only)
	if err != nil {
		return err
	}
	if o.work == "" {
		o.work = filepath.Join(dir, "bench", "work")
	}
	if o.work, err = filepath.Abs(o.work); err != nil {
		return err
	}
	if len(o.compose) == 0 {
		o.compose = []string{"docker", "compose", "-f", filepath.Join(dir, "docker-compose.yml")}
	}
	bin, version, binErr := soundcheck(ctx, o.bin)
	tools, err := pickTools(o.tools)
	if err != nil {
		return err
	}
	if err := Toggle(dir, "all", "good"); err != nil {
		return err
	}
	defer func() { _ = Toggle(dir, "all", "good") }()

	var outcomes []Outcome
	for _, s := range scenarios {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		start := time.Now()
		fmt.Fprintf(os.Stderr, "%s %s\n", s.ID, s.Name)
		out := Outcome{Scenario: s, Tools: map[string]Result{}}
		wd := filepath.Join(o.work, s.ID)
		if err := os.RemoveAll(wd); err != nil {
			return err
		}
		if err := os.MkdirAll(wd, 0o750); err != nil {
			return err
		}
		out.Soundcheck = benchScenario(ctx, o, dir, m, s, wd, bin, binErr, tools, out.Tools)
		out.Seconds = time.Since(start).Seconds()
		fmt.Fprintf(os.Stderr, "    soundcheck: %s %s\n", out.Soundcheck.Outcome, out.Soundcheck.Found+out.Soundcheck.Detail)
		for _, t := range tools {
			fmt.Fprintf(os.Stderr, "    %s: %s %s\n", t.Key, out.Tools[t.Key].Outcome, out.Tools[t.Key].Found+out.Tools[t.Key].Detail)
		}
		outcomes = append(outcomes, out)
	}

	var rounds []ControlRound
	if binErr == nil && o.control > 0 {
		if rounds, err = controlPass(ctx, o, dir, m, bin); err != nil {
			return err
		}
	}

	data, err := json.MarshalIndent(Results{Scenarios: outcomes, Control: rounds}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.work, "results.json"), data, 0o600); err != nil {
		return err
	}
	md := scoreboard(m, outcomes, rounds, version, binErr, tools, len(o.only) > 0)
	if err := os.MkdirAll(filepath.Dir(o.out), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(o.out, []byte(md), 0o600); err != nil {
		return err
	}
	sc := score(outcomes)
	if len(sc.notRun) > 0 {
		fmt.Fprintf(os.Stderr, "Not run and not counted: %s\n", strings.Join(sc.notRun, ", "))
	}
	switch {
	case binErr != nil:
		fmt.Fprintf(os.Stderr, "Soundcheck wasn't run: %v. Scoreboard: %s\n", binErr, o.out)
	case len(rounds) > 0:
		fmt.Fprintf(os.Stderr, "Soundcheck caught %d of %d (pending: %d of %d); %d false alarms in %d control compares. Scoreboard: %s\n",
			sc.caught, sc.total, sc.pendingCaught, sc.pending, falseAlarmCount(rounds), 2*len(rounds), o.out)
	default:
		fmt.Fprintf(os.Stderr, "Soundcheck caught %d of %d (pending: %d of %d). Scoreboard: %s\n",
			sc.caught, sc.total, sc.pendingCaught, sc.pending, o.out)
	}
	return nil
}

// benchScenario runs one scenario: good → snapshot (and tools' baseline),
// broken → compare (and tools' check). Third-party results go into tools.
func benchScenario(ctx context.Context, o options, dir string, m *Manifest, s Scenario, wd, bin string, binErr error,
	tools []*Tool, results map[string]Result) Result {
	o.timeout = max(o.timeout, s.Timeout)
	if s.Harness.Has("wp-update") {
		return wpUpdateScenario(ctx, o, m, s, wd, bin, binErr)
	}
	url := m.URL(s)
	cfgPath := filepath.Join(wd, ".soundcheck", "config.yaml")
	var owner map[string]any
	if s.Harness.Has("statement") {
		owner = statementOwner(m.Sites[s.Site])
	}
	if err := writeConfig(cfgPath, pages{Include: s.Include, Limit: s.Limit, PerGroup: s.PerGroup}, s.Levels, owner); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	if err := copyRules(dir, wd, s.Rules); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	if err := copyInto(dir, filepath.Join(wd, ".soundcheck", "flows"), s.Flows, nil); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	defer func() { _ = Toggle(dir, s.ID, "good") }()
	h, stopHarnesses, err := startHarnesses(dir, s)
	defer stopHarnesses()
	if err != nil {
		return Result{Outcome: Failed, Detail: "harness: " + err.Error()}
	}

	// Good state.
	if err := Toggle(dir, s.ID, "good"); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	var sc Result
	args := scenarioArgs(s, o.echo)
	switch {
	case binErr != nil:
		sc = Result{Outcome: NotRun, Detail: binErr.Error()}
	case s.Echo && o.echo == "":
		sc = Result{Outcome: NotRun, Detail: "needs Echo: start it (rhythm echo up), start the testbed with TESTBED_SMTP=host.docker.internal:1025 and pass -echo http://127.0.0.1:8025"}
	case s.Fixture != "":
		// A snapshot an older Soundcheck wrote stands in for one taken now.
		if err := copyFixture(dir, s.Fixture, filepath.Join(wd, ".soundcheck", "snapshots", "good"), m.Defaults[s.Site], m.Sites[s.Site]); err != nil {
			sc = Result{Outcome: Failed, Detail: "fixture: " + err.Error()}
		}
	default:
		snap := append([]string{"snapshot", url, "--name", "good", "--json", "-o", "good.json", "--config", cfgPath}, args...)
		if _, _, err := command(ctx, o.timeout, wd, env(s), bin, snap...); err != nil {
			sc = Result{Outcome: Failed, Detail: "snapshot: " + err.Error()}
		}
	}
	if sc.Outcome == "" && s.Harness.Has("delete-shots") {
		if err := os.RemoveAll(filepath.Join(wd, ".soundcheck", "snapshots", "good", "shots")); err != nil {
			sc = Result{Outcome: Failed, Detail: err.Error()}
		}
	}
	baselines := map[string]*Observation{}
	for _, t := range tools {
		obs, err := t.Observe(ctx, o.timeout, "good", url, filepath.Join(wd, t.Key))
		if err != nil {
			results[t.Key] = Result{Outcome: Failed, Detail: err.Error()}
			continue
		}
		baselines[t.Key] = obs
	}

	// Broken state (the good one again for a scenario marked same).
	if !s.Same {
		if err := Toggle(dir, s.ID, "broken"); err != nil {
			return Result{Outcome: Failed, Detail: err.Error()}
		}
	}
	if sc.Outcome == "" {
		cmp := append([]string{"compare", "--against-snapshot", "good", "--json", "-o", "compare.json", "--config", cfgPath}, args...)
		sc = compareScenario(ctx, o, s, wd, bin, cmp)
		sc = withHarnesses(sc, h.check(ctx, o, dir, s, wd, bin, cfgPath))
	}
	for _, t := range tools {
		base := baselines[t.Key]
		if base == nil {
			continue
		}
		obs, err := t.Observe(ctx, o.timeout, "broken", url, filepath.Join(wd, t.Key))
		if err != nil {
			results[t.Key] = Result{Outcome: Failed, Detail: err.Error()}
			continue
		}
		results[t.Key] = t.Judge(base, obs)
	}
	return sc
}

// scenarioArgs are the flags a scenario adds to both runs.
func scenarioArgs(s Scenario, echo string) []string {
	args := slices.Clone(s.Args)
	if len(s.Levels) > 0 {
		args = append(args, "--level", strings.Join(s.Levels, ","))
	}
	if len(s.Modules) > 0 {
		args = append(args, "--modules", strings.Join(s.Modules, ","))
	}
	if s.Echo && echo != "" {
		args = append(args, "--echo="+echo)
	}
	return args
}

// compareScenario runs compare in the broken state the way the
// scenario's harness says and evaluates what it wrote.
func compareScenario(ctx context.Context, o options, s Scenario, wd, bin string, args []string) Result {
	switch {
	case s.Harness.Has("interrupt"):
		code, err := interrupted(ctx, o.timeout, s.InterruptAfter, wd, env(s), bin, args...)
		if err != nil {
			return Result{Outcome: Failed, Detail: "compare: " + err.Error()}
		}
		if _, err := os.Stat(filepath.Join(wd, "compare.json")); err != nil {
			// compare didn't write its report; what did it store?
			_, _, _ = command(ctx, o.timeout, wd, env(s), bin, "report", "--json", "-o", "compare.json", "-q")
		}
		return evaluateFile(s, filepath.Join(wd, "compare.json"), code, nil)

	case s.Harness.Has("parallel"):
		a := slices.Clone(args)
		b := slices.Clone(args)
		a[slices.Index(a, "compare.json")] = "compare-a.json"
		b[slices.Index(b, "compare.json")] = "compare-b.json"
		var codes [2]int
		var errs [2]error
		var wg sync.WaitGroup
		for i, run := range [][]string{a, b} {
			wg.Go(func() { _, codes[i], errs[i] = command(ctx, o.timeout, wd, env(s), bin, run...) })
		}
		wg.Wait()
		ra := evaluateFile(s, filepath.Join(wd, "compare-a.json"), codes[0], errs[0])
		rb := evaluateFile(s, filepath.Join(wd, "compare-b.json"), codes[1], errs[1])
		runs, _ := os.ReadDir(filepath.Join(wd, ".soundcheck", "runs"))
		folders := 0
		for _, r := range runs {
			if r.IsDir() {
				folders++
			}
		}
		switch {
		case ra.Outcome != Caught:
			ra.Detail = strings.TrimSpace("first compare: " + ra.Detail)
			return ra
		case rb.Outcome != Caught:
			rb.Detail = strings.TrimSpace("second compare: " + rb.Detail)
			return rb
		case folders < 2:
			return Result{Outcome: Missed, Detail: fmt.Sprintf("both compares wrote into %d run folder", folders)}
		}
		ra.Found = fmt.Sprintf("both compares clean, in %d separate run folders", folders)
		return ra

	case s.Harness.Has("stress"):
		stop := stress(ctx)
		_, code, err := command(ctx, o.timeout, wd, env(s), bin, args...)
		stop()
		return evaluateFile(s, filepath.Join(wd, "compare.json"), code, err)
	}
	start := time.Now()
	_, code, err := command(ctx, o.timeout, wd, env(s), bin, args...)
	r := evaluateFile(s, filepath.Join(wd, "compare.json"), code, err)
	if took := time.Since(start); s.Within > 0 && took > s.Within && r.Outcome == Caught {
		return Result{Outcome: Missed, Detail: fmt.Sprintf("compare took %s, want at most %s", took.Round(time.Second), s.Within), Others: r.Others}
	}
	return r
}

// evaluateFile reads a compare's JSON output and evaluates it. A compare
// that wrote nothing is an error unless the scenario expects its exit code.
func evaluateFile(s Scenario, path string, code int, runErr error) Result {
	cmp, extra, rerr := readComparison(path)
	if rerr != nil {
		if !slices.ContainsFunc(s.Expect, func(e Expect) bool { return e.kind() == kindExit }) {
			if runErr != nil {
				return Result{Outcome: Failed, Detail: "compare: " + runErr.Error()}
			}
			return Result{Outcome: Failed, Detail: filepath.Base(path) + ": " + rerr.Error()}
		}
		cmp = nil
	}
	return Evaluate(s, Observed{Comparison: cmp, Interrupted: extra.Interrupted, Exit: code})
}

// interrupted runs a command and sends it Ctrl+C (SIGINT) after delay, the
// way a user stops a compare. It returns the exit code.
func interrupted(ctx context.Context, timeout, delay time.Duration, dir string, env []string, name string, args ...string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the benchmark runs the tool it was asked to
	cmd.Dir, cmd.Env = dir, env
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	timer := time.AfterFunc(delay, func() { _ = cmd.Process.Signal(os.Interrupt) })
	defer timer.Stop()
	err := cmd.Wait()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		return exit.ExitCode(), nil
	}
	return 0, err
}

// stress keeps every CPU busy until the returned stop is called, so a
// compare runs on a loaded machine (a busy CI runner).
func stress(ctx context.Context) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Go(func() {
			x := uint64(1)
			for ctx.Err() == nil {
				for range 1 << 16 {
					x = x*6364136223846793005 + 1442695040888963407
				}
			}
			_ = x
		})
	}
	return func() { cancel(); wg.Wait() }
}

// copyRules copies the scenario's rule files from the testbed into the
// run's .soundcheck/rules/, with their *_test.yaml samples when present.
func copyRules(testbed, wd string, rules []string) error {
	if len(rules) == 0 {
		return nil
	}
	dst := filepath.Join(wd, ".soundcheck", "rules")
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	for _, r := range rules {
		src := filepath.Join(testbed, r)
		samples := strings.TrimSuffix(src, ".yaml") + "_test.yaml"
		for _, f := range []string{src, samples} {
			data, err := os.ReadFile(f) //nolint:gosec // the testbed's own rule files
			if errors.Is(err, os.ErrNotExist) && f == samples {
				continue
			}
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dst, filepath.Base(f)), data, 0o600); err != nil { //nolint:gosec // a base name, inside the run folder
				return err
			}
		}
	}
	return nil
}

// copyInto copies the given files (relative to testbed/) into dst under
// their base names. edit, when set, may change each file's content.
func copyInto(testbed, dst string, files []string, edit func([]byte) []byte) error {
	if len(files) == 0 {
		return nil
	}
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(testbed, f)) //nolint:gosec // the testbed's own files
		if err != nil {
			return err
		}
		if edit != nil {
			data = edit(data)
		}
		if err := os.WriteFile(filepath.Join(dst, filepath.Base(f)), data, 0o600); err != nil { //nolint:gosec // a base name, inside the run folder
			return err
		}
	}
	return nil
}

// copyFixture copies a committed snapshot folder into dst. The fixture
// was taken of the site at its default address (scenarios.yaml); when
// the bench points the site elsewhere, from is replaced by to in it so
// the compare checks the same pages.
func copyFixture(testbed, fixture, dst, from, to string) error {
	entries, err := os.ReadDir(filepath.Join(testbed, fixture))
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, filepath.Join(fixture, e.Name()))
		}
	}
	return copyInto(testbed, dst, files, func(b []byte) []byte {
		if from == "" || from == to {
			return b
		}
		return bytes.ReplaceAll(b, []byte(strings.TrimRight(from, "/")), []byte(strings.TrimRight(to, "/")))
	})
}

// soundcheck finds the binary and its version. A missing binary isn't
// fatal: third-party tools can still be benchmarked.
func soundcheck(ctx context.Context, bin string) (string, string, error) {
	abs, err := filepath.Abs(bin)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", "", fmt.Errorf("%s not found (make build)", bin)
	}
	out, _, err := command(ctx, 30*time.Second, "", nil, abs, "--version")
	if err != nil {
		return abs, "", nil //nolint:nilerr // the version is only shown in the scoreboard
	}
	return abs, strings.TrimSpace(out), nil
}

// pages is the pages section of the config the bench writes.
type pages struct {
	Include  []string `yaml:"include,omitempty"`
	Limit    int      `yaml:"limit"`
	PerGroup int      `yaml:"per_group,omitempty"`
}

// writeConfig limits the run to the given pages (page limit default 20)
// and sets the interaction levels and, when given, the statement: block.
func writeConfig(path string, p pages, levels []string, statement map[string]any) error {
	type interactions struct {
		Levels []string `yaml:"levels,omitempty"`
	}
	if p.Limit == 0 {
		p.Limit = 20
	}
	cfg := struct {
		Pages        pages          `yaml:"pages"`
		Interactions *interactions  `yaml:"interactions,omitempty"`
		Statement    map[string]any `yaml:"statement,omitempty"`
	}{Pages: p, Statement: statement}
	if len(levels) > 0 {
		cfg.Interactions = &interactions{Levels: levels}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// env gives scenarios that need a login the testbed's account, unless
// the caller set one.
func env(s Scenario) []string {
	e := os.Environ()
	if s.Login && os.Getenv("SOUNDCHECK_USER") == "" {
		e = append(e, "SOUNDCHECK_USER="+testbedUser, "SOUNDCHECK_PASSWORD="+testbedPassword)
	}
	return e
}

// command runs name with args in dir and returns its combined output and
// exit code.
func command(ctx context.Context, timeout time.Duration, dir string, env []string, name string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the benchmark runs the tools it was asked to
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		tail := strings.TrimSpace(string(out))
		if len(tail) > 400 {
			tail = "…" + tail[len(tail)-400:]
		}
		return string(out), code, fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, tail)
	}
	return string(out), 0, nil
}

// comparisonExtra is what the bench reads from a comparison beyond
// model.Comparison: fields other lanes are adding (the interrupted marker).
type comparisonExtra struct {
	Interrupted bool `json:"interrupted"`
}

// readComparison reads `compare --json` output: a model.Comparison, or an
// object that wraps one in "comparison".
func readComparison(path string) (*model.Comparison, comparisonExtra, error) {
	var extra comparisonExtra
	data, err := os.ReadFile(path) //nolint:gosec // our own output
	if err != nil {
		return nil, extra, err
	}
	var c model.Comparison
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, extra, err
	}
	if c.Schema != "" || c.Changes != nil {
		_ = json.Unmarshal(data, &extra)
		return &c, extra, nil
	}
	var wrap struct {
		Comparison json.RawMessage `json:"comparison"`
	}
	if err := json.Unmarshal(data, &wrap); err == nil && len(wrap.Comparison) > 0 {
		var inner model.Comparison
		if err := json.Unmarshal(wrap.Comparison, &inner); err == nil {
			_ = json.Unmarshal(wrap.Comparison, &extra)
			return &inner, extra, nil
		}
	}
	return nil, extra, errors.New("no comparison in the JSON output")
}
