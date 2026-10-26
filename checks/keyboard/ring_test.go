// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/model"
)

// rings is a page of controls on white, each focused its own way.
const ringsPage = `<!doctype html><html lang="en"><head><title>Rings</title><style>
body { background: #fff; margin: 40px; font: 16px sans-serif; }
a, button, input { display: inline-block; margin: 20px; color: #000; text-decoration: none; }
button { background: #eee; border: 1px solid #999; }
#none:focus, #white:focus, #field:focus, #shadow:focus, #visible:focus, #under:focus, #bg:focus, #fade:focus { outline: none; }
#white:focus { background: #fff; }
#shadow:focus { box-shadow: 0 0 0 3px #1a73e8; }
#visible:focus-visible { outline: 3px solid #c00; }
#under:focus { text-decoration: underline; }
#bg:focus { background: #ffd54f; }
#fade { box-shadow: 0 0 0 0 transparent; transition: box-shadow 200ms; }
#fade:focus { box-shadow: 0 0 0 4px #1a73e8; }
#field { border: 1px solid #999; }
#spin { outline: none; animation: blink 120ms steps(2) infinite; }
@keyframes blink { from { background: #000; color: #fff; } to { background: #fff; color: #000; } }
</style></head><body><main>
<a id="none" href="#1">No ring</a>
<a id="white" href="#2">White on white</a>
<input id="field" type="email" aria-label="Email" value="someone@example.test">
<a id="shadow" href="#3">Shadow</a>
<a id="visible" href="#4">Focus visible</a>
<a id="under" href="#5">Underline</a>
<button id="bg">Background</button>
<a id="fade" href="#6">Fade in</a>
<a id="plain" href="#7">Default ring</a>
<a id="spin" href="#8">Blinking</a>
</main></body></html>`

