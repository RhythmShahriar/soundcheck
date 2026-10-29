// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"slices"
	"strings"
	"testing"
)

const vendoredAxe = "../checks/a11y/vendor/axe.min.js"

// The table was made from the vendored axe-core. After an axe bump this
// fails until scripts/gen-axe-rules.mjs is run again.
func TestAxeTableMatchesVendor(t *testing.T) {
	src, err := os.ReadFile(vendoredAxe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(src)
	if got := hex.EncodeToString(sum[:]); got != AxeSHA256 {
		t.Fatalf("axe.min.js has SHA-256 %s but the table was made from %s: run node scripts/gen-axe-rules.mjs", got, AxeSHA256)
	}
	if !strings.HasPrefix(string(src), "/*! axe v"+AxeVersion+"\n") {
		t.Errorf("axe.min.js isn't version %s", AxeVersion)
	}
}

func TestAxeRules(t *testing.T) {
	if len(AxeRules) < 90 {
		t.Fatalf("%d rules", len(AxeRules))
	}
	experimental := 0
	for id, r := range AxeRules {
		if len(r.Tags) == 0 {
			t.Errorf("%s has no tags", id)
		}
		if r.Experimental != slices.Contains(r.Tags, "experimental") {
			t.Errorf("%s: Experimental %v, tags %v", id, r.Experimental, r.Tags)
		}
		if r.Experimental {
			experimental++
		}
		for _, c := range r.WCAG {
			if !slices.Contains(r.Tags, "wcag"+strings.ReplaceAll(c, ".", "")) {
				t.Errorf("%s: criterion %s not in its tags", id, c)
			}
		}
		for _, c := range r.EN {
			if !strings.HasPrefix(c, "9.") || !slices.Contains(r.Tags, "EN-"+c) {
				t.Errorf("%s: clause %s not in its tags", id, c)
			}
		}
	}
	if experimental == 0 {
		t.Error("no experimental rule flagged")
	}
	// Spot checks against axe's documentation.
	if r := AxeRules["color-contrast"]; !slices.Equal(r.WCAG, []string{"1.4.3"}) || !slices.Equal(r.EN, []string{"9.1.4.3"}) || r.Experimental {
		t.Errorf("color-contrast: %+v", r)
	}
	if r := AxeRules["css-orientation-lock"]; !r.Experimental || r.Runs("2.1") {
		t.Errorf("css-orientation-lock: %+v", r)
	}
}

func TestAxeTags(t *testing.T) {
	if got := AxeTags("2.1"); !slices.Equal(got, []string{"wcag2a", "wcag2aa", "wcag21a", "wcag21aa"}) {
		t.Errorf("AxeTags(2.1) = %v", got)
	}
	if !slices.Equal(AxeTags(""), AxeTags("2.0")) || AxeTags("3.0") != nil || len(AxeTags("2.2")) != 5 {
		t.Error("AxeTags of 2.0, 3.0 or 2.2")
	}
	r := AxeRules["autocomplete-valid"] // wcag21aa
	if r.Runs("2.0") || !r.Runs("2.1") || !r.Runs("2.2") {
		t.Errorf("autocomplete-valid runs in the wrong versions")
	}
	if r := AxeRules["target-size"]; r.Runs("2.1") || !r.Runs("2.2") {
		t.Errorf("target-size runs in the wrong versions")
	}
	if r := AxeRules["region"]; r.Runs("2.2") {
		t.Error("a best practice runs")
	}
}

// video-caption and audio-caption fail only through their "caption"
// check, which in the vendored axe-core returns false or undefined,
// never true: they can't report a violation, so they test nothing.
// After an axe bump that changes this, revisit captionOnly.
func TestCaptionRulesCantFail(t *testing.T) {
	src, err := os.ReadFile(vendoredAxe)
	if err != nil {
		t.Fatal(err)
	}
	js := string(src)
	for _, id := range []string{"video-caption", "audio-caption"} {
		i := strings.Index(js, `{id:"`+id+`"`)
		if i < 0 {
			t.Fatalf("no rule %s in the vendored axe-core", id)
		}
		end := strings.Index(js[i:], "}")
		if rule := js[i : i+end]; !strings.Contains(rule, `all:[],any:[],none:["caption"]`) {
			t.Errorf("%s: %s", id, rule)
		}
		if CanFail(id) {
			t.Errorf("CanFail(%s) is true", id)
		}
	}
	if !strings.Contains(js, `{id:"caption",evaluate:"caption-evaluate"}`) {
		t.Error("the caption check changed")
	}
	j := strings.Index(js, `"caption-evaluate":`)
	if j < 0 {
		t.Fatal("no caption-evaluate")
	}
	name := strings.TrimSuffix(strings.SplitN(js[j+len(`"caption-evaluate":`):], ",", 2)[0], "}")
	body := "function " + name + "(e,t,r){return!"
	k := strings.Index(js, body)
	if k < 0 {
		t.Fatalf("caption-evaluate %s not found", name)
	}
	if fn := js[k : k+strings.Index(js[k:], "}function")]; !strings.HasSuffix(fn, "&&void 0") {
		t.Errorf("caption-evaluate can now return true: %s", fn)
	}
	if !CanFail("image-alt") {
		t.Error("CanFail(image-alt) is false")
	}
}

// ruleDef returns the definition of the axe rule id in the vendored
// source: the text from its {id:"<id>", up to the next {id:", which is
// the one with tags (a check can share a rule's id).
func ruleDef(t *testing.T, js, id string) string {
	t.Helper()
	var defs []string
	needle := `{id:"` + id + `",`
	for at := 0; ; {
		i := strings.Index(js[at:], needle)
		if i < 0 {
			break
		}
		start := at + i
		rest := js[start+len(needle):]
		end := len(rest)
		if j := strings.Index(rest, `{id:"`); j >= 0 {
			end = j
		}
		if def := js[start : start+len(needle)+end]; strings.Contains(def, "tags:[") {
			defs = append(defs, def)
		}
		at = start + len(needle)
	}
	if len(defs) != 1 {
		t.Fatalf("%s: %d rule definitions in the vendored axe-core", id, len(defs))
	}
	return defs[0]
}

// A reviewOnFail rule turns every failed check into an incomplete
// result and a run keeps violations only, so it can't fail: CanFail
// must say so for each one the vendored axe-core marks, read from its
// source, not from a list kept by hand.
func TestReviewOnlyRules(t *testing.T) {
	src, err := os.ReadFile(vendoredAxe)
	if err != nil {
		t.Fatal(err)
	}
	js := string(src)
	var marked []string
	for id, r := range AxeRules {
		review := strings.Contains(ruleDef(t, js, id), "reviewOnFail:!0")
		if review != r.ReviewOnFail {
			t.Errorf("%s: reviewOnFail %v in axe, %v in the table", id, review, r.ReviewOnFail)
		}
		if review && len(r.WCAG) > 0 {
			marked = append(marked, id)
			if CanFail(id) {
				t.Errorf("CanFail(%s) is true but axe marks it reviewOnFail", id)
			}
		}
	}
	slices.Sort(marked)
	if want := []string{"aria-braille-equivalent", "bypass", "duplicate-id-aria", "frame-title-unique", "no-autoplay-audio"}; !slices.Equal(marked, want) {
		t.Errorf("reviewOnFail WCAG rules %v, want %v", marked, want)
	}
	if !CanFail("button-name") || !CanFail("frame-title") {
		t.Error("a rule that can fail can't")
	}
}
