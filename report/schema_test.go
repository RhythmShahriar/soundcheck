// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// validate checks doc against the part of JSON Schema the published
// schemas use: type, const, enum, required, properties,
// additionalProperties, items, minimum, pattern and local $ref. It
// returns every problem, as "path: what".
func validate(schema map[string]any, doc any) []string {
	var problems []string
	var walk func(s map[string]any, v any, path string)
	walk = func(s map[string]any, v any, path string) {
		if ref, ok := s["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/$defs/")
			def, ok := schema["$defs"].(map[string]any)[name].(map[string]any)
			if !ok {
				problems = append(problems, path+": unknown $ref "+ref)
				return
			}
			s = def
		}
		bad := func(format string, args ...any) { problems = append(problems, path+": "+fmt.Sprintf(format, args...)) }
		if c, ok := s["const"]; ok && v != c {
			bad("%v isn't %v", v, c)
		}
		if e, ok := s["enum"].([]any); ok && !slices.Contains(e, v) {
			bad("%v isn't one of %v", v, e)
		}
		if t, ok := s["type"]; ok {
			var types []any
			if one, ok := t.(string); ok {
				types = []any{one}
			} else {
				types = t.([]any)
			}
			if !slices.ContainsFunc(types, func(t any) bool { return isType(v, t.(string)) }) {
				bad("%T isn't %v", v, t)
				return
			}
		}
		if m, ok := s["minimum"].(float64); ok {
			if n, isNum := v.(float64); isNum && n < m {
				bad("%v is below %v", n, m)
			}
		}
		if p, ok := s["pattern"].(string); ok {
			if str, isStr := v.(string); isStr && !regexp.MustCompile(p).MatchString(str) {
				bad("%q doesn't match %s", str, p)
			}
		}
		switch v := v.(type) {
		case map[string]any:
			if req, ok := s["required"].([]any); ok {
				for _, k := range req {
					if _, ok := v[k.(string)]; !ok {
						bad("%s is missing", k)
					}
				}
			}
			props, _ := s["properties"].(map[string]any)
			for k, val := range v {
				if ps, ok := props[k].(map[string]any); ok {
					walk(ps, val, path+"."+k)
				} else if as, ok := s["additionalProperties"].(map[string]any); ok {
					walk(as, val, path+"."+k)
				}
			}
		case []any:
			if is, ok := s["items"].(map[string]any); ok {
				for i, val := range v {
					walk(is, val, fmt.Sprintf("%s[%d]", path, i))
				}
			}
		}
	}
	walk(schema, doc, "$")
	return problems
}

func isType(v any, t string) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		n, ok := v.(float64)
		return ok && n == math.Trunc(n)
	}
	return false
}

func loadSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := model.Schemas.ReadFile("schema/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	return doc
}

func jsonOf(t *testing.T, r Result) map[string]any {
	t.Helper()
	var b bytes.Buffer
	if err := JSON(&b, r); err != nil {
		t.Fatal(err)
	}
	return decode(t, b.Bytes())
}

// The validator itself must catch what the schemas forbid.
func TestValidatorCatchesProblems(t *testing.T) {
	s := loadSchema(t, "comparison.v1.json")
	doc := jsonOf(t, Result{Run: sampleRun(), Comparison: sampleComparison("")})
	if p := validate(s, doc); len(p) != 0 {
		t.Fatalf("valid document rejected: %v", p)
	}
	delete(doc, "summary")
	doc["kind"] = "run"
	doc["changes"].([]any)[0].(map[string]any)["class"] = "worse"
	doc["changes"].([]any)[1].(map[string]any)["id"] = "XYZ"
	doc["pages"] = 1.5
	got := strings.Join(validate(s, doc), "\n")
	for _, want := range []string{"$: summary is missing", "$.kind: run isn't comparison", "$.changes[0].class: worse isn't one of",
		`$.changes[1].id: "XYZ" doesn't match`, "$.pages: float64 isn't integer"} {
		if !strings.Contains(got, want) {
			t.Errorf("validator missed %q; got:\n%s", want, got)
		}
	}
}

