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
