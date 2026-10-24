// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// device matches the names Windows reserves for devices. They stay
	// reserved with any extension: "aux.txt" opens the AUX device too.
	device = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\.|$)`)
)

// ValidName reports whether name can be used as a snapshot name.
func ValidName(name string) bool { return CheckName(name) == nil }

// CheckName says why name can't be a snapshot or folder name in the
// store, or returns nil when it can. Names must work on every system a
// store may be shared with, so Windows' rules apply on Linux and macOS
// too. The message suits a command's --name error.
func CheckName(name string) error {
	switch {
	case !safeName.MatchString(name):
		return fmt.Errorf("%q isn't a valid name: use letters, digits, dots, dashes and underscores", name)
	case strings.EqualFold(name, Latest):
		// A snapshot folder with that name would sit where older
		// versions keep the LATEST pointer.
		return fmt.Errorf("%q is reserved; pick another name", name)
	case device.MatchString(name):
		return fmt.Errorf("%q is a device name on Windows; pick another name", name)
	case strings.HasSuffix(name, "."):
		// Windows drops trailing dots, so "a." would be the folder "a".
		return fmt.Errorf("%q ends with a dot, which Windows drops; pick another name", name)
	}
	return nil
}

// caseClash returns an error when dir holds an entry whose name differs
// from name only in case. macOS and Windows disks see the two as the
// same folder, so one would silently replace the other there.
func caseClash(dir, name string) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	if other := sameButCase(names, name); other != "" {
		return fmt.Errorf("%q clashes with the existing snapshot %q: macOS and Windows see names that differ only in case as the same folder; use %q or pick another name", name, other, other)
	}
	return nil
}

// sameButCase returns the entry of names that equals name except for
// case, or "".
func sameButCase(names []string, name string) string {
	for _, n := range names {
		if n != name && strings.EqualFold(n, name) {
			return n
		}
	}
	return ""
}

// Stamp is the default name for snapshots and runs: UTC to the
// millisecond, so names sort by time and rarely collide.
func Stamp(t time.Time) string { return t.UTC().Format(stampLayout) }

const (
	stampLayout = "2006-01-02T15-04-05.000Z"
	// legacyLayout is how older versions named runs: local time to the
	// second, with no zone.
	legacyLayout = "2006-01-02T15-04-05"
)

// localZone is the zone legacy stamps are read in, swapped in tests.
var localZone = time.Local

// stampTime reads the time in a folder name made by Stamp (or an older
// version's local stamp), plus the n of a "-n" suffix NewDir added. ok
// is false for other names.
func stampTime(name string) (at time.Time, n int, ok bool) {
	parse := func(s string) (time.Time, bool) {
		if t, err := time.Parse(stampLayout, s); err == nil {
			return t, true
		}
		t, err := time.ParseInLocation(legacyLayout, s, localZone)
		return t, err == nil
	}
	if t, ok := parse(name); ok {
		return t, 1, true
	}
	if i := strings.LastIndexByte(name, '-'); i > 0 {
		if n, err := strconv.Atoi(name[i+1:]); err == nil && n > 1 {
			if t, ok := parse(name[:i]); ok {
				return t, n, true
			}
		}
	}
	return time.Time{}, 0, false
}

// sortByTime orders folder names oldest first. Names alone don't sort
// by time: legacy local stamps repeat an hour when daylight saving time
// ends and mix badly with UTC ones. Names that aren't stamps (snapshots
// named by the user) come first, by name.
func sortByTime(names []string) {
	slices.SortStableFunc(names, func(a, b string) int {
		ta, na, oka := stampTime(a)
		tb, nb, okb := stampTime(b)
		switch {
		case !oka && !okb:
			return strings.Compare(a, b)
		case !oka:
			return -1
		case !okb:
			return 1
		}
		return cmp.Or(ta.Compare(tb), cmp.Compare(na, nb), strings.Compare(a, b))
	})
}
