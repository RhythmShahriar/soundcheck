// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"

	"github.com/RhythmShahriar/soundcheck/internal/testenv"
	"github.com/RhythmShahriar/soundcheck/model"
)

// walkPage records the trace of a loaded page with settings s.
func walkPage(t *testing.T, tab *rod.Page, p *model.Page, s Settings) *Trace {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(60*time.Second))
	defer cancel()
	w := &walker{cc: withSaver(runContext(t)), tab: tab, page: p, set: s, start: time.Now()}
	tr, err := record(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func forwardSelectors(tr *Trace) []string { return stepSelectors(tr.Forward) }

func stepSelectors(steps []Step) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.Selector)
	}
	return out
}

// datePicker swallows Tab and Shift+Tab between its field and its
// button, so a keyboard user never reaches the link after it. Escape
// doesn't help either.
const datePicker = `<!doctype html><html lang="en"><head><title>Booking</title></head><body><main>
<a id="top" href="#t">Top</a>
<input id="q" aria-label="Search">
<input id="date" aria-label="Date"><button id="pick">Pick</button>
<a id="after" href="#a">After</a> <a id="after2" href="#b">After 2</a>
<script>
const date = document.getElementById('date'), pick = document.getElementById('pick');
for (const el of [date, pick]) {
  el.addEventListener('keydown', e => {
    if (e.key === 'Tab') { e.preventDefault(); (el === date ? pick : date).focus(); }
  });
}
</script></main></body></html>`

// A trap shows as the two stops repeating; the probe counts what it
// keeps out of reach and Escape doesn't get out.
func TestRecordTrap(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/": datePicker}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	tr := walkPage(t, tab, p, DefaultSettings())
	got := forwardSelectors(tr)
	if !slices.Equal(got[:6], []string{"#top", "#q", "#date", "#pick", "#date", "#pick"}) || tr.Capped {
		t.Fatalf("forward %q, capped %v", got, tr.Capped)
	}
	tp := tr.TrapProbe
	if tp == nil || !slices.Equal(tp.Set, []string{"#date", "#pick"}) || tp.EscapeLeft || tp.FocusablesAfter != 2 ||
		!slices.Equal(tp.After, []string{"#after", "#after2"}) {
		t.Fatalf("trap probe %+v", tp)
	}
	for _, s := range tr.Backward[:4] {
		if s.Selector != "#date" && s.Selector != "#pick" {
			t.Errorf("Shift+Tab left the trap: %q", stepSelectors(tr.Backward))
		}
	}
	if tr.MainIndex != 0 {
		t.Errorf("main index %d", tr.MainIndex)
	}
}

// A clean page goes round through the document's own stop and back to
// its first stop, which ends the walk.
func TestRecordClean(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/": links(3, "")}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/?token=abc&x=1")
	tr := walkPage(t, tab, p, DefaultSettings())
	got := forwardSelectors(tr)
	if !slices.Equal(got, []string{"#l0", "#l1", "#l2", "body", "#l0"}) || !tr.Wrapped() || tr.Capped || tr.Stops() != 3 {
		t.Fatalf("forward %q, wrapped %v, capped %v", got, tr.Wrapped(), tr.Capped)
	}
	// Chrome goes from the first stop back to the last one, then round
	// through the document's stop like Tab does.
	if !slices.Equal(stepSelectors(tr.Backward), []string{"#l2", "#l1", "#l0", "body", "#l2"}) {
		t.Errorf("backward %q", stepSelectors(tr.Backward))
	}
	if tr.TrapProbe != nil || tr.Page != "/" || tr.Viewport != "desktop" || tr.Schema != Schema {
		t.Errorf("trace %+v", tr)
	}
	for _, s := range tr.Forward[1:] {
		if s.AtMS <= tr.Forward[0].AtMS-1 || s.AtMS <= 0 {
			t.Errorf("at_ms %d after %d", s.AtMS, tr.Forward[0].AtMS)
		}
	}
	// A page with nothing to focus ends at once.
	srv2 := serve(t, &pages{html: map[string]string{"/": `<!doctype html><html lang="en"><body><p>Nothing here.</p></body></html>`}})
	tab, p = c.open(srv2.URL + "/")
	tr = walkPage(t, tab, p, DefaultSettings())
	if len(tr.Forward) != 2 || tr.Capped || tr.Stops() != 0 {
		t.Errorf("empty page: %q, capped %v", forwardSelectors(tr), tr.Capped)
	}
}

