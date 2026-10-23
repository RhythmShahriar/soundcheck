package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"slices"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/cache"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/checks/interact"
	"github.com/RhythmShahriar/soundcheck/checks/speed"
	"github.com/RhythmShahriar/soundcheck/checks/visual"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/flow"
	"github.com/RhythmShahriar/soundcheck/lighthouse"
	"github.com/RhythmShahriar/soundcheck/login"
)

// speedModule keeps per-run state between its hooks; one per process.
var speedModule = speed.New()

// modules returns the enabled modules in config.Modules order, except that
// interact comes last: its clicks and submits change the page, and the
// other modules' Live hooks must measure it as loaded.
func modules(cfg config.Config) []check.Module {
	all := map[string]check.Module{
		"health":   health.Module{},
		"visual":   visual.Module{},
		"interact": interact.New(),
		"cache":    cache.Module{},
		"speed":    speedModule,
	}
	var out []check.Module
	order := append(slices.DeleteFunc(slices.Clone(config.Modules), func(m string) bool { return m == "interact" }), "interact")
	for _, name := range order {
		if m, ok := all[name]; ok && cfg.Has(name) {
			out = append(out, m)
		}
	}
	return out
}

// loginFunc returns how to log in once the site's address is known, and
// who as (masked), when SOUNDCHECK_USER and SOUNDCHECK_PASSWORD are set.
func loginFunc(cfg config.Config, off bool) (func(*url.URL) func(context.Context, *rod.Browser) error, string) {
	user, _, ok := login.Credentials()
	if off || !ok {
		return nil, ""
	}
	return func(base *url.URL) func(context.Context, *rod.Browser) error {
		return login.New(login.Options{Base: base, Config: cfg})
	}, mask(user)
}

// mask shows enough of a name to recognize it: "ad***".
func mask(s string) string {
	r := []rune(s)
	if len(r) <= 2 {
		return "***"
	}
	return string(r[:2]) + "***"
}

// lighthouse hands the site to Unlighthouse for full Lighthouse audits,
// next to the run's own files.
func (s *session) lighthouse(site, dir string) {
	if !lighthouse.Available() {
		fmt.Fprintln(s.stderr, "rhythm soundcheck: --lighthouse needs npx; install Node.js from https://nodejs.org")
		return
	}
	out := filepath.Join(dir, "lighthouse")
	s.view.Step("Lighthouse", "running Unlighthouse (this takes a while)…")
	if err := lighthouse.Run(s.ctx, site, out); err != nil {
		fmt.Fprintln(s.stderr, "rhythm soundcheck: lighthouse:", err)
		return
	}
	s.view.Step("Lighthouse", "results in "+rel(filepath.Join(out, lighthouse.Result)))
}

// cmdFlows handles `flows init`.
func cmdFlows(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "init" {
		fmt.Fprintln(stderr, "rhythm soundcheck: usage: rhythm soundcheck flows init")
		return exitUsage
	}
	cfg, err := config.Load(filepath.Join(config.Dir, "config.yaml"))
	if err != nil {
		fmt.Fprintln(stderr, "rhythm soundcheck:", err)
		return exitUsage
	}
	path, err := flow.Init(cfg.Flows)
	if err != nil {
		fmt.Fprintln(stderr, "rhythm soundcheck:", err)
		return exitError
	}
	fmt.Fprintf(stdout, "Wrote %s. Edit it, then run flows with: --level inspect,click,flows\n", path)
	return exitOK
}