// The pixel comparison finds a removed ring whatever the CSS. It
// passes any visible change: a shadow, an outline from :focus-visible,
// an underline, a color or a ring that fades in. A field's caret is
// hidden, so a field without a ring fails too; its picture is never
// kept.
func TestRingsByPixels(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/": ringsPage}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	cc := withSaver(runContext(t))
	m := New()
	live(t, m, cc, tab, p)
	data, err := os.ReadFile(filepath.Join(cc.Dir, "keyboard", p.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	tr, err := DecodeTrace(data)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Ring{}
	for _, r := range tr.Rings {
		got[r.Selector] = r
	}
	// Something that moves by itself gives no verdict.
	if r := got["#spin"]; !r.Unstable {
		t.Errorf("#spin %+v, want unstable", r)
	}
	for sel, fail := range map[string]bool{"#none": true, "#white": true, "#field": true,
		"#shadow": false, "#visible": false, "#under": false, "#bg": false, "#fade": false, "#plain": false} {
		r, ok := got[sel]
		if !ok {
			t.Errorf("%s not measured; rings %+v", sel, tr.Rings)
			continue
		}
		if r.Unstable || (r.Changed < ringPixels) != fail {
			t.Errorf("%s: changed %d, unstable %v, want fail %v", sel, r.Changed, r.Unstable, fail)
		}
	}
	keys := map[string]bool{}
	for _, i := range p.Issues {
		if i.Check == ids.NoFocusRing {
			keys[i.Source] = true
			if !strings.Contains(i.Detail, "people using a keyboard can't see where they are") || i.WCAG[0] != "2.4.7" {
				t.Errorf("issue %+v", i)
			}
		}
	}
	if len(keys) != 3 || !keys["#none"] || !keys["#white"] || !keys["#field"] {
		t.Errorf("no-focus-ring on %v", keys)
	}
	if got["#field"].Crop != "" {
		t.Errorf("a field's picture was kept: %s", got["#field"].Crop)
	}
	crop1 := p.Keyboard.Crops["keyboard/no-focus-ring|#none"]
	if crop := crop1; crop != "eaa/focus/"+p.ID+"-1.png" {
		t.Errorf("crops %v", p.Keyboard.Crops)
	} else if _, err := os.Stat(filepath.Join(cc.Dir, filepath.FromSlash(crop))); err != nil {
		t.Error(err)
	}
	files, _ := filepath.Glob(filepath.Join(cc.Dir, "eaa", "focus", "*.png"))
	if len(files) != 2 {
		t.Errorf("crops on disk %v, want #none and #white only", files)
	}
	// A second page of the same group reuses the results.
	tab, p2 := c.open(srv.URL + "/")
	p2.ID, p2.Path = "second", "/"
	cc2 := withSaver(runContext(t))
	live(t, m, cc2, tab, p2)
	data, err = os.ReadFile(filepath.Join(cc2.Dir, "keyboard", "second.json"))
	if err != nil {
		t.Fatal(err)
	}
	tr2, _ := DecodeTrace(data)
	if len(tr2.Rings) != len(tr.Rings) || !tr2.Rings[0].Cached {
		t.Errorf("second page rings %+v", tr2.Rings)
	}
	if p2.Keyboard.Crops["keyboard/no-focus-ring|#none"] != crop1 {
		t.Errorf("second page crops %v", p2.Keyboard.Crops)
	}
}

// A ring is reported from the trace alone: fewer than 20 pixels and a
// stable picture.
func TestClassifyNoRing(t *testing.T) {
	tr := Trace{Schema: Schema, MainIndex: -1, Forward: []Step{{}, {}}, Rings: []Ring{
		{Stop: 1, Selector: "#a", Tag: "a", Name: "Cart", Changed: 3},
		{Stop: 2, Selector: "#b", Tag: "button", Changed: 20},
		{Stop: 3, Selector: "#c", Tag: "a", Changed: 0, Unstable: true},
		{Stop: 4, Selector: "#d", Tag: "div", Changed: 1},
		{Stop: 5, Selector: "#hid", Tag: "a", Changed: 0},
	}}
	tr.Forward = []Step{{Hidden: HiddenOpacity}}
	tr.Forward[0].Selector, tr.Forward[0].Tag = "#hid", "a"
	var got []model.Issue
	for _, i := range Classify(tr, defaults(), "2.1") {
		if i.Check == ids.NoFocusRing {
			got = append(got, i)
		}
	}
	if len(got) != 2 || got[0].Source != "#a" || got[1].Source != "#d" {
		t.Fatalf("issues %+v", got)
	}
	want := `Tab focus on "Cart" changes 3 pixels around it; people using a keyboard can't see where they are.`
	if got[0].Detail != want || got[0].Title != "No visible focus indicator" || got[0].Impact != "serious" || got[0].Level != model.Warning {
		t.Errorf("issue %+v", got[0])
	}
	if got[1].Detail != "Tab focus on div changes 1 pixel around it; people using a keyboard can't see where they are." {
		t.Errorf("detail %q", got[1].Detail)
	}
}

func TestDiffPixels(t *testing.T) {
	enc := func(f func(*image.RGBA)) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		for y := range 10 {
			for x := range 10 {
				img.Set(x, y, color.White)
			}
		}
		f(img)
		var b bytes.Buffer
		_ = png.Encode(&b, img)
		return b.Bytes()
	}
	white := enc(func(*image.RGBA) {})
	faint := enc(func(i *image.RGBA) { i.Set(1, 1, color.RGBA{240, 240, 240, 255}) })
	dark := enc(func(i *image.RGBA) { i.Set(1, 1, color.Black); i.Set(2, 2, color.RGBA{200, 255, 255, 255}) })
	if n, _ := diffPixels(white, faint); n != 0 {
		t.Errorf("a change of 15 counted: %d", n)
	}
	if n, _ := diffPixels(white, dark); n != 2 {
		t.Errorf("dark %d, want 2", n)
	}
	small := func() []byte {
		var b bytes.Buffer
		_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
		return b.Bytes()
	}()
	if n, _ := diffPixels(white, small); n != 100 {
		t.Errorf("sizes differ: %d", n)
	}
	if _, err := diffPixels([]byte("x"), white); err == nil {
		t.Error("not a PNG: no error")
	}
}

func TestURLGroup(t *testing.T) {
	for in, want := range map[string]string{"/": "/", "": "/", "/product/a/": "/product", "/product/b?x=1": "/product",
		"/about": "/about", "/blog/2026/10/x": "/blog"} {
		if got := urlGroup(in); got != want {
			t.Errorf("urlGroup(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRingMarginJS(t *testing.T) {
	if ringMarginJS != strconv.Itoa(ringMargin) {
		t.Errorf("ringMarginJS %q, ringMargin %d", ringMarginJS, ringMargin)
	}
}
