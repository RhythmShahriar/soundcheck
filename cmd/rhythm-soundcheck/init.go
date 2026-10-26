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
	"strings"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/flow"
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
modules: [health, visual, interact, cache, speed]  # also a11y, keyboard, seo; echo is added when Echo is on

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

// eaaModulesLine replaces the modules: line of configTemplate under
// init --eaa: a list in the file would keep its modules and only gain
// a11y and keyboard, so it is left as a comment.
const eaaModulesLine = `# modules: [health, visual, interact, cache, speed]  # with eaa: true the modules are health, a11y, keyboard; a list here gains a11y and keyboard`

// eaaTemplate is what init --eaa adds after site: eaa: true and the
// a11y, keyboard and statement blocks, commented out with their defaults
// so the file loads before they are filled in. The statement's contact
// details are published, so the comment asks for a role address.
const eaaTemplate = `
eaa: true               # same as --eaa on every snapshot, run and compare (rhythm soundcheck help eaa)

# The a11y module (axe-core).
# a11y:
#   wcag: "2.1"           # 2.0, 2.1 or 2.2 (--wcag)
#   max_elements: 20      # per rule per page, 1-200
#   iframes: false        # true audits same-origin iframes too

# The keyboard walk (rhythm soundcheck help keyboard).
# keyboard:
#   max_tabs: 150         # Tab presses per page before the walk stops, 10-2000
#   widgets: 10           # menus, dialogs, disclosures tested per page, 0-200
#   trap_after: 4         # presses on the same element or region that count as a trap, 3-20
#   skip_link: expected   # expected (missing is a notice), required (warning) or off
#   pages: ["/cart*", "/checkout*", "/my-account*", "/contact*"]  # widget tests run here and on the first page of each URL group
#   ignore: ["#cookie-banner"]  # selectors the walk steps over

# The accessibility statement draft (rhythm soundcheck statement). owner,
# website and contact.email are required. Everything here is published in
# the statement: give a role address such as accessibility@ rather than a
# person's own email or phone.
# statement:
#   owner: "[your organization]"
#   website: "https://[your public address]"
#   contact:
#     email: "accessibility@[your domain]"
#     phone: ""
#     address: ""
#   response_days: 10
#   country: ""           # ISO 3166 alpha-2, e.g. DE
#   enforcement: ""       # the authority visitors can turn to
#   scope: []             # e.g. [/, /shop/*, /checkout/*]
#   service: ""           # what the service is, for --layout service
#   known_issues: []      # e.g. [{criterion: 1.2.2, text: "Product videos have no captions yet.", until: 2027-03-31}]
#   manual: []            # what a person checked: [{criterion: 1.4.11, result: no failures found, by: Jane Doe, date: 2026-10-20}]
#   list_private_paths: false  # list paths behind login in the draft (default: count only)
`

// eaaConfig is the config template init --eaa writes.
func eaaConfig() string {
	t := strings.Replace(configTemplate, "modules: [health, visual, interact, cache, speed]  # also a11y, keyboard, seo; echo is added when Echo is on", eaaModulesLine, 1)
	site := strings.Index(t, "\n\npages:")
	return t[:site+1] + eaaTemplate + t[site+1:]
}

// keyboardTemplates are the key flow templates, a variable so the tests
// can stand in for the flow package's.
var keyboardTemplates = flow.KeyboardTemplates

// keyCheckout is the key flow init --eaa writes.
const keyCheckout = "keyboard-checkout.yaml"

// writeKeyFlows writes the named key flow templates into dir, refusing
// to replace a file that is there. It returns the paths written and,
// for a refusal, the paths it left alone.
func writeKeyFlows(dir string, names []string) (written, kept []string, err error) {
	tpl := keyboardTemplates()
	for _, n := range names {
		if _, ok := tpl[n]; !ok {
			return nil, nil, fmt.Errorf("this build has no key flow template %s", n)
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, nil, err
	}
	for _, n := range names {
		path := filepath.Join(dir, n)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // a flow meant to be committed, in the project's flows folder
		if errors.Is(err, fs.ErrExist) {
			kept = append(kept, path)
			continue
		}
		if err != nil {
			return written, kept, err
		}
		_, err = f.Write(tpl[n])
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return written, kept, err
		}
		written = append(written, path)
	}
	return written, kept, nil
}

// cmdInit writes .soundcheck/config.yaml, with EAA mode on for --eaa.
func cmdInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	force := flags.Bool("force", false, "")
	eaa := flags.Bool("eaa", false, "")
	pos, code, ok := subFlags(flags, args, initUsage, stdout, stderr)
	if !ok {
		return code
	}
	if len(pos) > 0 {
		fmt.Fprintln(stderr, prefix(), "usage:", cmdName(), "init [--eaa] [--force]")
		return exitUsage
	}
	template := configTemplate
	if *eaa {
		template = eaaConfig()
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
	if err := os.WriteFile(path, []byte(template), 0o644); err != nil { //nolint:gosec // a project file meant to be committed
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	store.Store{Root: config.Dir}.ExcludeFromGit()
	fmt.Fprintf(stdout, "Wrote %s. Every setting is optional; edit what you need.\n", path)
	if !*eaa {
		return exitOK
	}
	// The flows folder init writes into is the template's default, which
	// the file just written names.
	written, kept, err := writeKeyFlows(filepath.Join(config.Dir, "flows"), []string{keyCheckout})
	for _, p := range written {
		fmt.Fprintf(stdout, "Wrote %s, a key flow to edit; it runs with --level flows.\n", p)
	}
	for _, p := range kept {
		fmt.Fprintf(stdout, "Kept %s, which is already there.\n", p)
	}
	if err != nil {
		fmt.Fprintln(stderr, prefix(), named(err.Error()))
		return exitError
	}
	fmt.Fprintf(stdout, "Fill in statement: before you run %s statement.\n", cmdName())
	return exitOK
}
