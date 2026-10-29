// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mouseonly

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/model"
)

// mousePage has controls with mouse listeners of their own, a few only
// a mouse can use and the rest fine or out of scope.
const mousePage = `<!doctype html><html lang="en"><head><title>Mouse</title></head><body><main>
<a id="first" href="#top">Top</a>
<div id="nr">Open chat</div>
<div id="no" role="button" tabindex="0">Add note</div>
<button id="btn">Real button</button>
<span id="lnk" role="link" tabindex="0">Read more</span>
<ul id="list"><li id="li1">One</li><li id="li2">Two</li></ul>
<div id="post" role="button" tabindex="0">Save draft</div>
<div id="dead" role="button" tabindex="0">Nothing</div>
<span id="tiny" style="display: inline-block; width: 2px; height: 2px; background: #000"></span>
<button id="wrap"><span id="inner">Inside a button</span></button>
<label for="f"><span id="lab">Label part</span></label><input id="f" aria-label="Field">
<div id="hid" aria-hidden="true">Decor</div>
<div id="del" role="button" tabindex="0">Delete account</div>
<script>
const $ = id => document.getElementById(id);
$('nr').addEventListener('click', () => { $('nr').textContent = 'Chat open'; });
$('no').addEventListener('click', () => { $('no').textContent = 'Note added'; });
$('btn').addEventListener('click', () => { $('btn').textContent = 'Clicked'; });
const more = () => { $('lnk').textContent = 'Read less'; };
$('lnk').addEventListener('click', more);
$('lnk').addEventListener('keydown', e => { if (e.key === 'Enter') more(); });
document.addEventListener('click', e => { if (e.target.closest && e.target.closest('#list li')) e.target.textContent += '!'; });
const save = () => fetch('/draft', {method: 'POST', body: 'x'});
$('post').addEventListener('click', save);
$('post').addEventListener('keydown', e => { if (e.key === 'Enter') save(); });
$('dead').addEventListener('click', () => {});
for (const id of ['tiny', 'inner', 'lab', 'hid']) $(id).addEventListener('click', () => {});
window.deleted = 0;
$('del').addEventListener('click', () => { window.deleted++; });
$('del').addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') window.deleted++; });
</script>
</main></body></html>`

// The listener scan finds the control Tab never reaches and the one
// only a click works; a real button, a link role that takes Enter, a
// delegated listener, a control whose write is held, one a click
// doesn't move either and the guarded elements give nothing. The
// destructive control is never pressed and no write reaches the site.
func TestMouseOnly(t *testing.T) {
	var writes atomic.Int32
	pg := &pages{HTML: map[string]string{"/": mousePage}}
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			return
		}
		pg.ServeHTTP(w, r)
	}))
	c := launch(t)
	tab, p := c.Open(srv.URL + "/")
	cc := runContext(t)
	live(t, keyboard.New(), cc, tab, p)
	if a := p.Audits["keyboard"]; a.Status != model.AuditOK {
		t.Fatalf("audit %+v, warnings %v", a, cc.Run.Warnings)
	}
	var got []string
	for _, i := range p.Issues {
		if i.Check == ids.NotReachable || i.Check == ids.NotOperable {
			got = append(got, i.Check+"|"+i.Source+"|"+i.Detail)
		}
	}
	want := []string{
		// A plain div has no accessible name; its text is never read.
		ids.NotReachable + `|#nr|div reacts to click but Tab never reaches it.`,
		ids.NotOperable + `|#no|div "Add note" reacts to a mouse click but not to Enter or Space.`,
	}
	data, _ := os.ReadFile(filepath.Join(cc.Dir, "keyboard", p.ID+".json"))
	tr, err := keyboard.DecodeTrace(data)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("issues %q\nwant %q\nlisteners %+v", got, want, tr.Listeners)
	}
	byID := map[string]keyboard.Candidate{}
	for _, c := range tr.Listeners {
		byID[c.Selector] = c
	}
	for _, sel := range []string{"#btn", "#li1", "#li2", "#tiny", "#inner", "#lab", "#hid"} {
		if _, ok := byID[sel]; ok {
			t.Errorf("%s was a candidate", sel)
		}
	}
	if c := byID["#post"]; c.Enter != keyboard.PressRequest {
		t.Errorf("#post %+v, want Enter to count its held write", c)
	}
	if c := byID["#lnk"]; c.Enter != keyboard.PressDOM || c.Space != "" {
		t.Errorf("#lnk %+v", c)
	}
	if c := byID["#dead"]; !c.Tested || c.Click != "" {
		t.Errorf("#dead %+v", c)
	}
	if c := byID["#del"]; c.Tested || c.Skipped != "looks destructive" {
		t.Errorf("#del %+v", c)
	}
	if n, err := tab.Eval(`() => window.deleted`); err != nil || n.Value.Int() != 0 {
		t.Errorf("the destructive control was pressed: %v %v", n, err)
	}
	if n := writes.Load(); n != 0 {
		t.Errorf("%d writes reached the site", n)
	}
	if !slices.Contains(tr.Held, "POST /draft") {
		t.Errorf("held %q", tr.Held)
	}
}

