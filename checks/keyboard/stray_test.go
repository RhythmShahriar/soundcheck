// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/checks/keyboard/ids"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
	"github.com/RhythmShahriar/soundcheck/model"
)

// strayPage has a control only a click works, plus script.
func strayPage(script string) string {
	return `<!doctype html><html lang="en"><head><title>Stray</title><link rel="icon" href="data:,">
<script>` + script + `</script></head><body><main>
<a id="first" href="#top">Top</a>
<div id="menu" tabindex="0" role="button">Menu</div>
<p id="out"></p>
<script>document.getElementById('menu').addEventListener('click', () => { document.getElementById('out').textContent = 'Menu open'; });</script>
</main></body></html>`
}

// strayWalk walks html on a fresh tab of c. With at > 0 the page sends
// one request of its own in the walk's window number at, as the tail of
// a page's load does on a slow machine. It returns the not-operable
// controls, the trace's listeners, the number of windows the walk
// opened and whether the request was sent.
func strayWalk(t *testing.T, c *chrome, html string, at int) (got []string, ls []Candidate, windows int, sent bool) {
	t.Helper()
	pg := &pages{html: map[string]string{"/": html, "/stray": "ok"}}
	srv := serveIn(t, pg, 40500, 40600)
	tab, err := browser.NewTab(c.sess)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tab.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(20*time.Second))
	defer cancel()
	if err := tab.Context(ctx).Navigate(srv.URL + "/"); err != nil {
		t.Fatal(err)
	}
	if err := tab.Context(ctx).WaitLoad(); err != nil {
		t.Fatal(err)
	}
	p := &model.Page{ID: model.PageID(srv.URL + "/"), URL: srv.URL + "/", Path: "/", Status: 200}
	m := New()
	var n atomic.Int32
	m.walk = func(ctx context.Context, w *walker) (*Trace, error) {
		w.onWindow = func(ctx context.Context, i int) {
			n.Store(int32(i))
			if i == at {
				_, err := w.tab.Context(ctx).Eval(`() => { fetch('/stray'); }`)
				sent = err == nil
			}
		}
		return record(ctx, w)
	}
	cc := withSaver(runContext(t))
	live(t, m, cc, tab, p)
	if a := p.Audits["keyboard"]; a.Status != model.AuditOK {
		t.Fatalf("audit %+v, warnings %v", a, cc.Run.Warnings)
	}
	for _, i := range p.Issues {
		if i.Check == ids.NotOperable {
			got = append(got, i.Source)
		}
	}
	data, _ := os.ReadFile(filepath.Join(cc.Dir, "keyboard", p.ID+".json"))
	tr, _ := DecodeTrace(data)
	if sent && pg.count("/stray") != 1 {
		t.Errorf("/stray loaded %d times, want 1", pg.count("/stray"))
	}
	return got, tr.Listeners, int(n.Load()), sent
}

// A request the page makes once, in whichever window it lands, is no
// answer from the control pressed then: on a slow machine the tail of a
// page's load (a late favicon or a request the browser makes for it)
// comes in the middle of the walk. The mouse-only control is reported
// with the request in each of the windows a walk without it opened: a
// press's own window, the page body's baseline, the control's second
// press and the quiet window after it. So it is on a plain page, on one
// whose page-wide key handler marks the page as used by keyboard and on
// one whose handler pushes a new address on every key.
func TestMouseOnlyStrayRequest(t *testing.T) {
	c := launch(t)
	cases := []struct{ name, script string }{
		{"plain", ``},
		{"class", `document.addEventListener('keydown', function () { document.documentElement.classList.add('kbd-user'); });`},
		{"keypush", `document.addEventListener('keydown', e => history.pushState(null, '', '?n=' + Math.random()));`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The walks share one Chrome, each on a tab of its own.
			t.Parallel()
			html := strayPage(tc.script)
			got, ls, windows, _ := strayWalk(t, c, html, 0)
			if want := []string{"#menu"}; !slices.Equal(got, want) || windows == 0 {
				t.Fatalf("without a stray request: not-operable %q, want %q, %d windows\nlisteners %+v", got, want, windows, ls)
			}
			for at := 1; at <= windows; at++ {
				t.Run(fmt.Sprint(at), func(t *testing.T) {
					t.Parallel()
					got, ls, _, sent := strayWalk(t, c, html, at)
					if !sent {
						t.Fatalf("the request wasn't sent in window %d", at)
					}
					if want := []string{"#menu"}; !slices.Equal(got, want) {
						t.Errorf("not-operable %q, want %q\nlisteners %+v", got, want, ls)
					}
					for _, l := range ls {
						if l.Selector == "#menu" && (!noResponse(l.Enter) || !noResponse(l.Space)) {
							t.Errorf("#menu %+v, want no response to keys", l)
						}
					}
				})
			}
		})
	}
}
