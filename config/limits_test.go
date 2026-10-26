// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// laughs is a "billion laughs" document: each level holds ten aliases of
// the one before, so twelve levels (120 aliases) expand to 10^13
// strings. key puts it under a block key.
func laughs(key string) string { return laughsOf(key, 12) }

// laughsOf is laughs with the given number of levels.
func laughsOf(key string, levels int) string {
	var b strings.Builder
	if key != "" {
		b.WriteString(key + ":\n")
	}
	pad := ""
	if key != "" {
		pad = "  "
	}
	fmt.Fprintf(&b, "%sl0: &l0 [lol, lol, lol, lol, lol, lol, lol, lol, lol, lol]\n", pad)
	for i := 1; i <= levels; i++ {
		refs := strings.TrimSuffix(strings.Repeat(fmt.Sprintf("*l%d, ", i-1), 10), ", ")
		fmt.Fprintf(&b, "%sl%d: &l%d [%s]\n", pad, i, i, refs)
	}
	return b.String()
}

// A file of aliases is refused before anything expands them, at the
// top level and inside a module's block, which is decoded later by the
// module.
func TestBillionLaughs(t *testing.T) {
	RegisterBlock("laughs")
	// Nine levels hold 90 aliases, under MaxAliases, yet expand to 10^10
	// strings: the expansion bound refuses them with the same message.
	bodies := []string{laughs(""), laughs("statement"), laughs("laughs"), laughsOf("laughs", 9), laughsOf("", 9)}
	for _, body := range bodies {
		start := time.Now()
		_, err := Load(write(t, body))
		if took := time.Since(start); took > 100*time.Millisecond {
			t.Errorf("took %v", took)
		}
		if err == nil || !strings.Contains(err.Error(), "config.yaml uses too many YAML aliases") {
			t.Errorf("%.40q: %v", body, err)
		}
	}
	// Two levels (20 aliases, 1,000 strings) stay under the bound.
	if _, err := Load(write(t, laughsOf("laughs", 2))); err != nil {
		t.Errorf("two levels: %v", err)
	}
	// A few aliases, as a hand-written file might use, still load.
	ok := "pages:\n  include: &shop [/shop/*]\n  extra: [/checkout/]\nignore:\n  - check: visual/page\n    pages: *shop\n"
	c, err := Load(write(t, ok))
	if err != nil || len(c.Ignore) != 1 || c.Ignore[0].Pages[0] != "/shop/*" {
		t.Errorf("one alias: %+v %v", c.Ignore, err)
	}
}

func TestDepthLimit(t *testing.T) {
	RegisterBlock("deep")
	deep := "deep: " + strings.Repeat("[", 30) + strings.Repeat("]", 30) + "\n"
	if _, err := Load(write(t, deep)); err == nil || !strings.Contains(err.Error(), "config.yaml nests settings more than 20 levels deep") {
		t.Errorf("deep: %v", err)
	}
	shallow := "deep: " + strings.Repeat("[", 5) + strings.Repeat("]", 5) + "\n"
	if _, err := Load(write(t, shallow)); err != nil {
		t.Errorf("shallow: %v", err)
	}
}

// A file over 1 MB is refused without being parsed; one at the limit is
// read.
func TestSizeLimit(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "config.yaml")
	body := []byte("# " + strings.Repeat("x", MaxSize-3) + "\n")
	if err := os.WriteFile(big, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(big); err != nil {
		t.Errorf("exactly 1 MB: %v", err)
	}
	if err := os.WriteFile(big, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(big); err == nil || err.Error() != big+" is larger than 1 MB" {
		t.Errorf("1 MB + 1 byte: %v", err)
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err != nil {
		t.Errorf("missing: %v", err)
	}
	if _, err := readLimited(dir); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("a folder: %v", err)
	}
}

func TestIgnoreSource(t *testing.T) {
	c, err := Load(write(t, "ignore:\n  - check: a11y/color-contrast\n    source: \"footer a\"\n  - source: \"#chat *\"\n"))
	if err != nil || c.Ignore[0].Source != "footer a" || c.Ignore[1].Source != "#chat *" {
		t.Errorf("good sources: %+v %v", c.Ignore, err)
	}
	_, err = Load(write(t, "ignore:\n  - check: a/b\n  - check: a11y/label\n    source: \"input[name\"\n"))
	var se *SettingError
	if !errors.As(err, &se) || se.Key != "ignore.2.source" ||
		!strings.HasSuffix(err.Error(), `ignore.2.source: "input[name" isn't a glob: syntax error in pattern`) {
		t.Errorf("bad source: %v", err)
	}
}

// FuzzLoad feeds Load any file: it must return a config or an error,
// quickly, never panic or hang on aliases.
func FuzzLoad(f *testing.F) {
	f.Add([]byte("rate: 2\n"))
	f.Add([]byte(laughs("")))
	f.Add([]byte("ignore:\n  - source: \"[\"\n"))
	f.Add([]byte("a: &a [*a]\n"))
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = Load(path)
	})
}
