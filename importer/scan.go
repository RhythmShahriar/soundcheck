// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package importer

import "strings"

// A small, forgiving reader for JavaScript and TypeScript object
// literals. It knows strings, template literals and comments, so a
// bracket or key inside one isn't taken for code. It knows nothing else
// and never evaluates anything.

// skip returns the index just past the string, template literal or
// comment starting at i, or i when there is none there.
func skip(src string, i int) int {
	switch {
	case strings.HasPrefix(src[i:], "//"):
		if n := strings.IndexByte(src[i:], '\n'); n >= 0 {
			return i + n + 1
		}
		return len(src)
	case strings.HasPrefix(src[i:], "/*"):
		if n := strings.Index(src[i+2:], "*/"); n >= 0 {
			return i + 2 + n + 2
		}
		return len(src)
	case src[i] == '"' || src[i] == '\'' || src[i] == '`':
		q := src[i]
		for j := i + 1; j < len(src); j++ {
			switch src[j] {
			case '\\':
				j++
			case q:
				return j + 1
			}
		}
		return len(src)
	}
	return i
}

// keyIndex returns the index just past `key:` (also written "key":
// or 'key':) at or after from, outside strings and comments, or -1.
func keyIndex(src, key string, from int) int {
	for i := from; i < len(src); {
		if j := skip(src, i); j != i {
			// A quoted key: "pages": [...]
			if q := src[i]; (q == '"' || q == '\'') && src[i+1:j-1] == key {
				if k := colonAfter(src, j); k >= 0 {
					return k
				}
			}
			i = j
			continue
		}
		if strings.HasPrefix(src[i:], key) && !identChar(before(src, i)) && !identChar(at(src, i+len(key))) {
			if k := colonAfter(src, i+len(key)); k >= 0 {
				return k
			}
		}
		i++
	}
	return -1
}

// colonAfter returns the index past a ':' that follows i after spaces,
// or -1.
func colonAfter(src string, i int) int {
	for i < len(src) && strings.IndexByte(" \t\r\n", src[i]) >= 0 {
		i++
	}
	if i < len(src) && src[i] == ':' {
		return i + 1
	}
	return -1
}

// balanced returns the text of the bracketed list starting at open,
// brackets included. It also reports whether the list closes.
func balanced(src string, open int) (string, bool) {
	depth := 0
	for i := open; i < len(src); {
		if j := skip(src, i); j != i {
			i = j
			continue
		}
		switch src[i] {
		case '[', '{', '(':
			depth++
		case ']', '}', ')':
			depth--
			if depth == 0 {
				return src[open : i+1], true
			}
		}
		i++
	}
	return "", false
}

// value is one value read for a key.
type value struct {
	text     string // the string's content, or the expression when computed
	computed bool   // not a plain string: a variable, call or ${} template
}

// stringsAfter returns the value of every `key:` in src.
func stringsAfter(src, key string) []value {
	var out []value
	for i := keyIndex(src, key, 0); i >= 0; i = keyIndex(src, key, i) {
		for i < len(src) && strings.IndexByte(" \t\r\n", src[i]) >= 0 {
			i++
		}
		if i >= len(src) {
			break
		}
		if q := src[i]; q == '"' || q == '\'' || q == '`' {
			j := skip(src, i)
			body := src[i+1 : max(j-1, i+1)]
			if q == '`' && strings.Contains(body, "${") {
				out = append(out, value{text: src[i:j], computed: true})
			} else {
				out = append(out, value{text: unescape(body)})
			}
			i = j
			continue
		}
		// An expression: up to the next comma or closing brace.
		end := strings.IndexAny(src[i:], ",}\n")
		if end < 0 {
			end = len(src) - i
		}
		out = append(out, value{text: strings.TrimSpace(src[i : i+end]), computed: true})
		i += end
	}
	return out
}

// unescape undoes the escapes a path could carry: \/ \" \' \\.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func identChar(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func before(s string, i int) byte {
	if i == 0 {
		return ' '
	}
	return s[i-1]
}

func at(s string, i int) byte {
	if i >= len(s) {
		return ' '
	}
	return s[i]
}
