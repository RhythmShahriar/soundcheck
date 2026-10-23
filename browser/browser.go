// Package browser runs headless Chrome for Soundcheck. It uses the Chrome
// or Chromium already installed when there is one, and otherwise
// downloads a pinned Chromium into Rhythm's cache on first use.
package browser

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/utils"

	"github.com/RhythmShahriar/rhythm/kit/config"
)

type Options struct {
	// Bin is a Chrome/Chromium binary to use; empty finds one or downloads.
	Bin string
	// NoDownload fails instead of downloading Chromium (offline, CI).
	NoDownload bool
	// Proxy routes the browser's traffic, e.g. http://127.0.0.1:8080.
	Proxy string
	// Insecure accepts invalid TLS certificates.
	Insecure bool
	// Visible shows the browser window, for debugging flows.
	Visible bool
	// Log receives one-line progress messages, e.g. about a download.
	Log func(string)
}

// Browser is a running Chrome.
type Browser struct {
	*rod.Browser
	launcher *launcher.Launcher
	Bin      string
}

// DownloadDir is where a pinned Chromium is kept.
func DownloadDir() (string, error) {
	d, err := config.CacheDir("soundcheck")
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "browser"), nil
}

// Find returns the browser binary to use: opts.Bin, an installed Chrome,
// or a downloaded Chromium (downloading it unless NoDownload is set).
func Find(opts Options) (bin string, downloaded bool, err error) {
	if opts.Bin != "" {
		if _, err := os.Stat(opts.Bin); err != nil {
			return "", false, fmt.Errorf("browser %s: %w", opts.Bin, err)
		}
		return opts.Bin, false, nil
	}
	if p, ok := launcher.LookPath(); ok {
		return p, false, nil
	}
	dir, err := DownloadDir()
	if err != nil {
		return "", false, err
	}
	b := launcher.NewBrowser()
	b.RootDir = dir
	// rod logs download progress to stdout, where it would mix with --json.
	b.Logger = utils.Log(func(msg ...any) {
		if opts.Log != nil {
			opts.Log(strings.TrimSpace(fmt.Sprint(msg...)))
		}
	})
	if _, err := os.Stat(b.BinPath()); err == nil {
		return b.BinPath(), true, nil
	}
	if opts.NoDownload {
		return "", false, errors.New("no Chrome or Chromium found; install Chrome, pass --browser /path/to/chrome, or allow the one-time Chromium download")
	}
	if opts.Log != nil {
		opts.Log("Downloading Chromium once, into " + dir + " …")
	}
	if err := b.Download(); err != nil {
		return "", false, fmt.Errorf("downloading Chromium: %w", err)
	}
	return b.BinPath(), true, nil
}

// Launch starts headless Chrome.
func Launch(ctx context.Context, opts Options) (*Browser, error) {
	bin, _, err := Find(opts)
	if err != nil {
		return nil, err
	}
	l := launcher.New().Context(ctx).Bin(bin).Headless(!opts.Visible).Leakless(false).
		Set("disable-gpu").Set("hide-scrollbars").Set("mute-audio").
		Set("disable-features", "Translate,MediaRouter,OptimizationHints").
		Set("disable-background-networking").Set("no-first-run").Set("no-default-browser-check").
		// Pages are checked in parallel tabs, and all but one are in the
		// background: without these Chrome throttles their timers and
		// rendering, and the tabs end up taking turns.
		Set("disable-background-timer-throttling").Set("disable-backgrounding-occluded-windows").
		Set("disable-renderer-backgrounding").Set("disable-ipc-flooding-protection")
	if os.Geteuid() == 0 {
		l = l.NoSandbox(true) // Chrome refuses to run as root with its sandbox (containers, CI)
	}
	if opts.Proxy != "" {
		l = l.Proxy(opts.Proxy)
	}
	if opts.Insecure {
		l = l.Set("ignore-certificate-errors")
	}
	u, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	b := rod.New().ControlURL(u).Context(ctx)
	if err := b.Connect(); err != nil {
		l.Kill()
		return nil, fmt.Errorf("connecting to the browser: %w", err)
	}
	return &Browser{Browser: b, launcher: l, Bin: bin}, nil
}

// Session is an isolated browsing context with its own cookies, like a
// private window: anonymous crawling and a logged-in user never share one.
func (b *Browser) Session() (*rod.Browser, error) { return b.Incognito() }

// Close shuts the browser down and removes its temporary profile.
func (b *Browser) Close() {
	_ = b.Browser.Close()
	b.launcher.Kill()
	b.launcher.Cleanup()
}

// Prune deletes downloaded Chromium revisions other than the one this
// version of Soundcheck uses (all of them with all set, when an installed
// Chrome is used instead), and returns what it removed.
func Prune(all bool) ([]string, error) {
	dir, err := DownloadDir()
	if err != nil {
		return nil, err
	}
	b := launcher.NewBrowser()
	b.RootDir = dir
	keep := filepath.Base(b.Dir())
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() || (e.Name() == keep && !all) {
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
