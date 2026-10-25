// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/RhythmShahriar/rhythm/kit/term"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Site text (titles, console messages, URLs, labels) is written by
// whoever controls the site, or a comment on it. Printed as it is, an
// escape sequence moves the cursor and rewrites the summary, OSC 52 sets
// the clipboard and a right-to-left override makes "gnp.exe" read as
// "exe.png". Every format runs such text through one of these first.

// Term makes s safe to print in a terminal. Control characters other
// than tab (C0, DEL, C1) and bytes that aren't UTF-8 are shown as text
// like \x1b and bidirectional overrides (U+202A to U+202E, U+2066 to
// U+2069) are removed. It is the kit's term.Sanitize, with newlines and
// C1 controls written as \x0a and \x9b: one line stays one line
// and a C1 control reads the same whether it came as a character or a byte.
func Term(s string) string { return term.Sanitize(preEscape(s, false)) }

// MarkdownText makes s plain text in Markdown, in a table cell too: newlines
// become spaces, control characters are shown as with Term and every
// character Markdown or GitHub gives a meaning (links, emphasis, code,
// HTML, headings, tables, strikethrough, images, mentions) is escaped
// with a backslash. GitHub also links bare URLs, "www." names and email
// addresses without any of those, so the ":" of "://", the "." after
// "www" and every "@" are escaped too.
//
// The kit's term.EscapeMarkdown isn't used here: it also escapes what
// only starts a list or a block ("1.0.0" becomes "1\.0.0"), which would
// change every stored report and PR comment for no gain in a table cell.
func MarkdownText(s string) string {
	s = Term(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s))
	var b strings.Builder
	for i, r := range s {
		if strings.ContainsRune("\\`*_[]()<>#|~!@", r) || autolinks(s, i, r) {
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

// xmlText makes s safe for JUnit XML: as Term but newlines stay (a case's
// text has several lines) and the noncharacters U+FFFE and U+FFFF, which
// XML doesn't allow, are dropped. Everything else Term leaves is valid.
func xmlText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == 0xFFFE || r == 0xFFFF {
			return -1
		}
		return r
	}, term.Sanitize(preEscape(s, true)))
}

// noBidi removes bidirectional overrides, for HTML: html/template
// escapes everything else.
func noBidi(s string) string {
	return strings.Map(func(r rune) rune {
		if bidi(r) {
			return -1
		}
		return r
	}, s)
}

func bidi(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
}

// preEscape writes what term.Sanitize would show otherwise: C1
// characters as \x9b (the kit writes \u009b) and, unless lines is set,
// newlines as \x0a. Bytes that aren't UTF-8 are left for Sanitize.
func preEscape(s string, lines bool) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return (r == '\n' && !lines) || (r >= 0x80 && r <= 0x9F) }) {
		return s // the usual case: no copy
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case n > 1 && r >= 0x80 && r <= 0x9F, r == '\n' && !lines:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// termChange returns c with its site text made safe for a terminal. The
// detail and fix keep their first line only, all a terminal shows.
func termChange(c model.Change) model.Change {
	c.Title, c.Source, c.Before, c.After = Term(c.Title), Term(c.Source), Term(c.Before), Term(c.After)
	c.Detail, c.Fix = Term(firstLine(c.Detail)), Term(firstLine(c.Fix))
	c.Pages = termAll(c.Pages)
	return c
}

// termIssue is termChange for an issue of a single run.
func termIssue(i model.Issue) model.Issue {
	i.Title, i.Source = Term(i.Title), Term(i.Source)
	i.Detail, i.Fix = Term(firstLine(i.Detail)), Term(firstLine(i.Fix))
	i.Pages = termAll(i.Pages)
	return i
}

func termAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = Term(s)
	}
	return out
}
