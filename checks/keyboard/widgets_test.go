// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
	"github.com/RhythmShahriar/soundcheck/model"
)

// widgetPage wraps a fixture's body in a page with a link before and
// after it, so Tab has somewhere to go.
func widgetPage(body string) string {
	return `<!doctype html><html lang="en"><head><title>Widget</title><style>[hidden] { display: none !important; }</style></head><body><main>
<a id="before" href="#top">Top</a>
` + body + `
<a id="after" href="#end">End</a>
</main></body></html>`
}

// The widget fixtures. Each script finds its elements with
// getElementById; selfTest checks once that the fixture does what
// it claims with a click, before the walk tests it by keyboard.
var widgetFixtures = map[string]struct {
	html     string
	selfTest string
}{
	// Escape removes the dialog with the focused button in it.
	"/focus-lost": {widgetPage(`<button id="open" aria-haspopup="dialog" aria-controls="m">Open offer</button>
<div id="m" role="dialog" hidden><button id="x">Close</button></div>
<script>
const m = document.getElementById('m');
document.getElementById('open').addEventListener('click', () => { m.hidden = false; document.getElementById('x').focus(); });
m.addEventListener('keydown', e => { if (e.key === 'Escape') m.remove(); });
</script>`), `document.getElementById('open').click(); !document.getElementById('m').hidden && document.activeElement.id === 'x'`},
	// The dialog shows; focus stays on the button.
	"/dialog-nofocus": {widgetPage(`<button id="open" aria-haspopup="dialog" aria-controls="d">Newsletter</button>
<div id="d" role="dialog" hidden><p>Sign up</p><button id="ok">OK</button></div>
<script>
const d = document.getElementById('d');
document.getElementById('open').addEventListener('click', () => { d.hidden = false; });
document.addEventListener('keydown', e => { if (e.key === 'Escape') d.hidden = true; });
</script>`), `document.getElementById('open').click(); !document.getElementById('d').hidden && document.activeElement.id !== 'ok'`},
	// A modal that takes focus but lets Tab out to #after.
	"/dialog-leak": {widgetPage(`<button id="open" aria-controls="d">Filters</button>
<div id="d" role="dialog" aria-modal="true" hidden><button id="a">Apply</button><button id="b">Reset</button></div>
<script>
const d = document.getElementById('d'), open = document.getElementById('open');
open.addEventListener('click', () => { d.hidden = false; document.getElementById('a').focus(); });
d.addEventListener('keydown', e => { if (e.key === 'Escape') { d.hidden = true; open.focus(); } });
</script>`), `document.getElementById('open').click(); !document.getElementById('d').hidden && document.activeElement.id === 'a'`},
	// A contained modal that ignores Escape; its close button works.
	"/escape": {widgetPage(`<button id="open" aria-controls="d">Cookie settings</button>
<div id="d" role="dialog" aria-modal="true" hidden><button id="x" aria-label="Close">X</button><button id="s">Save</button></div>
<script>
const d = document.getElementById('d'), open = document.getElementById('open');
const x = document.getElementById('x'), s = document.getElementById('s');
open.addEventListener('click', () => { d.hidden = false; x.focus(); });
x.addEventListener('click', () => { d.hidden = true; open.focus(); });
d.addEventListener('keydown', e => {
  if (e.key !== 'Tab') return;
  e.preventDefault();
  (document.activeElement === x ? s : x).focus();
});
</script>`), `document.getElementById('open').click(); const a = !document.getElementById('d').hidden; document.getElementById('x').click(); a && document.getElementById('d').hidden`},
	// A submenu shown on hover only.
	"/menu-hover": {`<!doctype html><html lang="en"><head><title>Menu</title><style>
nav ul ul { display: none; } nav li:hover > ul { display: block; }
</style></head><body><nav><ul>
<li><a id="shop" href="/shop">Shop</a><ul><li><a id="shoes" href="/shop/shoes">Shoes</a></li></ul></li>
<li><a id="about" href="/about">About</a></li>
</ul></nav><main><a href="#x">Content</a></main></body></html>`, `document.getElementById('shop').focus(); getComputedStyle(document.getElementById('shoes').parentElement.parentElement).display === 'none'`},
	// A native modal dialog: Escape closes it and focus returns.
	"/native": {widgetPage(`<button id="open" aria-controls="d">Settings</button>
<dialog id="d"><button id="ok">OK</button><a href="#more">More</a></dialog>
<script>
document.getElementById('open').addEventListener('click', () => document.getElementById('d').showModal());
</script>`), `document.getElementById('open').click(); const o = document.getElementById('d').open; document.getElementById('d').close(); o`},
	// A custom modal done right: focus in, kept in, Escape, focus back.
	"/custom-modal": {widgetPage(`<button id="open" aria-controls="d" aria-haspopup="dialog">Share</button>
<div id="d" role="dialog" aria-modal="true" hidden><button id="a">Copy link</button><button id="b">Email</button></div>
<script>
const d = document.getElementById('d'), open = document.getElementById('open');
const a = document.getElementById('a'), b = document.getElementById('b');
open.addEventListener('click', () => { d.hidden = false; a.focus(); });
d.addEventListener('keydown', e => {
  if (e.key === 'Escape') { d.hidden = true; open.focus(); }
  if (e.key === 'Tab') { e.preventDefault(); (document.activeElement === a ? b : a).focus(); }
});
</script>`), `document.getElementById('open').click(); !document.getElementById('d').hidden && document.activeElement.id === 'a'`},
	// A non-modal dialog, which Tab may leave.
	"/nonmodal": {widgetPage(`<button id="open" aria-controls="d">Help</button>
<div id="d" role="dialog" hidden><button id="a">Got it</button></div>
<script>
const d = document.getElementById('d'), open = document.getElementById('open');
open.addEventListener('click', () => { d.hidden = false; document.getElementById('a').focus(); });
document.addEventListener('keydown', e => { if (e.key === 'Escape' && !d.hidden) { d.hidden = true; open.focus(); } });
</script>`), `document.getElementById('open').click(); !document.getElementById('d').hidden`},
	// A submenu shown on hover and on focus within.
	"/menu-focus": {`<!doctype html><html lang="en"><head><title>Menu</title><style>
nav ul ul { display: none; } nav li:hover > ul, nav li:focus-within > ul { display: block; }
</style></head><body><nav><ul>
<li><a id="shop" href="/shop">Shop</a><ul><li><a id="shoes" href="/shop/shoes">Shoes</a></li></ul></li>
<li><a id="about" href="/about">About</a></li>
</ul></nav><main><a href="#x">Content</a></main></body></html>`, `document.getElementById('shop').focus(); getComputedStyle(document.getElementById('shoes').parentElement.parentElement).display === 'block'`},
	// A disclosure button.
	"/disclosure": {widgetPage(`<button id="more" aria-expanded="false" aria-controls="p">More details</button>
<div id="p" hidden><a href="#d">Delivery</a></div>
<script>
const b = document.getElementById('more'), p = document.getElementById('p');
b.addEventListener('click', () => { const open = b.getAttribute('aria-expanded') !== 'true'; b.setAttribute('aria-expanded', String(open)); p.hidden = !open; });
</script>`), `document.getElementById('more').click(); const o = !document.getElementById('p').hidden; document.getElementById('more').click(); o && document.getElementById('p').hidden`},
}

