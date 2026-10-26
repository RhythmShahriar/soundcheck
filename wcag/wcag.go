// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wcag

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Criterion is one WCAG success criterion.
type Criterion struct {
	ID        string // "1.4.3"
	Title     string // "Contrast (Minimum)"
	Level     string // A or AA
	Principle string // Perceivable, Operable, Understandable or Robust
	Since     string // the WCAG version that added it: 2.0, 2.1 or 2.2
	EN        string // the EN 301 549 clause: "9." plus ID
	// Coverage says how far Soundcheck's tests can judge the criterion
	// and TestedBy lists the checks that test it.
	Coverage string
	TestedBy []string
}

// catalog is every WCAG 2.2 A and AA criterion plus 4.1.1 Parsing,
// which 2.2 removed. Removed says which version dropped a criterion.
var catalog = []struct {
	id, title, level, since, removed string
}{
	{"1.1.1", "Non-text Content", "A", "2.0", ""},
	{"1.2.1", "Audio-only and Video-only (Prerecorded)", "A", "2.0", ""},
	{"1.2.2", "Captions (Prerecorded)", "A", "2.0", ""},
	{"1.2.3", "Audio Description or Media Alternative (Prerecorded)", "A", "2.0", ""},
	{"1.2.4", "Captions (Live)", "AA", "2.0", ""},
	{"1.2.5", "Audio Description (Prerecorded)", "AA", "2.0", ""},
	{"1.3.1", "Info and Relationships", "A", "2.0", ""},
	{"1.3.2", "Meaningful Sequence", "A", "2.0", ""},
	{"1.3.3", "Sensory Characteristics", "A", "2.0", ""},
	{"1.3.4", "Orientation", "AA", "2.1", ""},
	{"1.3.5", "Identify Input Purpose", "AA", "2.1", ""},
	{"1.4.1", "Use of Color", "A", "2.0", ""},
	{"1.4.2", "Audio Control", "A", "2.0", ""},
	{"1.4.3", "Contrast (Minimum)", "AA", "2.0", ""},
	{"1.4.4", "Resize Text", "AA", "2.0", ""},
	{"1.4.5", "Images of Text", "AA", "2.0", ""},
	{"1.4.10", "Reflow", "AA", "2.1", ""},
	{"1.4.11", "Non-text Contrast", "AA", "2.1", ""},
	{"1.4.12", "Text Spacing", "AA", "2.1", ""},
	{"1.4.13", "Content on Hover or Focus", "AA", "2.1", ""},
	{"2.1.1", "Keyboard", "A", "2.0", ""},
	{"2.1.2", "No Keyboard Trap", "A", "2.0", ""},
	{"2.1.4", "Character Key Shortcuts", "A", "2.1", ""},
	{"2.2.1", "Timing Adjustable", "A", "2.0", ""},
	{"2.2.2", "Pause, Stop, Hide", "A", "2.0", ""},
	{"2.3.1", "Three Flashes or Below Threshold", "A", "2.0", ""},
	{"2.4.1", "Bypass Blocks", "A", "2.0", ""},
	{"2.4.2", "Page Titled", "A", "2.0", ""},
	{"2.4.3", "Focus Order", "A", "2.0", ""},
	{"2.4.4", "Link Purpose (In Context)", "A", "2.0", ""},
	{"2.4.5", "Multiple Ways", "AA", "2.0", ""},
	{"2.4.6", "Headings and Labels", "AA", "2.0", ""},
	{"2.4.7", "Focus Visible", "AA", "2.0", ""},
	{"2.4.11", "Focus Not Obscured (Minimum)", "AA", "2.2", ""},
	{"2.5.1", "Pointer Gestures", "A", "2.1", ""},
	{"2.5.2", "Pointer Cancellation", "A", "2.1", ""},
	{"2.5.3", "Label in Name", "A", "2.1", ""},
	{"2.5.4", "Motion Actuation", "A", "2.1", ""},
	{"2.5.7", "Dragging Movements", "AA", "2.2", ""},
	{"2.5.8", "Target Size (Minimum)", "AA", "2.2", ""},
	{"3.1.1", "Language of Page", "A", "2.0", ""},
	{"3.1.2", "Language of Parts", "AA", "2.0", ""},
	{"3.2.1", "On Focus", "A", "2.0", ""},
	{"3.2.2", "On Input", "A", "2.0", ""},
	{"3.2.3", "Consistent Navigation", "AA", "2.0", ""},
	{"3.2.4", "Consistent Identification", "AA", "2.0", ""},
	{"3.2.6", "Consistent Help", "A", "2.2", ""},
	{"3.3.1", "Error Identification", "A", "2.0", ""},
	{"3.3.2", "Labels or Instructions", "A", "2.0", ""},
	{"3.3.3", "Error Suggestion", "AA", "2.0", ""},
	{"3.3.4", "Error Prevention (Legal, Financial, Data)", "AA", "2.0", ""},
	{"3.3.7", "Redundant Entry", "A", "2.2", ""},
	{"3.3.8", "Accessible Authentication (Minimum)", "AA", "2.2", ""},
	{"4.1.1", "Parsing", "A", "2.0", "2.2"},
	{"4.1.2", "Name, Role, Value", "A", "2.0", ""},
	{"4.1.3", "Status Messages", "AA", "2.1", ""},
}

