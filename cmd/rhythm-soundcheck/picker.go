// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/RhythmShahriar/rhythm/kit/ratelimit"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/report"
	"github.com/RhythmShahriar/soundcheck/store"
)

// pick asks what to do, then runs it.
func pick(stdout, stderr io.Writer) int {
	st := store.Store{Root: config.Dir}
	var last string
	if dir, err := st.Snapshot(""); err == nil {
		if r, err := store.LoadRun(dir); err == nil {
			last = fmt.Sprintf("%q of %s, %s", r.Name, r.Target, report.Ago(r.Started))
		}
	}

	action := "snapshot"
	var opts []huh.Option[string]
	if last != "" {
		action = "compare"
		opts = append(opts, huh.NewOption("Compare with the latest snapshot ("+last+")", "compare"))
	}
	opts = append(opts,
		huh.NewOption("Take a snapshot: record how the site works now", "snapshot"),
		huh.NewOption("Check once, without comparing", "run"),
	)
	var site, output string
	output = "open"
	form := huh.NewForm(
		huh.NewGroup(huh.NewSelect[string]().Title("What should Soundcheck do?").Options(opts...).Value(&action)),
		huh.NewGroup(
			huh.NewInput().
				Title("Which site?").
				Description("Only check sites you own or are authorised to test.").
				Placeholder("staging.example.com").
				Value(&site).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return errors.New("enter a URL or hostname")
					}
					_, _, err := ratelimit.ParseTarget(strings.TrimSpace(s))
					return err
				}),
		).WithHideFunc(func() bool { return action == "compare" }),
		huh.NewGroup(huh.NewSelect[string]().Title("Report").Options(
			huh.NewOption("Terminal, then open the HTML report", "open"),
			huh.NewOption("Terminal only", "terminal"),
			huh.NewOption("Markdown file (soundcheck.md)", "md"),
		).Value(&output)),
	).WithTheme(huh.ThemeCharm())
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return exitCanceled
		}
		fmt.Fprintln(stderr, "rhythm soundcheck:", err)
		return exitError
	}
	args := []string{action}
	if action != "compare" {
		args = append(args, strings.TrimSpace(site))
	}
	switch output {
	case "open":
		args = append(args, "--open")
	case "md":
		args = append(args, "-o", "soundcheck.md")
	}
	return run(args, stdout, stderr)
}
