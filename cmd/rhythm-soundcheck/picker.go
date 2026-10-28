// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/RhythmShahriar/rhythm/kit/httpx"
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
			last = fmt.Sprintf("%q of %s, %s", r.Name, report.ShownTarget(r), report.Ago(r.Started))
		}
	}

	action := "snapshot"
	if last != "" {
		action = "compare"
	}
	opts := pickOptions(last)
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
					_, _, err := httpx.ParseTarget(strings.TrimSpace(s))
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
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	return run(pickArgs(action, site, output), stdout, stderr)
}

// pickEAA is the picker's action for an --eaa snapshot.
const pickEAA = "eaa"

// pickOptions are the picker's actions: comparing with the latest
// snapshot (described by last) when there is one, then the others.
func pickOptions(last string) []huh.Option[string] {
	var opts []huh.Option[string]
	if last != "" {
		opts = append(opts, huh.NewOption("Compare with the latest snapshot ("+last+")", "compare"))
	}
	return append(opts,
		huh.NewOption("Take a snapshot: record how the site works now", "snapshot"),
		huh.NewOption("Check once, without comparing", "run"),
		huh.NewOption("Check accessibility (EAA evidence)", pickEAA),
	)
}

// pickArgs turns the picker's answers into a command line. The
// accessibility check is a snapshot with --eaa, so a later compare has
// a baseline.
func pickArgs(action, site, output string) []string {
	args := []string{action}
	if action == pickEAA {
		args = []string{"snapshot"}
	}
	if action != "compare" {
		args = append(args, strings.TrimSpace(site))
	}
	if action == pickEAA {
		args = append(args, "--eaa")
	}
	switch output {
	case "open":
		args = append(args, "--open")
	case "md":
		args = append(args, "-o", "soundcheck.md")
	}
	return args
}
