package compare

import (
	"context"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func issue(chk string, level model.Level, title string) model.Issue {
	i := model.Issue{Check: chk, Level: level, Title: title}
	i.Identify()
	return i
}

func TestIssues(t *testing.T) {
	warn := issue("health/php-error", model.Warning, `PHP Warning: Undefined array key "id"`)
	fatal := issue("health/status", model.Error, "Server error 500")
	gone := issue("health/js-error", model.Warning, "JS error: x is not defined")
	before := &model.Run{Pages: []model.Page{
		{Path: "/", Issues: []model.Issue{gone}},
		{Path: "/a"}, {Path: "/b"},
		{Path: "/only-before", Issues: []model.Issue{fatal}},
	}}
	after := &model.Run{Pages: []model.Page{
		{Path: "/", Issues: []model.Issue{warn}},
		{Path: "/a", Issues: []model.Issue{warn}},
		{Path: "/b", Issues: []model.Issue{fatal}},
		{Path: "/only-after", Issues: []model.Issue{fatal}},
	}}
	cmp := Compare(context.Background(), &check.CompareContext{Before: before, After: after}, nil, "test")
	byClass := map[string][]model.Change{}
	for _, c := range cmp.Changes {
		byClass[c.Class] = append(byClass[c.Class], c)
	}
	if b := byClass[model.Broke]; len(b) != 1 || b[0].Title != "Server error 500" || len(b[0].Pages) != 1 || b[0].Pages[0] != "/b" {
		t.Errorf("broke = %+v", b)
	}
	if n := byClass[model.New]; len(n) != 1 || len(n[0].Pages) != 2 {
		t.Errorf("new = %+v (one warning grouped across / and /a)", n)
	}
	if f := byClass[model.Fixed]; len(f) != 1 || f[0].Title != "JS error: x is not defined" {
		t.Errorf("fixed = %+v", f)
	}
	if cmp.Changes[0].Class != model.Broke {
		t.Error("broke sorts first")
	}
	if len(cmp.Warnings) != 1 {
		t.Errorf("warnings = %v (one page from the snapshot wasn't checked)", cmp.Warnings)
	}
}
