// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keyboard

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/internal/focus"
	"github.com/RhythmShahriar/soundcheck/model"
)

// stop is a forward or backward step on the element id, named by sel.
func stop(id int, sel string) Step {
	return Step{Stop: focus.Stop{Selector: sel, BackendNodeID: id, Tag: "a", Connected: true, InViewport: true}}
}

var body = Step{Stop: focus.Stop{Selector: "body", Tag: "body", IsBody: true, Connected: true}}

func frame(id int, host string) Step {
	return Step{Stop: focus.Stop{Selector: "#pay", BackendNodeID: id, Tag: "iframe", IsFrame: true, FrameHost: host}}
}

// A trace's page is its path: no query and no token in a reset link.
func TestTracePage(t *testing.T) {
	token := strings.Repeat("a1B2", 10)
	cases := map[string]string{
		"https://shop.example/?token=abc":                        "/",
		"https://shop.example/cart/?add-to-cart=1#top":           "/cart/",
		"https://shop.example/password/reset/" + token + "?k=1":  "/password/reset/" + "•••••",
		"https://shop.example/account/orders/1234/":              "/account/orders/1234/",
		"https://shop.example/email/verify/7/" + token + "-more": "/email/verify/7/" + "•••••",
	}
	for raw, want := range cases {
		p := &model.Page{URL: raw, Path: raw[len("https://shop.example"):]}
		if got := tracePage(p); got != want {
			t.Errorf("tracePage(%s) = %q, want %q", raw, got, want)
		}
	}
}

// Stops counts distinct elements. Wrapped needs the document's stop
// between the first stop and its return. Frames are summed up by host
// only.
func TestTraceSummary(t *testing.T) {
	tr := Trace{Forward: []Step{stop(1, "#a"), stop(2, "#b"), body, stop(1, "#a")}}
	if tr.Stops() != 2 || !tr.Wrapped() {
		t.Errorf("clean: %d stops, wrapped %v", tr.Stops(), tr.Wrapped())
	}
	tr = Trace{Forward: []Step{stop(1, "#a"), stop(2, "#b"), stop(1, "#a")}}
	if tr.Wrapped() {
		t.Error("a cycle without the document's stop counts as wrapped")
	}
	var steps []Step
	for range 3 {
		steps = append(steps, frame(9, "pay.example:443"))
	}
	steps = append(steps, stop(1, "#a"))
	for range frameTrapPresses {
		steps = append(steps, frame(10, "maps.example"))
	}
	tr = Trace{Forward: steps}
	want := []string{"pay.example:443: 3 stops, trap no", "maps.example: 50 stops, trap yes"}
	if got := tr.FrameSummary(); !slices.Equal(got, want) {
		t.Errorf("frames %q, want %q", got, want)
	}
}

// suspect finds one element held for trap_after presses (50 for a
// frame) or two or three elements in turn twice over. It finds nothing
// in an ordinary walk.
func TestSuspect(t *testing.T) {
	a, b, c, d := stop(1, "#a"), stop(2, "#b"), stop(3, "#c"), stop(4, "#d")
	repeat := func(s Step, n int) []Step { return slices.Repeat([]Step{s}, n) }
	cases := []struct {
		name  string
		steps []Step
		want  []int
	}{
		{"one element", append([]Step{a}, repeat(b, 4)...), []int{2}},
		{"three presses", append([]Step{a}, repeat(b, 3)...), nil},
		{"two in turn", []Step{a, b, c, b, c}, []int{2, 3}},
		{"three in turn", []Step{d, a, b, c, a, b, c}, []int{1, 2, 3}},
		{"four in turn", []Step{a, b, c, d, a, b, c, d}, nil},
		{"document between", []Step{a, b, body, a, b}, nil},
		{"ends on the document", []Step{a, a, a, a, body}, nil},
		{"ordinary walk", []Step{a, b, c, d, body, a}, nil},
		{"frame for 10 presses", repeat(frame(5, "pay.example"), 10), nil},
		{"frame for 50 presses", repeat(frame(5, "pay.example"), frameTrapPresses), []int{5}},
		{"frame in a cycle", []Step{a, frame(5, "x"), a, frame(5, "x")}, nil},
		{"empty", nil, nil},
	}
	for _, c := range cases {
		if got := suspect(c.steps, 4); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The trace file keeps to 2 MB: a huge walk loses steps from its end
// and says it is capped; reading one back holds it to the caps.
func TestTraceCaps(t *testing.T) {
	long := strings.Repeat("x", 5000)
	tr := Trace{Schema: Schema, Page: "/", MainIndex: 1900}
	for i := range maxSteps {
		s := stop(i+1, long)
		s.Name = long
		tr.Forward = append(tr.Forward, s)
		tr.Backward = append(tr.Backward, s)
	}
	data, err := tr.Encode()
	if err != nil || len(data) > MaxTraceBytes {
		t.Fatalf("%d bytes, %v", len(data), err)
	}
	back, err := DecodeTrace(data)
	if err != nil || !back.Capped || len(back.Forward) == 0 {
		t.Fatalf("decoded capped %v, %d steps, %v", back.Capped, len(back.Forward), err)
	}
	if n := len([]rune(back.Forward[0].Selector)); n != maxSelectorRunes || len([]rune(back.Forward[0].Name)) != maxName {
		t.Errorf("selector %d runes, name %d", n, len([]rune(back.Forward[0].Name)))
	}
	if back.MainIndex >= len(back.Forward) {
		t.Errorf("main index %d past %d steps", back.MainIndex, len(back.Forward))
	}
	if _, err := DecodeTrace(make([]byte, MaxTraceBytes+1)); !errors.Is(err, ErrTraceTooBig) {
		t.Errorf("an oversized file: %v", err)
	}
	if _, err := DecodeTrace([]byte(`{"schema":"other/v9"}`)); err == nil {
		t.Error("another schema was read")
	}
	// The fixed top-level keys and a stop's fields at the top level of
	// each entry, as the report reads them.
	small := Trace{Schema: Schema, Page: "/", Forward: []Step{stop(1, "#a"), body}}
	data, _ = small.Encode()
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	for _, k := range []string{"schema", "page", "viewport", "forward", "backward", "main_index", "widgets", "listeners", "held", "events", "capped"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("no %q in %s", k, data)
		}
	}
	var fwd []map[string]any
	_ = json.Unmarshal(raw["forward"], &fwd)
	if fwd[0]["selector"] != "#a" || fwd[1]["is_body"] != true || fwd[0]["backend_node_id"] != 1.0 {
		t.Errorf("forward entries %v", fwd)
	}
}
