// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"slices"
	"strings"
)

// AddList applies a modules or levels value to base, the list it would
// otherwise be: a plain list replaces base, one whose first value starts
// with "+" adds to it ("+echo" or "+submit,flows"). The values are not
// checked here; Validate does that once everything is applied.
func AddList(base, values []string) ([]string, error) {
	if len(values) == 0 || !strings.HasPrefix(values[0], "+") {
		for _, v := range values {
			if strings.HasPrefix(v, "+") {
				return nil, errors.New("put + before the first value to add to the list, e.g. +" + strings.TrimPrefix(v, "+"))
			}
		}
		return values, nil
	}
	out := slices.Clone(base)
	for _, v := range values {
		if v = strings.TrimPrefix(v, "+"); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out, nil
}

// Cumulative makes interaction levels cumulative: each level brings the
// safer ones before it, so "submit" is inspect, click and submit, and
// "flows" all four. Asking for submit and silently losing the click
// checks was a trap. Values that aren't levels are kept, at the end, for
// Validate to report.
func Cumulative(levels []string) []string {
	top := -1
	var unknown []string
	for _, l := range levels {
		if i := slices.Index(Levels, l); i >= 0 {
			top = max(top, i)
		} else if !slices.Contains(unknown, l) {
			unknown = append(unknown, l)
		}
	}
	return append(slices.Clone(Levels[:top+1]), unknown...)
}
