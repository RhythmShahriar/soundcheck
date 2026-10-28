// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Soundcheck gives evidence, never a verdict: no text it prints may say
// a site complies, conforms, is certified, passes WCAG, is fully
// accessible or is guaranteed anything (PRD 11.4). The exceptions are
// fixed text that says the opposite.

// banned matches the words no output may use.
var banned = regexp.MustCompile(`(?i)complian|conform|certif|passes wcag|fully accessible|guarantee`)

// certificate is the TLS kind (-k, "TLS certificate not verified for
// this run"), which stays.
var certificate = regexp.MustCompile(`(?i)\bcertificates?\b`)

// claim returns the first banned word in s once the allowed phrases and
// TLS certificates are taken out, or "".
func claim(s string, allowed []string) string {
	for _, a := range allowed {
		s = strings.ReplaceAll(s, a, "")
	}
	return banned.FindString(certificate.ReplaceAllString(s, ""))
}

// allowedPhrases are the fixed text that may hold a banned word because
// it denies what the word claims, plus the EARL vocabulary of the W3C
// Report Tool, which is a key name, not a claim. The address of
// WCAG-EM, the W3C's method for a person's review, is allowed too.
func allowedPhrases() []string {
	out := []string{model.EvidenceNote, "not a compliance guarantee", StatusOptions,
		"https://www.w3.org/WAI/test-evaluate/conformance/wcag-em/",
		`"fully compliant", "partially compliant" or "not compliant"`,
		"conformanceTarget", "WCAG2A-Conformance", "WCAG2AA-Conformance", "WCAG2AAA-Conformance"}
	return append(out, Limits...)
}

// wordingDirs are the folders whose text reaches people; wordingFiles
// are single files, paths from the module root. Folders a lane hasn't made yet
// are skipped, so each lane's text is covered as it merges.
var (
	wordingDirs  = []string{"cmd", "report", "clientreport", "statement", "wcag", "flow/templates"}
	wordingFiles = []string{"README.md", "action.yml", "testbed/README.md", "docs/benchmark.md"}
)

// wordingHits lists every banned word in the text files under root as
// "file:line: text". Go files count by their string literals only;
// test files, testdata and vendor are skipped.
func wordingHits(t *testing.T, root string) []string {
	t.Helper()
	allowed := allowedPhrases()
	var hits []string
	check := func(file string, line int, s string) {
		if m := claim(s, allowed); m != "" {
			hits = append(hits, file+":"+strconv.Itoa(line)+": "+strings.TrimSpace(m))
		}
	}
	scan := func(path string) {
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		switch ext := filepath.Ext(path); {
		case strings.HasSuffix(path, "_test.go"):
		case ext == ".go":
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Errorf("%s: %v", rel, err)
				return
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					s = lit.Value
				}
				check(rel, fset.Position(lit.Pos()).Line, s)
				return true
			})
		case ext == ".md", ext == ".tmpl", ext == ".yml", ext == ".yaml":
			data, err := os.ReadFile(path) //nolint:gosec // files of the module
			if err != nil {
				t.Error(err)
				return
			}
			for i, l := range strings.Split(string(data), "\n") {
				check(rel, i+1, l)
			}
		}
	}
	for _, d := range wordingDirs {
		top := filepath.Join(root, filepath.FromSlash(d))
		if _, err := os.Stat(top); err != nil {
			continue
		}
		err := filepath.WalkDir(top, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() && (e.Name() == "testdata" || e.Name() == "vendor" || e.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if !e.IsDir() {
				scan(path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range wordingFiles {
		if path := filepath.Join(root, f); fileExists(path) {
			scan(path)
		}
	}
	slices.Sort(hits)
	return hits
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// TestWording walks the module's text. Every merge runs it, so a lane's
// new help text or template is checked too.
func TestWording(t *testing.T) {
	for _, h := range wordingHits(t, "..") {
		t.Errorf("%s: Soundcheck gives evidence, not a verdict; reword it (see PRD 11.4)", h)
	}
}

// The test finds what it is for: a planted "your site is compliant"
// and friends fail it, by file and line, while comments, test files,
// TLS certificates and the fixed denials pass.
func TestWordingCatchesAPlantedClaim(t *testing.T) {
	got := wordingHits(t, filepath.Join("testdata", "wording"))
	want := []string{
		"README.md:3: complian",
		"cmd/tool/main.go:7: passes WCAG",
		"cmd/tool/main.go:8: fully accessible",
		"report/page.tmpl:1: Certif",
	}
	if !slices.Equal(got, want) {
		t.Errorf("hits:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// What the renderers print passes the same test; it names the
// standard the criteria come from.
func TestDraftWording(t *testing.T) {
	allowed := allowedPhrases()
	for name, in := range fixtures(t) {
		for _, layout := range Layouts {
			in.Layout = layout
			d := mustBuild(t, in)
			for ext, s := range render(t, d) {
				if ext == "md" || ext == "html" {
					if want := wantStandard(in); !strings.Contains(s, want) {
						t.Errorf("%s %s %s lacks %q", name, layout, ext, want)
					}
				}
				if ext == "html" {
					s = html.UnescapeString(s)
				}
				if m := claim(s, allowed); m != "" {
					t.Errorf("%s %s %s says %q", name, layout, ext, m)
				}
			}
		}
	}
}

func wantStandard(in Input) string {
	if in.Run.Options.WCAG == "2.2" {
		return "WCAG 2.2 level AA / EN 301 549 V4.1.1"
	}
	return "WCAG 2.1 level AA / EN 301 549 V3.2.1"
}