// focusNavPage has a link that starts a navigation whenever it gets
// focus and a control only a click works: the walk stops that
// navigation, which must not count as the control's own response.
const focusNavPage = `<!doctype html><html lang="en"><head><title>Focus nav</title></head><body><main>
<a id="first" href="#top">Top</a>
<a id="away" href="/other">Other page</a>
<div id="no" role="button" tabindex="0">Add note</div>
<div id="late" role="button" tabindex="0">Add tag</div>
<div id="go" role="button" tabindex="0">Go on</div>
<script>
const $ = id => document.getElementById(id);
$('away').addEventListener('focus', () => { location.href = '/elsewhere'; });
document.addEventListener('focusin', e => { if (e.target.id === 'no') setTimeout(() => { location.href = '/later'; }, 100); });
$('no').addEventListener('click', () => { $('no').textContent = 'Note added'; });
$('late').addEventListener('click', () => { $('late').textContent = 'Tag added'; });
const go = () => { location.href = '/go'; };
$('go').addEventListener('click', go);
$('go').addEventListener('keydown', e => { if (e.key === 'Enter') go(); });
</script>
</main></body></html>`

// A navigation another element starts on focus, or a timer the
// control's focus set, is no answer to Enter or Space: the
// mouse-only controls are still reported. One the control's own Enter
// handler starts is.
func TestMouseOnlyForeignNavigation(t *testing.T) {
	pg := &pages{HTML: map[string]string{"/": focusNavPage, "/other": links(1, "")}}
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
	if want := []string{"#no", "#late"}; !slices.Equal(got, want) {
		t.Errorf("not-operable %q, want %q\nlisteners %+v", got, want, tr.Listeners)
	}
	for _, c := range tr.Listeners {
		if c.Selector == "#go" && c.Enter != keyboard.PressNavigation {
			t.Errorf("#go %+v, want Enter to count its navigation", c)
		}
	}
	for _, path := range []string{"/elsewhere", "/later", "/go"} {
		if n := pg.Count(path); n != 0 {
			t.Errorf("%s loaded %d times", path, n)
		}
	}
}

// busyPage has a control only a click works, one Enter works and the
// page's own background activity: a ticking clock or a poll.
func busyPage(background string) string {
	return `<!doctype html><html lang="en"><head><title>Busy</title></head><body><main>
<a id="first" href="#top">Top</a>
<div id="add" role="button" tabindex="0">Add to basket</div>
<div id="ok" role="button" tabindex="0">Show more</div>
<span id="clock"></span>
<script>
const $ = id => document.getElementById(id);
` + background + `
$('add').addEventListener('click', () => { $('add').textContent = 'Added'; });
const more = () => { $('ok').textContent = 'Show less'; };
$('ok').addEventListener('click', more);
$('ok').addEventListener('keydown', e => { if (e.key === 'Enter') more(); });
</script>
</main></body></html>`
}

