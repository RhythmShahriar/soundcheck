// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync"

	"github.com/go-rod/rod/lib/proto"
)

// The focus indicator check compares pictures of an element blurred and
// focused, so it finds a ring, an underline or a color change whatever
// CSS draws it. It never trusts :focus-visible matching (a rule can
// match and draw nothing).
const (
	// maxRings caps the distinct stops measured on a page.
	maxRings = 40
	// ringMargin is how far around the element the pictures reach, in
	// CSS pixels: an outline or a shadow is drawn outside the box.
	ringMargin   = 8
	ringMarginJS = "8" // ringMargin, for the probe text
	// ringPixels is how many pixels must change for focus to count as
	// visible; channelDelta is how much one channel of a pixel must
	// change for the pixel to count.
	ringPixels   = 20
	channelDelta = 16
	// unstableAfter is how many pixels two blurred pictures may differ by
	// before the element counts as animated and isn't judged.
	unstableAfter = 2
)

// Ring is the focus indicator measured on one stop of the walk.
type Ring struct {
	Stop     int    `json:"stop"` // its forward Tab stop, from 1
	Selector string `json:"selector"`
	Tag      string `json:"tag"`
	Name     string `json:"name"` // the accessible name, redacted
	// Changed counts the pixels in the region that differ between the
	// element blurred and focused.
	Changed int `json:"changed"`
	// Unstable is true when two pictures of the element blurred already
	// differed: something on it moves, so no verdict is given.
	Unstable bool `json:"unstable,omitempty"`
	// Cached is true when the element was measured on another page of
	// the same URL group and the result reused.
	Cached bool `json:"cached,omitempty"`
	// Crop is the picture of the focused element saved for the report,
	// relative to the run's folder; never one of a text field.
	Crop string `json:"crop,omitempty"`
}

// ringCache keeps focus indicator results across the pages of a run, by
// URL group and selector: a header link measured on one product page
// is the same link on the next.
type ringCache struct {
	mu   sync.Mutex
	seen map[string]Ring
}

func (c *ringCache) get(key string) (Ring, bool) {
	if c == nil {
		return Ring{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.seen[key]
	return r, ok
}

func (c *ringCache) put(key string, r Ring) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string]Ring{}
	}
	c.seen[key] = r
}

// urlGroup is the group of pages a path belongs to: its first segment,
// as the pages of one template share it (/product/a and /product/b are
// /product). The home page is a group of its own.
func urlGroup(path string) string {
	path, _, _ = strings.Cut(path, "?")
	seg, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	return "/" + seg
}

// ringJS is called on a stop to measure. It scrolls the element to the
// middle of the viewport and returns the region to take pictures of in
// document coordinates of the top page: the element's box (blurred and
// focused together, as focus may move it) plus
// ringMargin, joined with its parent's box when the parent is at most
// twice as big (a ring drawn on a wrapper), cut to the viewport. text
// is true for a field a visitor types into, whose picture is never kept
// (security M5).
const ringJS = `function () {
  const el = this;` + prelude + `
  el.scrollIntoView({block: 'center', inline: 'nearest'});
  const offset = d => {
    let x = 0, y = 0;
    for (let w = d.defaultView; w && w !== w.parent;) {
      let f = null;
      try { f = w.frameElement; } catch (e) { f = null; }
      if (!f) break;
      const fr = f.getBoundingClientRect();
      x += fr.x + f.clientLeft; y += fr.y + f.clientTop;
      w = w.parent;
    }
    return [x, y];
  };
  const [ox, oy] = offset(el.ownerDocument);
  // The box blurred and focused: an element that moves into view on
  // focus (an offset skip link) is pictured where it shows, not only
  // where it hides.
  const r0 = el.getBoundingClientRect();
  el.focus({preventScroll: true});
  const r1 = el.getBoundingClientRect();
  const boxes = [r0, r1].filter(b => b.width * b.height > 0);
  const all = boxes.length ? boxes : [r1];
  const r = {left: Math.min(...all.map(b => b.left)), top: Math.min(...all.map(b => b.top)),
    right: Math.max(...all.map(b => b.right)), bottom: Math.max(...all.map(b => b.bottom))};
  r.width = r.right - r.left; r.height = r.bottom - r.top;
  const m = ` + ringMarginJS + `;
  let l = r.left - m, t = r.top - m, rt = r.right + m, b = r.bottom + m;
  const p = up(el);
  if (p && p.getBoundingClientRect && p.ownerDocument === el.ownerDocument) {
    const pr = p.getBoundingClientRect();
    if (pr.width * pr.height <= 2 * r.width * r.height) {
      l = Math.min(l, pr.left); t = Math.min(t, pr.top); rt = Math.max(rt, pr.right); b = Math.max(b, pr.bottom);
    }
  }
  l = Math.max(0, l + ox); t = Math.max(0, t + oy);
  rt = Math.min(innerWidth, rt + ox); b = Math.min(innerHeight, b + oy);
  const type = (el.getAttribute('type') || 'text').toLowerCase();
  const text = el.isContentEditable || el.localName === 'textarea' ||
    (el.localName === 'input' && ['text', 'email', 'search', 'tel', 'url', 'password', 'number', ''].includes(type));
  return JSON.stringify({x: l + scrollX, y: t + scrollY, w: rt - l, h: b - t, text});
}`

