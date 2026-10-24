// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/store"
)

// configTemplate documents every setting with its default. It must stay
// loadable: TestConfigTemplateLoads checks it.
const configTemplate = `# Soundcheck settings. Everything here is optional: delete what you don't
# change. Reference: https://github.com/RhythmShahriar/rhythm/wiki/Soundcheck-Configuration
# Commit this file (and flows/) to share them with your team; snapshots and
# runs are kept out of git automatically.

site: ""                # checked by snapshot and run when no URL is given, e.g. staging.example.com

pages:
  limit: 300            # most pages to check
  per_group: 3          # pages checked per URL pattern (/blog/{slug}/ …); -1 checks all
  include: []           # only pages matching these globs, e.g. ["/shop/*"]
  exclude: []           # skip pages matching, e.g. ["/tag/*", "/author/*"]
  ignore_robots: false  # crawl pages robots.txt disallows (your own site)
  extra: []             # always check these, even if no link points to them, e.g. ["/checkout/"]

viewports: [desktop, mobile]
modules: [health, visual, interact, cache, speed]  # also a11y, seo; echo is added when Echo is on

visual:
  threshold: 0.5        # % of a page's pixels that must change to report it
  ignore: []            # CSS selectors hidden before screenshots, e.g. [".ad", "#live-chat"]

interactions:
  levels: [inspect, click]  # submit (which also clicks) sends test data through forms; flows runs flows/ too
  max_clicks: 15            # per page
  allow: []                 # selectors to click even if they look destructive
  forms: []                 # forms that may be submitted on production sites, by CSS selector

# Live sites. Form submits are refused on these unless the form is in
# interactions.forms. A host covers http and https, www and any port;
# *.example.com covers subdomains, example.com/shop only that path.
production: []          # e.g. ["example.com", "*.example.com"]

login:
  url: ""               # login page on the site; default /wp-login.php on WordPress
  flow: ""              # or a flow file that logs in, for other sites
# Credentials come only from SOUNDCHECK_USER and SOUNDCHECK_PASSWORD.

flows: .soundcheck/flows  # folder of YAML flows (the flows init command writes one)

speed:
  slower: 25            # % slower before a page is reported SLOWER

# Echo (rhythm echo up) records the emails, SMS and webhooks each form submit
# and flow sends, so comparisons catch lost or changed messages. Off while url
# is empty; --echo or SOUNDCHECK_ECHO turn it on too.
echo:
  url: ""               # e.g. http://127.0.0.1:8025
  token_env: RHYTHM_ECHO_TOKEN  # variable with Echo's token, if it has one (RHYTHM_ECHO_*)
  quiet: 1500ms         # after a submit, wait until nothing new arrives for this long
  timeout: 10s          # but no longer than this

rate: 5                 # page loads per second per site, 0.01 to 1000
tabs: 8                 # pages checked at once; politeness comes from rate
timeout: 30s            # per page: 30s, 1m or a number of seconds

# Extra request headers, each read from an environment variable so no
# secret is committed: {X-Staging-Auth: SOUNDCHECK_STAGING_AUTH}. Only
# SOUNDCHECK_* variables, unless --allow-env names another.
headers: {}

# Exit 1 at or above this, when --fail-on isn't given: broke, new, changed
# or slower for compare; error, warning or notice for run and snapshot;
# none never fails.
fail_on: ""

# Changes that are known and shouldn't count: listed as ignored, never
# failing a build. Every field given must match (check and title are globs
# with *); until is the last day the rule applies. review adds rules here.
ignore: []              # e.g. [{check: "visual/page", pages: ["/team/*"], until: "2026-12-31", reason: "weekly photos"}]

# Programs that check every page: each gets the page as JSON on stdin and
# answers {"issues": [...]}. They run only with --allow-exec.
exec: []                # e.g. [{name: laravel-log, run: ["./bin/check-log"], timeout: 10s}]
`

// cmdInit writes .soundcheck/config.yaml.
func cmdInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	force := flags.Bool("force", false, "replace an existing config.yaml")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	path := filepath.Join(config.Dir, "config.yaml")
	if _, err := os.Stat(path); err == nil && !*force {
		fmt.Fprintf(stderr, "%s %s already exists (--force replaces it)\n", prefix(), path)
		return exitUsage
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	if err := os.MkdirAll(config.Dir, 0o750); err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	if err := os.WriteFile(path, []byte(configTemplate), 0o644); err != nil { //nolint:gosec // a project file meant to be committed
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	store.Store{Root: config.Dir}.ExcludeFromGit()
	fmt.Fprintf(stdout, "Wrote %s. Every setting is optional; edit what you need.\n", path)
	return exitOK
}