func TestJSONMatchesSchemas(t *testing.T) {
	cmpSchema, runSchema := loadSchema(t, "comparison.v1.json"), loadSchema(t, "run.v1.json")
	valid := func(name string, s map[string]any, doc map[string]any) {
		t.Helper()
		if p := validate(s, doc); len(p) != 0 {
			t.Errorf("%s doesn't match its schema:\n%s", name, strings.Join(p, "\n"))
		}
	}
	full := Result{Run: sampleRun(), Comparison: sampleComparison(""), FailOn: "new"}
	valid("comparison", cmpSchema, jsonOf(t, full))
	valid("interrupted comparison", cmpSchema, jsonOf(t, interruptedResult()))
	valid("hostile comparison", cmpSchema, jsonOf(t, hostileResult()))
	valid("run", runSchema, jsonOf(t, Result{Run: sampleRun(), FailOn: "error"}))
	valid("empty run", runSchema, jsonOf(t, Result{Run: &model.Run{}}))

	// What compare and the store write, not only what JSON prints.
	dir := t.TempDir()
	run := sampleRun()
	run.Pages[0].Shots = map[string]model.Shot{"desktop": {File: "shots/a.png", Width: 1, Height: 1, Dynamic: []model.Rect{{X: 1, Y: 2, W: 3, H: 4}}}}
	run.Pages[0].Failed = []model.Request{{URL: "http://brew.test/x.js", Status: 404}}
	if err := store.SaveRun(dir, run); err != nil {
		t.Fatal(err)
	}
	c := compare.Compare(context.Background(), &check.CompareContext{Before: &model.Run{Pages: []model.Page{{Path: "/"}, {Path: "/shop/"}}}, After: run}, nil, "test")
	if err := store.SaveComparison(dir, c); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]map[string]any{"run.json": runSchema, "comparison.json": cmpSchema} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		doc := decode(t, data)
		valid(name, s, doc)
		if _, ok := doc["groups"]; ok && name == "run.json" {
			t.Error("run.json stores the groups, which are for output only")
		}
	}
}

func TestJSONSummaryAndGroups(t *testing.T) {
	doc := jsonOf(t, Result{Run: sampleRun(), Comparison: sampleComparison(""), FailOn: "broke"})
	s := doc["summary"].(map[string]any)
	if s["broke"] != 1.0 || s["new"] != 1.0 || s["fixed"] != 1.0 || s["verdict"] != "broke" || s["failed"] != true {
		t.Errorf("summary = %v", s)
	}
	doc = jsonOf(t, Result{Run: sampleRun(), Comparison: &model.Comparison{Changes: []model.Change{{Class: model.Slower}}}})
	if s := doc["summary"].(map[string]any); s["verdict"] != "changed" || s["failed"] != nil {
		t.Errorf("summary = %v; want changed and no failed without --fail-on", s)
	}

	run := jsonOf(t, Result{Run: sampleRun(), FailOn: "warning"})
	if run["kind"] != "run" {
		t.Errorf("kind = %v", run["kind"])
	}
	rs := run["summary"].(map[string]any)
	if rs["errors"] != 1.0 || rs["warnings"] != 1.0 || rs["notices"] != 1.0 || rs["verdict"] != "errors" || rs["failed"] != true {
		t.Errorf("run summary = %v", rs)
	}
	groups := run["groups"].([]any)
	if len(groups) != 3 {
		t.Fatalf("groups = %v", groups)
	}
	php := groups[1].(map[string]any)
	if php["check"] != "health/php-error" || php["level"] != "warning" || len(php["pages"].([]any)) != 2 || php["key"] == "" {
		t.Errorf("group = %v; want the PHP warning on both pages", php)
	}
}

func TestJSONEmptyComparisonHasLists(t *testing.T) {
	same := func() *model.Run { return &model.Run{Pages: []model.Page{{Path: "/", Status: 200}}} }
	c := compare.Compare(context.Background(), &check.CompareContext{Before: same(), After: same()}, nil, "test")
	var b bytes.Buffer
	if err := JSON(&b, Result{Run: same(), Comparison: c}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"changes": []`, `"warnings": []`, `"verdict": "clean"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("JSON lacks %s:\n%s", want, b.String())
		}
	}
	// A comparison read from an older comparison.json has nil lists.
	b.Reset()
	if err := JSON(&b, Result{Run: same(), Comparison: &model.Comparison{}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"changes": []`) || !strings.Contains(b.String(), `"kind": "comparison"`) {
		t.Errorf("old comparison isn't completed:\n%s", b.String())
	}
}

func TestChangeIDsStable(t *testing.T) {
	runs := func() (*model.Run, *model.Run) {
		before, after := &model.Run{}, &model.Run{}
		for _, p := range []string{"/", "/a", "/b"} {
			before.Pages = append(before.Pages, model.Page{Path: p})
			i := model.Issue{Check: "health/php-error", Level: model.Warning, Title: "PHP Warning: " + p}
			i.Identify()
			after.Pages = append(after.Pages, model.Page{Path: p, Issues: []model.Issue{i}})
		}
		return before, after
	}
	ids := func() []string {
		b, a := runs()
		c := compare.Compare(context.Background(), &check.CompareContext{Before: b, After: a}, nil, "test")
		var out []string
		for _, ch := range c.Changes {
			out = append(out, ch.ID)
		}
		return out
	}
	first, second := ids(), ids()
	if len(first) != 3 || !slices.Equal(first, second) {
		t.Fatalf("ids differ between identical compares: %v and %v", first, second)
	}
	if len(slices.Compact(slices.Sorted(slices.Values(first)))) != 3 {
		t.Errorf("different changes share an id: %v", first)
	}
}
