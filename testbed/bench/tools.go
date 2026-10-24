// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Pinned versions of the third-party tools in the benchmark.
const (
	backstopVersion     = "backstopjs@6.3.25"
	lycheeImage         = "lycheeverse/lychee:0.23.0"
	unlighthouseVersion = "@unlighthouse/cli@0.19.1"
)

// Observation is what a tool saw in one state of a scenario.
type Observation struct {
	Failed  bool               // the tool's own pass/fail (BackstopJS test)
	Errors  []string           // problems it listed (lychee's failing links)
	Scores  map[string]float64 // category scores (Unlighthouse)
	Command string
}

// Tool is a third-party tool the benchmark compares Soundcheck with.
type Tool struct {
	Key, Name string
	About     string // what it checks, for the scoreboard
	HowTo     string // how to make it available
	cmd       []string
	observe   func(ctx context.Context, t *Tool, timeout time.Duration, phase, url, dir string) (*Observation, error)
	judge     func(good, broken *Observation) Result
}

// Observe runs the tool on one state ("good" or "broken").
func (t *Tool) Observe(ctx context.Context, timeout time.Duration, phase, url, dir string) (*Observation, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return t.observe(ctx, t, timeout, phase, url, dir)
}

// Judge decides whether the tool caught the scenario.
func (t *Tool) Judge(good, broken *Observation) Result { return t.judge(good, broken) }

func (t *Tool) run(ctx context.Context, timeout time.Duration, dir string, env []string, args ...string) (string, error) {
	return command(ctx, timeout, dir, env, t.cmd[0], append(slices.Clone(t.cmd[1:]), args...)...)
}

var allTools = []*Tool{
	{
		Key: "backstop", Name: "BackstopJS",
		About: "visual regression: reference screenshots in the good state, test in the broken one (desktop 1366 and phone 390, 0.1% mismatch threshold)",
		HowTo: "`npm i -g " + backstopVersion + "` (or `-tools backstop` to run it through npx)",
		observe: func(ctx context.Context, t *Tool, timeout time.Duration, phase, url, dir string) (*Observation, error) {
			if err := writeBackstopConfig(dir, url); err != nil {
				return nil, err
			}
			cmd := "reference"
			if phase == "broken" {
				cmd = "test"
			}
			out, err := t.run(ctx, timeout, dir, append(os.Environ(), "PUPPETEER_SKIP_DOWNLOAD=1"), cmd, "--config=backstop.json")
			obs := &Observation{Command: cmd}
			if err != nil {
				var exit *exec.ExitError
				if phase == "broken" && errors.As(err, &exit) && strings.Contains(strings.ToLower(out), "mismatch errors found") {
					obs.Failed = true
					return obs, nil
				}
				return nil, err
			}
			return obs, nil
		},
		judge: func(_, broken *Observation) Result {
			if broken.Failed {
				return Result{Outcome: Caught, Found: "screenshots differ"}
			}
			return Result{Outcome: Missed}
		},
	},
	{
		Key: "lychee", Name: "lychee",
		About: "link checker: every link and image on the scenario's page, in both states",
		HowTo: "install `lychee`, or have Docker (the benchmark runs `" + lycheeImage + "`)",
		observe: func(ctx context.Context, t *Tool, timeout time.Duration, _, url, dir string) (*Observation, error) {
			out, err := t.run(ctx, timeout, dir, nil, "--no-progress", "--format", "json", "--max-concurrency", "4", url)
			var report struct {
				ErrorMap map[string][]struct {
					URL    string `json:"url"`
					Status struct {
						Code int `json:"code"`
					} `json:"status"`
				} `json:"error_map"`
				FailMap map[string][]struct {
					URL string `json:"url"`
				} `json:"fail_map"` // older lychee versions
			}
			if jerr := json.Unmarshal([]byte(jsonPart(out)), &report); jerr != nil {
				if err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("lychee output: %w", jerr)
			}
			obs := &Observation{}
			for _, list := range report.ErrorMap {
				for _, e := range list {
					obs.Errors = append(obs.Errors, fmt.Sprintf("%s (%d)", e.URL, e.Status.Code))
				}
			}
			for _, list := range report.FailMap {
				for _, e := range list {
					obs.Errors = append(obs.Errors, e.URL)
				}
			}
			return obs, nil
		},
		judge: func(good, broken *Observation) Result {
			var added []string
			for _, e := range broken.Errors {
				if !slices.Contains(good.Errors, e) {
					added = append(added, e)
				}
			}
			if len(added) > 0 {
				return Result{Outcome: Caught, Found: "broken link: " + strings.Join(added, ", ")}
			}
			return Result{Outcome: Missed}
		},
	},
	{
		Key: "unlighthouse", Name: "Unlighthouse",
		About: "Lighthouse on the scenario's page in both states (desktop); caught when a category score drops by 0.03 or more (performance: 0.10)",
		HowTo: "`npm i -g " + unlighthouseVersion + "` (or `-tools unlighthouse` to run it through npx)",
		observe: func(ctx context.Context, t *Tool, timeout time.Duration, phase, raw, dir string) (*Observation, error) {
			u, err := url.Parse(raw)
			if err != nil {
				return nil, err
			}
			out := filepath.Join(dir, phase)
			site := u.Scheme + "://" + u.Host
			if _, err := t.run(ctx, timeout, dir, nil, "--site", site, "--urls", u.Path, "--desktop", "--no-cache",
				"--reporter", "jsonExpanded", "--output-path", out); err != nil {
				return nil, err
			}
			data, err := os.ReadFile(filepath.Join(out, "ci-result.json")) //nolint:gosec // the tool's output
			if err != nil {
				return nil, err
			}
			var report struct {
				Summary struct {
					Categories map[string]struct {
						Score float64 `json:"score"`
					} `json:"categories"`
				} `json:"summary"`
			}
			if err := json.Unmarshal(data, &report); err != nil {
				return nil, err
			}
			obs := &Observation{Scores: map[string]float64{}}
			for k, v := range report.Summary.Categories {
				obs.Scores[k] = v.Score
			}
			return obs, nil
		},
		judge: func(good, broken *Observation) Result {
			var drops []string
			for _, k := range slices.Sorted(maps.Keys(good.Scores)) {
				limit := 0.03
				if k == "performance" {
					limit = 0.10
				}
				if b, ok := broken.Scores[k]; ok && good.Scores[k]-b >= limit-1e-9 {
					drops = append(drops, fmt.Sprintf("%s %.0f→%.0f", k, good.Scores[k]*100, b*100))
				}
			}
			if len(drops) > 0 {
				return Result{Outcome: Caught, Found: strings.Join(drops, ", ")}
			}
			return Result{Outcome: Missed}
		},
	},
}

