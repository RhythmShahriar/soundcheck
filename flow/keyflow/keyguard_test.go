// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyflow

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/soundcheck/flow"
)

// autosave sends a draft the second time #terms loses focus: as Tab
// leaves it on the way to Place order in checkoutFlow, the way a
// checkout saves a field or refreshes its totals on focusout.
const autosave = `<script>
let left = 0;
document.getElementById('terms').addEventListener('focusout', () => {
  if (++left === 2) fetch('/draft', {method: 'POST', body: 'x'});
});
</script>`

// A write held while Tab looks for the step's target isn't the target
// working: a mouse-only Place order is still a key failure in a dry
// flow. A reachable one is held at its own request, not the draft.
func TestKeyFlowHoldIsTheStepsOwn(t *testing.T) {
	needChrome(t)
	for _, tc := range []struct {
		name, button string
		fails        bool
	}{
		{"mouse only", placeMouse, true},
		{"reachable", placeButton, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newShop(t, tc.button+autosave)
			res, _ := playOn(t, s, "true", keyOptions(s.URL))
			if !tc.fails {
				if len(res.Failures) != 0 || res.Held == nil || *res.Held != (flow.Held{Step: 10, Method: "POST", Path: "/order/"}) {
					t.Fatalf("failures:\n%s\nheld %+v\ntrace:\n%s", describe(res), res.Held, strings.Join(res.Trace, "\n"))
				}
				return
			}
			if res.Held != nil {
				t.Errorf("held %+v: a write from the Tab walk passed the step", res.Held)
			}
			if len(res.Failures) != 1 {
				t.Fatalf("failures:\n%s\ntrace:\n%s", describe(res), strings.Join(res.Trace, "\n"))
			}
			f := res.Failures[0]
			if f.Step != 10 || f.Key == nil || !strings.HasPrefix(f.Key.Reason, "not reached after 12 Tab stops; focus stayed on ") {
				t.Errorf("failure: %+v %+v", f, f.Key)
			}
			if n := len(s.placed()); n != 0 {
				t.Errorf("%d orders placed", n)
			}
		})
	}
}

// sink is another host that logs what it gets: where a page sends what
// the flow typed when nothing stops it.
type sink struct {
	mu  sync.Mutex
	got []string
}

func (k *sink) requests() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	var out []string
	for _, q := range k.got {
		if q != "/favicon.ico" {
			out = append(out, q)
		}
	}
	return out
}

// Enter and Space send a form as a click does, so press steps and the
// keys a key flow presses keep what the flow read from the environment
// on the site: a form whose action is another host is refused, one on
// the site is sent.
func TestKeysKeepSecretsOnSite(t *testing.T) {
	needChrome(t)
	const fill = "  - fill: {selector: \"#pass\", value: \"${env:SOUNDCHECK_KEYS_CANARY}\"}\n"
	for _, tc := range []struct{ name, keyboard, step string }{
		{"mouse flow Enter in the field", "false", `press: {key: Enter, on: "#pass"}`},
		{"mouse flow Space on the button", "false", `press: {key: Space, on: "#go"}`},
		{"key flow Enter in the field", "send", `press: {key: Enter, on: "#pass"}`},
		{"key flow Space on the button", "send", `press: {key: Space, on: "#go"}`},
		{"key flow click on the field", "send", `click: "#pass"`},
		{"key flow click on the button", "send", `click: "#go"`},
	} {
		for _, offSite := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s off site %v", tc.name, offSite), func(t *testing.T) {
				other := &sink{}
				evil := listen(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					other.mu.Lock()
					other.got = append(other.got, r.URL.RequestURI())
					other.mu.Unlock()
					fmt.Fprint(w, `<html><body>Got it</body></html>`)
				}))
				action := "/done"
				if offSite {
					action = evil.URL + "/collect"
				}
				var mu sync.Mutex
				var done []string
				site := listen(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/done" {
						mu.Lock()
						done = append(done, r.URL.Query().Get("pass"))
						mu.Unlock()
						fmt.Fprint(w, `<html><body>Done</body></html>`)
						return
					}
					fmt.Fprintf(w, `<html><body><form method="get" action="%s">
<input id="q" name="q"><input id="pass" name="pass" type="password"><button id="go">Go</button>
</form></body></html>`, action)
				}))
				f, err := flow.Parse([]byte("name: Keys\nkeyboard: " + tc.keyboard + "\nsteps:\n" + fill + "  - " + tc.step + "\n"))
				if err != nil {
					t.Fatal(err)
				}
				page := isolatedTab(t, site.URL+"/")
				res := flow.Play(context.Background(), page, f, keyOptions(site.URL))
				if strings.Contains(describe(res)+strings.Join(res.Trace, "\n"), canary) {
					t.Error("the canary leaked")
				}
				mu.Lock()
				sent := append([]string(nil), done...)
				mu.Unlock()
				if !offSite {
					if len(res.Failures) != 0 || len(sent) != 1 || sent[0] != canary {
						t.Errorf("failures:\n%s\nsent %d times", describe(res), len(sent))
					}
					return
				}
				want := "not sending what the flow typed: " + hostOf(evil.URL) + " isn't the site being checked"
				if len(res.Failures) != 1 || res.Failures[0].Step != 2 || !strings.HasPrefix(res.Failures[0].Err.Error(), want) {
					t.Errorf("failures:\n%s\nwant step 2 refused", describe(res))
				}
				if hit := other.requests(); len(hit) != 0 {
					t.Errorf("the other host saw %q", hit)
				}
			})
		}
	}
}
