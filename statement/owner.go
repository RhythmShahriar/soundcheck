// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"fmt"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RhythmShahriar/soundcheck/config"
	"github.com/RhythmShahriar/soundcheck/wcag"
	"gopkg.in/yaml.v3"
)

// Manual results the owner can record for a criterion
// (ManualCheck.Result).
const (
	ManualFailures   = "failures found"
	ManualNoFailures = "no failures found"
	ManualNotChecked = "not checked"
)

// manualResults are the ManualCheck.Result values, in the order the
// error message lists them.
var manualResults = []string{ManualFailures, ManualNoFailures, ManualNotChecked}

// Length limits on the owner's text. The draft is a page, not a store:
// a value past these is a mistake (a pasted file), not a statement.
const (
	maxLine = 300  // one-line values: owner, website, email, dates
	maxText = 4000 // free text: enforcement, service, known issue text
	maxList = 500  // entries in scope, known_issues and manual
)

// countries are the ISO 3166-1 alpha-2 codes.
var countries = strings.Fields(`
AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ
BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR
CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR
GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU
ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ
LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ
MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF
PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI
SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR
TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW`)

// DecodeOwner reads the statement: block of cfg strictly: an unknown
// key, a value of the wrong type or an invalid value is a
// config.SettingError naming the setting and its line. A file without
// the block gives the zero Owner; Build then asks for the required
// keys.
func DecodeOwner(cfg config.Config) (Owner, error) {
	return decodeOwner(cfg.Block("statement"))
}

func decodeOwner(node yaml.Node) (Owner, error) {
	var o Owner
	if err := config.DecodeBlock("statement", node, &o); err != nil {
		return Owner{}, err
	}
	if err := o.validate(node); err != nil {
		return Owner{}, err
	}
	return o, nil
}

// validate checks the values DecodeBlock can't: formats, ranges and
// criterion numbers. node gives the lines for the errors.
func (o *Owner) validate(node yaml.Node) error {
	bad := func(msg string, path ...string) error {
		return &config.SettingError{Key: "statement." + strings.Join(path, "."), Line: lineOf(node, path...), Msg: msg}
	}
	for _, f := range []struct {
		key, value string
		max        int
	}{
		{"owner", o.Owner, maxLine}, {"website", o.Website, maxLine}, {"country", o.Country, maxLine},
		{"enforcement", o.Enforcement, maxText}, {"service", o.Service, maxText},
	} {
		if err := checkText(f.value, f.max); err != nil {
			return bad(err.Error(), f.key)
		}
	}
	for _, f := range []struct{ key, value string }{{"email", o.Contact.Email}, {"phone", o.Contact.Phone}, {"address", o.Contact.Address}} {
		if err := checkText(f.value, maxLine); err != nil {
			return bad(err.Error(), "contact", f.key)
		}
	}
	if o.Website != "" {
		if err := checkWebsite(o.Website); err != nil {
			return bad(err.Error(), "website")
		}
	}
	if o.Contact.Email != "" {
		if a, err := mail.ParseAddress(o.Contact.Email); err != nil || a.Address != o.Contact.Email || a.Name != "" {
			return bad(fmt.Sprintf("%q isn't an email address such as access@shop.example", o.Contact.Email), "contact", "email")
		}
	}
	if o.ResponseDays != 0 && (o.ResponseDays < 1 || o.ResponseDays > 365) {
		return bad("must be a number of days between 1 and 365", "response_days")
	}
	if o.Country != "" && !slices.Contains(countries, o.Country) {
		msg := fmt.Sprintf("%q isn't an ISO 3166 country code such as DE, FR or IE", o.Country)
		if up := strings.ToUpper(o.Country); slices.Contains(countries, up) {
			msg += fmt.Sprintf(" (did you mean %q?)", up)
		}
		return bad(msg, "country")
	}
	if len(o.Scope) > maxList || len(o.KnownIssues) > maxList || len(o.Manual) > maxList {
		return bad(fmt.Sprintf("lists at most %d entries each in scope, known_issues and manual", maxList))
	}
	for i, s := range o.Scope {
		if err := checkText(s, maxLine); err != nil {
			return bad(err.Error(), "scope", strconv.Itoa(i))
		}
		if !strings.HasPrefix(s, "/") {
			return bad(fmt.Sprintf("%q: list page paths that start with /, such as /shop/*", s), "scope", strconv.Itoa(i))
		}
	}
	for i, k := range o.KnownIssues {
		at := []string{"known_issues", strconv.Itoa(i)}
		if err := checkCriterion(k.Criterion); err != nil {
			return bad(err.Error(), append(at, "criterion")...)
		}
		if strings.TrimSpace(k.Text) == "" {
			return bad("says what the problem is", append(at, "text")...)
		}
		if err := checkText(k.Text, maxText); err != nil {
			return bad(err.Error(), append(at, "text")...)
		}
		if err := checkDate(k.Until, true); err != nil {
			return bad(err.Error(), append(at, "until")...)
		}
	}
	for i, m := range o.Manual {
		at := []string{"manual", strconv.Itoa(i)}
		if err := checkCriterion(m.Criterion); err != nil {
			return bad(err.Error(), append(at, "criterion")...)
		}
		if err := config.OneOf(m.Result, manualResults); err != nil {
			return bad(err.Error(), append(at, "result")...)
		}
		if err := checkText(m.By, maxLine); err != nil {
			return bad(err.Error(), append(at, "by")...)
		}
		if strings.TrimSpace(m.By) == "" {
			return bad("names who checked it", append(at, "by")...)
		}
		if err := checkDate(m.Date, false); err != nil {
			return bad(err.Error(), append(at, "date")...)
		}
	}
	return nil
}

