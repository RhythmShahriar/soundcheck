// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"strings"
	"testing"
)

// Summary prints every hint in order and leaves out empty ones, as the
// snapshot's "full report:" line followed by what to run next.
func TestSummaryHints(t *testing.T) {
	const full, next = "full report: .soundcheck/snapshots/a/report.html", "next: run compare"
	for _, plain := range []bool{true, false} {
		for _, compared := range []bool{true, false} {
			r := Result{Run: sampleRun()}
			if compared {
				r.Comparison = sampleComparison("")
			}
			var buf bytes.Buffer
			NewPretty(&buf, &buf, PrettyOptions{Plain: plain}).Summary(r, full, "", next)
			out := buf.String()
			i, j := strings.Index(out, full), strings.Index(out, next)
			if i < 0 || j < i {
				t.Errorf("plain %v, compared %v: want %q, then %q:\n%s", plain, compared, full, next, out)
			}
			if plain && !strings.Contains(out, full+"\n"+next+"\n") {
				t.Errorf("plain: the hints aren't one per line:\n%s", out)
			}

			buf.Reset()
			NewPretty(&buf, &buf, PrettyOptions{Plain: plain, Quiet: true}).Summary(r, full, next)
			if out := buf.String(); strings.Contains(out, "full report:") || strings.Contains(out, "next:") {
				t.Errorf("plain %v, compared %v: -q prints hints:\n%s", plain, compared, out)
			}
		}
	}
}
