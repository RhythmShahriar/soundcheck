// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/checks/seo"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/model"
)

// pageIssues are issues as each module that reports page issues makes
// them, keyed the way it keys them. The a11y one carries the key a run
// stores (check and selector, no title), like the runs of 1.2.0 do.
func pageIssues() map[string]model.Issue {
	identified := func(i model.Issue) model.Issue {
		i.Identify()
		return i
	}
	return map[string]model.Issue{
		"health": identified(model.Issue{Check: "health/php-error", Level: model.Warning,
			Title: `PHP Warning: Undefined array key "id"`, Source: "lib.php:6"}),
		"a11y": {Check: "a11y/label", Level: model.Warning, Title: "Form elements must have labels",
			Detail: `<input id="email" name="email">`, Source: "#email", Key: "a11y/label|#email"},
		"speed": identified(model.Issue{Check: "speed/weight", Level: model.Warning, Title: "Page weighs over 3 MB"}),
		"interact": identified(model.Issue{Check: "interact/form-action", Level: model.Error,
			Title: "Form posts to a page that doesn't exist", Source: "form#signup"}),
		"echo": identified(model.Issue{Check: "echo/no-message", Level: model.Notice,
			Title: "Form says sent but no message went out", Source: "form#contact"}),
		"exec": identified(model.Issue{Check: "exec/brand/logo", Level: model.Error, Title: "Logo missing"}),
	}
}

// A page issue every module reports keeps its class when the confirm
// visit sees it again and becomes UNSTABLE when it doesn't, so a real
// one fails --fail-on and a flaky one doesn't.
func TestConfirmPageIssuesPerModule(t *testing.T) {
	for module, found := range pageIssues() {
		t.Run(module, func(t *testing.T) {
			class := model.New
			if found.Level == model.Error {
				class = model.Broke
			}
			page := func(issues ...model.Issue) *model.Run {
				return &model.Run{Pages: []model.Page{{Path: "/form/", Status: 200, Issues: issues}}}
			}
			// The second visit finds the same problem with other details.
			seen := found
			seen.Detail += " (second visit)"
			for name, tc := range map[string]struct {
				again *model.Run
				want  string
			}{
				"real":  {page(seen), class},
				"flaky": {page(), model.Unstable},
			} {
				before := page()
				c := compare.Compare(context.Background(), &check.CompareContext{Before: before, After: page(found)}, nil, "test")
				if len(c.Changes) != 1 || c.Changes[0].Class != class {
					t.Fatalf("%s: before confirming: %+v", name, c.Changes)
				}
				if got := compare.Confirmable(c); len(got) != 1 || got[0] != "/form/" {
					t.Errorf("%s: the page should be visited again: %v", name, got)
				}
				compare.Confirm(c, before, tc.again)
				if len(c.Changes) != 1 || c.Changes[0].Class != tc.want {
					t.Errorf("%s: want %s after confirming: %+v", name, tc.want, c.Changes)
				}
			}
		})
	}
}

// The seo module reports its own changes from the facts each run stores,
// not page issues: a re-visit can't judge them, so the confirm pass
// leaves them. A page that turned noindex stays BROKE with confirm on.
func TestConfirmLeavesSEOChanges(t *testing.T) {
	stored := func(robots string, facts seo.Facts) (*model.Run, string) {
		dir := t.TempDir()
		u := "https://shop.test/p/"
		p := model.Page{ID: model.PageID(u), URL: u, FinalURL: u, Path: "/p/", Status: 200, Robots: robots}
		data, err := json.Marshal(facts)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "seo"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "seo", p.ID+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		return &model.Run{Target: "https://shop.test/", Pages: []model.Page{p},
			Options: model.Options{Modules: []string{"health", "seo"}}}, dir
	}
	good := seo.Facts{Description: "Fresh beans, roasted weekly.", H1: 1}
	gone := good
	gone.Description, gone.H1 = "", 0
	before, bdir := stored("", good)
	after, adir := stored("noindex", gone)
	cc := &check.CompareContext{Before: before, After: after, BeforeDir: bdir, AfterDir: adir}
	c := compare.Compare(context.Background(), cc, []check.Module{seo.Module{}}, "test")
	want := map[string]string{}
	for _, ch := range c.Changes {
		if (ch.Class == model.Broke || ch.Class == model.New) && strings.HasPrefix(ch.Check, "seo/") {
			want[ch.Check] = ch.Class
		}
	}
	if want["seo/robots-meta"] != model.Broke || want["seo/description"] != model.New || want["seo/h1"] != model.New {
		t.Fatalf("the fixture should break the page's SEO: %+v", c.Changes)
	}
	// The re-visit sees none of it: the site was fixed, or flaked.
	compare.Confirm(c, before, &model.Run{Pages: []model.Page{{Path: "/p/", Status: 200}}})
	for _, ch := range c.Changes {
		if cls, ok := want[ch.Check]; ok && ch.Class != cls {
			t.Errorf("%s became %s: the confirm pass can't judge it", ch.Check, ch.Class)
		}
	}
}
