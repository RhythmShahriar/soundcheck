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

	// run.json's pages are in the file it names, one page per line.
	doc := decode(t, must(os.ReadFile(filepath.Join(dir, "run.json"))))
	if doc["schema"] != store.SplitSchema || doc["page_count"] != float64(len(run.Pages)) || len(doc["pages"].([]any)) != 0 {
		t.Errorf("run.json head: schema %v, page_count %v, pages %v", doc["schema"], doc["page_count"], doc["pages"])
	}
	lines := strings.Split(strings.TrimSuffix(string(must(os.ReadFile(filepath.Join(dir, doc["pages_file"].(string))))), "\n"), "\n")
	if len(lines) != len(run.Pages) {
		t.Fatalf("%d page lines, want %d", len(lines), len(run.Pages))
	}
	page := map[string]any{"$ref": "#/$defs/page"}
	if len(validatePage(runSchema, page, map[string]any{"path": 1.0})) == 0 {
		t.Error("a page with a number for its path passed $defs/page")
	}
	for i, line := range lines {
		if p := validatePage(runSchema, page, decode(t, []byte(line))); len(p) != 0 {
			t.Errorf("pages line %d doesn't match $defs/page:\n%s", i+1, strings.Join(p, "\n"))
		}
	}
}

// validatePage checks doc against a part of schema, such as its page
// definition.
func validatePage(schema, part map[string]any, doc any) []string {
	whole := map[string]any{"$defs": schema["$defs"]}
	for k, v := range part {
		whole[k] = v
	}
	return validate(whole, doc)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
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

// eaaSummary is the accessibility summary of an --eaa run, with every
// field set.
func eaaSummary() *model.EAASummary {
	return &model.EAASummary{
		WCAG: "2.1", EN301549: "V3.2.1", Axe: "4.14.0",
		Criteria: model.EAACriteria{
			Failed:          []model.CriterionCount{{ID: "2.1.2", Issues: 1, Pages: 1}},
			NoFailuresFound: []string{"1.1.1"},
			NotTested:       []string{"1.2.3"},
		},
		Keyboard: model.EAAKeyboard{PagesWalked: 2, WidgetsTested: 3, FlowsRun: 0,
			FlowsSkipped: []model.SkippedFlow{{File: "keyboard-checkout.yaml", Reason: "needs --level flows"}}},
		NotAudited:   []model.NotAudited{{Path: "/catalog/", Module: "a11y", Status: model.AuditTimeout, Reason: "axe-core timed out after 45s"}},
		Open:         3,
		EvidenceNote: model.EvidenceNote,
	}
}

// eaaRun is sampleRun made with --eaa: every field EAA mode adds is set.
func eaaRun() *model.Run {
	run := sampleRun()
	run.Options.EAA, run.Options.WCAG = true, "2.1"
	run.Options.A11y = &model.A11yRules{Axe: "4.14.0", AxeSHA256: strings.Repeat("ab", 32), Tags: []string{"wcag2a", "wcag2aa", "wcag21a", "wcag21aa"},
		Rules: []string{"color-contrast", "label"}, MaxElements: 20}
	run.Options.Keyboard = &model.KeyboardOptions{MaxTabs: 150, Widgets: 10, TrapAfter: 4, SkipLink: "expected"}
	run.KeyFlows = &model.KeyFlows{Skipped: []model.SkippedFlow{{File: "keyboard-checkout.yaml", Reason: "needs --level flows"}}}
	trap := model.Issue{Check: "keyboard/trap", Level: model.Error, Title: "Keyboard focus is trapped", Source: "#date",
		WCAG: []string{"2.1.2"}, EN301549: []string{"9.2.1.2"}, Impact: "critical", FocusPath: []string{"#name", "#date", "#date"}}
	trap.Identify()
	p := &run.Pages[0]
	p.Issues = append(p.Issues, trap)
	p.Audits = map[string]model.Audit{
		"a11y":     {Status: model.AuditCapped, Frames: []string{"pay.example"}, CappedRules: map[string]int{"color-contrast": 12}},
		"keyboard": {Status: model.AuditOK},
	}
	p.Keyboard = &model.KeyboardSummary{Stops: 23, Wrapped: true, SkipLink: "a.skip-link", Widgets: 3, Frames: []string{"pay.example: 2 stops, trap no"},
		Evidence: "keyboard/" + model.PageID(p.URL) + ".json", Crops: map[string]string{trap.Key: "eaa/focus/x-1.png"},
		WidgetKinds: []string{"details", "dialog", "menu"}, MS: 900}
	run.EAA = eaaSummary()
	return run
}

// Output of an --eaa run and comparison matches the schemas. run.json
// keeps the run's EAA options and page records. It leaves out the
// summary, which is for output only.
func TestEAAJSONMatchesSchemas(t *testing.T) {
	cmpSchema, runSchema := loadSchema(t, "comparison.v1.json"), loadSchema(t, "run.v1.json")
	doc := jsonOf(t, Result{Run: eaaRun(), FailOn: "error"})
	if p := validate(runSchema, doc); len(p) != 0 {
		t.Errorf("EAA run doesn't match its schema:\n%s", strings.Join(p, "\n"))
	}
	if eaa, ok := doc["eaa"].(map[string]any); !ok || eaa["evidence_note"] != model.EvidenceNote {
		t.Errorf("EAA run output has no eaa summary: %v", doc["eaa"])
	}

	c := sampleComparison("")
	c.Changes = append(c.Changes, model.Change{Class: model.Broke, Check: "keyboard/trap", Title: "Keyboard focus is trapped", Source: "#date",
		Pages: []string{"/"}, WCAG: []string{"2.1.2"}, EN301549: []string{"9.2.1.2"}, Impact: "critical", FocusPath: []string{"#date"}})
	c.EAA = eaaSummary()
	if p := validate(cmpSchema, jsonOf(t, Result{Run: eaaRun(), Comparison: c})); len(p) != 0 {
		t.Errorf("EAA comparison doesn't match its schema:\n%s", strings.Join(p, "\n"))
	}

	// A wrong audit status or impact is caught.
	bad := jsonOf(t, Result{Run: eaaRun()})
	page := bad["pages"].([]any)[0].(map[string]any)
	page["audits"].(map[string]any)["a11y"].(map[string]any)["status"] = "done"
	page["issues"].([]any)[2].(map[string]any)["impact"] = "huge"
	got := strings.Join(validate(runSchema, bad), "\n")
	for _, want := range []string{"$.pages[0].audits.a11y.status: done isn't one of", "$.pages[0].issues[2].impact: huge isn't one of"} {
		if !strings.Contains(got, want) {
			t.Errorf("validator missed %q; got:\n%s", want, got)
		}
	}

	dir := t.TempDir()
	if err := store.SaveRun(dir, eaaRun()); err != nil {
		t.Fatal(err)
	}
	stored := decode(t, must(os.ReadFile(filepath.Join(dir, "run.json"))))
	if p := validate(runSchema, stored); len(p) != 0 {
		t.Errorf("EAA run.json doesn't match its schema:\n%s", strings.Join(p, "\n"))
	}
	if _, ok := stored["eaa"]; ok {
		t.Error("run.json stores the eaa summary, which is for output only")
	}
	if opts := stored["options"].(map[string]any); opts["eaa"] != true || opts["wcag"] != "2.1" || opts["a11y"] == nil || opts["keyboard"] == nil {
		t.Errorf("run.json options lost the EAA settings: %v", opts)
	}
	if stored["key_flows"] == nil {
		t.Error("run.json lost key_flows")
	}
	back, err := store.LoadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if bp := back.Pages[0]; bp.Keyboard == nil || bp.Keyboard.Stops != 23 || !bp.Audits["a11y"].Finished() || bp.Issues[2].Impact != "critical" {
		t.Errorf("stored page lost its EAA records: %+v", bp)
	}
}

// A snapshot and a comparison carry the eaa block too, the comparison's
// taken from its run when only the run has it. The evidence note is
// always in it and its lists are never null.
func TestEAAJSONSnapshotAndCompare(t *testing.T) {
	cmpSchema, runSchema := loadSchema(t, "comparison.v1.json"), loadSchema(t, "run.v1.json")
	snap := eaaRun()
	snap.Name = "eaa-1"
	snap.EAA.EvidenceNote = ""
	snap.EAA.Criteria = model.EAACriteria{}
	doc := jsonOf(t, Result{Run: snap})
	if p := validate(runSchema, doc); len(p) != 0 {
		t.Errorf("EAA snapshot doesn't match its schema:\n%s", strings.Join(p, "\n"))
	}
	eaa, _ := doc["eaa"].(map[string]any)
	if eaa == nil || eaa["evidence_note"] != model.EvidenceNote {
		t.Fatalf("snapshot eaa block lacks the evidence note: %v", doc["eaa"])
	}
	for _, list := range []string{"failed", "no_failures_found", "not_tested"} {
		if _, ok := eaa["criteria"].(map[string]any)[list].([]any); !ok {
			t.Errorf("criteria.%s isn't a list: %v", list, eaa["criteria"])
		}
	}
	if snap.EAA.EvidenceNote != "" {
		t.Error("JSON changed the result it wrote")
	}

	run := eaaRun()
	c := sampleComparison("")
	doc = jsonOf(t, Result{Run: run, Comparison: c})
	if p := validate(cmpSchema, doc); len(p) != 0 {
		t.Errorf("EAA comparison doesn't match its schema:\n%s", strings.Join(p, "\n"))
	}
	if eaa, _ := doc["eaa"].(map[string]any); eaa == nil || eaa["wcag"] != "2.1" {
		t.Errorf("comparison has no eaa block from its run: %v", doc["eaa"])
	}
	if c.EAA != nil {
		t.Error("JSON changed the comparison it wrote")
	}
	var ch map[string]any
	for _, v := range jsonOf(t, eaaComparison())["changes"].([]any) {
		if m := v.(map[string]any); m["check"] == "keyboard/trap" {
			ch = m
		}
	}
	if ch == nil || fmt.Sprint(ch["wcag"]) != "[2.1.2]" || fmt.Sprint(ch["en301549"]) != "[9.2.1.2]" || ch["impact"] != "critical" || len(ch["focus_path"].([]any)) != 8 {
		t.Errorf("trap change lost its EAA fields: %v", ch)
	}

	// Without --eaa there is no eaa block.
	if _, ok := jsonOf(t, Result{Run: sampleRun()})["eaa"]; ok {
		t.Error("a run without --eaa has an eaa block")
	}
}

// A run stored by 1.2, before EAA mode, loads and renders in every
// format: none of the new fields is required.
func TestRun12StillRenders(t *testing.T) {
	run, err := store.LoadRun(filepath.Join("testdata", "run-1.2"))
	if err != nil {
		t.Fatal(err)
	}
	if run.Version != "1.2.2" || len(run.Pages) != 2 || run.Options.WCAG != "" || run.EAA != nil {
		t.Fatalf("1.2 run read back wrong: %+v", run)
	}
	r := Result{Run: run}
	if p := validate(loadSchema(t, "run.v1.json"), jsonOf(t, r)); len(p) != 0 {
		t.Errorf("1.2 run output doesn't match the schema:\n%s", strings.Join(p, "\n"))
	}
	var b bytes.Buffer
	NewPretty(&b, &b, PrettyOptions{Plain: true}).Summary(r, "")
	NewPretty(&b, &b, PrettyOptions{}).Summary(r, "")
	for _, fn := range []func() error{
		func() error { return Markdown(&b, r) },
		func() error { return JUnit(&b, r, "") },
		func() error { return HTML(&b, r) },
	} {
		if err := fn(); err != nil {
			t.Fatal(err)
		}
	}
	out := b.String()
	if !strings.Contains(out, "Form elements must have labels") || strings.Contains(out, "wcag:") || strings.Contains(out, "Automated and keyboard") {
		t.Errorf("1.2 run rendered with EAA lines or without its a11y issue:\n%s", out)
	}
}