// principles names each guideline's principle by its first digit.
var principles = map[byte]string{'1': "Perceivable", '2': "Operable", '3': "Understandable", '4': "Robust"}

// Versions are the WCAG versions Criteria knows, oldest first.
var Versions = []string{"2.0", "2.1", "2.2"}

// Criteria returns the A and AA criteria of a WCAG version in number
// order: 38 for 2.0, 50 for 2.1 and 55 for 2.2. Empty is 2.0, the rules
// of a run made before 1.3. An unknown version has none.
func Criteria(version string) []Criterion {
	if version == "" {
		version = "2.0"
	}
	if !slices.Contains(Versions, version) {
		return nil
	}
	var out []Criterion
	for _, c := range catalog {
		if older(version, c.since) || (c.removed != "" && !older(version, c.removed)) {
			continue
		}
		out = append(out, Criterion{ID: c.id, Title: c.title, Level: c.level, Principle: principles[c.id[0]], Since: c.since, EN: "9." + c.id})
	}
	slices.SortFunc(out, func(a, b Criterion) int { return Compare(a.ID, b.ID) })
	return out
}

// older reports whether version a comes before version b.
func older(a, b string) bool { return Compare(a, b) < 0 }

// Compare orders criterion numbers and versions part by part, as
// numbers: 1.4.3 before 1.4.10.
func Compare(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(pa), len(pb)) {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if c := cmp.Compare(x, y); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(pa), len(pb))
}

// EN301549 returns the EN 301 549 version whose clause 9 matches a WCAG
// version: V3.2.1 for 2.1 and V4.1.1 for 2.2. Other versions have none.
func EN301549(version string) string {
	switch version {
	case "2.1":
		return "V3.2.1"
	case "2.2":
		return "V4.1.1"
	}
	return ""
}

// Standard names what a run's checks are measured against, as reports
// and the statement draft write it.
func Standard(version string) string {
	switch version {
	case "2.1", "2.2":
		return "WCAG " + version + " level AA / EN 301 549 " + EN301549(version)
	case "", "2.0":
		return "WCAG 2.0 level AA"
	}
	return "WCAG " + version + " level AA"
}

// Summarize sums up an --eaa run against the criteria of its WCAG
// version: pages yields the run's pages (they may come from disk one by
// one), site its site-level issues and ignored the changes ignore rules
// and accepts left out. For a comparison, run is the current run.
func Summarize(run *model.Run, pages func(func(*model.Page) bool), site []model.Issue, ignored []model.Change) model.EAASummary {
	version := run.Options.WCAG
	if version == "" {
		version = "2.0"
	}
	return model.EAASummary{WCAG: version, EN301549: EN301549(version), EvidenceNote: model.EvidenceNote}
}

// ForCheck returns the criteria an issue of check fails under a WCAG
// version, for checks whose issues don't carry their own.
func ForCheck(check, version string) []string { return nil }
