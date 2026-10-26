// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// earlOf writes the EARL of a fixture and decodes it.
func earlOf(t *testing.T, in Input) (map[string]any, string) {
	t.Helper()
	var b bytes.Buffer
	if err := EARL(&b, mustBuild(t, in)); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	return doc, b.String()
}

// The EARL has everything an evaluation the Report Tool saved has: its
// top-level keys, the keys of each step and its JSON-LD context with
// the same meaning for every term.
func TestEARLHasTheReportToolShape(t *testing.T) {
	data, err := os.ReadFile("testdata/wcagem-empty.jsonld")
	if err != nil {
		t.Fatal(err)
	}
	var ref map[string]any
	if err := json.Unmarshal(data, &ref); err != nil {
		t.Fatal(err)
	}
	for name, in := range fixtures(t) {
		doc, _ := earlOf(t, in)
		superset(t, name, "$", ref, doc)
		if doc["type"] != "Evaluation" || doc["reportToolVersion"] != ref["reportToolVersion"] {
			t.Errorf("%s: type %v, version %v", name, doc["type"], doc["reportToolVersion"])
		}
	}
}

// superset fails when got lacks a key of want's objects, or an object
// or a context term of want has another value in got. Values of other
// kinds are the evaluation's own.
func superset(t *testing.T, name, path string, want, got map[string]any) {
	t.Helper()
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("%s: %s.%s is missing", name, path, k)
			continue
		}
		switch w := w.(type) {
		case map[string]any:
			g, ok := g.(map[string]any)
			if !ok {
				t.Errorf("%s: %s.%s isn't an object", name, path, k)
				continue
			}
			superset(t, name, path+"."+k, w, g)
		default:
			if path == "$.@context" && !reflect.DeepEqual(w, g) {
				t.Errorf("%s: context term %s is %v, the tool's is %v", name, k, g, w)
			}
		}
	}
}

// One assertion per criterion, on the website, with the outcome of the
// row and never earl:passed.
func TestEARLAssertions(t *testing.T) {
	f := fixtures(t)
	doc, raw := earlOf(t, f["findings"])
	if strings.Contains(raw, "earl:passed") {
		t.Error("EARL says passed")
	}
	sample := doc["auditSample"].([]any)
	if len(sample) != 50 {
		t.Fatalf("%d assertions, want 50", len(sample))
	}
	got := map[string][2]string{}
	for _, a := range sample {
		a := a.(map[string]any)
		id := a["test"].(map[string]any)["id"].(string)
		if !strings.HasPrefix(id, "WCAG21:") || strings.TrimPrefix(id, "WCAG21:") == "" {
			t.Errorf("test id %q", id)
		}
		if typ := a["subject"].(map[string]any)["type"].([]any); typ[1] != "Website" {
			t.Errorf("%s: subject %v", id, typ)
		}
		if typ := a["result"].(map[string]any)["outcome"].(map[string]any)["type"].([]any); typ[1] == "Pass" {
			t.Errorf("%s: outcome %v", id, typ)
		}
		mode, _ := a["mode"].(string)
		got[id] = [2]string{a["result"].(map[string]any)["outcome"].(map[string]any)["id"].(string), mode}
	}
	for id, want := range map[string][2]string{
		"WCAG21:contrast-minimum":       {outcomeFailed, "earl:automatic"},   // axe
		"WCAG21:no-keyboard-trap":       {outcomeFailed, "earl:semiAuto"},    // keyboard walk
		"WCAG21:non-text-content":       {outcomeCantTell, "earl:automatic"}, // nothing found: not a pass
		"WCAG21:keyboard":               {outcomeCantTell, "earl:semiAuto"},  // axe and keyboard
		"WCAG21:captions-live":          {outcomeUntested, ""},               // needs a person
		"WCAG21:parsing":                {outcomeUntested, ""},               // 4.1.1
		"WCAG21:non-text-contrast":      {outcomeFailed, "earl:manual"},      // the owner found failures
		"WCAG21:focus-order":            {outcomeCantTell, "earl:manual"},    // the owner found none
		"WCAG21:info-and-relationships": {outcomeCantTell, "earl:automatic"}, // no failures
	} {
		if got[id] != want {
			t.Errorf("%s = %v, want %v", id, got[id], want)
		}
	}
	scope := doc["defineScope"].(map[string]any)
	if scope["wcagVersion"] != "2.1" || scope["conformanceTarget"] != "AA" {
		t.Errorf("scope = %v", scope)
	}
	pages := doc["selectSample"].(map[string]any)["structuredSample"].([]any)
	if len(pages) != 5 || pages[0].(map[string]any)["id"] != "https://shop.example/" {
		t.Errorf("sample = %v", pages)
	}

	// WCAG 2.2 has 55 criteria and its own test ids.
	doc, _ = earlOf(t, f["gaps"])
	sample = doc["auditSample"].([]any)
	if len(sample) != 55 || doc["defineScope"].(map[string]any)["wcagVersion"] != "2.2" ||
		!strings.HasPrefix(sample[0].(map[string]any)["test"].(map[string]any)["id"].(string), "WCAG22:") {
		t.Errorf("2.2: %d assertions", len(sample))
	}
}

// A run made logged in lists no pages in the sample; the run's host
// never appears.
func TestEARLPrivate(t *testing.T) {
	in := fixtures(t)["findings"]
	in.Run.Options.LoggedIn = true
	_, raw := earlOf(t, in)
	for _, leak := range []string{"/about/", "/legal/", "127.0.0.1", "37060"} {
		if strings.Contains(raw, leak) {
			t.Errorf("EARL holds %q", leak)
		}
	}
	if !strings.Contains(raw, `"structuredSample": []`) || !strings.Contains(raw, "5 pages were checked logged in") {
		t.Error("private pages listed or not counted")
	}
}
