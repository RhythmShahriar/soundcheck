// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

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
	f, err := Parse([]byte("name: More\nkeyboard: send\nsteps:\n" + steps + "  - expect: {text: +++}\n"))
	if err != nil {
		t.Fatal(err)
	}
	page := isolatedTab(t, site.URL+"/")
	if res := Play(context.Background(), page, f, keyOptions(site.URL)); len(res.Failures) != 0 {
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