// blurJS takes focus off the element and waits for whatever its blur
// starts to settle: the page's finite animations and transitions (at
// most 300 ms) and a frame. focusJSSettled focuses it again with the
// same wait, frameJS waits a frame alone. The timers stand in for a
// frame in a tab Chrome doesn't paint.
const (
	settle = `
  const anims = document.getAnimations ? document.getAnimations().filter(a => a.effect && a.effect.getTiming().iterations !== Infinity) : [];
  if (anims.length) await Promise.race([Promise.all(anims.map(a => a.finished.catch(() => {}))), new Promise(r => setTimeout(r, 300))]);
  await new Promise(r => { requestAnimationFrame(() => requestAnimationFrame(r)); setTimeout(r, 100); });
  return '';`
	blurJS         = `async function () { this.blur();` + settle + `}`
	frameJS        = `async function () { await new Promise(r => { requestAnimationFrame(() => requestAnimationFrame(r)); setTimeout(r, 100); }); return ''; }`
	focusJSSettled = `async function () { this.focus({preventScroll: true});` + settle + `}`
)

type region struct {
	X, Y, W, H float64
	Text       bool
}

// rings measures the focus indicator of up to maxRings distinct stops
// of the forward walk. A stop that is hidden (focus-invisible says so
// already), in a cross-origin frame, ignored or gone is left out.
func (w *walker) rings(ctx context.Context, t *Trace) error {
	group := urlGroup(w.page.Path)
	seen := map[int]bool{}
	for i, s := range t.Forward {
		if len(t.Rings) >= maxRings {
			break
		}
		if s.IsBody || s.IsFrame || s.Ignored || s.Hidden != "" || !s.Connected || s.BackendNodeID == 0 || seen[s.BackendNodeID] {
			continue
		}
		seen[s.BackendNodeID] = true
		if ctx.Err() != nil {
			return ctx.Err()
		}
		key := group + "\x00" + s.Selector
		if r, ok := w.ringSeen.get(key); ok {
			r.Stop, r.Cached = i+1, true
			t.Rings = append(t.Rings, r)
			continue
		}
		r, err := w.ring(ctx, i, s)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue // gone or moved: a stop that can't be measured isn't judged
		}
		w.ringSeen.put(key, r)
		t.Rings = append(t.Rings, r)
	}
	return nil
}

// ring measures one stop: two pictures blurred, one focused.
func (w *walker) ring(ctx context.Context, i int, s Step) (Ring, error) {
	r := Ring{Stop: i + 1, Selector: s.Selector, Tag: s.Tag, Name: s.Name}
	raw, err := w.reader.CallOn(ctx, ringJS, s.BackendNodeID)
	if err != nil {
		return r, err
	}
	var reg region
	if err := json.Unmarshal([]byte(raw), &reg); err != nil {
		return r, err
	}
	if reg.W < 1 || reg.H < 1 {
		return r, fmt.Errorf("no room on screen")
	}
	clip := &proto.PageViewport{X: reg.X, Y: reg.Y, Width: reg.W, Height: reg.H, Scale: 1}
	if _, err := w.reader.CallOn(ctx, blurJS, s.BackendNodeID); err != nil {
		return r, err
	}
	a, err := w.shot(ctx, clip)
	if err != nil {
		return r, err
	}
	if _, err := w.reader.CallOn(ctx, frameJS, s.BackendNodeID); err != nil {
		return r, err
	}
	b, err := w.shot(ctx, clip)
	if err != nil {
		return r, err
	}
	if n, err := diffPixels(a, b); err != nil || n > unstableAfter {
		r.Unstable = true
		return r, err
	}
	if _, err := w.reader.CallOn(ctx, focusJSSettled, s.BackendNodeID); err != nil {
		return r, err
	}
	focused, err := w.shot(ctx, clip)
	if err != nil {
		return r, err
	}
	n, err := diffPixels(a, focused)
	if err != nil {
		return r, err
	}
	r.Changed = n
	if n < ringPixels && !reg.Text {
		name := fmt.Sprintf("eaa/focus/%s-%d.png", w.page.ID, i+1)
		if w.cc.Save != nil && w.cc.Save(name, focused) == nil {
			r.Crop = name
		}
	}
	return r, nil
}

// shot takes a PNG picture of clip, in document coordinates.
func (w *walker) shot(ctx context.Context, clip *proto.PageViewport) ([]byte, error) {
	res, err := proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng, Clip: clip}.Call(w.tab.Context(ctx))
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

// diffPixels counts the pixels of two PNG pictures of one region where
// a color channel differs by more than channelDelta. Pictures of
// different sizes differ everywhere.
func diffPixels(a, b []byte) (int, error) {
	ia, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		return 0, err
	}
	ib, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	ba, bb := ia.Bounds(), ib.Bounds()
	if ba.Dx() != bb.Dx() || ba.Dy() != bb.Dy() {
		return ba.Dx() * ba.Dy(), nil
	}
	n := 0
	for y := range ba.Dy() {
		for x := range ba.Dx() {
			if pixelDiffers(ia, ib, image.Pt(ba.Min.X+x, ba.Min.Y+y), image.Pt(bb.Min.X+x, bb.Min.Y+y)) {
				n++
			}
		}
	}
	return n, nil
}

func pixelDiffers(a, b image.Image, pa, pb image.Point) bool {
	r1, g1, b1, a1 := a.At(pa.X, pa.Y).RGBA()
	r2, g2, b2, a2 := b.At(pb.X, pb.Y).RGBA()
	d := func(x, y uint32) bool {
		x, y = x>>8, y>>8
		if x > y {
			return x-y > channelDelta
		}
		return y-x > channelDelta
	}
	return d(r1, r2) || d(g1, g2) || d(b1, b2) || d(a1, a2)
}
