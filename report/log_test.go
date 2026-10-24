// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"testing"
)

// -q shows results only: status lines ("~") go, lines that need the
// user's attention ("!") stay; without -q both show.
func TestLogQuietHidesStatusLines(t *testing.T) {
	for _, tc := range []struct {
		quiet bool
		want  string
	}{
		{false, "Browser: Chrome 140\nhttps refused; using http\n"},
		{true, "https refused; using http\n"},
	} {
		var out, errOut bytes.Buffer
		p := NewPretty(&out, &errOut, PrettyOptions{Plain: true, Quiet: tc.quiet})
		p.Log("~Browser: Chrome 140")
		p.Log("!https refused; using http")
		p.Log("a verbose-only line")
		if errOut.String() != tc.want {
			t.Errorf("quiet %v: %q, want %q", tc.quiet, errOut.String(), tc.want)
		}
	}
}

// The closing Skipped line gives the final counts, largest first, so
// pages left out after the Pages line still show in the terminal.
func TestSkippedLine(t *testing.T) {
	for _, tc := range []struct {
		skipped map[string]int
		quiet   bool
		want    string
	}{
		{map[string]int{"other site": 3, "robots-unavailable": 12}, false, "Skipped: 12 (robots unavailable), 3 (other site)\n"},
		{map[string]int{"other site": 2, "grouped": 2, "page limit": 0}, false, "Skipped: 2 (grouped), 2 (other site)\n"},
		{map[string]int{}, false, ""},
		{map[string]int{"other site": 1}, true, ""},
	} {
		var out, errOut bytes.Buffer
		p := NewPretty(&out, &errOut, PrettyOptions{Plain: true, Quiet: tc.quiet})
		p.Skipped(tc.skipped)
		if out.String() != tc.want {
			t.Errorf("%v (quiet %v): %q, want %q", tc.skipped, tc.quiet, out.String(), tc.want)
		}
	}
}