// widgetIssueChecks are the checks the widget test raises.
var widgetIssueChecks = []string{ids.FocusLost, ids.DialogFocus, ids.Escape, ids.Menu}

// Each fixture gives exactly the widget issues it was built for;
// the ones done right give none.
func TestWidgets(t *testing.T) {
	html := map[string]string{}
	for path, f := range widgetFixtures {
		html[path] = f.html
	}
	srv := serve(t, &pages{html: html})
	c := launch(t)
	want := map[string][]string{
		"/focus-lost":     {ids.FocusLost + `|#open|After Escape on "Open offer" focus went to a removed element.`},
		"/dialog-nofocus": {ids.DialogFocus + `|#open|The dialog opened by "Newsletter" didn't get focus.`},
		"/dialog-leak":    {ids.DialogFocus + `|#open|The dialog opened by "Filters" let Tab reach #after behind it.`},
		"/escape":         {ids.Escape + `|#open|The dialog opened by "Cookie settings" is still visible 500 ms after Escape.`},
		"/menu-hover":     {ids.Menu + `|#shop|The submenu of "Shop" doesn't open with Tab.`},
		"/native":         nil,
		"/custom-modal":   nil,
		"/nonmodal":       nil,
		"/menu-focus":     nil,
		"/disclosure":     nil,
	}
	for path, f := range widgetFixtures {
		t.Run(strings.TrimPrefix(path, "/"), func(t *testing.T) {
			tab, _ := c.open(srv.URL + path)
			ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(10*time.Second))
			defer cancel()
			ok, err := tab.Context(ctx).Eval(`() => eval(` + jsString(f.selfTest) + `)`)
			if err != nil || !ok.Value.Bool() {
				t.Fatalf("the fixture doesn't do what it claims: %v %v", ok, err)
			}
			tab, p := c.open(srv.URL + path)
			cc := withSaver(runContext(t))
			live(t, New(), cc, tab, p)
			if a := p.Audits["keyboard"]; a.Status != model.AuditOK {
				t.Fatalf("audit %+v, warnings %v", a, cc.Run.Warnings)
			}
			var got []string
			for _, i := range p.Issues {
				if slices.Contains(widgetIssueChecks, i.Check) {
					got = append(got, i.Check+"|"+i.Source+"|"+i.Detail)
				}
			}
			if !slices.Equal(got, want[path]) {
				data, _ := os.ReadFile(filepath.Join(cc.Dir, "keyboard", p.ID+".json"))
				tr, _ := DecodeTrace(data)
				t.Errorf("issues %q\nwant %q\nwidgets %+v\nevents %q", got, want[path], tr.Widgets, tr.Events)
			}
			if p.Keyboard == nil || p.Keyboard.Widgets == 0 || len(p.Keyboard.WidgetKinds) == 0 {
				t.Errorf("summary %+v", p.Keyboard)
			}
		})
	}
}

