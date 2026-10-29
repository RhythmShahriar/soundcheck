// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package compare

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Unjudger is a module that can say which of its issues a page can't
// judge: issues the page doesn't report whose absence proves nothing.
// The keyboard walk is one. A walk capped by keyboard.max_tabs is a
// finished audit (model.Audit.Finished). Yet a trap past the cap or a
// stop it never reached isn't fixed for being missing.
//
// keys are the issue keys looked for on p; Unjudged returns those of
// the module's own issues that p can't judge. It never returns a key p
// reports.
type Unjudger interface {
	Unjudged(p *model.Page, keys []string) []string
}

// unjudgers returns the registered modules that are Unjudgers, by name.
// Tests replace it.
var unjudgers = func() map[string]Unjudger {
	var out map[string]Unjudger
	for _, m := range check.Registered() {
		if u, ok := m.(Unjudger); ok {
			if out == nil {
				out = map[string]Unjudger{}
			}
			out[m.Name()] = u
		}
	}
	return out
}

// judges are the Unjudgers a comparison asks.
type judges map[string]Unjudger

// cantJudge reports whether p can't judge the issue with key, which it
// doesn't report: the issue's module is an Unjudger that says so.
func (js judges) cantJudge(p *model.Page, key string) bool {
	u := js.of(key)
	if u == nil {
		return false
	}
	for _, k := range u.Unjudged(p, []string{key}) {
		if k == key {
			return true
		}
	}
	return false
}

// of returns the Unjudger of the module an issue key belongs to, or nil.
func (js judges) of(key string) Unjudger {
	if len(js) == 0 {
		return nil
	}
	mod, _, _ := strings.Cut(key, "/")
	return js[mod]
}

// unjudged is the reason and note of an issue missing from the later
// page that its module can't judge there, worded like an unfinished
// audit's (see gaps). kept names the earlier run (see keptFrom). A walk
// that finished yet can't judge the issue with key either tested the
// control and couldn't tell whether it answers (its keyboard summary kb
// lists key as unjudged) or didn't test the page's widgets and controls:
// another page of its URL group was the one tested.
func unjudged(path, check, key string, audits map[string]model.Audit, kb *model.KeyboardSummary, kept string) (reason, note string) {
	mod, _, _ := strings.Cut(check, "/")
	a, ok := audits[mod]
	status, why := a.Status, a.Reason
	if !ok || status == "" {
		status, why = model.AuditSkipped, "not audited"
	}
	if why == "" {
		why = statusWord(status)
	}
	word := statusWord(status)
	switch {
	case status == model.AuditOK && kb != nil && slices.Contains(kb.Unjudged, key):
		word, why = "control not judged", "the walk couldn't tell whether the control answers Enter or Space"
	case status == model.AuditOK:
		word, why = "controls tested on another page", "its controls were tested on another page of its URL group"
	}
	return fmt.Sprintf("%s: %s %s", model.NotCheckedReason, mod, word),
		fmt.Sprintf("not checked: %s (%s: %s); its issues are kept from %s, not counted as fixed", path, mod, why, kept)
}
