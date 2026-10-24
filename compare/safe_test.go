// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// panicky is a module whose Compare writes to a nil map, three calls
// deep.
type panicky struct{}

func (panicky) Name() string { return "panicky" }

func (panicky) Compare(context.Context, *check.CompareContext) []model.Change {
	return outer()
}

func outer() []model.Change { return inner() }

func inner() []model.Change {
	var m map[string]int
	m["boom"]++ //nolint:staticcheck // the panic under test
	return nil
}

// badFilter drops every change in place, then panics.
type badFilter struct{}

func (badFilter) Name() string { return "badfilter" }

func (badFilter) FilterChanges(changes []model.Change) []model.Change {
	clear(changes)
	panic("filter broke")
}

// steady is a module that reports one change.
type steady struct{}

func (steady) Name() string { return "steady" }

func (steady) Compare(context.Context, *check.CompareContext) []model.Change {
	return []model.Change{{Class: model.Changed, Check: "steady/x", Title: "Steady change", Pages: []string{"/"}}}
}

func TestPanickingModulesBecomeWarnings(t *testing.T) {
	php := issue("health/php-error", model.Warning, "PHP Warning: x")
	before := &model.Run{Pages: []model.Page{{Path: "/"}}}
	after := &model.Run{Pages: []model.Page{{Path: "/", Issues: []model.Issue{php}}}}
	cmp := Compare(context.Background(), &check.CompareContext{Before: before, After: after},
		[]check.Module{panicky{}, steady{}, badFilter{}}, "test")
	var titles []string
	for _, c := range cmp.Changes {
		titles = append(titles, c.Title)
	}
	if strings.Join(titles, "|") != "PHP Warning: x|Steady change" {
		t.Errorf("changes = %v, want the generic diff and the other module's", titles)
	}
	if len(cmp.Warnings) != 2 {
		t.Fatalf("warnings = %q", cmp.Warnings)
	}
	stack := regexp.MustCompile(`^panicky: panic in Compare: assignment to entry in nil map \(compare\.inner safe_test\.go:\d+, compare\.outer safe_test\.go:\d+, compare\.panicky\.Compare safe_test\.go:\d+\)$`)
	if !stack.MatchString(cmp.Warnings[0]) {
		t.Errorf("warning = %q", cmp.Warnings[0])
	}
	filter := regexp.MustCompile(`^badfilter: panic in FilterChanges: filter broke \(compare\.badFilter\.FilterChanges safe_test\.go:\d+\)$`)
	if !filter.MatchString(cmp.Warnings[1]) {
		t.Errorf("warning = %q", cmp.Warnings[1])
	}
}