// A change the page makes by itself while the walk waits for a press
// to show an effect is no answer to the press: a mouse-only control
// on a page with a ticking clock or a poll is still reported while a
// control whose own Enter handler answers is not.
func TestMouseOnlyBackgroundActivity(t *testing.T) {
	cases := map[string]string{
		"clock": `let n = 0; setInterval(() => { $('clock').textContent = ++n; }, 100);`,
		"poll":  `setInterval(() => fetch('/x?' + Date.now()), 150);`,
	}
	for name, bg := range cases {
		t.Run(name, func(t *testing.T) {
			pg := &pages{HTML: map[string]string{"/": busyPage(bg), "/x": "x"}}
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
			if want := []string{"#add"}; !slices.Equal(got, want) {
				t.Errorf("not-operable %q, want %q\nlisteners %+v", got, want, tr.Listeners)
			}
			for _, c := range tr.Listeners {
				switch c.Selector {
				case "#add":
					if !noResponse(c.Enter) || !noResponse(c.Space) || c.Click != keyboard.PressDOM {
						t.Errorf("#add %+v, want no response to keys and dom on click", c)
					}
				case "#ok":
					if c.Enter != keyboard.PressDOM {
						t.Errorf("#ok %+v, want Enter to count its own change", c)
					}
				}
			}
		})
	}
}

// asyncPage has a control only a click works: its handler asks the
// server and adds an item when the reply comes. background is the
// page's own activity, if any.
func asyncPage(background string) string {
	return `<!doctype html><html lang="en"><head><title>Async</title></head><body><main>
<a id="first" href="#top">Top</a>
<div id="more" tabindex="0" role="button">Load more</div>
<ul id="list"></ul>
<p id="clock"></p>
<script>
const $ = id => document.getElementById(id);
$('more').addEventListener('click', () => fetch('/api/more').then(r => r.text()).then(t => {
  const li = document.createElement('li'); li.textContent = t; $('list').appendChild(li);
}));
` + background + `
</script>
</main></body></html>`
}

// A control whose click asks the server and changes the page only when
// the reply comes can't be judged on a page that also changes by
// itself: the walk can't tell its click did anything. It isn't
// reported there, yet its key is listed as unjudged, so a comparison
// keeps the issue the quiet page reported instead of counting it as
// fixed.
func TestMouseOnlyAsyncClickOnBusyPage(t *testing.T) {
	cases := []struct {
		name, background string
		report           bool
	}{
		{"quiet", "", true},
		{"clock", `setInterval(() => { $('clock').textContent = Date.now(); }, 50);`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pg := &pages{HTML: map[string]string{"/": asyncPage(c.background), "/api/more": "Item"}}
			srv := serve(t, pg)
			ch := launch(t)
			tab, p := ch.Open(srv.URL + "/")
			cc := runContext(t)
			live(t, keyboard.New(), cc, tab, p)
			if a := p.Audits["keyboard"]; a.Status != model.AuditOK {
				t.Fatalf("audit %+v, warnings %v", a, cc.Run.Warnings)
			}
			key := ids.NotOperable + "|#more"
			data, _ := os.ReadFile(filepath.Join(cc.Dir, "keyboard", p.ID+".json"))
			tr, _ := keyboard.DecodeTrace(data)
			if got := reports(p, key); got != c.report {
				t.Errorf("reported %v, want %v\nlisteners %+v", got, c.report, tr.Listeners)
			}
			if p.Keyboard == nil || !p.Keyboard.WidgetsOn {
				t.Fatalf("summary %+v", p.Keyboard)
			}
			if got := slices.Contains(p.Keyboard.Unjudged, key); got == c.report {
				t.Errorf("unjudged %q\nlisteners %+v", p.Keyboard.Unjudged, tr.Listeners)
			}
			if !c.report && !slices.Contains(keyboard.Unjudged(p, []string{key}), key) {
				t.Error("the confirm pass would count the issue as fixed")
			}
		})
	}
}
