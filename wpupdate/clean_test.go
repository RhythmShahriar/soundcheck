// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpupdate

import (
	"bytes"
	"strings"
	"testing"
)

// A slug or version WP-CLI reported is quoted in the copy-paste rollback
// hint. Control characters from the site never reach the terminal.
func TestSummaryQuotesAndCleans(t *testing.T) {
	l := &Log{Target: "https://staging.test", Plugins: []Plugin{{Slug: "evil;rm -rf ~", Old: "1.0 $(id)", New: "1.1",
		Status: Kept, Verdict: "broke", Evidence: "Fatal \x1b[31merror\x1b[0m on /x/"}}}
	sum := strings.Join(l.Summary(), "\n")
	if !strings.Contains(sum, `Roll it back with: wp plugin install 'evil;rm -rf ~' '--version=1.0 $(id)' --force`) {
		t.Errorf("hint isn't quoted:\n%s", sum)
	}
	if strings.ContainsRune(sum, '\x1b') {
		t.Errorf("escape sequence in the summary: %q", sum)
	}
	var b bytes.Buffer
	if err := l.Text(&b); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(b.String(), '\x1b') || !strings.Contains(b.String(), "\n") {
		t.Errorf("text log: %q", b.String())
	}
	var out bytes.Buffer
	if _, err := (cleanWriter{&out}).Write([]byte("[1/2] p\x07 1.0 → 1.1: clean\n")); err != nil || out.String() != "[1/2] p  1.0 → 1.1: clean\n" {
		t.Errorf("progress line = %q, %v", out.String(), err)
	}
}
