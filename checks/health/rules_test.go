// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package health

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/rules"
)

// legacyScreens is how CheckPage found error screens before they became
// rules (rules/builtin), kept to prove the rules report exactly the same.
func legacyScreens(p *model.Page) []model.Issue {
	wpCritical := regexp.MustCompile(`(?i)There has been a critical error on (this|your) website`)
	frameworkErr := regexp.MustCompile(`(?i)Whoops, looks like something went wrong|Symfony\\Component\\ErrorHandler|Ignition|Stack trace:\s*#0|Uncaught (Error|Exception|TypeError|ArgumentCountError)`)
	dbError := regexp.MustCompile(`(?i)Error establishing a database connection|SQLSTATE\[[0-9A-Z]+\]`)
	var out []model.Issue
	add := func(i model.Issue) {
		i.Identify()
		out = append(out, i)
	}
	text := p.Text
	switch {
	case wpCritical.MatchString(text):
		add(model.Issue{Check: "health/wp-critical", Level: model.Error, Title: "WordPress \"critical error\" screen",
			Fix: "A plugin or theme threw a fatal error. Check the admin email WordPress sent, or wp-content/debug.log with WP_DEBUG_LOG on."})
	case dbError.MatchString(text):
		add(model.Issue{Check: "health/database", Level: model.Error, Title: "Database error on the page",
			Fix: "Check the database server and the credentials in wp-config.php or .env."})
	case frameworkErr.MatchString(text) || strings.Contains(strings.ToLower(p.Title), "whoops"):
		add(model.Issue{Check: "health/error-page", Level: model.Error, Title: "Application error page",
			Fix: "Read the exception shown; turn APP_DEBUG/display_errors off in production."})
	case p.Status == 200 && p.TextLength == 0 && !loadsContent(p):
		add(model.Issue{Check: "health/white-screen", Level: model.Error, Title: "Blank page (white screen)",
			Fix: "A PHP fatal error with display_errors off usually causes this; check the error log."})
	}
	return out
}

func TestRulesMatchTheOldScreens(t *testing.T) {
	screens := []string{"health/wp-critical", "health/database", "health/error-page", "health/white-screen"}
	pages := []model.Page{
		// The pages health_test.go checks.
		{Status: 500, Text: "There has been a critical error on this website. Learn more about troubleshooting WordPress."},
		{Status: 200, TextLength: 50, Text: "Whoops, looks like something went wrong."},
		{Status: 200, TextLength: 40, Text: "Error establishing a database connection"},
		{Status: 200, TextLength: 300, Text: `Fatal error: Uncaught Error: Call to undefined function wc_get_x() in /home/site/public_html/wp-content/plugins/shop-ext/ext.php:88 Stack trace: #0 {main} thrown in /home/site/public_html/wp-content/plugins/shop-ext/ext.php on line 88`},
		{Status: 200},
		{Status: 200, Resources: []model.Resource{{URL: "http://x.test/photo.jpg", Type: "Image", Status: 200}}},
		// Overlaps, where the order decides.
		{Status: 500, Text: "There has been a critical error on your website. Fatal error: Uncaught TypeError: x() Stack trace: #0 {main}"},
		{Status: 500, Text: "SQLSTATE[HY000] [2002] Connection refused. Stack trace: #0 /var/www/vendor/laravel/framework/src/Database.php"},
		{Status: 500, Title: "Whoops! There was an error.", Text: "Error establishing a database connection"},
		{Status: 500, Title: "Whoops! There was an error.", Text: "Something broke"},
		{Status: 200, Title: "Whoops", TextLength: 0},
		{Status: 500, Text: "symfony\\component\\errorhandler\\Error\\FatalError ignition"},
		// Pages that are none of them.
		{Status: 200, TextLength: 30, Text: "We fixed a critical error on this site last week."},
		{Status: 200, TextLength: 30, Text: "Fresh coffee, roasted every Monday."},
	}
	for i, p := range pages {
		if p.URL == "" {
			p.URL, p.FinalURL, p.Path = "http://x.test/", "http://x.test/", "/"
		}
		want := legacyScreens(&p)
		Module{}.CheckPage(&check.Context{}, &p)
		var got []model.Issue
		for _, is := range p.Issues {
			if slices.Contains(screens, is.Check) {
				got = append(got, is)
			}
		}
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		if string(g) != string(w) {
			t.Errorf("page %d (%.40q):\n got %s\nwant %s", i, p.Text, g, w)
		}
	}
}

