// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pagewide

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/model"
)

// markerPage has a control only a click works and a script that
// listens for keys on the whole page, as focus-visible polyfills and
// what-input do, plus a control a delegated keydown handler answers.
func markerPage(script string) string {
	return `<!doctype html><html lang="en"><head><title>Markers</title>
<script>` + script + `</script></head><body><main>
<a id="first" href="#top">Top</a>
<div id="menu" tabindex="0" role="button">Menu</div>
<div id="tags" tabindex="0" role="button">Tags</div>
<div id="ok" tabindex="0" role="button">Show more</div>
<p id="out"></p>
<script>
const $ = id => document.getElementById(id);
$('menu').addEventListener('click', () => { $('out').textContent = 'Menu open'; });
$('tags').addEventListener('click', () => { $('out').textContent = 'Tags open'; });
$('ok').addEventListener('click', () => { $('out').textContent = 'More'; });
document.addEventListener('keydown', e => { if (e.target.id === 'ok' && e.key === 'Enter') $('out').textContent = 'More'; });
</script>
</main></body></html>`
}

// A DOM change a page-wide key handler makes on every press is no
// answer from the control the key was pressed on: the mouse-only
// controls are still reported. A delegated handler that answers Enter
// for its control still counts.
func TestMouseOnlyPageWideKeyHandler(t *testing.T) {
	cases := map[string]string{
		"class":  `document.addEventListener('keydown', function () { document.documentElement.classList.add('kbd-user'); });`,
		"window": `addEventListener('keydown', function () { document.documentElement.classList.add('kbd-user'); }, true);`,
		"what-input": `document.addEventListener('keydown', function () { document.documentElement.setAttribute('data-whatinput', 'keyboard'); });
document.addEventListener('mousedown', function () { document.documentElement.setAttribute('data-whatinput', 'mouse'); });`,
		"live region": `document.addEventListener('keydown', function (e) { const s = document.getElementById('said'); if (s) s.textContent = e.key; });`,
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			html := markerPage(script)
			if name == "live region" {
				html = strings.Replace(html, `<p id="out"></p>`, `<p id="out"></p><p id="said" aria-live="polite"></p>`, 1)
			}
			pg := &pages{HTML: map[string]string{"/": html}}
			srv := serve(t, pg)
			ch := launch(t)
			tab, p := ch.Open(srv.URL + "/")
			cc := runContext(t)
			live(t, keyboard.New(), cc, tab, p)
			if a := p.Audits["keyboard"]; a.Status != model.AuditOK {
				t.Fatalf("audit %+v, warnings %v", a, cc.Run.Warnings)
			}
			var got []string
			for _, i := range p.Issues {
				if i.Check == ids.NotOperable {
					got = append(got, i.Source)
				}
			}
			data, _ := os.ReadFile(filepath.Join(cc.Dir, "keyboard", p.ID+".json"))
			tr, _ := keyboard.DecodeTrace(data)
			if want := []string{"#menu", "#tags"}; !slices.Equal(got, want) {
				t.Errorf("not-operable %q, want %q\nlisteners %+v", got, want, tr.Listeners)
			}
			for _, c := range tr.Listeners {
				if c.Selector == "#ok" && c.Enter != keyboard.PressDOM {
					t.Errorf("#ok %+v, want Enter to count its delegated answer", c)
				}
			}
		})
	}
}

// scrollPage has two controls only a click works, one at the top and
// one below the fold, on a long page whose scroll listener draws a
// reading-progress bar: Space on a focused div scrolls the page by
// default; focusing the lower control scrolls it into view.
const scrollPage = `<!doctype html><html lang="en"><head><title>Scroll</title></head><body><main>
<a id="first" href="#top">Top</a>
<div id="menu" tabindex="0" role="button">Menu</div>
<p id="out"></p>
<div style="height: 3000px">Long text</div>
<div id="low" tabindex="0" role="button">More</div>
<div style="height: 3000px">More text</div>
<div id="bar" style="position: fixed; top: 0; left: 0; height: 3px; width: 0; background: #000"></div>
<script>
const $ = id => document.getElementById(id);
$('menu').addEventListener('click', () => { $('out').textContent = 'Menu open'; });
$('low').addEventListener('click', () => { $('out').textContent = 'More open'; });
addEventListener('scroll', () => {
  $('bar').style.width = Math.round(100 * scrollY / (document.documentElement.scrollHeight - innerHeight)) + '%';
});
</script>
</main></body></html>`

