//go:build testbed

// Command bench runs Soundcheck's public benchmark against the testbed:
// for each scenario in testbed/scenarios.yaml it sets the good state,
// takes a snapshot, breaks the scenario, compares, and checks the
// comparison against the scenario's expectation. Third-party tools
// (BackstopJS, lychee, Unlighthouse) run on the same states when they're
// available. The scoreboard is written to docs/benchmark.md.
//
// Run from the module root with the testbed up (make -C testbed up):
//
//	make build
//	go run -tags testbed ./testbed/bench [-bin bin/rhythm-soundcheck] [-only s01,s05] [-tools auto|none|all|lychee,…]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
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
}

func main() {
	var o options
	var only, wp, php string
	flag.StringVar(&o.bin, "bin", "bin/rhythm-soundcheck", "the rhythm-soundcheck binary to benchmark")
	flag.StringVar(&o.out, "out", "docs/benchmark.md", "where to write the scoreboard")
	flag.StringVar(&o.work, "work", "", "working folder for runs (default testbed/bench/work)")
	flag.StringVar(&only, "only", "", "comma-separated scenario IDs to run (default all)")
	flag.StringVar(&o.tools, "tools", "auto", "third-party tools: auto (those installed), none, all, or a list: backstop,lychee,unlighthouse")
	flag.DurationVar(&o.timeout, "timeout", 5*time.Minute, "time limit for each command")
	flag.StringVar(&wp, "wp", "", "WordPress testbed URL (default from scenarios.yaml)")
	flag.StringVar(&php, "php", "", "plain-PHP testbed URL (default from scenarios.yaml)")
	flag.Parse()
	if only != "" {
		o.only = strings.Split(only, ",")
	}
	o.sites = map[string]string{"wp": wp, "php": php}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
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

	data, err := json.MarshalIndent(outcomes, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.work, "results.json"), data, 0o600); err != nil {
		return err
	}
	md := scoreboard(m, outcomes, version, binErr, tools, len(o.only) > 0)
	if err := os.MkdirAll(filepath.Dir(o.out), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(o.out, []byte(md), 0o600); err != nil {
		return err
	}
	caught := 0
	for _, x := range outcomes {
		if x.Soundcheck.Outcome == Caught {
			caught++
		}
	}
	if binErr != nil {
		fmt.Fprintf(os.Stderr, "Soundcheck wasn't run: %v. Scoreboard: %s\n", binErr, o.out)
	} else {
		fmt.Fprintf(os.Stderr, "Soundcheck caught %d of %d. Scoreboard: %s\n", caught, len(outcomes), o.out)
	}
	return nil
}

// benchScenario runs one scenario: good → snapshot (and tools' baseline),
// broken → compare (and tools' check). Third-party results go into tools.
func benchScenario(ctx context.Context, o options, dir string, m *Manifest, s Scenario, wd, bin string, binErr error,
	tools []*Tool, results map[string]Result) Result {
	url := m.URL(s)
	cfgPath := filepath.Join(wd, ".soundcheck", "config.yaml")
	if err := writeConfig(cfgPath, s); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	defer func() { _ = Toggle(dir, s.ID, "good") }()

	// Good state.
	if err := Toggle(dir, s.ID, "good"); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	var sc Result
	if binErr != nil {
		sc = Result{Outcome: NotRun, Detail: binErr.Error()}
	} else {
		args := []string{"snapshot", url, "--name", "good", "--json", "-o", "good.json", "--config", cfgPath}
		if len(s.Levels) > 0 {
			args = append(args, "--level", strings.Join(s.Levels, ","))
		}
		if _, err := command(ctx, o.timeout, wd, env(s), bin, args...); err != nil {
			sc = Result{Outcome: Failed, Detail: "snapshot: " + err.Error()}
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

	// Broken state.
	if err := Toggle(dir, s.ID, "broken"); err != nil {
		return Result{Outcome: Failed, Detail: err.Error()}
	}
	if sc.Outcome == "" {
		_, err := command(ctx, o.timeout, wd, env(s), bin, "compare", "--against-snapshot", "good", "--json", "-o", "compare.json", "--config", cfgPath)
		cmp, rerr := readComparison(filepath.Join(wd, "compare.json"))
		switch {
		case rerr != nil && err != nil:
			sc = Result{Outcome: Failed, Detail: "compare: " + err.Error()}
		case rerr != nil:
			sc = Result{Outcome: Failed, Detail: "compare.json: " + rerr.Error()}
		default:
			sc = Evaluate(s, cmp)
		}
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
	out, err := command(ctx, 30*time.Second, "", nil, abs, "--version")
	if err != nil {
		return abs, "", nil //nolint:nilerr // the version is only shown in the scoreboard
	}
	return abs, strings.TrimSpace(out), nil
}

// writeConfig limits the run to the scenario's pages.
func writeConfig(path string, s Scenario) error {
	type pages struct {
		Include []string `yaml:"include"`
		Limit   int      `yaml:"limit"`
	}
	type interactions struct {
		Levels []string `yaml:"levels,omitempty"`
	}
	cfg := struct {
		Pages        pages         `yaml:"pages"`
		Interactions *interactions `yaml:"interactions,omitempty"`
	}{Pages: pages{Include: s.Include, Limit: 20}}
	if len(s.Levels) > 0 {
		cfg.Interactions = &interactions{Levels: s.Levels}
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

// command runs name with args in dir and returns its combined output.
func command(ctx context.Context, timeout time.Duration, dir string, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the benchmark runs the tools it was asked to
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 400 {
			tail = "…" + tail[len(tail)-400:]
		}
		return string(out), fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, tail)
	}
	return string(out), nil
}

// readComparison reads `compare --json` output: a model.Comparison, or an
// object that wraps one in "comparison".
func readComparison(path string) (*model.Comparison, error) {
	data, err := os.ReadFile(path) //nolint:gosec // our own output
	if err != nil {
		return nil, err
	}
	var c model.Comparison
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Schema != "" || c.Changes != nil {
		return &c, nil
	}
	var wrap struct {
		Comparison *model.Comparison `json:"comparison"`
	}
	if err := json.Unmarshal(data, &wrap); err == nil && wrap.Comparison != nil {
		return wrap.Comparison, nil
	}
	return nil, errors.New("no comparison in the JSON output")
}