// A mega menu with more links than max_tabs ends capped, with the
// warning that says so. It is never a trap.
func TestRecordCapped(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/": links(500, "")}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	cc := withSaver(runContext(t))
	live(t, New(), cc, tab, p)
	want := "keyboard: /: stopped after 150 Tab stops (keyboard.max_tabs); raise it if the page has more"
	if len(cc.Run.Warnings) != 1 || cc.Run.Warnings[0] != want {
		t.Errorf("warnings %q, want %q", cc.Run.Warnings, want)
	}
	if a := p.Audits["keyboard"]; a.Status != model.AuditCapped || a.Reason != "stopped after 150 Tab stops (keyboard.max_tabs)" {
		t.Errorf("audit %+v", a)
	}
	if p.Keyboard == nil || !p.Keyboard.Capped || p.Keyboard.Stops != 150 || p.Keyboard.Wrapped {
		t.Errorf("summary %+v", p.Keyboard)
	}
	data, err := os.ReadFile(filepath.Join(cc.Dir, "keyboard", p.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	tr, err := DecodeTrace(data)
	if err != nil || !tr.Capped || len(tr.Forward) != 150 || tr.TrapProbe != nil {
		t.Errorf("trace capped %v, %d stops, %v", tr.Capped, len(tr.Forward), err)
	}
}

// Four tabs walking at once each get their own page's order: input goes
// to each tab's own page, not to whichever has the window's focus.
func TestRecordParallelTabs(t *testing.T) {
	html := map[string]string{}
	for i := range 4 {
		var b strings.Builder
		b.WriteString(`<!doctype html><html lang="en"><body>`)
		for j := range 5 {
			fmt.Fprintf(&b, `<a id="p%d-%d" href="#x">%d</a> `, i, j, j)
		}
		b.WriteString(`</body></html>`)
		html[fmt.Sprintf("/p%d", i)] = b.String()
	}
	srv := serve(t, &pages{html: html})
	c := launch(t)
	type open struct {
		tab  *rod.Page
		page *model.Page
	}
	var tabs []open
	for i := range 4 {
		tab, p := c.open(fmt.Sprintf("%s/p%d", srv.URL, i))
		tabs = append(tabs, open{tab, p})
	}
	got := make([]*Trace, 4)
	var wg sync.WaitGroup
	for i, o := range tabs {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(60*time.Second))
			defer cancel()
			w := &walker{cc: withSaver(runContext(t)), tab: o.tab, page: o.page, set: DefaultSettings(), start: time.Now()}
			tr, err := record(ctx, w)
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = tr
		})
	}
	wg.Wait()
	for i, tr := range got {
		if tr == nil {
			continue
		}
		var want []string
		for j := range 5 {
			want = append(want, fmt.Sprintf("#p%d-%d", i, j))
		}
		want = append(want, "body", want[0])
		if s := forwardSelectors(tr); !slices.Equal(s, want) {
			t.Errorf("tab %d: %q, want %q", i, s, want)
		}
	}
}