// pickTools resolves -tools: "auto" uses tools already installed (lychee
// also through a local Docker image), "all" or a list also fetches them
// with npx / docker pull, "none" runs none.
func pickTools(spec string) ([]*Tool, error) {
	explicit := map[string]bool{}
	switch spec {
	case "none", "":
		return nil, nil
	case "auto":
	case "all":
		for _, t := range allTools {
			explicit[t.Key] = true
		}
	default:
		for _, k := range strings.Split(spec, ",") {
			if !slices.ContainsFunc(allTools, func(t *Tool) bool { return t.Key == k }) {
				return nil, fmt.Errorf("-tools: unknown tool %q (backstop, lychee, unlighthouse)", k)
			}
			explicit[k] = true
		}
	}
	var out []*Tool
	for _, t := range allTools {
		if cmd := findTool(t.Key, explicit[t.Key], spec == "auto"); cmd != nil {
			t.cmd = cmd
			out = append(out, t)
		}
	}
	return out, nil
}

// findTool returns the command that runs a tool, or nil when it isn't
// available (or wasn't asked for).
func findTool(key string, explicit, auto bool) []string {
	if !explicit && !auto {
		return nil
	}
	installed := map[string]string{"backstop": "backstop", "lychee": "lychee", "unlighthouse": "unlighthouse-ci"}[key]
	if p, err := exec.LookPath(installed); err == nil {
		return []string{p}
	}
	if key == "lychee" {
		if _, err := exec.LookPath("docker"); err == nil {
			local := exec.Command("docker", "image", "inspect", lycheeImage).Run() == nil //nolint:noctx // quick local check
			if explicit || local {
				return []string{"docker", "run", "--rm", "--network", "host", lycheeImage}
			}
		}
		return nil
	}
	if !explicit {
		return nil
	}
	if _, err := exec.LookPath("npx"); err != nil {
		return nil
	}
	if key == "backstop" {
		return []string{"npx", "--yes", backstopVersion}
	}
	return []string{"npx", "--yes", "-p", unlighthouseVersion, "unlighthouse-ci"}
}

// writeBackstopConfig writes backstop.json for one page.
func writeBackstopConfig(dir, url string) error {
	engine := map[string]any{"args": []string{"--no-sandbox"}}
	for _, p := range []string{"/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser"} {
		if _, err := os.Stat(p); err == nil {
			engine["executablePath"] = p
			break
		}
	}
	cfg := map[string]any{
		"id": "soundcheck-testbed",
		"viewports": []map[string]any{
			{"label": "desktop", "width": 1366, "height": 900},
			{"label": "phone", "width": 390, "height": 844},
		},
		"scenarios": []map[string]any{{"label": "page", "url": url, "delay": 500, "misMatchThreshold": 0.1}},
		"paths": map[string]string{
			"bitmaps_reference": "ref", "bitmaps_test": "test", "html_report": "html", "ci_report": "ci", "engine_scripts": "scripts",
		},
		"engine": "puppeteer", "engineOptions": engine, "report": []string{"CI"}, "asyncCaptureLimit": 2, "asyncCompareLimit": 4,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "backstop.json"), data, 0o600)
}

// jsonPart drops log lines printed around a JSON report.
func jsonPart(s string) string {
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return s
	}
	return s[i : j+1]
}
