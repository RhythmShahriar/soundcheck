// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyflow

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhythmShahriar/soundcheck/flow"
)

// A dry key flow whose Pay button posts to another host from script
// (a payment gateway's API) holds the post without ending the flow, as
// it does for analytics. The expectation after it then fails; its
// failure names what was held, so it doesn't read as the site's fault.
// The gateway sees nothing. Posting to the site itself ends the flow as
// held, with no note.
func TestKeyFlowNotesOffHostHold(t *testing.T) {
	needChrome(t)
	for _, offHost := range []bool{true, false} {
		t.Run(fmt.Sprintf("off host %v", offHost), func(t *testing.T) {
			var hits atomic.Int32
			gateway := listen(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				fmt.Fprint(w, `<html><body>Thank you</body></html>`)
			}))
			action := "/paid"
			if offHost {
				action = gateway.URL + "/pay"
			}
			site := listen(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `<html><body><button id="pay" onclick="fetch('%s', {method: 'POST', mode: 'no-cors'})">Pay</button>
</body></html>`, action)
			}))
			f, err := flow.Parse([]byte("name: Pay\nkeyboard: true\nsteps:\n  - click: \"#pay\"\n  - expect: {text: Thank you}\n"))
			if err != nil {
				t.Fatal(err)
			}
			page := isolatedTab(t, site.URL+"/")
			res := flow.Play(context.Background(), page, f, keyOptions(site.URL))
			if hits.Load() != 0 {
				t.Errorf("the gateway got %d requests", hits.Load())
			}
			if !offHost {
				if len(res.Failures) != 0 || res.Held == nil || res.Held.Step != 1 {
					t.Errorf("held %+v, failures:\n%s", res.Held, describe(res))
				}
				return
			}
			want := "(the dry run held a POST to " + hostOf(gateway.URL) + " at step 1)"
			if res.Held != nil || len(res.Failures) != 1 || res.Failures[0].Step != 2 ||
				!strings.HasSuffix(res.Failures[0].Err.Error(), want) {
				t.Errorf("held %+v, failures:\n%s\nwant step 2 ending %q", res.Held, describe(res), want)
			}
		})
	}
}

// What watches a key press is gone once the press is judged: after a
// flow of several operated steps the page keeps no watcher in window,
// so a long flow doesn't pile up observers and listeners.
func TestKeyFlowStopsWatching(t *testing.T) {
	needChrome(t)
	site := listen(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body><button id="more" onclick="document.getElementById('n').textContent += '+'">More</button>
<p id="n"></p></body></html>`)
	}))
	steps := strings.Repeat("  - click: \"#more\"\n", 3)
	f, err := flow.Parse([]byte("name: More\nkeyboard: send\nsteps:\n" + steps + "  - expect: {text: +++}\n"))
	if err != nil {
		t.Fatal(err)
	}
	page := isolatedTab(t, site.URL+"/")
	if res := flow.Play(context.Background(), page, f, keyOptions(site.URL)); len(res.Failures) != 0 {
		t.Fatalf("failures:\n%s", describe(res))
	}
	left, err := page.Eval(`() => Object.keys(window).filter(k => k.startsWith('__soundcheckKey')).length`)
	if err != nil {
		t.Fatal(err)
	}
	if n := left.Value.Int(); n != 0 {
		t.Errorf("%d watchers left in window", n)
	}
}
