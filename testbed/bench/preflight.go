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
	// WordPress: the checkout (testbed-shop) and the theme.
	{ID: "s49", Site: "wp", Path: "/checkout/", Good: "false", Broken: "true",
		JS: `() => { const e = new KeyboardEvent('keydown', {key: 'Tab', bubbles: true, cancelable: true}); document.getElementById('tb_coupon').dispatchEvent(e); return e.defaultPrevented; }`},
	{ID: "s50", Site: "wp", Path: "/checkout/", Good: "3", Broken: "0",
		JS: `() => ['tb_billing_name', 'tb_billing_email', 'tb_billing_address'].filter(id => document.getElementById(id).labels.length > 0).length`},
	{ID: "s51", Site: "wp", Path: "/s51-newsletter/", Good: `"news-open"`, Broken: `"body"`,
		JS: `() => { document.getElementById('news-open').click(); const c = document.getElementById('news-close'); c.focus(); c.click(); const a = document.activeElement; return a === document.body ? 'body' : a.id; }`},
	{ID: "s52", Site: "wp", Path: "/s52-modal/", Good: "true", Broken: "false",
		JS: `() => { document.getElementById('grind-open').click(); const last = document.getElementById('grind-close'); last.focus(); const e = new KeyboardEvent('keydown', {key: 'Tab', bubbles: true, cancelable: true}); last.dispatchEvent(e); last.click(); return e.defaultPrevented; }`},
	{ID: "s54", Site: "wp", Path: "/s54-focus/", Good: "false", Broken: "true",
		JS: `() => document.getElementById('tb-no-outline') !== null`},
	{ID: "s55", Site: "wp", Path: "/s55-skip/", Good: "true", Broken: "false",
		JS: `() => document.getElementById(document.querySelector('.skip-link').getAttribute('href').slice(1)) !== null`},
	{ID: "s56", Site: "wp", Path: "/s56-toggle/", Good: `"button"`, Broken: `"div"`,
		JS: `() => { const t = document.querySelector('.menu-toggle'); t.click(); const open = !document.getElementById('browse-panel').hidden; t.click(); return open ? t.localName : 'dead'; }`},
	{ID: "s58", Site: "wp", Path: "/s58-submenu/", Good: "true", Broken: "false",
		JS: `() => { const t = document.querySelector('.has-sub > a'); t.focus(); const shown = getComputedStyle(document.querySelector('.has-sub .sub-menu')).display !== 'none'; t.blur(); return shown; }`},
	{ID: "s59", Site: "wp", Path: "/checkout/", Good: `"name email street-address"`, Broken: `"nope nope nope"`,
		JS: `() => ['tb_billing_name', 'tb_billing_email', 'tb_billing_address'].map(id => document.getElementById(id).getAttribute('autocomplete')).join(' ')`},
	{ID: "s60", Site: "wp", Path: "/checkout/", Good: `"3 labels, 0 placeholders"`, Broken: `"0 labels, 3 placeholders"`,
		JS: `() => { const f = ['tb_billing_name', 'tb_billing_email', 'tb_billing_address'].map(id => document.getElementById(id)); return f.filter(e => e.labels.length).length + ' labels, ' + f.filter(e => e.placeholder).length + ' placeholders'; }`},
	{ID: "s61", Site: "wp", Path: "/checkout/", Good: `"rgb(255, 255, 255)"`, Broken: `"rgb(200, 200, 200)"`,
		JS: `() => getComputedStyle(document.getElementById('place_order')).color`},
	{ID: "s62", Site: "wp", Path: "/s62-lang/", Good: "true", Broken: "false",
		JS: `() => document.documentElement.hasAttribute('lang')`},
	{ID: "s63", Site: "wp", Path: "/s63-gallery/", Good: "3", Broken: "0",
		JS: `() => Array.from(document.querySelectorAll('.tb-gallery img')).filter(i => i.hasAttribute('alt')).length`},
	{ID: "s64", Site: "wp", Path: "/contact/", Good: `"true tb_email"`, Broken: `"null "`,
		JS: `() => { document.getElementById('tb_name').value = 'Sam'; document.getElementById('tb_email').value = 'not an email'; document.getElementById('tb_message').value = 'Hello'; document.getElementById('tb-contact').requestSubmit(); return document.getElementById('tb_email').getAttribute('aria-invalid') + ' ' + document.activeElement.id; }`},
	{ID: "s68", Site: "wp", Path: "/my-account/", Login: true, Good: "1", Broken: "0",
		JS: `() => document.getElementById('acc_phone').labels.length`},
	{ID: "s70", Site: "wp", Path: "/checkout/", Good: `"0 submit"`, Broken: `"-1 button"`,
		JS: `() => { const b = document.getElementById('place_order'); return b.tabIndex + ' ' + b.type; }`},
	{ID: "s73", Site: "wp", Path: "/s73-overlay/", Good: "false", Broken: "true",
		JS: `() => window.testbedOverlayStub === true`},
	{ID: "s80", Site: "wp", Path: "/s80-skip/", Good: `"shown on focus, plain"`, Broken: `"shown on focus, clip"`,
		JS: `() => { const s = document.querySelector('.skip-link'); s.focus(); const r = s.getBoundingClientRect(); const shown = r.top >= 0 && r.height > 10 && getComputedStyle(s).clipPath === 'none'; s.blur(); return (shown ? 'shown on focus, ' : 'hidden, ') + (s.classList.contains('skip-link-clip') ? 'clip' : 'plain'); }`},
	{ID: "s86", Site: "wp", Path: "/checkout/", Good: "false", Broken: "true",
		JS: `() => document.body.textContent.includes('critical error')`},

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
