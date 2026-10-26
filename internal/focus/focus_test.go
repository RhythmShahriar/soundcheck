// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package focus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/browser"
	"github.com/RhythmShahriar/soundcheck/internal/testenv"
)

// Every key has something to press; a key Press doesn't know fails
// before it touches the page.
func TestKeys(t *testing.T) {
	for _, k := range []Key{Tab, ShiftTab, Enter, Space, Escape, ArrowUp, ArrowDown, ArrowLeft, ArrowRight, Home, End} {
		if _, ok := keys[k]; !ok {
			t.Errorf("%s presses nothing", k)
		}
	}
	if len(keys) != 11 {
		t.Errorf("%d keys, want 11", len(keys))
	}
	if err := Press(context.Background(), nil, "Ctrl+W"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("Press(Ctrl+W) = %v", err)
	}
}

// walk is a page whose second stop is inside a shadow root. It lies
// about document.activeElement, which Active must not believe.
const walk = `<!doctype html><html lang="en"><body>
<input id="first">
<x-field></x-field>
<button>Last</button>
<script>
customElements.define('x-field', class extends HTMLElement {
  connectedCallback() { this.attachShadow({mode: 'open'}).innerHTML = '<input name="inner">'; }
});
Object.defineProperty(Document.prototype, 'activeElement', {get() { return document.body; }});
</script>
</body></html>`

// Tab and Shift+Tab move trusted focus and Active follows it into the
// shadow root.
func TestPressAndActive(t *testing.T) {
	if testing.Short() {
		t.Skip("browser test")
	}
	if _, _, err := browser.Find(browser.Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome or Chromium installed: ", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testenv.Timeout(30*time.Second))
	defer cancel()
	b, err := browser.Launch(ctx, browser.Options{NoDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	sess, err := b.Session()
	if err != nil {
		t.Fatal(err)
	}
	tab, err := browser.NewTab(sess)
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.Context(ctx).SetDocumentContent(walk); err != nil {
		t.Fatal(err)
	}
	start, err := Active(ctx, tab)
	if err != nil || !start.IsBody {
		t.Fatalf("before any key: %+v, %v", start, err)
	}
	var got []Stop
	for _, k := range []Key{Tab, Tab, Tab, ShiftTab} {
		if err := Press(ctx, tab, k); err != nil {
			t.Fatal(k, err)
		}
		s, err := Active(ctx, tab)
		if err != nil {
			t.Fatal(k, err)
		}
		got = append(got, s)
	}
	want := []string{"#first", `x-field >>> input[name="inner"]`, "button", `x-field >>> input[name="inner"]`}
	for i, s := range got {
		if s.Selector != want[i] || !s.Connected || s.IsBody || s.BackendNodeID == 0 {
			t.Errorf("stop %d = %+v, want selector %s", i+1, s, want[i])
		}
	}
	if got[0].Tag != "input" || got[2].Tag != "button" || !got[0].InViewport {
		t.Errorf("tags or viewport: %+v", got)
	}
}
