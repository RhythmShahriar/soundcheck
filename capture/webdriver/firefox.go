// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package webdriver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/RhythmShahriar/soundcheck/capture"
)

// Options for starting a browser.
type Options struct {
	// Bin is the browser (Firefox) or driver (WebKitWebDriver,
	// safaridriver) to run; empty looks for one on PATH.
	Bin string
	// Proxy routes the browser's traffic, e.g. http://127.0.0.1:8080.
	Proxy string
	// Insecure accepts invalid TLS certificates.
	Insecure bool
	// Visible shows the browser window instead of running headless.
	Visible bool
	// Log receives one-line progress notes.
	Log func(string)
}

// firefoxNames are the Firefox binaries looked for on PATH, release
// first.
var firefoxNames = []string{"firefox", "firefox-esr", "firefox-developer-edition", "firefox-nightly"}

// FindFirefox returns the Firefox binary to run: bin when set, or the
// first Firefox on PATH. Soundcheck never downloads one.
func FindFirefox(bin string) (string, error) {
	if bin != "" {
		if _, err := os.Stat(bin); err != nil {
			return "", fmt.Errorf("firefox %s: %w", bin, err)
		}
		return bin, nil
	}
	for _, n := range firefoxNames {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	if p := macApp("Firefox"); p != "" {
		return p, nil
	}
	return "", errors.New("no Firefox found; install it or pass the path to its binary")
}

// macApp is the binary of a macOS app in /Applications, or "".
func macApp(name string) string {
	p := filepath.Join("/Applications", name+".app", "Contents", "MacOS", strings.ToLower(name))
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// listening is the line Firefox prints on stderr once its BiDi server is
// up: "WebDriver BiDi listening on ws://127.0.0.1:41234".
var listening = regexp.MustCompile(`WebDriver BiDi listening on (ws://\S+)`)

// firefoxPrefs make a fresh profile quiet: no first-run pages, updates,
// telemetry or background requests that would show up in a run's
// network log. No pop-up asks to restore a session either.
var firefoxPrefs = map[string]any{
	"app.update.disabledForTesting":              true,
	"browser.shell.checkDefaultBrowser":          false,
	"browser.startup.homepage_override.mstone":   "ignore",
	"browser.startup.page":                       0,
	"browser.newtabpage.enabled":                 false,
	"browser.sessionstore.resume_from_crash":     false,
	"browser.safebrowsing.malware.enabled":       false,
	"browser.safebrowsing.phishing.enabled":      false,
	"datareporting.policy.dataSubmissionEnabled": false,
	"toolkit.telemetry.reportingpolicy.firstRun": false,
	"network.captive-portal-service.enabled":     false,
	"network.connectivity-service.enabled":       false,
	"extensions.update.enabled":                  false,
	"dom.disable_open_during_load":               false,
	// Scrollbars that take no room, like Chrome's --hide-scrollbars: a
	// page is as wide in a screenshot as its viewport.
	"ui.useOverlayScrollbars": 1,
	// Parallel tabs are all in the background. Like Chrome, Firefox
	// slows the timers of a hidden page, which would make the tabs take
	// turns.
	"dom.min_background_timeout_value":           4,
	"dom.timeout.enable_budget_timer_throttling": false,
}

// LaunchFirefox starts Firefox in a throwaway profile with its WebDriver
// BiDi server on a free local port and opens a session.
func LaunchFirefox(ctx context.Context, opts Options) (*Browser, error) {
	bin, err := FindFirefox(opts.Bin)
	if err != nil {
		return nil, err
	}
	profile, err := os.MkdirTemp("", "soundcheck-firefox-")
	if err != nil {
		return nil, err
	}
	prefs := map[string]any{}
	for k, v := range firefoxPrefs {
		prefs[k] = v
	}
	if opts.Proxy != "" {
		if err := proxyPrefs(prefs, opts.Proxy); err != nil {
			_ = os.RemoveAll(profile)
			return nil, err
		}
	}
	if err := writePrefs(filepath.Join(profile, "user.js"), prefs); err != nil {
		_ = os.RemoveAll(profile)
		return nil, err
	}
	args := []string{"--remote-debugging-port=0", "--remote-allow-origins=http://127.0.0.1",
		"--no-remote", "--new-instance", "--profile", profile}
	if !opts.Visible {
		args = append(args, "--headless")
	}
	args = append(args, "about:blank")
	// Not CommandContext: the browser outlives the start-up context and
	// is stopped by Close.
	cmd := exec.Command(bin, args...) //nolint:gosec // the user's own browser binary
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = os.RemoveAll(profile)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(profile)
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	b := &Browser{engine: capture.Firefox, bin: bin, cmd: cmd, profile: profile, logf: opts.Log}
	wsURL, err := waitListening(ctx, stderr, 30*time.Second)
	if err != nil {
		_ = b.Close()
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	conn, err := Dial(ctx, wsURL+"/session")
	if err != nil {
		_ = b.Close()
		return nil, err
	}
	b.conn = conn
	if err := b.newSession(ctx, opts.Insecure); err != nil {
		_ = b.Close()
		return nil, err
	}
	return b, nil
}

// waitListening reads Firefox's stderr until it names its BiDi URL, and
// keeps draining it afterwards so the browser never blocks on a full
// pipe.
func waitListening(ctx context.Context, stderr io.Reader, limit time.Duration) (string, error) {
	found := make(chan string, 1)
	var tail []string
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stderr)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			if !sent {
				if m := listening.FindStringSubmatch(line); m != nil {
					found <- m[1]
					sent = true
					continue
				}
				lines <- line
			}
		}
	}()
	t := time.NewTimer(limit)
	defer t.Stop()
	for {
		select {
		case u := <-found:
			return u, nil
		case line, ok := <-lines:
			if !ok {
				select {
				case u := <-found:
					return u, nil
				default:
				}
				return "", fmt.Errorf("it exited before its WebDriver BiDi server started: %s", lastLines(tail))
			}
			tail = append(tail, line)
		case <-t.C:
			return "", errors.New("its WebDriver BiDi server didn't start in time")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func lastLines(lines []string) string {
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	if len(lines) == 0 {
		return "no output"
	}
	return strings.Join(lines, " / ")
}

// proxyPrefs routes every protocol through proxy, a URL such as
// http://127.0.0.1:8080, the way Chrome's --proxy-server does.
func proxyPrefs(prefs map[string]any, proxy string) error {
	u, err := url.Parse(proxy)
	if err != nil || u.Hostname() == "" || u.Port() == "" {
		return fmt.Errorf("proxy %q: want a URL with a host and port, e.g. http://127.0.0.1:8080", proxy)
	}
	host, port := u.Hostname(), u.Port()
	prefs["network.proxy.type"] = 1
	if strings.HasPrefix(u.Scheme, "socks") {
		prefs["network.proxy.socks"], prefs["network.proxy.socks_port"] = host, atoi(port)
		prefs["network.proxy.socks_remote_dns"] = true
	} else {
		prefs["network.proxy.http"], prefs["network.proxy.http_port"] = host, atoi(port)
		prefs["network.proxy.ssl"], prefs["network.proxy.ssl_port"] = host, atoi(port)
	}
	// Firefox skips the proxy for localhost unless told otherwise.
	prefs["network.proxy.allow_hijacking_localhost"] = true
	prefs["network.proxy.no_proxies_on"] = ""
	return nil
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// writePrefs writes prefs as a profile's user.js.
func writePrefs(path string, prefs map[string]any) error {
	var sb strings.Builder
	for k, v := range prefs {
		switch v := v.(type) {
		case string:
			fmt.Fprintf(&sb, "user_pref(%q, %q);\n", k, v)
		default:
			fmt.Fprintf(&sb, "user_pref(%q, %v);\n", k, v)
		}
	}
	return os.WriteFile(path, []byte(sb.String()), 0o600)
}
