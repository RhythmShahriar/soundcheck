// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package browser runs headless Chrome for Soundcheck. It uses the Chrome
// or Chromium already installed when there is one, and otherwise
// downloads a pinned Chrome for Testing build from Google into Rhythm's
// cache on first use, checking its SHA-256 before unpacking it.
package browser

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/rhythm/kit/config"

	"github.com/RhythmShahriar/soundcheck/internal/retry"
)

// Options choose and configure the browser. The zero value finds an
// installed Chrome or downloads one.
type Options struct {
	// Bin is a Chrome/Chromium binary to use; empty finds one or downloads.
	Bin string
	// NoDownload fails instead of downloading Chrome for Testing (offline, CI).
	NoDownload bool
	// NoSandbox runs Chrome without its sandbox, which it needs as root.
	// SOUNDCHECK_NO_SANDBOX=1 does the same.
	NoSandbox bool
	// Proxy routes the browser's traffic, e.g. http://127.0.0.1:8080.
	Proxy string
	// Insecure accepts invalid TLS certificates.
	Insecure bool
	// Visible shows the browser window, for debugging flows.
	Visible bool
	// Log receives one-line progress messages, e.g. about a download,
	// the browser version and sandbox warnings.
	Log func(string)
}

// Browser is a running Chrome.
type Browser struct {
	*rod.Browser
	proc *process
	Bin  string
	// Version is the running browser's version, e.g. 153.0.8010.52. It
	// hides rod's Browser.Version method, which asks Chrome each time.
	Version string
	// Downloaded is set when Bin is Soundcheck's own Chrome for Testing.
	Downloaded bool
}

// Describe names the browser for the run's log, e.g. "Chrome for
// Testing 153.0.8010.52 (downloaded)".
func (b *Browser) Describe() string {
	if b.Downloaded {
		return "Chrome for Testing " + b.Version + " (downloaded)"
	}
	return "Chrome " + b.Version + " (" + b.Bin + ")"
}

// DownloadDir is where the pinned Chrome for Testing is kept.
func DownloadDir() (string, error) {
	d, err := config.CacheDir("soundcheck")
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "browser"), nil
}

// Find returns the browser binary to use: opts.Bin, an installed Chrome,
// or the pinned Chrome for Testing (downloading it unless NoDownload is
// set). It is FindContext with a context that is never canceled.
func Find(opts Options) (bin string, downloaded bool, err error) {
	return FindContext(context.Background(), opts)
}

// FindContext is Find with a context: canceling ctx stops a Chrome for
// Testing download.
func FindContext(ctx context.Context, opts Options) (bin string, downloaded bool, err error) {
	if opts.Bin != "" {
		if _, err := os.Stat(opts.Bin); err != nil {
			return "", false, fmt.Errorf("browser %s: %w", opts.Bin, err)
		}
		return opts.Bin, false, nil
	}
	if p, ok := lookPath(); ok {
		return p, false, nil
	}
	dir, err := DownloadDir()
	if err != nil {
		return "", false, err
	}
	platform := Platform()
	if pinned.Version != "" {
		if p := binPath(installDir(dir, pinned.Version), platform); fileExists(p) {
			return p, true, nil
		}
	}
	if opts.NoDownload {
		return "", false, errors.New("no Chrome or Chromium found; install Chrome, pass --browser /path/to/chrome, or allow the one-time Chrome for Testing download")
	}
	p, err := install(ctx, pinned, platform, dir, opts.Log)
	if err != nil {
		return "", false, err
	}
	return p, true, nil
}

