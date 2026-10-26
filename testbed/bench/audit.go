// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"github.com/RhythmShahriar/soundcheck/internal/focus"
)

// The testbed's own accessibility audit. It holds the good states to the
// bar --eaa sets (step zero in testbed/README.md) with a small, separate
// implementation: axe-core with the WCAG 2.1 A and AA tags, a placeholder
// label check, reflow at 320 px and a Tab walk that looks for a skip link
// to #main, a trap, hidden stops and missing focus rings. It is no
// substitute for the a11y and keyboard modules: it exists so the
// testbed's pages are known to be clean on their own terms: a
// scenario's broken state is what a detector reports, not a flaw the
// good state already had.

// Finding is one problem the audit saw on a page.
type Finding struct {
	Page     string `json:"page"`
	Check    string `json:"check"`
	Selector string `json:"selector,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

func (f Finding) String() string {
	s := f.Page + " " + f.Check
	if f.Selector != "" {
		s += " " + f.Selector
	}
	if f.Detail != "" {
		s += ": " + f.Detail
	}
	return s
}

// auditTags are the axe tags --eaa runs for WCAG 2.1 (PRD 8.1).
var auditTags = []string{"wcag2a", "wcag2aa", "wcag21a", "wcag21aa"}

// auditor audits pages in one Chrome tab.
type auditor struct {
	axe string // axe-core's source
	// walk is how many Tab presses the walk makes at most before it
	// calls the page a trap.
	walk int
}

// newAuditor loads the vendored axe-core from the module the testbed
// sits in.
func newAuditor(testbed string) (*auditor, error) {
	src, err := os.ReadFile(filepath.Join(testbed, "..", "checks", "a11y", "vendor", "axe.min.js")) //nolint:gosec // the module's own vendored file
	if err != nil {
		return nil, fmt.Errorf("axe-core: %w", err)
	}
	return &auditor{axe: string(src), walk: 200}, nil
}

// Audit loads url in tab and returns what it finds on it. path names the
// page in the findings.
func (a *auditor) Audit(ctx context.Context, tab *rod.Page, url, path string) ([]Finding, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	page := tab.Context(ctx)
	if err := load(page, url); err != nil {
		return nil, err
	}
	var out []Finding
	axe, err := a.axeRun(page, path)
	if err != nil {
		return nil, err
	}
	out = append(out, axe...)
	labels, err := placeholderLabels(page, path)
	if err != nil {
		return nil, err
	}
	out = append(out, labels...)
	reflow, err := reflowAt320(page, path)
	if err != nil {
		return nil, err
	}
	out = append(out, reflow...)
	if err := load(page, url); err != nil { // a fresh page for the walk
		return nil, err
	}
	walk, err := a.tabWalk(ctx, page, path)
	if err != nil {
		return nil, err
	}
	return append(out, walk...), nil
}

// load navigates and waits for the load event and a quiet moment.
func load(page *rod.Page, url string) error {
	if err := page.Navigate(url); err != nil {
		return err
	}
	if err := page.WaitLoad(); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond) // scripts that run after load (fixtures' self-tests)
	return nil
}

// isolated runs js in a fresh isolated world of page and returns its
// string result.
func isolated(page *rod.Page, js string) (string, error) {
	world, err := proto.PageCreateIsolatedWorld{FrameID: page.FrameID, WorldName: "testbed-audit"}.Call(page)
	if err != nil {
		return "", err
	}
	res, err := proto.RuntimeEvaluate{Expression: js, ContextID: world.ExecutionContextID, AwaitPromise: true, ReturnByValue: true}.Call(page)
	if err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		msg := res.ExceptionDetails.Text
		if e := res.ExceptionDetails.Exception; e != nil && e.Description != "" {
			msg, _, _ = strings.Cut(e.Description, "\n")
		}
		return "", errors.New(msg)
	}
	if res.Result == nil {
		return "", nil
	}
	return res.Result.Value.Str(), nil
}

// auditAxeJS runs axe with auditTags; %s is the tag list as JSON.
const auditAxeJS = `(async () => {
  const r = await axe.run(document, {runOnly: {type: 'tag', values: %s}, resultTypes: ['violations'], iframes: false});
  return JSON.stringify(r.violations.flatMap(v => v.nodes.map(n => ({check: 'a11y/' + v.id, selector: n.target.join(' >>> '), detail: v.help}))));
})()`

func (a *auditor) axeRun(page *rod.Page, path string) ([]Finding, error) {
	world, err := proto.PageCreateIsolatedWorld{FrameID: page.FrameID, WorldName: "testbed-axe"}.Call(page)
	if err != nil {
		return nil, err
	}
	if _, err := (proto.RuntimeEvaluate{Expression: a.axe, ContextID: world.ExecutionContextID}).Call(page); err != nil {
		return nil, fmt.Errorf("load axe-core: %w", err)
	}
	tags, _ := json.Marshal(auditTags)
	res, err := proto.RuntimeEvaluate{Expression: fmt.Sprintf(auditAxeJS, tags), ContextID: world.ExecutionContextID, AwaitPromise: true, ReturnByValue: true}.Call(page)
	if err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		return nil, fmt.Errorf("axe-core: %s", res.ExceptionDetails.Text)
	}
	return findings(path, res.Result.Value.Str())
}

func findings(path, raw string) ([]Finding, error) {
	var out []Finding
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Page = path
	}
	return out, nil
}

// placeholderLabelJS lists visible fields whose only name is their
// placeholder (PRD 8.1 a11y/placeholder-label, with its exceptions).
const placeholderLabelJS = `(() => {
  const skip = ['hidden', 'submit', 'button', 'image', 'reset', 'checkbox', 'radio'];
  const out = [];
  for (const el of document.querySelectorAll('input, textarea, select')) {
    if (el.localName === 'input' && skip.includes((el.getAttribute('type') || 'text').toLowerCase())) continue;
    const r = el.getBoundingClientRect();
    if (!r.width || !r.height || getComputedStyle(el).visibility === 'hidden') continue;
    if (el.type === 'search' || el.closest('[role=search]')) continue;
    const by = (el.getAttribute('aria-labelledby') || '').split(/\s+/).filter(Boolean).map(id => (document.getElementById(id) || {}).textContent || '').join('');
    const name = [...(el.labels || [])].map(l => l.textContent).join('') + (el.getAttribute('aria-label') || '') + by + (el.getAttribute('title') || '');
    if (name.trim() === '' && (el.getAttribute('placeholder') || '').trim() !== '') out.push({check: 'a11y/placeholder-label', selector: el.id ? '#' + el.id : el.localName + '[name="' + el.name + '"]'});
  }
  return JSON.stringify(out);
})()`

func placeholderLabels(page *rod.Page, path string) ([]Finding, error) {
	raw, err := isolated(page, placeholderLabelJS)
	if err != nil {
		return nil, err
	}
	return findings(path, raw)
}

// reflowJS reports sideways scrolling and the widest element causing it.
const reflowJS = `(() => {
  const d = document.documentElement;
  if (d.scrollWidth <= d.clientWidth + 1) return '[]';
  let widest = null, w = 0;
  for (const el of document.body.querySelectorAll('*')) {
    const r = el.getBoundingClientRect();
    if (r.right > d.clientWidth + 1 && r.width > w && !el.closest('table, pre, code, canvas, video, iframe, svg, [role=img]')) { widest = el; w = r.width; }
  }
  if (!widest) return '[]';
  return JSON.stringify([{check: 'a11y/reflow', selector: widest.id ? '#' + widest.id : widest.localName + (widest.className ? '.' + String(widest.className).trim().split(/\s+/).join('.') : ''), detail: 'scrollWidth ' + d.scrollWidth + ' at 320 px'}]);
})()`

// reflowAt320 checks the page at a 320 CSS px viewport and restores the
// viewport afterwards.
func reflowAt320(page *rod.Page, path string) ([]Finding, error) {
	if err := (proto.EmulationSetDeviceMetricsOverride{Width: 320, Height: 640, DeviceScaleFactor: 1}).Call(page); err != nil {
		return nil, err
	}
	defer func() { _ = proto.EmulationClearDeviceMetricsOverride{}.Call(page) }()
	time.Sleep(200 * time.Millisecond)
	raw, err := isolated(page, reflowJS)
	if err != nil {
		return nil, err
	}
	return findings(path, raw)
}

// hideCaretJS keeps a blinking caret out of the focus ring screenshots.
const hideCaretJS = `(() => {
  const s = document.createElement('style');
  s.textContent = '*{caret-color:transparent !important}';
  document.head.appendChild(s);
  return '';
})()`

// focusedBoxJS is the deep focused element's box in page coordinates,
// with 8 px around it, plus whether it can be seen.
const focusedBoxJS = `(() => {
  let el = document.activeElement;
  while (el && el.shadowRoot && el.shadowRoot.activeElement) el = el.shadowRoot.activeElement;
  if (!el || el === document.body) return '';
  const r = el.getBoundingClientRect();
  const cs = getComputedStyle(el);
  const hidden = cs.visibility === 'hidden' || Number(cs.opacity) === 0 || r.width < 2 || r.height < 2 ||
    r.bottom <= 0 || r.right <= 0 || r.top >= innerHeight || r.left >= innerWidth;
  const skip = el.localName === 'a' && /^#./.test(el.getAttribute('href') || '') && /skip|main|content/i.test(el.textContent + ' ' + el.className);
  return JSON.stringify({x: r.x + scrollX - 8, y: r.y + scrollY - 8, w: r.width + 16, h: r.height + 16, hidden, skip,
    target: skip ? (el.getAttribute('href') || '').slice(1) : '', found: skip ? !!document.getElementById((el.getAttribute('href') || '').slice(1)) : false});
})()`

// blurJS and refocusJS take focus off the focused element and give it
// back, for the unfocused screenshot.
const (
	blurJS = `(() => {
  let el = document.activeElement;
  while (el && el.shadowRoot && el.shadowRoot.activeElement) el = el.shadowRoot.activeElement;
  window.__testbedFocused = el; el.blur(); return '';
})()`
	refocusJS = `(() => { const el = window.__testbedFocused; if (el) el.focus({preventScroll: true}); return ''; })()`
)

type focusBox struct {
	X, Y, W, H   float64
	Hidden, Skip bool
	Target       string
	Found        bool
}

// tabWalk presses Tab through the page. It reports a missing or broken
// skip link to #main, a walk that never wraps (trap), stops that can't
// be seen and stops whose focused look differs from the unfocused one by
// fewer than 20 pixels (no focus ring).
func (a *auditor) tabWalk(ctx context.Context, page *rod.Page, path string) ([]Finding, error) {
	world, err := proto.PageCreateIsolatedWorld{FrameID: page.FrameID, WorldName: "testbed-walk"}.Call(page)
	if err != nil {
		return nil, err
	}
	eval := func(js string) (string, error) {
		res, err := proto.RuntimeEvaluate{Expression: js, ContextID: world.ExecutionContextID, ReturnByValue: true}.Call(page)
		if err != nil {
			return "", err
		}
		if res.ExceptionDetails != nil {
			return "", errors.New(res.ExceptionDetails.Text)
		}
		return res.Result.Value.Str(), nil
	}
	if _, err := eval(hideCaretJS); err != nil {
		return nil, err
	}
	var out []Finding
	seen := map[string]bool{}
	wrapped := false
	last, stuck := "", 0 // the previous stop and how many presses left focus on it
	for i := range a.walk {
		if err := focus.Press(ctx, page, focus.Tab); err != nil {
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
		stop, err := focus.Active(ctx, page)
		if err != nil {
			return nil, err
		}
		if stop.IsBody || stop.IsFrame {
			if i > 0 {
				wrapped = true
				break
			}
			continue
		}
		if stop.Selector == last {
			if stuck++; stuck >= 10 {
				out = append(out, Finding{Page: path, Check: "keyboard/trap", Selector: stop.Selector, Detail: "Tab doesn't move focus on"})
				return out, nil
			}
			continue
		}
		last, stuck = stop.Selector, 0
		if seen[stop.Selector] {
			wrapped = true // back at a stop it saw: the cycle closed without the document stop
			break
		}
		seen[stop.Selector] = true
		raw, err := eval(focusedBoxJS)
		if err != nil || raw == "" {
			continue
		}
		var box focusBox
		if err := json.Unmarshal([]byte(raw), &box); err != nil {
			return nil, err
		}
		if len(seen) == 1 {
			switch {
			case !box.Skip:
				out = append(out, Finding{Page: path, Check: "keyboard/skip-link-missing", Selector: stop.Selector, Detail: "the first stop isn't a skip link"})
			case box.Target != "main" || !box.Found:
				out = append(out, Finding{Page: path, Check: "keyboard/skip-link-broken", Selector: stop.Selector, Detail: "skip link to #" + box.Target})
			}
		}
		if box.Hidden {
			out = append(out, Finding{Page: path, Check: "keyboard/focus-invisible", Selector: stop.Selector})
			continue
		}
		if len(seen) > 40 {
			continue
		}
		diff, err := ringDiff(page, box, eval)
		if err != nil {
			return nil, err
		}
		if diff < 20 {
			out = append(out, Finding{Page: path, Check: "keyboard/no-focus-ring", Selector: stop.Selector, Detail: fmt.Sprintf("%d px differ", diff)})
		}
	}
	if !wrapped {
		out = append(out, Finding{Page: path, Check: "keyboard/trap", Detail: fmt.Sprintf("no way round after %d Tab presses (%d stops)", a.walk, len(seen))})
	}
	return out, nil
}

// ringDiff counts the pixels around the focused element that change when
// it loses focus. It waits for transitions first.
func ringDiff(page *rod.Page, box focusBox, eval func(string) (string, error)) (int, error) {
	clip := &proto.PageViewport{X: max(box.X, 0), Y: max(box.Y, 0), Width: box.W, Height: box.H, Scale: 1}
	shot := func() (image.Image, error) {
		time.Sleep(320 * time.Millisecond) // focus transitions settle
		data, err := page.Screenshot(false, &proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng, Clip: clip})
		if err != nil {
			return nil, err
		}
		return png.Decode(bytes.NewReader(data))
	}
	focused, err := shot()
	if err != nil {
		return 0, err
	}
	if _, err := eval(blurJS); err != nil {
		return 0, err
	}
	blurred, err := shot()
	if _, ferr := eval(refocusJS); ferr != nil && err == nil {
		err = ferr
	}
	if err != nil {
		return 0, err
	}
	return pixelDiff(focused, blurred), nil
}

// pixelDiff counts the pixels that differ by more than a little in any
// channel (font smoothing noise stays under it).
func pixelDiff(a, b image.Image) int {
	r := a.Bounds().Intersect(b.Bounds())
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			if far(ar, br) || far(ag, bg) || far(ab, bb) {
				n++
			}
		}
	}
	return n
}

func far(a, b uint32) bool {
	const noise = 16 << 8
	if a > b {
		return a-b > noise
	}
	return b-a > noise
}
