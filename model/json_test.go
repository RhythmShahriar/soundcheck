// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package model

import (
	"encoding/json"
	"regexp"
	"testing"
)

func TestChangeID(t *testing.T) {
	a := Change{Class: New, Check: "health/js-error", Title: "JS error: x", Source: "app.js:1", Pages: []string{"/", "/a"}}
	id := ChangeID(a)
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(id) {
		t.Fatalf("ChangeID = %q, want 8 hex digits", id)
	}
	if b := a; ChangeID(b) != id {
		t.Error("the same change got another id")
	}
	reclassified := a
	reclassified.Class, reclassified.Detail = Unstable, "seen once"
	if ChangeID(reclassified) != id {
		t.Error("class and detail must not change the id")
	}
	for name, c := range map[string]Change{
		"check":  {Check: "health/php-error", Title: a.Title, Source: a.Source, Pages: a.Pages},
		"title":  {Check: a.Check, Title: "JS error: y", Source: a.Source, Pages: a.Pages},
		"source": {Check: a.Check, Title: a.Title, Source: "app.js:2", Pages: a.Pages},
		"pages":  {Check: a.Check, Title: a.Title, Source: a.Source, Pages: []string{"/"}},
	} {
		if ChangeID(c) == id {
			t.Errorf("another %s kept the id", name)
		}
	}
	// An issue grouped by its key keeps its id on more pages.
	grouped := a
	grouped.Key = a.Check + "|" + a.Title + "|" + a.Source
	spread := grouped
	spread.Pages = []string{"/", "/a", "/b"}
	if ChangeID(spread) != ChangeID(grouped) {
		t.Error("an issue on one more page got another id")
	}
	other := grouped
	other.Key, other.Title = "health/js-error|JS error: y|app.js:1", "JS error: y"
	if ChangeID(other) == ChangeID(grouped) {
		t.Error("another issue kept the id")
	}
}

func TestIdentifyKeepsIDsUnique(t *testing.T) {
	title := func(before, after string) Change {
		return Change{Class: Changed, Check: "health/title", Title: "Page title changed", Pages: []string{"/"}, Before: before, After: after}
	}
	cs := []Change{title("A", "B"), title("C", "D"), title("C", "D"), {ID: "kept"}}
	identify(cs)
	seen := map[string]bool{}
	for _, c := range cs {
		if c.ID == "" || seen[c.ID] {
			t.Fatalf("ids = %q %q %q %q", cs[0].ID, cs[1].ID, cs[2].ID, cs[3].ID)
		}
		seen[c.ID] = true
	}
	if cs[0].ID != ChangeID(cs[0]) || cs[3].ID != "kept" {
		t.Error("the first change keeps its plain id and a set id stays")
	}
}

func TestSummarize(t *testing.T) {
	for want, c := range map[string]Comparison{
		VerdictClean:       {Changes: []Change{{Class: Fixed}}},
		VerdictChanged:     {Changes: []Change{{Class: New}, {Class: Slower}}},
		VerdictBroke:       {Changes: []Change{{Class: Broke}, {Class: Changed}}},
		VerdictInterrupted: {Changes: []Change{{Class: Broke}}, Interrupted: true},
	} {
		if got := c.Summarize().Verdict; got != want {
			t.Errorf("verdict of %+v = %s, want %s", c.Changes, got, want)
		}
	}
	s := (&Comparison{Changes: []Change{{Class: Broke}, {Class: New}, {Class: New}, {Class: Unstable}}}).Summarize()
	if s.Broke != 1 || s.New != 2 || s.Unstable != 1 || s.Failed != nil {
		t.Errorf("summary = %+v", s)
	}
}

func TestCompleteFillsLists(t *testing.T) {
	c := &Comparison{Changes: []Change{{Class: New, Check: "x/y", Title: "t"}}}
	failed := true
	c.Summary.Failed = &failed
	c.Complete()
	if c.Kind != KindComparison || c.Schema != Schema || c.Warnings == nil || c.Changes[0].Pages == nil || c.Changes[0].ID == "" {
		t.Errorf("incomplete: %+v", c)
	}
	if c.Summary.New != 1 || c.Summary.Failed != &failed {
		t.Errorf("summary = %+v; want counts and the caller's failed kept", c.Summary)
	}
	r := &Run{}
	r.Complete()
	data, _ := json.Marshal(r)
	for _, want := range []string{`"kind":"run"`, `"pages":[]`, `"warnings":[]`} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).Match(data) {
			t.Errorf("run JSON lacks %s: %s", want, data)
		}
	}
	for _, name := range []string{"schema/run.v1.json", "schema/comparison.v1.json"} {
		data, err := Schemas.ReadFile(name)
		if err != nil || !json.Valid(data) {
			t.Errorf("%s: %v (valid JSON: %v)", name, err, json.Valid(data))
		}
	}
}
