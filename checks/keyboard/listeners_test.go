// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

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
	pg := &pages{html: map[string]string{"/": mousePage}}
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			return
		}
		pg.ServeHTTP(w, r)
	}))
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	cc := withSaver(runContext(t))
	live(t, New(), cc, tab, p)
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
	tr, err := DecodeTrace(data)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("issues %q\nwant %q\nlisteners %+v", got, want, tr.Listeners)
	}
	byID := map[string]Candidate{}
	for _, c := range tr.Listeners {
		byID[c.Selector] = c
	}
	for _, sel := range []string{"#btn", "#li1", "#li2", "#tiny", "#inner", "#lab", "#hid"} {
		if _, ok := byID[sel]; ok {
			t.Errorf("%s was a candidate", sel)
		}
	}
	if c := byID["#post"]; c.Enter != PressRequest {
		t.Errorf("#post %+v, want Enter to count its held write", c)
	}
	if c := byID["#lnk"]; c.Enter != PressDOM || c.Space != "" {
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

// The rules from hand-written candidates.
func TestClassifyMouseOnly(t *testing.T) {
	email := "jane" + "@" + "example.test"
	cases := []struct {
		name string
		c    Candidate
		want string
	}{
		{"unreachable", Candidate{Selector: "#a", Events: []string{"mousedown"}, Tag: "div", Name: "Menu"},
			ids.NotReachable + `|div "Menu" reacts to mousedown but Tab never reaches it.`},
		{"unreachable, name redacted", Candidate{Selector: "#a", Events: []string{"click"}, Tag: "span", Name: email},
			ids.NotReachable + `|span "***@***" reacts to click but Tab never reaches it.`},
		{"tabindex -1 the walk passed", Candidate{Selector: "#a", Events: []string{"click"}, Tag: "div", Seen: true}, ""},
		{"focusable, untested", Candidate{Selector: "#a", Events: []string{"click"}, Tag: "div", Focusable: true}, ""},
		{"click only", Candidate{Selector: "#b", Events: []string{"click"}, Tag: "div", Focusable: true, Tested: true, Click: PressDOM},
			ids.NotOperable + `|div reacts to a mouse click but not to Enter or Space.`},
		{"link: Space not expected", Candidate{Selector: "#b", Events: []string{"click"}, Tag: "span", Focusable: true, Link: true, Tested: true, Click: PressNavigation},
			ids.NotOperable + `|span reacts to a mouse click but not to Enter or Space.`},
		{"Enter works", Candidate{Selector: "#b", Events: []string{"click"}, Focusable: true, Tested: true, Enter: PressRequest}, ""},
		{"Space works", Candidate{Selector: "#b", Events: []string{"click"}, Focusable: true, Tested: true, Space: PressDOM}, ""},
		{"keys: only a navigation the press didn't start", Candidate{Selector: "#b", Events: []string{"click"}, Tag: "div", Focusable: true, Tested: true, Enter: PressUnknown, Space: PressUnknown, Click: PressDOM},
			ids.NotOperable + `|div reacts to a mouse click but not to Enter or Space.`},
		{"click: only a navigation it didn't start", Candidate{Selector: "#b", Events: []string{"click"}, Focusable: true, Tested: true, Enter: PressUnknown, Click: PressUnknown}, ""},
		{"click does nothing either", Candidate{Selector: "#b", Events: []string{"click"}, Focusable: true, Tested: true}, ""},
		{"covered", Candidate{Selector: "#b", Events: []string{"click"}, Focusable: true, Tested: true, Skipped: "covered", Click: PressDOM}, ""},
		{"no events", Candidate{Selector: "#b", Tag: "div"}, ""},
	}
	for _, c := range cases {
		tr := Trace{Schema: Schema, MainIndex: -1, Listeners: []Candidate{c.c}}
		var got []string
		for _, i := range Classify(tr, defaults(), "2.2") {
			got = append(got, i.Check+"|"+i.Detail)
			if i.Source != c.c.Selector || i.WCAG[0] != "2.1.1" || i.Level != model.Warning {
				t.Errorf("%s: issue %+v", c.name, i)
			}
		}
		if strings.Join(got, "\n") != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, strings.Join(got, "\n"), c.want)
		}
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
	pg := &pages{html: map[string]string{"/": focusNavPage, "/other": links(1, "")}}
	srv := serveIn(t, pg, 40400, 40450)
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	cc := withSaver(runContext(t))
	live(t, New(), cc, tab, p)
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
	tr, _ := DecodeTrace(data)
	if want := []string{"#no", "#late"}; !slices.Equal(got, want) {
		t.Errorf("not-operable %q, want %q\nlisteners %+v", got, want, tr.Listeners)
	}
	for _, c := range tr.Listeners {
		if c.Selector == "#go" && c.Enter != PressNavigation {
			t.Errorf("#go %+v, want Enter to count its navigation", c)
		}
	}
	for _, path := range []string{"/elsewhere", "/later", "/go"} {
		if n := pg.count(path); n != 0 {
			t.Errorf("%s loaded %d times", path, n)
		}
	}
}