// A project's rule shows up on the page like a built-in one, after them.
func TestCheckPageUsesProjectRules(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"maintenance.yaml": "id: health/maintenance\nmatch: text\npattern: '(?i)briefly unavailable for scheduled maintenance'\nlevel: error\n" +
			"title: WordPress maintenance mode is on\nfix: Delete the .maintenance file in the WordPress folder.\n",
		"maintenance_test.yaml": "positive:\n  - status: 503\n    text: Briefly unavailable for scheduled maintenance. Check back in a minute.\nnegative:\n  - text: Our maintenance plans start at $9.\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &model.Page{URL: "http://x.test/", Path: "/", Status: 503, TextLength: 70, Text: "Briefly unavailable for scheduled maintenance. Check back in a minute."}
	Module{Rules: set}.CheckPage(&check.Context{}, p)
	var checks []string
	for _, i := range p.Issues {
		checks = append(checks, i.Check)
	}
	if !slices.Equal(checks, []string{"health/status", "health/maintenance"}) {
		t.Errorf("issues = %+v", p.Issues)
	}
	if i := p.Issues[len(p.Issues)-1]; i.Key != "health/maintenance|WordPress maintenance mode is on|" || i.Level != model.Error {
		t.Errorf("rule issue = %+v", i)
	}
}

// A project rule in the error-screen group adds its issue but doesn't
// hide the built-in Whoops title check.
func TestProjectErrorScreenKeepsBuiltins(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"oops.yaml": "id: health/oops\nmatch: text\npattern: '(?i)something broke'\nlevel: warning\ngroup: error-screen\n" +
			"title: The shop says something broke\nfix: Look at the shop's log.\n",
		"oops_test.yaml": "positive:\n  - text: Something broke on our end.\nnegative:\n  - text: Fresh coffee.\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &model.Page{URL: "http://x.test/", Path: "/", Status: 200, Title: "Whoops! There was an error.", TextLength: 27, Text: "Something broke on our end."}
	Module{Rules: set}.CheckPage(&check.Context{}, p)
	var checks []string
	for _, i := range p.Issues {
		checks = append(checks, i.Check)
	}
	if !slices.Equal(checks, []string{"health/oops", "health/error-page"}) {
		t.Errorf("issues = %+v", p.Issues)
	}
}

// Every check this package reports from Go is in Checks, so no rule file
// can take its id.
func TestChecksListsEveryGoCheck(t *testing.T) {
	lit := regexp.MustCompile(`Check:\s*"(health/[a-z0-9/-]+)"`)
	files, _ := filepath.Glob("*.go")
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range lit.FindAllStringSubmatch(string(data), -1) {
			found++
			if !slices.Contains(Checks, m[1]) && rules.Builtin().Rule(m[1]) == nil {
				t.Errorf("%s reports %s, which isn't in Checks", f, m[1])
			}
		}
	}
	if found < 10 {
		t.Errorf("found only %d checks; did the code change shape?", found)
	}
	for _, id := range Checks {
		if rules.Builtin().Rule(id) != nil {
			t.Errorf("%s is both a Go check and a built-in rule", id)
		}
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "s.yaml"), []byte("id: health/status\nmatch: status\npattern: '^5'\nlevel: notice\ntitle: Server error\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "s_test.yaml"), []byte("positive:\n  - status: 500\nnegative:\n  - status: 200\n"), 0o600)
	if _, err := LoadRules(dir); err == nil || !strings.Contains(err.Error(), "health/status belongs to a built-in check") {
		t.Errorf("a rule downgrading health/status must be refused: %v", err)
	}
}
