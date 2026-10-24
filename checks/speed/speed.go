// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package speed measures how fast pages load and finds what slows them
// down. Each page gets lab Core Web Vitals checks (LCP, CLS, TBT) plus
// TTFB, weight and request count; a comparison reports pages that got
// measurably slower; and at the end of a run, site-wide suggestions are
// grouped by cause (oversized images, render-blocking files, unused
// CSS/JS, missing compression or caching, heavy third parties, and the
// cost of each WordPress plugin and theme).
//
// Numbers come from one unthrottled desktop load in headless Chrome, so
// they're lab measurements: good for spotting changes and causes, not a
// replacement for field data from real visitors.
package speed

import (
	"fmt"
	"math"
	"sync"

	"github.com/go-rod/rod/lib/proto"
	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Module is the speed module. It keeps per-run state (coverage trackers
// and DOM facts gathered while pages are open), so use one *Module per
// process: New, or &speed.Module{}.
type Module struct {
	mu       sync.Mutex
	tabs     map[proto.TargetTargetID]*tracker
	facts    map[*model.Run]map[string]*facts // by page path
	settings Settings
}

// Settings are the speed: block of the config file (see Configure).
type Settings struct {
	// Slower is how many percent slower a page must get to be reported
	// SLOWER (and faster to be FIXED).
	Slower float64 `yaml:"slower"`
}

// DefaultSettings are the settings without a speed: block.
var DefaultSettings = Settings{Slower: 25}

// New returns a speed module with the default settings.
func New() *Module { return &Module{settings: DefaultSettings} }

// Configure reads the speed: block of the config file
// (check.Configurable).
func (m *Module) Configure(node yaml.Node) error {
	s := DefaultSettings
	if err := config.DecodeBlock("speed", node, &s); err != nil {
		return err
	}
	if math.IsNaN(s.Slower) || s.Slower < 1 || s.Slower > 1000 {
		return &config.SettingError{Key: "speed.slower", Line: config.KeyLine(node, "slower"), Msg: "must be a percentage between 1 and 1000"}
	}
	m.mu.Lock()
	m.settings = s
	m.mu.Unlock()
	return nil
}

// slower is the Slower setting in use.
func (m *Module) slower() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings.Slower <= 0 { // a Module{} that New didn't make
		return DefaultSettings.Slower
	}
	return m.settings.Slower
}

// Name is the module's name in --modules and the config file.
func (*Module) Name() string { return "speed" }

// Thresholds for page issues. LCP, CLS and TBT use Google's Core Web
// Vitals bands ("needs improvement" → warning, "poor" → error; TBT
// stands in for INP in a lab). TTFB uses web.dev's 0.8 s / 1.8 s.
const (
	lcpWarn, lcpError   = 2500.0, 4000.0
	clsWarn, clsError   = 0.1, 0.25
	tbtWarn, tbtError   = 200.0, 600.0
	ttfbWarn, ttfbError = 800.0, 1800.0
	heavyPage           = int64(3 << 20)
	manyRequests        = 100
)

const lab = "one unthrottled desktop load in headless Chrome; real visitors on slower devices see higher numbers"

// CheckPage flags pages whose lab metrics fall outside the good bands.
// Titles name the band, not the value, so the same problem keeps the same
// key across runs while the measured value goes in Detail.
func (*Module) CheckPage(_ *check.Context, p *model.Page) {
	if p.Error != "" || p.Status >= 400 {
		return
	}
	m := p.Metrics
	band := func(v, warn, bad float64, name, unitWarn, unitBad, value, fix string) {
		switch {
		case v > bad:
			check.Page(p, model.Issue{Check: "speed/" + name, Level: model.Error, Title: unitBad,
				Detail: value + " (lab: " + lab + ")", Fix: fix})
		case v > warn:
			check.Page(p, model.Issue{Check: "speed/" + name, Level: model.Warning, Title: unitWarn,
				Detail: value + " (lab: " + lab + ")", Fix: fix})
		}
	}
	band(m.LCP, lcpWarn, lcpError, "lcp", "Largest Contentful Paint over 2.5 s (lab)", "Largest Contentful Paint over 4 s (lab)",
		"LCP "+ms(m.LCP),
		"Make the largest element (usually the hero image or heading) load first: serve it at its displayed size, don't lazy-load it, preload it and cut render-blocking CSS/JS.")
	band(m.CLS, clsWarn, clsError, "cls", "Layout shift (CLS) over 0.1 (lab)", "Layout shift (CLS) over 0.25 (lab)",
		"CLS "+fmt.Sprintf("%.2f", m.CLS),
		"Give images, embeds and ads a width and height (or aspect-ratio) and don't insert banners above content after it has rendered.")
	band(m.TBT, tbtWarn, tbtError, "tbt", "Total Blocking Time over 200 ms (lab)", "Total Blocking Time over 600 ms (lab)",
		"TBT "+ms(m.TBT),
		"Long JavaScript tasks block clicks and typing: defer scripts the page doesn't need at first and remove plugins that add heavy JS to every page.")
	if m.TTFB > ttfbError {
		check.Page(p, model.Issue{Check: "speed/ttfb", Level: model.Warning, Title: "Server response (TTFB) over 1.8 s (lab)",
			Detail: "TTFB " + ms(m.TTFB) + " (lab: " + lab + ")", Fix: ttfbFix})
	} else if m.TTFB > ttfbWarn {
		check.Page(p, model.Issue{Check: "speed/ttfb", Level: model.Notice, Title: "Server response (TTFB) over 0.8 s (lab)",
			Detail: "TTFB " + ms(m.TTFB) + " (lab: " + lab + ")", Fix: ttfbFix})
	}
	if m.Bytes > heavyPage {
		check.Page(p, model.Issue{Check: "speed/weight", Level: model.Warning, Title: "Page weighs over 3 MB",
			Detail: fmt.Sprintf("%s in %d requests", size(m.Bytes), m.Requests),
			Fix:    "Resize and compress images (WebP/AVIF) and drop scripts, fonts and videos the page doesn't need."})
	}
	if m.Requests > manyRequests {
		check.Page(p, model.Issue{Check: "speed/requests", Level: model.Notice, Title: "Page makes over 100 requests",
			Detail: fmt.Sprintf("%d requests", m.Requests),
			Fix:    "Combine or remove small files and stop plugins from loading assets on pages that don't use them."})
	}
}

const ttfbFix = "The server takes long to build the page: turn on page caching (a caching plugin or the host's cache), use a persistent object cache and look for slow plugins or queries (Query Monitor)."

// ms formats milliseconds the way people read load times.
func ms(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.1f s", v/1000)
	}
	return fmt.Sprintf("%.0f ms", v)
}

// size formats a byte count.
func size(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(b)/(1<<10))
	}
	return fmt.Sprintf("%d B", b)
}
