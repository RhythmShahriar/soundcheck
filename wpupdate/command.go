// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpupdate

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// SplitCommand splits the --wp command prefix into argv the way a POSIX
// shell splits words: spaces separate words, '...' is literal, "..."
// keeps spaces and takes \" \\ \$ and \` escapes; a backslash
// outside quotes escapes the next character. Nothing is expanded (no
// variables, ~ or globs). An unquoted ; | & < > ` or $( is refused,
// since the command runs without a shell and would get them as words.
func SplitCommand(s string) ([]string, error) {
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		quote   rune // ' or " while inside quotes
		escaped bool
	)
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case escaped:
			if quote == '"' && !strings.ContainsRune("\"\\$`\n", r) {
				cur.WriteRune('\\') // "\n" keeps its backslash inside double quotes
			}
			if r != '\n' { // a backslash before a newline joins the lines
				cur.WriteRune(r)
			}
			escaped, inWord = false, true
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped, inWord = true, true
		case quote == '"':
			if r == '"' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case strings.ContainsRune(";|&<>`", r), r == '$' && i+1 < len(runes) && runes[i+1] == '(':
			op := string(r)
			if r == '$' {
				op = "$("
			}
			return nil, fmt.Errorf("%q needs a shell; the wp command runs without one: give the command itself, e.g. \"ssh deploy@staging -- wp --path=/srv/www\"", op)
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if escaped {
		return nil, errors.New("the command ends with a backslash")
	}
	if inWord {
		words = append(words, cur.String())
	}
	if len(words) == 0 {
		return nil, errors.New("the wp command is empty")
	}
	return words, nil
}

// remoteShell reports whether the command prefix hands its arguments to
// a shell on another machine. ssh joins its arguments with spaces and the
// remote shell splits them again, so arguments added after the prefix
// must be quoted for that shell.
func remoteShell(prefix []string) bool {
	if len(prefix) == 0 {
		return false
	}
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(prefix[0], `\`, "/")))
	return name == "ssh" || name == "ssh.exe"
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./=:,@%+-]+$`)

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// wpArgv builds the argv for one WP-CLI call: the prefix as the user gave
// it, then args, each one word even through ssh.
func wpArgv(prefix []string, args ...string) []string {
	argv := append([]string(nil), prefix...)
	quote := remoteShell(prefix)
	for _, a := range args {
		if quote {
			a = shellQuote(a)
		}
		argv = append(argv, a)
	}
	return argv
}
