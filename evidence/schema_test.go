// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package evidence

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/model"
)

// validate checks doc against the part of JSON Schema evidence.v1.json
// uses: type, const, enum, required, properties, additionalProperties
// (false or a schema), items, minimum, pattern and local $ref. It
// returns every problem, as "path: what".
func validate(schema map[string]any, doc any) []string {
	var problems []string
	var walk func(s map[string]any, v any, path string)
	walk = func(s map[string]any, v any, path string) {
		if ref, ok := s["$ref"].(string); ok {
			def, ok := schema["$defs"].(map[string]any)[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
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
			types, ok := t.([]any)
			if !ok {
				types = []any{t}
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
				} else if s["additionalProperties"] == false {
					bad("%s isn't in the schema", k)
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
	case "integer":
		n, ok := v.(float64)
		return ok && n == math.Trunc(n)
	}
	return false
}

// A written pack matches evidence.v1.json whether chained or not; the
// validator catches what the schema forbids.
func TestPackMatchesSchema(t *testing.T) {
	data, err := model.Schemas.ReadFile("schema/evidence.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for i, prev := range []*Ref{nil, {Run: "runs/r0", SHA256: strings.Repeat("a", 64)}} {
		dir, run := saveRun(t, root, "runs", fmt.Sprint("r", i+1), time.Unix(1000, 0), true, target)
		if err := Write(dir, run, prev); err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(must(os.ReadFile(filepath.Join(dir, File))), &doc); err != nil {
			t.Fatal(err)
		}
		if p := validate(schema, doc); len(p) > 0 {
			t.Errorf("pack %d: %v", i, p)
		}
		doc["files"].([]any)[0].(map[string]any)["path"] = "../x"
		doc["previous"] = map[string]any{"run": "elsewhere", "sha256": "x"}
		doc["extra"] = true
		got := strings.Join(validate(schema, doc), "\n")
		for _, want := range []string{`$.files[0].path: "../x" doesn't match`, `$.previous.run: "elsewhere" doesn't match`, "$: extra isn't in the schema"} {
			if !strings.Contains(got, want) {
				t.Errorf("validator missed %q:\n%s", want, got)
			}
		}
	}
}