// skipPage has a skip link, ten navigation links and the main content.
func skipPage(href string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><body>
<a class="skip-link screen-reader-text" href="` + href + `">Skip to content</a><nav>`)
	for i := range 10 {
		fmt.Fprintf(&b, `<a id="n%d" href="/n%d">Nav %d</a> `, i, i, i)
	}
	b.WriteString(`</nav><main id="main"><h1>Shop</h1><a id="in" href="/in">In the content</a></main></body></html>`)
	return b.String()
}

// The skip link is followed with Enter and the next Tab lands in the
// main content; one pointing at a missing id is recorded as such.
func TestRecordSkipLink(t *testing.T) {
	empty := strings.Replace(skipPage("#main"), `<a id="in" href="/in">In the content</a>`, "<p>Text only.</p>", 1)
	srv := serve(t, &pages{html: map[string]string{"/": skipPage("#main"), "/broken": skipPage("#content"), "/empty": empty}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	tr := walkPage(t, tab, p, DefaultSettings())
	sl := tr.SkipLink
	if sl == nil || sl.Selector != "a" || sl.TargetID != "main" || !sl.TargetFound || sl.NextStop != "#in" ||
		!sl.NextInsideOrAfterTarget || sl.Name != "Skip to content" {
		t.Fatalf("skip link %+v", sl)
	}
	if tr.MainIndex != 11 {
		t.Errorf("main index %d, want 11", tr.MainIndex)
	}
	tab, p = c.open(srv.URL + "/broken")
	tr = walkPage(t, tab, p, DefaultSettings())
	if sl := tr.SkipLink; sl == nil || sl.TargetFound || sl.TargetID != "content" || sl.NextInsideOrAfterTarget || sl.NothingAfter {
		t.Errorf("broken skip link %+v", sl)
	}
	// Nothing to focus in or after the main content: the next Tab goes
	// back to the top, which says nothing about the link.
	tab, p = c.open(srv.URL + "/empty")
	tr = walkPage(t, tab, p, DefaultSettings())
	if sl := tr.SkipLink; sl == nil || !sl.TargetFound || !sl.NothingAfter {
		t.Errorf("skip link with nothing after its target %+v", sl)
	}
	if issues := skipLinks(*tr, SkipLinkExpected, "2.1"); len(issues) != 0 {
		t.Errorf("issues %+v", issues)
	}
}

// judged is a page with a stop of each kind the probe judges and the
// focus handlers that try to leave the page or send something.
const judged = `<!doctype html><html lang="en"><body style="margin:0">
<header id="bar" style="position:fixed;top:0;left:0;right:0;height:120px;background:#fff;z-index:10">Shop</header>
<div style="height:130px"></div>
<a id="ok" href="#o">Fine</a>
<a id="off" href="#x" style="position:absolute;left:-9999px;top:200px">Off screen</a>
<a id="clear" href="#y" style="opacity:0">Transparent</a>
<a id="sr" href="#z" style="position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0)">Clipped</a>
<div id="cookie-banner"><button id="consent">OK</button></div>
<button id="ping" onfocus="fetch('/api?k=1', {method: 'POST', body: 'x'})">Ping</button>
<form id="f" method="post" action="/post?x=1"><input id="auto" aria-label="Auto" onfocus="document.getElementById('f').submit()"></form>
<a id="go" href="#g" onfocus="location.href = '/away?token=abc'">Go</a>
<a id="last" href="#l">Last</a>
<a id="under" href="#u" style="position:fixed;top:20px;left:20px;z-index:1">Under the bar</a>
</body></html>`

// Each stop records whether it is hidden, covered or ignored; a Tab
// press that would leave the page or send a form records its effect
// and sends nothing.
func TestRecordJudgesStops(t *testing.T) {
	var sent atomic.Int32
	h := &pages{html: map[string]string{"/": judged}}
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/favicon.ico" {
			t.Logf("sent %s %s", r.Method, r.URL)
			sent.Add(1)
		}
		h.ServeHTTP(w, r)
	}))
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	s := DefaultSettings()
	s.Ignore = []string{"#cookie-banner", `a[title="it's \"quoted\""]`}
	tr := walkPage(t, tab, p, s)
	by := map[string]Step{}
	for _, st := range tr.Forward {
		if _, ok := by[st.Selector]; !ok {
			by[st.Selector] = st
		}
	}
	checks := []struct {
		sel  string
		want func(Step) bool
	}{
		{"#ok", func(s Step) bool { return s.Hidden == "" && !s.Obscured && !s.Ignored && s.Effect == "" }},
		{"#off", func(s Step) bool { return s.Hidden == HiddenOffscreen }},
		{"#clear", func(s Step) bool { return s.Hidden == HiddenOpacity }},
		{"#sr", func(s Step) bool { return s.Hidden == HiddenClip }},
		{"#consent", func(s Step) bool { return s.Ignored }},
		{"#ping", func(s Step) bool { return s.Effect == "" }},
		{"#auto", func(s Step) bool { return s.Effect == EffectForm }},
		{"#go", func(s Step) bool { return s.Effect == EffectNavigation }},
		{"#under", func(s Step) bool { return s.Obscured && s.ObscuredBy == "#bar" && s.Hidden == "" }},
	}
	for _, c := range checks {
		st, ok := by[c.sel]
		if !ok || !c.want(st) {
			t.Errorf("%s: %+v (found %v)", c.sel, st, ok)
		}
	}
	if !slices.Contains(tr.Held, "POST /api") || !slices.Contains(tr.Held, "POST /post") {
		t.Errorf("held %q", tr.Held)
	}
	if !slices.Contains(tr.Events, "navigation stopped: GET /away") {
		t.Errorf("events %q", tr.Events)
	}
	if n := sent.Load(); n != 0 {
		t.Errorf("the walk sent %d requests", n)
	}
	if !tr.Wrapped() {
		t.Errorf("the walk didn't finish: %q", forwardSelectors(tr))
	}
	if strings.Contains(fmt.Sprint(tr.Held, tr.Events), "token") {
		t.Errorf("a query string was kept: %q %q", tr.Held, tr.Events)
	}
}

// A held request or a navigation the page started keeps its path in the
// trace, with a token-like segment (a reset or verify link's key)
// masked, like the page's own path.
func TestRecordMasksTokenPaths(t *testing.T) {
	// Assembled here, so no file holds a token-shaped literal.
	token := strings.Repeat("k9", 8) + "Zq" + strings.Repeat("x7", 6)
	page := `<!doctype html><html lang="en"><body><main>
<button id="reset" onfocus="fetch('/password/reset/` + token + `', {method: 'POST', body: 'x'})">Reset</button>
<a id="verify" href="#v" onfocus="location.href = '/email/verify/7/` + token + `'">Verify</a>
<a id="last" href="#l">Last</a>
</main></body></html>`
	srv := serve(t, &pages{html: map[string]string{"/": page}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	tr := walkPage(t, tab, p, DefaultSettings())
	all := fmt.Sprint(tr.Held, tr.Events)
	if strings.Contains(all, token) {
		t.Fatalf("a token was kept: held %q events %q", tr.Held, tr.Events)
	}
	if !slices.ContainsFunc(tr.Held, func(h string) bool { return strings.HasPrefix(h, "POST /password/reset/") }) {
		t.Errorf("held %q", tr.Held)
	}
	if !slices.ContainsFunc(tr.Events, func(e string) bool { return strings.HasPrefix(e, "navigation stopped: GET /email/verify/7/") }) {
		t.Errorf("events %q", tr.Events)
	}
}

// pathOf and pathOfRaw mask a token-like segment and keep a short one.
func TestPathOfMasksTokens(t *testing.T) {
	token := strings.Repeat("ab12", 6) + "Cd"
	for raw, keep := range map[string]bool{
		"http://h/password/reset/" + token + "?x=1": false,
		"http://h/email/verify/7/" + token:          false,
		"http://h/password/reset/short":             true,
		"http://h/blog/" + token:                    true, // no reset or verify segment before it
	} {
		got := pathOfRaw("POST", raw)
		if strings.Contains(got, token) != (keep && strings.Contains(raw, token)) || strings.Contains(got, "?") {
			t.Errorf("pathOfRaw(%q) = %q", raw, got)
		}
	}
	u, _ := url.Parse("http://h/password/reset/" + token)
	if got := pathOf("GET", u); strings.Contains(got, token) || !strings.HasPrefix(got, "GET /password/reset/") {
		t.Errorf("pathOf %q", got)
	}
}

// autoNavPage loads another view as soon as its select gets focus.
const autoNavPage = `<!doctype html><html lang="en"><body><main>
<label for="view">Show</label> <select id="view"><option>month</option><option>year</option></select>
<a id="plain" href="/other">Other</a>
<script>document.getElementById('view').addEventListener('focus', function () { location.assign('/?view=year'); });</script>
</main></body></html>`

// A navigation a focus handler starts is the Tab press's doing, however
// soon after the load: the stop says so and gets context-change.
func TestRecordNavigationByFocus(t *testing.T) {
	srv := serve(t, &pages{html: map[string]string{"/": autoNavPage}})
	c := launch(t)
	tab, p := c.open(srv.URL + "/")
	tr := walkPage(t, tab, p, DefaultSettings())
	var view, plain *Step
	for i := range tr.Forward {
		switch tr.Forward[i].Selector {
		case "#view":
			view = &tr.Forward[i]
		case "#plain":
			plain = &tr.Forward[i]
		}
	}
	if view == nil || view.Effect != EffectNavigation || !view.ByFocus {
		t.Fatalf("select stop %+v", view)
	}
	if plain == nil || plain.Effect != "" || plain.ByFocus {
		t.Errorf("plain link stop %+v", plain)
	}
	if _, ok := find(Classify(*tr, defaults(), "2.1"), "keyboard/context-change"); !ok {
		t.Error("no context-change")
	}
}
