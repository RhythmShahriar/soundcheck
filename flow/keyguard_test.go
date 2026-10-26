// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"strings"
	"testing"
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
				if len(res.Failures) != 0 || res.Held == nil || *res.Held != (Held{Step: 10, Method: "POST", Path: "/order/"}) {
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
