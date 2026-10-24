// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package browser

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// needChrome skips tests that launch a real browser where none is
// installed.
func needChrome(t *testing.T) {
	t.Helper()
	if _, _, err := Find(Options{NoDownload: true}); err != nil {
		t.Skip("no Chrome installed:", err)
	}
}

func TestLaunchLogsTheVersion(t *testing.T) {
	needChrome(t)
	var logs []string
	b, err := Launch(context.Background(), Options{NoDownload: true, Log: func(m string) { logs = append(logs, m) }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`).MatchString(b.Version) {
		t.Errorf("Version = %q", b.Version)
	}
	if len(logs) == 0 || logs[len(logs)-1] != "Browser: Chrome "+b.Version+" ("+b.Bin+")" {
		t.Errorf("logs = %q", logs)
	}
	if strings.Contains(strings.Join(logs, "\n"), "WARNING") {
		t.Errorf("warned with the sandbox on: %q", logs)
	}
}
