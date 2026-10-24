// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package speed

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// tracker follows one tab's JS and CSS coverage from before navigation
// until Live reads it.
type tracker struct {
	mu     sync.Mutex
	sheets map[proto.CSSStyleSheetID]*proto.CSSCSSStyleSheetHeader
	js     bool // JS coverage started
	css    bool // CSS rule tracking started
	stop   func()
}

// facts are what the open page tells us that the network log can't:
// how images are displayed and which files blocked the first render.
type facts struct {
	Viewport float64    `json:"vh"`
	DPR      float64    `json:"dpr"`
	Images   []picture  `json:"images"`
	Blocking []blocking `json:"blocking"`
}

type picture struct {
	Src      string  `json:"src"`
	Natural  int     `json:"nw"`
	NaturalH int     `json:"nh"`
	Width    float64 `json:"w"`
	Height   float64 `json:"h"`
	Top      float64 `json:"top"`
	Loading  string  `json:"loading"`
	Lazy     bool    `json:"lazy"` // a lazy-load library placeholder (data-src…)
}

type blocking struct {
	URL      string  `json:"url"`
	Duration float64 `json:"ms"`
}

// Prepare starts JS block coverage and CSS rule tracking before the page
// loads, so every script and stylesheet the page uses is measured. Binary
// block coverage (no call counts) keeps the overhead on lab timings small;
// it's on in both a snapshot and a compare, so regressions stay fair.
// Coverage is best effort: if Chrome refuses, the unused-code suggestion
// is simply skipped.
func (m *Module) Prepare(ctx context.Context, cc *check.Context, tab *rod.Page) error {
	t := &tracker{sheets: map[proto.CSSStyleSheetID]*proto.CSSCSSStyleSheetHeader{}}
	limit := 6 * time.Duration(max(cc.Config.Timeout, 5)) * time.Second // ends the listener if Live never runs
	ectx, cancel := context.WithTimeout(ctx, limit)
	t.stop = cancel
	wait := tab.Context(ectx).EachEvent(func(e *proto.CSSStyleSheetAdded) {
		if e.Header != nil {
			t.mu.Lock()
			t.sheets[e.Header.StyleSheetID] = e.Header
			t.mu.Unlock()
		}
	})
	go wait()
	page := tab.Context(ctx)
	if (proto.ProfilerEnable{}).Call(page) == nil {
		_, err := proto.ProfilerStartPreciseCoverage{Detailed: true}.Call(page)
		t.js = err == nil
	}
	if (proto.DOMEnable{}).Call(page) == nil && (proto.CSSEnable{}).Call(page) == nil {
		t.css = proto.CSSStartRuleUsageTracking{}.Call(page) == nil
	}
	m.mu.Lock()
	if m.tabs == nil {
		m.tabs = map[proto.TargetTargetID]*tracker{}
	}
	m.tabs[tab.TargetID] = t
	m.mu.Unlock()
	return nil
}

// factsJS reads image sizes and render-blocking files from the open page.
// Chrome marks render-blocking resources in Resource Timing
// (renderBlockingStatus); older browsers fall back to scanning <head> for
// stylesheets and synchronous scripts.
const factsJS = `() => {
  const vh = innerHeight, dpr = devicePixelRatio || 1;
  const images = [...document.images].map(i => {
    const r = i.getBoundingClientRect();
    return {src: i.currentSrc || i.src || '', nw: i.naturalWidth, nh: i.naturalHeight, w: i.clientWidth, h: i.clientHeight,
      top: r.top + scrollY, loading: (i.getAttribute('loading') || '').toLowerCase(),
      lazy: !!(i.dataset.src || i.dataset.lazySrc || i.dataset.srcset || i.dataset.lazy || /\blazy/i.test(i.className))};
  }).filter(i => i.src && !i.src.startsWith('data:')).slice(0, 500);
  const entries = performance.getEntriesByType('resource');
  const time = url => { const e = entries.find(e => e.name === url); return e ? Math.max(0, e.responseEnd - e.startTime) : 0; };
  let blocking;
  if (entries.length && 'renderBlockingStatus' in entries[0]) {
    blocking = entries.filter(e => e.renderBlockingStatus === 'blocking').map(e => ({url: e.name, ms: Math.max(0, e.responseEnd - e.startTime)}));
  } else {
    const css = [...document.head.querySelectorAll('link[rel~="stylesheet" i][href]')].filter(l => !l.disabled && !/^(print|not all)$/i.test(l.media || '')).map(l => l.href);
    const js = [...document.head.querySelectorAll('script[src]')].filter(s => !s.async && !s.defer && s.type !== 'module').map(s => s.src);
    blocking = [...css, ...js].map(url => ({url, ms: time(url)}));
  }
  return {vh, dpr, images, blocking};
}`

// Live reads coverage and DOM facts while the page is open. Unused bytes
// are stored on the page's resources (model.Resource.Unused), scaled to
// the transferred size so they compare with Bytes; image and
// render-blocking facts are kept for CheckSite.
func (m *Module) Live(ctx context.Context, cc *check.Context, tab *rod.Page, p *model.Page) {
	page := tab.Context(ctx)
	m.mu.Lock()
	t := m.tabs[tab.TargetID]
	delete(m.tabs, tab.TargetID)
	m.mu.Unlock()
	if t != nil {
		m.coverage(page, t, p)
	}
	var f facts
	if obj, err := page.Eval(factsJS); err == nil {
		_ = json.Unmarshal([]byte(obj.Value.JSON("", "")), &f)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.facts == nil {
		m.facts = map[*model.Run]map[string]*facts{}
	}
	if m.facts[cc.Run] == nil {
		m.facts[cc.Run] = map[string]*facts{}
	}
	m.facts[cc.Run][p.Path] = &f
}

func (m *Module) coverage(page *rod.Page, t *tracker, p *model.Page) {
	defer t.stop()
	use := map[string]usage{}
	if t.js {
		if res, err := (proto.ProfilerTakePreciseCoverage{}).Call(page); err == nil {
			for u, x := range jsUsage(res.Result) {
				use[u] = x
			}
		}
		_ = proto.ProfilerStopPreciseCoverage{}.Call(page)
		_ = proto.ProfilerDisable{}.Call(page)
	}
	if t.css {
		if res, err := (proto.CSSStopRuleUsageTracking{}).Call(page); err == nil {
			t.mu.Lock()
			for u, x := range cssUsage(t.sheets, res.RuleUsage) {
				use[u] = x
			}
			t.mu.Unlock()
		}
		_ = proto.CSSDisable{}.Call(page)
	}
	applyUnused(p, use)
}

// applyUnused records the unused share of each script and stylesheet on
// the page's resources, as transferred bytes.
func applyUnused(p *model.Page, use map[string]usage) {
	for i := range p.Resources {
		r := &p.Resources[i]
		if r.Type != "Script" && r.Type != "Stylesheet" {
			continue
		}
		if u, ok := use[r.URL]; ok && u.total > 0 {
			r.Unused = int64(float64(r.Bytes) * u.unusedShare())
		}
	}
}

// pageFacts returns what Live saw on each page of run, and forgets it.
func (m *Module) pageFacts(run *model.Run) map[string]*facts {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.facts[run]
	delete(m.facts, run)
	return f
}
