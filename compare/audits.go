// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/model"
)

// auditModules are the modules whose pages record whether their audit
// finished (model.Page.Audits) even when the page has no audits: a page
// of an a11y run without one wasn't audited. Other modules that record
// audits are judged by the ones they record.
var auditModules = []string{"a11y", "keyboard"}

// unaudited says which modules' issues on a page pair can't be diffed:
// an audit that timed out, failed or was skipped on either side found
// nothing on that side, which isn't the same as finding no problem.
// Diffing it would call every issue FIXED (or NEW). The reason, by
// module, is the one Ignored changes give; note is the comparison's
// warning for the page.
type unaudited map[string]auditGap

type auditGap struct {
	reason string // "not checked on this page: a11y timed out"
	note   string // the comparison's warning
	now    bool   // the later run's own audit didn't finish
}

// gaps works out the unaudited modules of the page at path from both
// sides' audits. ran says whether a module ran in both runs and listed
// whether the later run names it: a page of that run without an audit
// (one that failed to load) wasn't audited. A page of the earlier run
// without one comes from a run made before 1.3 (nothing was recorded
// then) and counts as audited.
func gaps(path string, before, after map[string]model.Audit, ran, listed func(string) bool) unaudited {
	mods := auditModules // most pages: no other module records audits
	if len(before)+len(after) > 0 {
		mods = slices.Clone(auditModules)
		for m := range maps.Keys(before) {
			mods = append(mods, m)
		}
		for m := range maps.Keys(after) {
			mods = append(mods, m)
		}
		slices.Sort(mods)
		mods = slices.Compact(mods)
	}
	var out unaudited
	for _, m := range mods {
		if !ran(m) {
			continue
		}
		b, inBefore := before[m]
		a, inAfter := after[m]
		var gap model.Audit
		now := false
		switch {
		case inAfter && !a.Finished():
			gap, now = a, true
		case !inAfter && slices.Contains(auditModules, m) && listed(m):
			gap = model.Audit{Status: model.AuditSkipped, Reason: "not audited"}
		case inBefore && !b.Finished():
			gap = b
		default:
			continue
		}
		reason := gap.Reason
		if reason == "" {
			reason = statusWord(gap.Status)
		}
		if out == nil {
			out = unaudited{}
		}
		out[m] = auditGap{
			reason: fmt.Sprintf("%s: %s %s", model.NotCheckedReason, m, statusWord(gap.Status)),
			note:   fmt.Sprintf("not checked: %s (%s: %s); its issues are kept from the snapshot, not counted as fixed", path, m, reason),
			now:    now,
		}
	}
	return out
}

// newPageNotes are the warnings for a page only the later run has whose
// audits didn't finish, worded like the run's own not-checked lines:
// there is nothing in the snapshot to keep.
func newPageNotes(path string, after map[string]model.Audit, ran func(string) bool) []string {
	var out []string
	for _, m := range slices.Sorted(maps.Keys(after)) {
		a := after[m]
		if !ran(m) || a.Finished() {
			continue
		}
		out = append(out, fmt.Sprintf("not checked: %s (%s: %s)", path, m, cmp.Or(a.Reason, statusWord(a.Status))))
	}
	return out
}

// dropCovered leaves out of warnings a module's own warning about a
// page and module a not-checked note already names: "a11y: /big/:
// reflow check at 320 px: timed out after 10s" next to "not checked:
// /big/ (a11y: ...)". One note per page and module is enough.
func dropCovered(warnings []string) []string {
	covered := map[string]bool{} // "a11y: /big/: "
	for _, w := range warnings {
		rest, ok := strings.CutPrefix(w, "not checked: ")
		if !ok {
			continue
		}
		path, rest, ok := strings.Cut(rest, " (")
		if !ok {
			continue
		}
		if mod, _, ok := strings.Cut(rest, ":"); ok {
			covered[mod+": "+path+": "] = true
		}
	}
	if len(covered) == 0 {
		return warnings
	}
	return slices.DeleteFunc(warnings, func(w string) bool {
		mod, rest, _ := strings.Cut(w, ": ")
		path, _, _ := strings.Cut(rest, ": ")
		return covered[mod+": "+path+": "]
	})
}

// statusWord words an audit's status for a reason: "a11y timed out".
func statusWord(status string) string {
	switch status {
	case model.AuditTimeout:
		return "timed out"
	case model.AuditError:
		return "failed"
	case model.AuditSkipped:
		return "skipped"
	}
	return status
}

// of returns the gap of the module an issue's check belongs to.
func (u unaudited) of(check string) (auditGap, bool) {
	if u == nil {
		return auditGap{}, false
	}
	mod, _, _ := strings.Cut(check, "/")
	g, ok := u[mod]
	return g, ok
}

// mergeAudits is the audits of several pages with one path: a module's
// audit is unfinished when any of them didn't finish.
func mergeAudits(into, more map[string]model.Audit) map[string]model.Audit {
	if len(more) == 0 {
		return into
	}
	if into == nil {
		return maps.Clone(more)
	}
	for m, a := range more {
		if have, ok := into[m]; !ok || (have.Finished() && !a.Finished()) {
			into[m] = a
		}
	}
	return into
}

// ranIn reports whether a module ran in r. A run that names no modules
// (one made by hand, or before modules were recorded) ran them all.
func ranIn(r *model.Run) func(string) bool {
	return func(m string) bool {
		return len(r.Options.Modules) == 0 || slices.Contains(r.Options.Modules, m)
	}
}

// ranInBoth is ranIn for both runs: a module that ran in only one has
// nothing to diff (compared drops its changes anyway).
func ranInBoth(before, after *model.Run) func(string) bool {
	b, a := ranIn(before), ranIn(after)
	return func(m string) bool { return b(m) && a(m) }
}

// addedByEAA reports whether --eaa added module m to a comparison with
// a snapshot taken without it: a11y or keyboard, run now and not in the
// snapshot. Such a module's issues are compared with none, so each is
// new, where any other module that ran only once is left out.
func addedByEAA(before, after *model.Run) func(string) bool {
	b, a := ranIn(before), ranIn(after)
	return func(m string) bool {
		return after.Options.EAA && !before.Options.EAA && slices.Contains(eaaModules, m) && !b(m) && a(m)
	}
}

// eaaModules are the modules --eaa adds.
var eaaModules = []string{"a11y", "keyboard"}

// diffedIn reports whether the issues of module m are diffed: it ran in
// both runs or --eaa added it (addedByEAA). An unfinished audit of such
// a module keeps its page's issues out of the count (gaps).
func diffedIn(before, after *model.Run) func(string) bool {
	both, added := ranInBoth(before, after), addedByEAA(before, after)
	return func(m string) bool { return both(m) || added(m) }
}
