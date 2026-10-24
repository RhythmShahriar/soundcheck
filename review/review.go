// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package review lets a person step through the changes of a comparison
// and decide on each one: accept it as the new baseline, ignore it from
// now on, or skip it for now. Terminal does it in the terminal; Serve
// does it in the HTML report, opened from a server on the loopback
// address.
//
// What a decision does (update the snapshot, write an ignore rule) is up
// to the caller: both take a decide function, so this package never
// writes to the store itself.
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/RhythmShahriar/soundcheck/model"
)

// Action is what the reviewer decided for one change.
type Action string

const (
	Accept Action = "accept" // the change is intended: make it the new baseline
	Ignore Action = "ignore" // never report this change again
	Skip   Action = "skip"   // decide later; nothing is written
)

// ParseAction reads an action name as sent by the review page.
func ParseAction(s string) (Action, error) {
	switch a := Action(s); a {
	case Accept, Ignore, Skip:
		return a, nil
	}
	return "", fmt.Errorf("%q isn't a review action: want accept, ignore or skip", s)
}

// Decide is called once for every change the reviewer accepts or
// ignores. An error leaves the change undecided and is shown to the
// reviewer, who can try again or move on.
type Decide func(model.Change, Action) error

// ID is a short id for a change, the same across runs for the same
// problem. It is the change's own ID when the comparison gave it one
// (the id `accept <id>` takes), else the first 8 hex characters of a
// hash of its check, title and source.
func ID(c model.Change) string {
	if c.ID != "" {
		return c.ID
	}
	sum := sha256.Sum256([]byte(c.Check + "|" + c.Title + "|" + c.Source))
	return hex.EncodeToString(sum[:4])
}

// Reviewable returns the changes a reviewer can decide on, in report
// order (by class, then as found). Fixed problems have nothing to
// accept, so they're left out.
func Reviewable(cs []model.Change) []model.Change {
	var out []model.Change
	for _, class := range model.Classes {
		if class == model.Fixed {
			continue
		}
		for _, c := range cs {
			if c.Class == class {
				out = append(out, c)
			}
		}
	}
	return out
}
