// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package a11y

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/model"
)

// The note a comparison with a 1.2 snapshot prints names the rules the
// way a real 1.3 run records them: axe's 2.1 rules from the golden plus
// the module's own checks, which no 1.2 snapshot ran either.
func TestBaselineNoteFromRecordedRules(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "compare", "testdata", "run-1.2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var before model.Run
	if err := json.Unmarshal(data, &before); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "rules-2.1.golden"))
	if err != nil {
		t.Fatal(err)
	}
	after := &model.Run{Options: model.Options{Modules: []string{"health", "a11y"}, WCAG: "2.1"}}
	recordRules(&check.Context{Run: after}, auditOptions{Tags: tags("2.1"), Max: 20}, strings.Fields(string(golden)))
	if after.Options.A11y == nil {
		t.Fatal("no rules recorded")
	}
	for _, p := range before.Pages {
		p.Audits = map[string]model.Audit{"a11y": {Status: model.AuditOK}}
		after.Pages = append(after.Pages, p)
	}
	c := compare.Compare(context.Background(), &check.CompareContext{Before: &before, After: after}, nil, "test")
	want := "note: 6 rules didn't run in the baseline (autocomplete-valid, avoid-inline-spacing, label-content-name-mismatch, overlay, placeholder-label, reflow); their issues aren't counted as new. Take a new snapshot to include them"
	if !slices.Contains(c.Warnings, want) {
		t.Errorf("warnings = %q\nwant %q", c.Warnings, want)
	}

	// A 1.3 snapshot recorded the same rules: no note.
	c = compare.Compare(context.Background(), &check.CompareContext{Before: after, After: after}, nil, "test")
	for _, w := range c.Warnings {
		if strings.Contains(w, "didn't run in the baseline") {
			t.Errorf("same rules, yet %q", w)
		}
	}
}