// lookPath finds an installed Chrome; tests replace it to force a
// download.
var lookPath = launcher.LookPath

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Launch starts headless Chrome. A start or connection that fails is
// tried again, twice at most, in a new profile after a pause: on a busy
// machine Chrome can be too slow to print its DevTools address or to
// answer the first connection.
func Launch(ctx context.Context, opts Options) (*Browser, error) {
	noSandbox, err := sandbox(opts)
	if err != nil {
		return nil, err
	}
	bin, downloaded, err := FindContext(ctx, opts)
	if err != nil {
		return nil, err
	}
	var b *rod.Browser
	var proc *process
	err = retry.Do(ctx, retry.Policy{Tries: launchTries, Pause: launchPause, Again: launchAgain,
		Note: func(_ int, err error) { logf(opts, "Chrome didn't start ("+err.Error()+"); starting it again") },
	}, func() error {
		var err error
		b, proc, err = launchOnce(ctx, opts, bin, noSandbox)
		return err
	})
	if err != nil {
		return nil, err
	}
	br := &Browser{Browser: b, proc: proc, Bin: bin, Downloaded: downloaded}
	if v, err := (proto.BrowserGetVersion{}).Call(b); err == nil {
		br.Version = productVersion(v.Product)
	}
	if opts.Log != nil {
		opts.Log("Browser: " + br.Describe())
	}
	return br, nil
}

// launchTries and launchPause bound Launch's tries: the pause doubles
// after each one.
var (
	launchTries = 3
	launchPause = 500 * time.Millisecond
)

// launchAgain reports whether a failed start may work on another try.
// A sandbox Chrome can't set up fails the same way every time.
func launchAgain(err error) bool {
	return !strings.Contains(strings.ToLower(err.Error()), "sandbox")
}

// launchOnce is one try of Launch: it starts Chrome in a new profile and
// connects to it.
func launchOnce(ctx context.Context, opts Options, bin string, noSandbox bool) (*rod.Browser, *process, error) {
	l := launcher.New().Context(ctx).Headless(!opts.Visible).
		Set("disable-gpu").Set("hide-scrollbars").Set("mute-audio").
		Set("disable-features", "Translate,MediaRouter,OptimizationHints").
		Set("disable-background-networking").Set("no-first-run").Set("no-default-browser-check").
		// Pages are checked in parallel tabs, and all but one are in the
		// background: without these Chrome throttles their timers and
		// rendering, and the tabs end up taking turns.
		Set("disable-background-timer-throttling").Set("disable-backgrounding-occluded-windows").
		Set("disable-renderer-backgrounding").Set("disable-ipc-flooding-protection")
	// rod turns the sandbox off by itself inside containers; only the
	// rule in sandbox decides here.
	l = l.NoSandbox(noSandbox)
	if opts.Proxy != "" {
		l = l.Proxy(opts.Proxy)
	}
	if opts.Insecure {
		l = l.Set("ignore-certificate-errors")
	}
	u, proc, err := start(ctx, l, bin)
	if err != nil {
		return nil, nil, fmt.Errorf("starting %s: %w%s", bin, err, sandboxHint(err, noSandbox))
	}
	b := rod.New().ControlURL(u).Context(ctx)
	if err := b.Connect(); err != nil {
		proc.stop()
		return nil, nil, fmt.Errorf("connecting to the browser: %w", err)
	}
	return b, proc, nil
}

// productVersion turns "HeadlessChrome/153.0.8010.52" into the number.
func productVersion(product string) string {
	if _, v, ok := strings.Cut(product, "/"); ok {
		return v
	}
	return product
}

// geteuid is os.Geteuid; tests replace it to act as root.
var geteuid = os.Geteuid

// NoSandboxEnv turns Chrome's sandbox off when set to 1.
const NoSandboxEnv = "SOUNDCHECK_NO_SANDBOX"

// sandbox reports whether Chrome must run without its sandbox. Chrome
// can't use it as root, which is common in containers and CI images.
// A page that exploits a renderer bug then runs with no barrier around
// it. So root needs an explicit opt-in. Every run without
// the sandbox says so loudly.
func sandbox(opts Options) (off bool, err error) {
	off = opts.NoSandbox || os.Getenv(NoSandboxEnv) == "1"
	if geteuid() == 0 && !off {
		return false, errors.New("running as root, where Chrome can't use its sandbox. Run Soundcheck as a normal user, or pass --no-sandbox (or set " + NoSandboxEnv + "=1) to run Chrome without it (only for sites you trust, e.g. in a throwaway container)")
	}
	if off {
		logf(opts, "WARNING: Chrome is running without its sandbox. A page that exploits a browser bug could take over this machine; only check sites you trust.")
	}
	return off, nil
}

