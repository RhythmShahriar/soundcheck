// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"errors"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/config"
	"gopkg.in/yaml.v3"
)

// fullBlock is the statement: block of the PRD's example config.
const fullBlock = `owner: Example Coffee Ltd
website: https://shop.example
contact:
  email: access@shop.example
  phone: "+49 30 1234567"
  address: Example Str. 1, Berlin
response_days: 10
country: DE
enforcement: |
  Marktüberwachungsstelle der Länder für die Barrierefreiheit von Produkten und Dienstleistungen (MLBF)
scope: [/, /shop/*, /checkout/*, /account/*]
service: Online shop for coffee and equipment
known_issues:
  - criterion: 1.2.2
    text: Product videos have no captions yet.
    until: 2027-03-31
manual:
  - criterion: 1.4.11
    result: no failures found
    by: Jane Doe
    date: 2026-10-20
list_private_paths: false
`

// block parses the body of a statement: block.
func block(t testing.TB, body string) yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Content) == 0 {
		return yaml.Node{}
	}
	return *doc.Content[0]
}

func TestDecodeOwner(t *testing.T) {
	o, err := decodeOwner(block(t, fullBlock))
	if err != nil {
		t.Fatal(err)
	}
	if o.Owner != "Example Coffee Ltd" || o.Website != "https://shop.example" || o.Contact.Email != "access@shop.example" ||
		o.ResponseDays != 10 || o.Country != "DE" || len(o.Scope) != 4 || !strings.Contains(o.Enforcement, "MLBF") ||
		len(o.KnownIssues) != 1 || o.KnownIssues[0].Until != "2027-03-31" ||
		len(o.Manual) != 1 || o.Manual[0] != (ManualCheck{Criterion: "1.4.11", Result: ManualNoFailures, By: "Jane Doe", Date: "2026-10-20"}) {
		t.Errorf("owner = %+v", o)
	}
	if o, err := decodeOwner(yaml.Node{}); err != nil || o.Owner != "" {
		t.Errorf("no block: %+v, %v", o, err)
	}
}

// Every value the draft can't use is an error naming the setting.
func TestDecodeOwnerRejects(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"ownr: x\n", `unknown setting "statement.ownr" (did you mean "statement.owner"?)`},
		{"contact:\n  emial: a@b.example\n", `unknown setting "statement.contact.emial"`},
		{"manual:\n  - criterion: 1.4.3\n    reslt: x\n", `unknown setting "statement.manual.reslt"`},
		{"response_days: ten\n", "statement.response_days"},
		{"response_days: 400\n", "statement.response_days: must be a number of days between 1 and 365"},
		{"response_days: -1\n", "statement.response_days: must be"},
		{"country: de\n", `statement.country: "de" isn't an ISO 3166 country code such as DE, FR or IE (did you mean "DE"?)`},
		{"country: XX\n", `"XX" isn't an ISO 3166 country code`},
		{"website: shop.example\n", `statement.website: "shop.example" isn't the site's address`},
		{"website: ftp://shop.example\n", `isn't the site's address`},
		{"website: https://jane:secret@shop.example\n", "has a user name or password in it"},
		{"website: https://shop.example/?ref=x\n", "has a query or fragment"},
		{"contact:\n  email: not an email\n", `statement.contact.email: "not an email" isn't an email address`},
		{"contact:\n  email: Jane <jane@shop.example>\n", "isn't an email address"},
		{"scope: [shop/*]\n", `statement.scope.0: "shop/*": list page paths that start with /`},
		{"known_issues:\n  - criterion: 9.9.9\n    text: x\n", `statement.known_issues.0.criterion: "9.9.9" isn't a WCAG A or AA success criterion`},
		{"known_issues:\n  - criterion: 1.4.6\n    text: x\n", `"1.4.6" isn't a WCAG A or AA`}, // AAA
		{"known_issues:\n  - criterion: 1.2.2\n", "statement.known_issues.0.text: says what the problem is"},
		{"known_issues:\n  - criterion: 1.2.2\n    text: x\n    until: 31.03.2027\n", `"31.03.2027" isn't a date as YYYY-MM-DD`},
		{"manual:\n  - criterion: 1.4.11\n    result: passed\n    by: J\n    date: 2026-10-20\n", `statement.manual.0.result: "passed" isn't one of failures found, no failures found, not checked`},
		{"manual:\n  - criterion: 1.4.11\n    result: not checked\n    date: 2026-10-20\n", "statement.manual.0.by: names who checked it"},
		{"manual:\n  - criterion: 1.4.11\n    result: not checked\n    by: J\n", `statement.manual.0.date: "" isn't a date`},
		{"owner: \"Example\\x1b[31m Ltd\"\n", "statement.owner: has a control character (U+001B)"},
		{"owner: " + strings.Repeat("x", 301) + "\n", "statement.owner: is 301 characters long"},
		{"owner: [a, b]\n", "statement.owner"},
	} {
		_, err := decodeOwner(block(t, tc.body))
		var se *config.SettingError
		if err == nil || !errors.As(err, &se) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.body, err, tc.want)
		}
	}
}

// Errors point at the line of the bad value.
func TestDecodeOwnerLine(t *testing.T) {
	body := "owner: x\nmanual:\n  - criterion: 1.4.11\n    result: not checked\n    by: J\n    date: 2026-10-20\n  - criterion: 1.4.3\n    result: not checked\n    by: J\n    date: tomorrow\n"
	_, err := decodeOwner(block(t, body))
	var se *config.SettingError
	if !errors.As(err, &se) || se.Line != 10 || se.Key != "statement.manual.1.date" {
		t.Errorf("err = %#v", err)
	}
}

// Whatever the block holds, decoding never panics. What it accepts
// holds no unknown key and passes its own checks again.
func FuzzOwner(f *testing.F) {
	f.Add(fullBlock)
	f.Add("ownr: x\n")
	f.Add("manual: [{criterion: 1.4.3, result: failures found, by: x, date: 2026-01-01}]\n")
	f.Add("contact: {email: a@b.c, extra: 1}\n")
	f.Add("known_issues: [{criterion: [1], text: {a: b}}]\n")
	f.Add("&a [*a, *a, *a]\n")
	f.Add("response_days: 99999999999999999999\n")
	f.Fuzz(func(t *testing.T, body string) {
		var doc yaml.Node
		if yaml.Unmarshal([]byte(body), &doc) != nil || len(doc.Content) == 0 {
			return
		}
		n := *doc.Content[0]
		o, err := decodeOwner(n)
		if err != nil {
			return
		}
		if err := o.validate(n); err != nil {
			t.Errorf("accepted %q but it doesn't validate: %v", body, err)
		}
		if strings.Contains(body, "ownr:") && n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == "ownr" {
					t.Errorf("accepted an unknown key in %q", body)
				}
			}
		}
	})
}
