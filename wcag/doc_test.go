// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// changesDoc is the docs team's note for this lane. Its criteria tables
// and counts are copied from wcag/criteria.yaml, so they can't drift from
// what the statement prints.
var changesDoc = filepath.Join("..", "docs", "changes", "1.3-statement.md")

// docBlocks are the generated parts of changesDoc, by marker name.
func docBlocks(t *testing.T) map[string]string {
	t.Helper()
	blocks := map[string]string{"counts": countsTable()}
	for _, v := range []string{"2.1", "2.2"} {
		want, err := os.ReadFile(filepath.Join("testdata", "coverage-"+v+".md"))
		if err != nil {
			t.Fatal(err)
		}
		blocks["coverage-"+v] = string(want)
	}
	return blocks
}

// countsTable is the number of A and AA criteria per WCAG version.
func countsTable() string {
	var b strings.Builder
	b.WriteString("| WCAG | Criteria | Level A | Level AA |\n|---|---|---|---|\n")
	for _, v := range []string{"2.0", "2.1", "2.2"} {
		var a, aa int
		for _, c := range Criteria(v) {
			if c.Level == "A" {
				a++
			} else {
				aa++
			}
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d |\n", v, a+aa, a, aa)
	}
	return b.String()
}

// fillBlock puts body between <!-- name --> and <!-- /name --> in doc.
func fillBlock(doc, name, body string) (string, error) {
	open, end := "<!-- "+name+" -->\n", "<!-- /"+name+" -->"
	i := strings.Index(doc, open)
	j := strings.Index(doc, end)
	if i < 0 || j < i {
		return "", fmt.Errorf("%s lacks the markers %q and %q", changesDoc, strings.TrimSpace(open), end)
	}
	return doc[:i+len(open)] + body + doc[j:], nil
}

// checkBlock reports whether doc holds exactly body between the markers.
func checkBlock(doc, name, body string) error {
	filled, err := fillBlock(doc, name, body)
	if err != nil {
		return err
	}
	if filled != doc {
		return fmt.Errorf("%s: the %s block differs from wcag/criteria.yaml (run go test ./wcag -run ChangesDocTables -update)", changesDoc, name)
	}
	return nil
}

func TestChangesDocTables(t *testing.T) {
	data, err := os.ReadFile(changesDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	blocks := docBlocks(t)
	for _, name := range []string{"counts", "coverage-2.1", "coverage-2.2"} {
		if *update {
			if doc, err = fillBlock(doc, name, blocks[name]); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := checkBlock(doc, name, blocks[name]); err != nil {
			t.Error(err)
		}
	}
	if *update {
		if err := os.WriteFile(changesDoc, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// The doc's counts agree with the WCAG numbers everyone quotes.
func TestChangesDocCounts(t *testing.T) {
	got := countsTable()
	for _, row := range []string{"| 2.0 | 38 | 25 | 13 |", "| 2.1 | 50 | 30 | 20 |", "| 2.2 | 55 | 31 | 24 |"} {
		if !strings.Contains(got, row+"\n") {
			t.Errorf("counts table lacks %q:\n%s", row, got)
		}
	}
}

// A doc whose table was edited by hand, or lost its markers, fails.
func TestChangesDocDrift(t *testing.T) {
	body := "| 1.1.1 Non-text Content | A | 9.1.1.1 | partly |\n"
	good := "intro\n<!-- coverage-2.1 -->\n" + body + "<!-- /coverage-2.1 -->\nend\n"
	if err := checkBlock(good, "coverage-2.1", body); err != nil {
		t.Errorf("matching block: %v", err)
	}
	for name, doc := range map[string]string{
		"edited":  strings.Replace(good, "partly", "partly tested", 1),
		"no end":  strings.Replace(good, "<!-- /coverage-2.1 -->", "", 1),
		"no open": strings.Replace(good, "<!-- coverage-2.1 -->\n", "", 1),
		"swapped": "<!-- /coverage-2.1 -->\n" + body + "<!-- coverage-2.1 -->\n",
		"other":   strings.ReplaceAll(good, "2.1", "2.2"),
	} {
		if err := checkBlock(doc, "coverage-2.1", body); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
