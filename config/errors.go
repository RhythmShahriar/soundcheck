// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"strings"
)

// SettingError is a bad or unknown setting. Load fills in File and Line
// when the setting came from the config file, so the message points at
// the line to fix; the CLI names the flag instead when a flag set it.
type SettingError struct {
	Key  string // the setting, dotted ("interactions.levels"); "" for an unknown one
	Msg  string // what's wrong, without the key
	File string // the config file, when the value came from one
	Line int    // the line in File, when known
}

func (e *SettingError) Error() string {
	var b strings.Builder
	if e.File != "" {
		b.WriteString(e.File)
		if e.Line > 0 {
			fmt.Fprintf(&b, " line %d", e.Line)
		}
		b.WriteString(": ")
	}
	if e.Key != "" {
		b.WriteString(e.Key + ": ")
	}
	b.WriteString(e.Msg)
	return b.String()
}

func settingErr(key, format string, args ...any) *SettingError {
	return &SettingError{Key: key, Msg: fmt.Sprintf(format, args...)}
}

// OneOf reports value when it isn't one of options, suggesting the
// closest one: `"helth" isn't one of health, visual (did you mean
// "health"?)`. It returns nil for a valid value.
func OneOf(value string, options []string) error {
	for _, o := range options {
		if value == o {
			return nil
		}
	}
	return fmt.Errorf("%q isn't one of %s%s", value, strings.Join(options, ", "), DidYouMean(value, options))
}

// DidYouMean is ` (did you mean "x"?)` for the option closest to s, or ""
// when none is close enough to be a typo.
func DidYouMean(s string, options []string) string {
	if m := Suggest(s, options); m != "" {
		return fmt.Sprintf(" (did you mean %q?)", m)
	}
	return ""
}

// Suggest returns the option closest to s by edit distance, ignoring
// case, or "" when even the closest needs more than a third of s changed
// (at least one edit is always allowed), which is past a typo.
func Suggest(s string, options []string) string {
	best, bestDist := "", 0
	ls := strings.ToLower(s)
	for _, o := range options {
		d := distance(ls, strings.ToLower(o))
		if best == "" || d < bestDist {
			best, bestDist = o, d
		}
	}
	if best == "" || bestDist > max(1, len([]rune(s))/3) {
		return ""
	}
	return best
}

// distance is the optimal string alignment distance between a and b:
// the fewest single-character insertions, deletions, substitutions and
// swaps of two neighbors that turn one into the other. A swap counts as
// one edit because it's the most common typo ("submti" for "submit").
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	// Three rows: a swap looks two rows back.
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(rb)]
}
