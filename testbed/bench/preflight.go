// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// The preflight proves the accessibility scenarios' pages do what the
// manifest says before any detector is judged on them (qa.md 6: a
// fixture whose script silently does nothing makes a good state pass
// for the wrong reason). For each probe it switches the scenario good
// and broken, loads the page and reads one value from it.
//
// Every plain-PHP page under a11y/ tests itself once on load and puts
// the result in <body data-ok>, "1" when it behaves the way its state
// says. The WordPress pages are checked from here, with a script that
// reads the part of the page the scenario changes.

// probe is one page the preflight checks in both states of a scenario.
type probe struct {
	ID    string // the scenario switched
	Site  string // wp or php
	Path  string
	Login bool // load the page as the testbed's user
	// JS is a function expression run in the page; its result, as JSON,
	// must be Good in the good state and Broken in the broken one. Empty
	// reads <body data-ok> and wants "1" in both.
	JS           string
	Good, Broken string
}

// selfTest is the probe JS for pages that test themselves.
const selfTest = ""

// probes are the accessibility scenarios' pages. A scenario whose
// broken state is the good one (same: true) shows the same page in both
// states; s82 shares s83's page and s85 changes no page.
var probes = []probe{
	// Plain PHP: the a11y/ pages test themselves.
	{ID: "s53", Site: "php", Path: "/a11y/dialog-nofocus.php"},
	{ID: "s57", Site: "php", Path: "/a11y/wishlist.php"},
	{ID: "s65", Site: "php", Path: "/a11y/consent.php"},
	{ID: "s66", Site: "php", Path: "/a11y/zoom.php"},
	{ID: "s67", Site: "php", Path: "/a11y/widget.php"},
	{ID: "s69", Site: "php", Path: "/a11y/promo.php"},
	{ID: "s71", Site: "php", Path: "/a11y/autonav.php"},
	{ID: "s72", Site: "php", Path: "/a11y/offcanvas.php"},
	{ID: "s74", Site: "php", Path: "/a11y/big.php"},
	{ID: "s75", Site: "php", Path: "/a11y/noescape.php"},
	{ID: "s76", Site: "php", Path: "/a11y/reload-trap.php"},
	{ID: "s77", Site: "php", Path: "/a11y/dialog.php"},
	{ID: "s78", Site: "php", Path: "/a11y/modal.php"},
	{ID: "s79", Site: "php", Path: "/a11y/rings.php"},
	{ID: "s81", Site: "php", Path: "/a11y/live.php"},
	{ID: "s83", Site: "php", Path: "/a11y/backlog.php"},
	{ID: "s84", Site: "php", Path: "/a11y/embed.php"},
	{ID: "s87", Site: "php", Path: "/a11y/legacy.php"},
	{ID: "s88", Site: "php", Path: "/a11y/sidefx.php"},
	{ID: "s89", Site: "php", Path: "/a11y/index.php"},
}

// probeResult is what one probe saw in one state.
type probeResult struct {
	Probe probe
	State string
	Got   string
	Err   error
}

// OK reports whether the page was as the state says.
func (r probeResult) OK() bool {
	if r.Err != nil {
		return false
	}
	want := r.Probe.Good
	if r.State == "broken" {
		want = r.Probe.Broken
	}
	if r.Probe.JS == selfTest {
		want = `"1"`
	}
	return r.Got == want
}

func (r probeResult) String() string {
	s := fmt.Sprintf("%s %s %s %s: ", r.Probe.ID, r.State, r.Probe.Site, r.Probe.Path)
	if r.Err != nil {
		return s + r.Err.Error()
	}
	return s + r.Got
}

// preflight runs every probe in both states. login, when set, signs the
// tab in for probes that need it.
func preflight(ctx context.Context, testbed string, sites map[string]string, tab *rod.Page, login func(*rod.Page) error) ([]probeResult, error) {
	var out []probeResult
	loggedIn := false
	for _, p := range probes {
		if p.Login && !loggedIn {
			if err := login(tab); err != nil {
				return out, fmt.Errorf("log in: %w", err)
			}
			loggedIn = true
		}
		for _, state := range []string{"good", "broken"} {
			if err := Toggle(testbed, p.ID, state); err != nil {
				return out, err
			}
			got, err := runProbe(ctx, tab, strings.TrimRight(sites[p.Site], "/")+p.Path, p.JS)
			out = append(out, probeResult{Probe: p, State: state, Got: got, Err: err})
		}
		if err := Toggle(testbed, p.ID, "good"); err != nil {
			return out, err
		}
	}
	return out, nil
}

// runProbe loads url and returns js's result as JSON, or the page's
// data-ok once its self-test has run.
func runProbe(ctx context.Context, tab *rod.Page, url, js string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	page := tab.Context(ctx)
	if err := page.Navigate(url); err != nil {
		return "", err
	}
	if err := page.WaitLoad(); err != nil {
		return "", err
	}
	if js == selfTest {
		// Some self-tests wait (live.php watches its carousel turn).
		for {
			res, err := page.Eval(`() => [document.body.getAttribute('data-ok'), document.body.getAttribute('data-why')]`)
			if err != nil {
				return "", err
			}
			var v [2]*string
			if err := json.Unmarshal([]byte(res.Value.JSON("", "")), &v); err != nil {
				return "", err
			}
			if v[0] != nil {
				if *v[0] != "1" && v[1] != nil {
					return fmt.Sprintf("%q (%s)", *v[0], *v[1]), nil
				}
				return fmt.Sprintf("%q", *v[0]), nil
			}
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("no data-ok after %s: the page's self-test never finished", 30*time.Second)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	res, err := page.Eval(js)
	if err != nil {
		return "", err
	}
	return res.Value.JSON("", ""), nil
}

// wpLogin signs tab in to the WordPress testbed as its own user.
func wpLogin(base string) func(*rod.Page) error {
	return func(tab *rod.Page) error {
		page := tab.Timeout(30 * time.Second)
		if err := page.Navigate(strings.TrimRight(base, "/") + "/wp-login.php"); err != nil {
			return err
		}
		if err := page.WaitLoad(); err != nil {
			return err
		}
		for sel, v := range map[string]string{"#user_login": testbedUser, "#user_pass": testbedPassword} {
			el, err := page.Element(sel)
			if err != nil {
				return err
			}
			if err := el.Input(v); err != nil {
				return err
			}
		}
		btn, err := page.Element("#wp-submit")
		if err != nil {
			return err
		}
		wait := page.WaitNavigation(proto.PageLifecycleEventNameLoad)
		if err := btn.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return err
		}
		wait()
		return nil
	}
}
