// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/soundcheck/compare"
	"github.com/RhythmShahriar/soundcheck/model"
)

// ignoredResult is the sample comparison with its BROKE and NEW changes
// ignored by rules.
func ignoredResult() Result {
	c := sampleComparison("")
	compare.ApplyIgnore(c, []compare.IgnoreRule{
		{Check: "health/status", Reason: "the shop is being rebuilt"},
		{Check: "health/php-*", Pages: []string{"/shop/"}},
	}, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	return Result{Run: sampleRun(), Comparison: c, FailOn: "broke"}
}

func TestIgnoredNeverFail(t *testing.T) {
	r := ignoredResult()
	if FailOn(r, "broke") {
		t.Error("an ignored BROKE change failed the build")
	}
	if !FailOn(r, "new") {
		t.Error("the PHP warning on / isn't ignored and should fail at new")
	}
	if s := r.Comparison.Summary; s.Ignored != 2 || s.Broke != 0 || s.New != 1 || s.Verdict != model.VerdictChanged {
		t.Errorf("summary = %+v", s)
	}
}

func TestIgnoredInOutput(t *testing.T) {
	r := ignoredResult()
	var plain bytes.Buffer
	NewPretty(&plain, &plain, PrettyOptions{Plain: true}).Summary(r, "")
	if !strings.Contains(plain.String(), " · 2 changes ignored · ") || strings.Contains(plain.String(), "[BROKE]") {
		t.Errorf("plain output:\n%s", plain.String())
	}
	var md bytes.Buffer
	if err := Markdown(&md, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), "2 changes ignored") {
		t.Errorf("markdown lacks the ignored count:\n%s", md.String())
	}
	valid := validate(loadSchema(t, "comparison.v1.json"), jsonOf(t, r))
	if len(valid) != 0 {
		t.Errorf("JSON with ignored changes doesn't match the schema:\n%s", strings.Join(valid, "\n"))
	}
	doc := jsonOf(t, r)
	ignored, _ := doc["ignored"].([]any)
	if len(ignored) != 2 || ignored[0].(map[string]any)["reason"] != "the shop is being rebuilt" {
		t.Errorf("ignored = %v", doc["ignored"])
	}
}
