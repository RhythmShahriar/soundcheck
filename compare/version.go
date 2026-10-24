// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"fmt"
	"strconv"
	"strings"
)

// versionNote warns when the snapshot was made by another Soundcheck
// release line. A detector that changed between releases makes its old
// issues look FIXED and its new ones NEW, though the site didn't change.
//
// Patch releases don't change detectors, so only the major and minor
// numbers count. Two dev builds aren't compared (their numbers say
// nothing) and neither is a run that recorded no version.
func versionNote(snapshot, now string) string {
	if snapshot == "" || now == "" {
		return ""
	}
	bMaj, bMin, bOK := releaseLine(snapshot)
	aMaj, aMin, aOK := releaseLine(now)
	switch {
	case !bOK && !aOK:
		return ""
	case bOK && aOK && bMaj == aMaj && bMin == aMin:
		return ""
	}
	return fmt.Sprintf("snapshot made by Soundcheck %s, this is %s; changed detectors can show as NEW or FIXED",
		strings.TrimPrefix(snapshot, "v"), strings.TrimPrefix(now, "v"))
}

// releaseLine reads the major and minor numbers of a version such as
// "1.2.0", "v1.2.3" or "1.3.0-rc.1". ok is false for a dev build ("dev",
// "(devel)") or anything else without them.
func releaseLine(v string) (major, minor int, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	// "1.3-rc1" has no patch number; the minor part ends at the suffix.
	minorPart, _, _ := strings.Cut(parts[1], "-")
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(minorPart)
	if err1 != nil || err2 != nil || major < 0 || minor < 0 {
		return 0, 0, false
	}
	return major, minor, true
}