// jsString quotes s as a JavaScript string literal.
func jsString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`).Replace(s) + "'"
}

// The widget rules, from hand-written results: each issue with its
// Detail, plus the guards that keep each quiet.
func TestClassifyWidgets(t *testing.T) {
	email := "jane" + "@" + "example.test"
	cases := []struct {
		name string
		r    WidgetResult
		want string // check|detail, "" for no issue
	}{
		{"focus lost to the body", WidgetResult{Kind: WidgetDialog, Trigger: "#o", Name: "Open", Opened: true, FocusInside: true,
			EscapeTried: true, EscapeClosed: true, Closed: ClosedEscape, FocusAfter: FocusBody},
			ids.FocusLost + `|After Escape on "Open" focus went to the page body.`},
		{"focus lost with the close control", WidgetResult{Kind: WidgetDialog, Trigger: "#o", Name: "Open", Opened: true, FocusInside: true,
			EscapeTried: true, Closed: ClosedClose, FocusAfter: FocusHidden},
			ids.Escape + `|The dialog opened by "Open" is still visible 500 ms after Escape.` + "\n" +
				ids.FocusLost + `|After Enter on the close control on "Open" focus went to a hidden element.`},
		{"focus lost on toggle, no name", WidgetResult{Kind: WidgetDisclosure, Trigger: "#o", Tag: "button", Opened: true,
			Closed: ClosedToggle, FocusAfter: FocusRemoved},
			ids.FocusLost + `|After toggle on button focus went to a removed element.`},
		{"focus returned", WidgetResult{Kind: WidgetDialog, Trigger: "#o", Name: "Open", Opened: true, FocusInside: true,
			EscapeTried: true, EscapeClosed: true, Closed: ClosedEscape}, ""},
		{"focus after without a close is no issue", WidgetResult{Kind: WidgetDisclosure, Trigger: "#o", Opened: true, FocusAfter: FocusBody}, ""},
		{"dialog without focus", WidgetResult{Kind: WidgetDialog, Trigger: "#o", Name: "Sign up " + email, Opened: true,
			EscapeTried: true, EscapeClosed: true, Closed: ClosedEscape},
			ids.DialogFocus + `|The dialog opened by "Sign up ***@***" didn't get focus.`},
		{"modal leaks", WidgetResult{Kind: WidgetDialog, Trigger: "#o", Name: "Filters", Opened: true, Modal: true, FocusInside: true,
			Leaked: "#after", EscapeTried: true, EscapeClosed: true, Closed: ClosedEscape},
			ids.DialogFocus + `|The dialog opened by "Filters" let Tab reach #after behind it.`},
		{"non-modal may be left", WidgetResult{Kind: WidgetDialog, Trigger: "#o", Opened: true, FocusInside: true, Leaked: "#after",
			EscapeTried: true, EscapeClosed: true, Closed: ClosedEscape}, ""},
		{"menu ignores Escape", WidgetResult{Kind: WidgetMenu, Trigger: "#m", Name: "Shop", Opened: true, OpenedBy: "Enter",
			EscapeTried: true, Closed: ClosedToggle},
			ids.Escape + `|The menu opened by "Shop" is still visible 500 ms after Escape.`},
		{"disclosure has no Escape test", WidgetResult{Kind: WidgetDisclosure, Trigger: "#d", Opened: true, Closed: ClosedToggle}, ""},
		{"submenu doesn't open by key", WidgetResult{Kind: WidgetMenu, Trigger: "#m", Name: "Shop", Submenu: true, Keys: []string{"Enter", "Space", "ArrowDown"}},
			ids.Menu + `|The submenu of "Shop" doesn't open with Enter, Space or ArrowDown.`},
		{"submenu doesn't open by Tab", WidgetResult{Kind: WidgetMenu, Trigger: "#m", Name: "Shop", Link: true, Submenu: true, Keys: []string{"Tab"}},
			ids.Menu + `|The submenu of "Shop" doesn't open with Tab.`},
		{"popup the walk can't see", WidgetResult{Kind: WidgetMenu, Trigger: "#m", Name: "Shop"}, ""},
		{"a disclosure that didn't open", WidgetResult{Kind: WidgetDisclosure, Trigger: "#d"}, ""},
		{"a press left the page", WidgetResult{Kind: WidgetMenu, Trigger: "#m", Submenu: true, Left: true}, ""},
		{"no trigger", WidgetResult{Kind: WidgetMenu, Submenu: true}, ""},
	}
	for _, c := range cases {
		tr := Trace{Schema: Schema, MainIndex: -1, Widgets: []WidgetResult{c.r}}
		var got []string
		for _, i := range Classify(tr, defaults(), "2.1") {
			got = append(got, i.Check+"|"+i.Detail)
			if i.Source != c.r.Trigger {
				t.Errorf("%s: source %q", c.name, i.Source)
			}
		}
		if strings.Join(got, "\n") != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, strings.Join(got, "\n"), c.want)
		}
	}
	i := newIssue(ids.Escape, "2.2", "#a", "d")
	if i.Level != model.Notice || i.WCAG != nil || i.Title != "Escape doesn't close it" {
		t.Errorf("escape issue %+v", i)
	}
	i = newIssue(ids.Menu, "2.2", "#a", "d")
	if i.Level != model.Warning || i.WCAG[0] != "2.1.1" || i.EN301549[0] != "9.2.1.1" {
		t.Errorf("menu issue %+v", i)
	}
}