// sandboxHint points at NoSandboxEnv when Chrome failed to start
// because it couldn't set up its sandbox. Inside containers that is
// common for normal users too (Docker's default seccomp profile,
// Ubuntu's AppArmor limits on user namespaces). Chrome's own error
// ("No usable sandbox!") doesn't say how to get past it.
func sandboxHint(err error, noSandbox bool) string {
	if noSandbox || !strings.Contains(strings.ToLower(err.Error()), "sandbox") {
		return ""
	}
	return "\n(pass --no-sandbox, or set " + NoSandboxEnv + "=1, if this container can't give Chrome its sandbox; only for sites you trust)"
}

// logf sends a line to opts.Log, or to stderr when nothing listens, so
// warnings are never lost.
func logf(opts Options, msg string) {
	if opts.Log != nil {
		opts.Log(msg)
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}

// Session is an isolated browsing context with its own cookies, like a
// private window: anonymous crawling and a logged-in user never share one.
// A Chrome that has only just started may not create one at the first
// ask on a busy machine, so it is asked twice more after a pause.
func (b *Browser) Session() (*rod.Browser, error) {
	var sess *rod.Browser
	err := retry.Do(b.GetContext(), retry.Policy{Tries: launchTries, Pause: launchPause}, func() error {
		var err error
		sess, err = b.Incognito()
		return err
	})
	return sess, err
}

// Close shuts the browser down, kills any of its processes still
// running and removes its temporary profile.
func (b *Browser) Close() {
	_ = b.Browser.Close()
	b.proc.stop()
}

// Prune deletes downloaded browsers other than the Chrome for Testing
// version this build uses (all of them with all set, when an installed
// Chrome is used instead), including Chromium revisions older versions
// of Soundcheck downloaded. Hidden entries, such as the .unpack-* folder
// another process is unpacking a download into, are left alone. It
// returns what it removed.
func Prune(all bool) ([]string, error) {
	dir, err := DownloadDir()
	if err != nil {
		return nil, err
	}
	keep := ""
	if pinned.Version != "" {
		keep = filepath.Base(installDir(dir, pinned.Version))
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || (e.Name() == keep && !all) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			return removed, err
		}
		removed = append(removed, p)
	}
	return removed, nil
}

// NewTab opens a blank page in sess, in its own window, that renders as if
// it were in front. Headless Chrome treats every tab but the front one as
// hidden: they don't paint (no FCP, LCP or long tasks), requestAnimation-
// Frame never fires, so animations stand still, and input stalls. Its own
// window plus focus emulation lifts that, so pages checked in parallel
// behave like the one a visitor sees.
//
// Chrome on a macOS runner has been seen to never answer the first
// command sent to a new window, which hung the run. A try that takes
// longer than newTabWait is given up and a second window is opened; the
// stuck one is closed if it ever answers.
func NewTab(sess *rod.Browser) (*rod.Page, error) {
	var err error
	for range 2 {
		var tab *rod.Page
		tab, err = newTabOnce(sess, newTabWait)
		if !errors.Is(err, errTabStuck) {
			return tab, err
		}
	}
	return nil, err
}

// newTabWait is how long one try at opening a tab may take.
var newTabWait = 30 * time.Second

var errTabStuck = errors.New("no new tab from Chrome")

// newTabOnce runs openTab, giving up after wait.
func newTabOnce(sess *rod.Browser, wait time.Duration) (*rod.Page, error) {
	type result struct {
		tab *rod.Page
		err error
	}
	done := make(chan result, 1)
	open := openTab
	go func() {
		tab, err := open(sess)
		done <- result{tab, err}
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.tab, r.err
	case <-timer.C:
		go func() {
			if r := <-done; r.tab != nil {
				_ = r.tab.Close()
			}
		}()
		return nil, fmt.Errorf("%w in %s", errTabStuck, wait)
	}
}

// openTab opens the window and turns on focus emulation. A variable so
// the tests can stand in for a Chrome that doesn't answer.
var openTab = func(sess *rod.Browser) (*rod.Page, error) {
	tab, err := sess.Page(proto.TargetCreateTarget{URL: "about:blank", NewWindow: true})
	if err != nil {
		return nil, err
	}
	if err := (proto.EmulationSetFocusEmulationEnabled{Enabled: true}).Call(tab); err != nil {
		_ = tab.Close()
		return nil, err
	}
	return tab, nil
}