// checkText rejects a value too long for the draft and control
// characters other than line breaks and tabs, which no statement needs
// and which could hide text in a terminal.
func checkText(s string, limit int) error {
	if n := utf8.RuneCountInString(s); n > limit {
		return fmt.Errorf("is %d characters long; keep it under %d", n, limit)
	}
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return fmt.Errorf("has a control character (%U)", r)
		}
	}
	return nil
}

// checkWebsite wants the site's public address: an http or https URL
// with a host and nothing a visitor shouldn't see, such as a password.
func checkWebsite(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("%q isn't the site's address, such as https://shop.example", s)
	}
	if u.User != nil {
		return fmt.Errorf("%q has a user name or password in it; give the public address", s)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("%q has a query or fragment; give the public address", s)
	}
	return nil
}

// checkCriterion wants an A or AA criterion of WCAG 2.0, 2.1 or 2.2.
func checkCriterion(id string) error {
	if _, ok := wcag.Lookup(id); !ok {
		return fmt.Errorf("%q isn't a WCAG A or AA success criterion, such as 1.4.3", id)
	}
	return nil
}

// checkDate wants YYYY-MM-DD, or nothing when optional.
func checkDate(s string, optional bool) error {
	if s == "" && optional {
		return nil
	}
	if _, err := time.Parse(time.DateOnly, s); err != nil {
		return fmt.Errorf("%q isn't a date as YYYY-MM-DD, such as 2027-03-31", s)
	}
	return nil
}

// lineOf finds the line of the value at path in a block: map keys and
// list indexes. It falls back to the closest line it reached.
func lineOf(node yaml.Node, path ...string) int {
	n := &node
	line := n.Line
	for _, p := range path {
		switch n.Kind {
		case yaml.MappingNode:
			var next *yaml.Node
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == p {
					line, next = n.Content[i].Line, n.Content[i+1]
					break
				}
			}
			if next == nil {
				return line
			}
			n = next
		case yaml.SequenceNode:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(n.Content) {
				return line
			}
			n = n.Content[i]
			line = n.Line
		default:
			return line
		}
	}
	return line
}
