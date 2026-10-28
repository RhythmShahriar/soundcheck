// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/capture"
	"github.com/RhythmShahriar/soundcheck/model"
)

// reflowWidth is the width WCAG 1.4.10 asks content to fit without
// scrolling sideways: 1280 CSS px zoomed to 400 percent.
const reflowWidth = 320

// reflowTimeout bounds the reflow test of one page: a resize, a short
// wait for the layout and one pass over the elements.
var reflowTimeout = 10 * time.Second

// reflowJS waits for the page to lay out at its new width and lists the
// elements that make it scroll sideways, outermost ones only. Content
// that needs two dimensions to be understood is exempt (tables, code,
// media, maps, [data-soundcheck-2d] for a site's own). So is
// anything hidden or clipped by a container that scrolls on its own.
var reflowJS = strings.NewReplacer("EDGE", fmt.Sprint(reflowWidth+1)).Replace(`async () => {
  ` + pathJS + `
  const frame = () => new Promise(r => { requestAnimationFrame(() => r()); setTimeout(r, 100); });
  await frame();
  await frame();
  await new Promise(r => {
    let quiet;
    const done = () => { ro.disconnect(); clearTimeout(quiet); clearTimeout(cap); r(); };
    const ro = new ResizeObserver(() => { clearTimeout(quiet); quiet = setTimeout(done, 50); });
    const cap = setTimeout(done, 300);
    ro.observe(document.documentElement);
    if (document.body) ro.observe(document.body);
    quiet = setTimeout(done, 50);
  });
  const doc = document.scrollingElement || document.documentElement;
  const width = doc.scrollWidth;
  if (width <= doc.clientWidth + 1 || !document.body) return JSON.stringify({width, offenders: []});
  const skip = 'table, pre, code, canvas, video, iframe, svg, [role="img"], [data-soundcheck-2d]';
  const x = window.scrollX;
  const shown = el => el.checkVisibility ? el.checkVisibility({opacityProperty: true, visibilityProperty: true}) :
    getComputedStyle(el).visibility !== 'hidden';
  const clipped = el => {
    for (let a = el.parentElement; a && a !== document.body && a !== document.documentElement; a = a.parentElement) {
      if (getComputedStyle(a).overflowX !== 'visible' && a.getBoundingClientRect().right + x <= EDGE) return true;
    }
    return false;
  };
  const out = [];
  for (const el of document.body.querySelectorAll('*')) {
    const r = el.getBoundingClientRect();
    if (r.right + x <= EDGE || r.width === 0 || r.height === 0) continue;
    if (el.closest(skip) || el.closest('[aria-hidden="true"]') || !shown(el) || clipped(el)) continue;
    out.push({el, right: Math.round(r.right + x)});
    if (out.length >= 1000) break;
  }
  const all = new Set(out.map(o => o.el));
  const top = out.filter(o => {
    for (let a = o.el.parentElement; a; a = a.parentElement) if (all.has(a)) return false;
    return true;
  });
  return JSON.stringify({width, offenders: top.slice(0, 50).map(o => ({path: path(o.el), right: o.right}))});
}`)

// reflowReport is what reflowJS sends back.
type reflowReport struct {
	Width     int        `json:"width"` // the page's scroll width at reflowWidth
	Offenders []offender `json:"offenders"`
}

// offender is an element that sticks out at reflowWidth.
type offender struct {
	Path  [][]Elem `json:"path"`  // see Selector
	Right int      `json:"right"` // how far right it reaches, in CSS px
}

// reflow narrows the page to reflowWidth, finds what makes it scroll
// sideways and puts the desktop viewport back, whatever happens: the
// modules after this one see the page as it was. It returns the
// a11y/reflow issue, or nil when the page reflows.
// reflowReason words why the reflow check didn't finish: its own time
// running out reads "timed out after 10s", not Go's "context deadline
// exceeded".
func reflowReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return "timed out after " + reflowTimeout.String()
	}
	return err.Error()
}

func reflow(ctx context.Context, tab *rod.Page) (*model.Issue, error) {
	ctx, cancel := context.WithTimeout(ctx, reflowTimeout)
	defer cancel()
	page := tab.Context(ctx)
	narrow := proto.EmulationSetDeviceMetricsOverride{Width: reflowWidth, Height: 640, DeviceScaleFactor: 1}
	if err := narrow.Call(page); err != nil {
		return nil, err
	}
	defer func() {
		// The page was at the run's desktop viewport (check.Live), an
		// override too: clearing it would leave the browser window's own
		// size instead. Its own time, as ctx may be over.
		desktop := capture.Viewports["desktop"]
		_ = desktop.Call(tab.Timeout(5 * time.Second))
	}()
	world, err := proto.PageCreateIsolatedWorld{FrameID: page.FrameID, WorldName: "soundcheck-a11y-reflow"}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("can't open an isolated world: %w", err)
	}
	raw, err := eval(page, world.ExecutionContextID, "("+reflowJS+")()", true)
	if err != nil {
		return nil, err
	}
	var rep reflowReport
	if len(raw) > maxResult {
		return nil, fmt.Errorf("reflow result too large")
	}
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		return nil, fmt.Errorf("the reflow result: %w", err)
	}
	return reflowIssue(rep), nil
}

// reflowIssue is the a11y/reflow issue of a page whose report has
// offenders: its source is the one that reaches furthest right.
func reflowIssue(rep reflowReport) *model.Issue {
	var sels []string
	widest, right := "", 0
	for _, o := range rep.Offenders {
		sel := Selector(o.Path)
		if sel == "" || slices.Contains(sels, sel) {
			continue
		}
		sels = append(sels, sel)
		if o.Right > right {
			widest, right = sel, o.Right
		}
	}
	if len(sels) == 0 {
		return nil
	}
	verb := "sticks"
	if len(sels) > 1 {
		verb = "stick"
	}
	i := model.Issue{Check: "a11y/reflow", Level: model.Warning, Title: "Page scrolls sideways at 320 px wide",
		Detail: fmt.Sprintf("At 320 CSS px the page is %d px wide; %s %s out.", rep.Width, strings.Join(sels[:min(3, len(sels))], ", "), verb),
		Source: widest,
		Fix:    "Let the content wrap at 320 px: relative widths and max-width: 100% instead of fixed widths; wide tables or code in their own scrolling box.",
		WCAG:   []string{"1.4.10"}, EN301549: []string{"9.1.4.10"}, Impact: "serious"}
	i.Identify()
	return &i
}
