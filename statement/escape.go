// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package statement

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// A draft holds text from the site (page paths) and from the owner, who
// may paste anything into the config file. A statement is published, so
// a path that reads as a link, a table cell that breaks the table or a
// right-to-left override that reverses a sentence would end up on the
// owner's site. The rules are the report package's (report/escape.go),
// copied so the statement keeps them under its own tests, with one
// difference: a statement is for people, not a terminal log, so control
// characters are dropped rather than shown as \x1b.

// plain makes s one line of plain text: newlines and tabs become spaces,
// other control characters (C0, DEL, C1), bidirectional overrides,
// isolates and marks are removed and bytes that aren't UTF-8 become
// U+FFFD.
func plain(s string) string {
	if !strings.ContainsFunc(s, unsafeRune) && utf8.ValidString(s) {
		return s // the usual case: no copy
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unsafeRune(r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
}

// unsafeRune reports a control character or a bidirectional control.
func unsafeRune(r rune) bool { return unicode.IsControl(r) || bidi(r) }

// bidi reports the characters that change the direction text is shown
// in: the embeddings and overrides (U+202A to U+202E), the isolates
// (U+2066 to U+2069) and the marks LRM, RLM and ALM.
func bidi(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F || r == 0x061C
}

// markdownText makes s plain text in Markdown, in a table cell too:
// plain, then every character Markdown or GitHub gives a meaning
// (links and images, emphasis, code, HTML, headings, tables,
// strikethrough, mentions) is escaped with a backslash. So is "&": a
// character reference such as &#x202E; would bring back the override
// plain removed. Parentheses and "!" stay: with every bracket escaped
// they can't make a link. GitHub also links bare URLs, "www." names and
// email addresses, so the ":" of "://", the "." after "www" and every
// "@" are escaped too.
func markdownText(s string) string {
	s = plain(s)
	var b strings.Builder
	for i, r := range s {
		if strings.ContainsRune("\\`*_[]<>#|~@&", r) || autolinks(s, i, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// autolinks reports whether r at byte i of s is where GitHub would start
// a link of its own: the ":" of "http://" (any scheme) or the "." of
// "www.".
func autolinks(s string, i int, r rune) bool {
	switch r {
	case ':':
		return strings.HasPrefix(s[i+1:], "//")
	case '.':
		return i >= 3 && strings.EqualFold(s[i-3:i], "www")
	}
	return false
}

// leadEscape escapes what would start a block at the beginning of a
// paragraph or list item that came from outside the draft: a list ("- ", "+ ", "1. ")
// or a line of "=" or "-" that turns the line above into a heading.
// markdownText already escapes "#", ">" and "*".
func leadEscape(md string) string {
	switch {
	case md == "":
		return md
	case strings.ContainsRune("-+=", rune(md[0])):
		return "\\" + md
	}
	i := 0
	for i < len(md) && i < 9 && md[i] >= '0' && md[i] <= '9' {
		i++
	}
	if i > 0 && i < len(md) && (md[i] == '.' || md[i] == ')') {
		return md[:i] + "\\" + md[i:]
	}
	return md
}
