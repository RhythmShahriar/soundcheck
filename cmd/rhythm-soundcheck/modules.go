package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/health"
	"github.com/RhythmShahriar/soundcheck/config"
)

// modules returns the enabled modules in config.Modules order.
func modules(cfg config.Config) []check.Module {
	all := map[string]check.Module{
		"health": health.Module{},
	}
	var out []check.Module
	for _, name := range config.Modules {
		if m, ok := all[name]; ok && cfg.Has(name) {
			out = append(out, m)
		}
	}
	return out
}

// loginFunc returns how to log in, and who as (masked), when
// SOUNDCHECK_USER and SOUNDCHECK_PASSWORD are set.
func loginFunc(_ config.Config, off bool) (func(context.Context, *rod.Browser) error, string) {
	if off || os.Getenv("SOUNDCHECK_USER") == "" || os.Getenv("SOUNDCHECK_PASSWORD") == "" {
		return nil, ""
	}
	return nil, ""
}

func (s *session) lighthouse(string, string) {
	fmt.Fprintln(s.stderr, "rhythm soundcheck: --lighthouse isn't available in this build")
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
	fmt.Fprintln(stdout, "flows:", cfg.Flows)
	return exitOK
}