// Widgets are tested on the first page of each URL group and on the
// pages keyboard.pages names.
func TestWidgetGroups(t *testing.T) {
	var g widgetGroups
	pages := DefaultSettings().Pages
	for _, c := range []struct {
		path string
		want bool
	}{
		{"/", true}, {"/product/a/", true}, {"/product/b/", false}, {"/product/a/", false},
		{"/cart/", true}, {"/cart/", true}, {"/checkout/pay/", true}, {"/about", true}, {"/about?x=1", false},
	} {
		if got := g.claim(c.path, pages); got != c.want {
			t.Errorf("claim(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// With the run's plan, a group's widgets are tested on its first page
// by path, whichever page is walked first; a group the plan lacks still
// takes its first walked page.
func TestWidgetGroupsPlanned(t *testing.T) {
	var g widgetGroups
	pages := DefaultSettings().Pages
	New().Plan(nil) // a module with no pages planned is fine
	g.plan([]string{"/product/c/", "/product/b/", "/product/d/", "/blog/x"})
	for _, c := range []struct {
		path string
		want bool
	}{
		{"/product/d/", false}, {"/product/c/", false}, {"/product/b/", true}, {"/product/b/", true},
		{"/blog/x", true}, {"/news/a", true}, {"/news/b", false}, {"/cart/", true},
	} {
		if got := g.claim(c.path, pages); got != c.want {
			t.Errorf("claim(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// The destructive list keeps its words in every language it has;
// leaves ordinary widgets alone.
func TestDestructiveWidget(t *testing.T) {
	for _, s := range []string{"Delete account", "Log out", "Abmelden", "Zur Kasse", "Se déconnecter", "Cerrar sesión",
		"Acquista ora", "Afrekenen", "Pay", "Buy", "Cancel order", "Empty cart", "?action=remove", "Place order"} {
		if !destructiveWidget.MatchString(s) {
			t.Errorf("%q is not destructive", s)
		}
	}
	for _, s := range []string{"Menu", "Shop", "More details", "Filters", "Close", "Open offer", "Payment methods info", "Display"} {
		if destructiveWidget.MatchString(s) {
			t.Errorf("%q is destructive", s)
		}
	}
}

// A press that takes the page away is recorded and the run is told,
// with the trigger's name or tag.
func TestWidgetLeftPage(t *testing.T) {
	cc := runContext(t)
	w := &walker{cc: cc, page: &model.Page{Path: "/news"}}
	r, ok, err := w.leftPage(context.Background(), WidgetResult{Name: "Newsletter"}, "Enter")
	if !ok || err != nil || !r.Left {
		t.Fatalf("%+v %v %v", r, ok, err)
	}
	_, _, _ = w.leftPage(context.Background(), WidgetResult{Tag: "button"}, "Space")
	want := []string{`keyboard: /news: Enter on "Newsletter" left the page; skipped the rest of that widget`,
		`keyboard: /news: Space on "button" left the page; skipped the rest of that widget`}
	if !slices.Equal(cc.Run.Warnings, want) {
		t.Errorf("warnings %q", cc.Run.Warnings)
	}
}

// neverPressed has widget triggers the walk must leave alone: a
// destructive one, a submit button, a link to another page and one in
// keyboard.ignore. Each records its own press.
const neverPressed = `<!doctype html><html lang="en"><head><title>Never</title></head><body><main>
<button id="del" aria-expanded="false" aria-controls="p1">Delete account</button><div id="p1" hidden>Sure?</div>
<form action="/send" method="post"><button id="send" aria-expanded="false" aria-controls="p2">More</button><div id="p2" hidden>x</div></form>
<a id="away" href="/elsewhere" aria-expanded="false" aria-controls="p3">Elsewhere</a><div id="p3" hidden>x</div>
<div class="chat"><button id="chat" aria-expanded="false" aria-controls="p4">Chat</button><div id="p4" hidden>x</div></div>
<button id="fine" aria-expanded="false" aria-controls="p5">Details</button><div id="p5" hidden>x</div>
<script>
window.pressed = [];
for (const id of ['del', 'send', 'away', 'chat', 'fine']) {
  document.getElementById(id).addEventListener('click', e => { e.preventDefault(); window.pressed.push(id); });
}
document.getElementById('fine').addEventListener('click', () => {
  const b = document.getElementById('fine'), open = b.getAttribute('aria-expanded') !== 'true';
  b.setAttribute('aria-expanded', String(open)); document.getElementById('p5').hidden = !open;
});
</script>
</main></body></html>`

// Only the safe trigger is pressed. With widgets: 0 nothing is; a
// second page of the same group isn't tested again.
func TestWidgetsNeverPressed(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/a/1": neverPressed, "/a/2": neverPressed}})
	c := launch(t)
	m := New()
	m.set.Ignore = []string{".chat"}
	pressed := func(path string, m *Module) ([]string, *model.Page) {
		tab, p := c.open(srv.URL + path)
		live(t, m, withSaver(runContext(t)), tab, p)
		res, err := tab.Eval(`() => window.pressed.join(',')`)
		if err != nil {
			t.Fatal(err)
		}
		if s := res.Value.Str(); s != "" {
			return strings.Split(s, ","), p
		}
		return nil, p
	}
	got, p := pressed("/a/1", m)
	if !slices.Equal(got, []string{"fine", "fine"}) || p.Keyboard.Widgets != 1 {
		t.Errorf("pressed %q, widgets %+v", got, p.Keyboard)
	}
	if got, p := pressed("/a/2", m); got != nil || p.Keyboard.Widgets != 0 {
		t.Errorf("second page of the group: pressed %q, widgets %d", got, p.Keyboard.Widgets)
	}
	none := New()
	none.set.Widgets = 0
	if got, _ := pressed("/a/1", none); got != nil {
		t.Errorf("widgets: 0 pressed %q", got)
	}
}
