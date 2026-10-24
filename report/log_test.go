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