// What a page's scroll listener changes is no answer to a press: Space
// doesn't scroll the page while a control is pressed and a scroll the
// walk didn't hold back can't credit the press. Both mouse-only
// controls are reported.
func TestMouseOnlyScrollListener(t *testing.T) {
	pg := &pages{HTML: map[string]string{"/": scrollPage}}
	srv := serve(t, pg)
	c := launch(t)
	tab, p := c.Open(srv.URL + "/")
	cc := runContext(t)
	live(t, keyboard.New(), cc, tab, p)
	if a := p.Audits["keyboard"]; a.Status != model.AuditOK {
		t.Fatalf("audit %+v, warnings %v", a, cc.Run.Warnings)
	}
	var got []string
	for _, i := range p.Issues {
		if i.Check == ids.NotOperable {
			got = append(got, i.Source)
		}
	}
	data, _ := os.ReadFile(filepath.Join(cc.Dir, "keyboard", p.ID+".json"))
	tr, _ := keyboard.DecodeTrace(data)
	if want := []string{"#menu", "#low"}; !slices.Equal(got, want) {
		t.Errorf("not-operable %q, want %q\nlisteners %+v", got, want, tr.Listeners)
	}
}

// navPage has a control only a click works and a control a delegated
// keydown handler answers with a navigation, plus script.
func navPage(script string) string {
	return `<!doctype html><html lang="en"><head><title>Nav</title>
<script>` + script + `</script></head><body><main>
<a id="first" href="#top">Top</a>
<div id="menu" tabindex="0" role="button">Menu</div>
<div id="ok" tabindex="0" role="button">Show more</div>
<p id="out"></p>
<script>
const $ = id => document.getElementById(id);
$('menu').addEventListener('click', () => { $('out').textContent = 'Menu open'; });
$('ok').addEventListener('click', () => { location.href = '/more'; });
document.addEventListener('keydown', e => { if (e.target.id === 'ok' && e.key === 'Enter') location.href = '/more'; });
</script>
</main></body></html>`
}

// A navigation or an address change a page-wide key handler makes on
// every press is no answer from the control the key was pressed on:
// the mouse-only control is still reported. A delegated handler that
// navigates for its own control still counts, unless every press goes
// to the same place.
func TestMouseOnlyPageWideNavigation(t *testing.T) {
	cases := []struct {
		name, script string
		okNav        bool // #ok's Enter counts as its navigation
	}{
		{"none", ``, true},
		{"enternav", `document.addEventListener('keydown', e => { if (e.key === 'Enter' && e.target.id !== 'ok') location.href = '/search'; });`, true},
		{"anynav", `document.addEventListener('keydown', e => { if (e.key === 'Enter') location.href = '/search'; });`, true},
		{"samenav", `document.addEventListener('keydown', e => { if (e.key === 'Enter') location.href = '/more'; });`, false},
		{"keyreplace", `document.addEventListener('keydown', e => history.replaceState(null, '', '?k=' + e.key));`, true},
		{"keypush", `document.addEventListener('keydown', e => history.pushState(null, '', '?n=' + Math.random()));`, true},
		{"randompath", `document.addEventListener('keydown', e => history.pushState(null, '', '/p' + Math.random()));`, true},
		{"hash", `document.addEventListener('keydown', e => { location.hash = 'k' + Math.random(); });`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pg := &pages{HTML: map[string]string{"/": navPage(tc.script), "/search": links(1, ""), "/more": links(1, "")}}
			srv := serve(t, pg)
			ch := launch(t)
			tab, p := ch.Open(srv.URL + "/")
			cc := runContext(t)
			live(t, keyboard.New(), cc, tab, p)
			if a := p.Audits["keyboard"]; a.Status != model.AuditOK {
				t.Fatalf("audit %+v, warnings %v", a, cc.Run.Warnings)
			}
			var got []string
			for _, i := range p.Issues {
				if i.Check == ids.NotOperable {
					got = append(got, i.Source)
				}
			}
			data, _ := os.ReadFile(filepath.Join(cc.Dir, "keyboard", p.ID+".json"))
			tr, _ := keyboard.DecodeTrace(data)
			want := []string{"#menu"}
			if !tc.okNav {
				want = []string{"#menu", "#ok"}
			}
			if !slices.Equal(got, want) {
				t.Errorf("not-operable %q, want %q\nlisteners %+v", got, want, tr.Listeners)
			}
			for _, c := range tr.Listeners {
				if c.Selector == "#ok" && tc.okNav && c.Enter != keyboard.PressNavigation {
					t.Errorf("#ok %+v, want Enter to count its delegated navigation", c)
				}
			}
			for _, path := range []string{"/search", "/more"} {
				if n := pg.Count(path); n != 0 {
					t.Errorf("%s loaded %d times", path, n)
				}
			}
		})
	}
}
