// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/soundcheck/model"
)

// SnapshotFile records what was done to a snapshot after it was taken,
// next to its run.json.
const SnapshotFile = "snapshot.json"

// KindSnapshot is the kind of snapshot.json.
const KindSnapshot = "snapshot"

// SnapshotInfo is snapshot.json: the changes accepted into a snapshot
// since it was taken, oldest first.
type SnapshotInfo struct {
	Schema   string     `json:"schema"`
	Kind     string     `json:"kind"`
	Accepted []Accepted `json:"accepted"`
}

// Accepted is one change accepted on one page: from then on the snapshot
// holds what that page looked like when the change was found.
type Accepted struct {
	ID    string    `json:"id"`    // the change's ID in the comparison it was accepted from
	Check string    `json:"check"` // e.g. "visual/page"
	Title string    `json:"title"`
	Page  string    `json:"page,omitempty"` // empty for a change of the whole site
	Who   string    `json:"who"`
	At    time.Time `json:"at"`
}

// Selector picks the changes of a comparison to accept: by ID, or by a
// page glob and a check glob (see model.Glob). Either glob may be left
// empty to match everything, but not both. Only the pages a page glob
// matches are accepted, so "/team/*" accepts a change on /team/ and
// /shop/ for /team/ alone.
type Selector struct {
	IDs   []string
	Page  string
	Check string
}

// now is time.Now, fixed in tests.
var now = time.Now

// LoadSnapshotInfo reads snapshot.json from a snapshot folder. A snapshot
// nothing was accepted into has none, which reads as an empty record.
func LoadSnapshotInfo(dir string) (*SnapshotInfo, error) {
	info := &SnapshotInfo{Schema: model.Schema, Kind: KindSnapshot, Accepted: []Accepted{}}
	err := cache.ReadJSON(filepath.Join(dir, SnapshotFile), info)
	if errors.Is(err, fs.ErrNotExist) {
		return info, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if info.Accepted == nil {
		info.Accepted = []Accepted{}
	}
	return info, nil
}

// target is a change to accept and the pages to accept it on (none for
// a change of the whole site).
type target struct {
	change model.Change
	pages  []string
}

// Accept makes the changes sel picks part of the snapshot in snapshotDir,
// so comparing the same site again doesn't report them. c is the
// comparison they come from and afterDir the folder of its current run.
//
// Only the accepted pages' baselines change, and only in what the change
// is about: a visual change copies that page's screenshots from the run,
// an issue change replaces the page's issues with its key (the site's,
// for a site-wide issue such as a broken link), a speed change copies
// the page's metrics and resources, and title, text, redirect, cache and
// Echo changes copy that one record. Every other page and baseline stays.
// Each accepted change and page is recorded with who and when in
// snapshot.json.
//
// It runs under the store's lock. New screenshots are written under new
// names before run.json is replaced (atomically), so a crash leaves the
// old snapshot or the new one, never a mix; the replaced screenshots are
// removed last.
func Accept(snapshotDir, afterDir string, c *model.Comparison, sel Selector, who string) ([]Accepted, error) {
	targets, err := pick(c, sel)
	if err != nil {
		return nil, err
	}
	// snapshotDir is <store>/snapshots/<name>.
	unlock, err := Store{Root: filepath.Dir(filepath.Dir(snapshotDir))}.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	before, err := LoadRun(snapshotDir)
	if err != nil {
		return nil, err
	}
	after, err := LoadRun(afterDir)
	if err != nil {
		return nil, err
	}
	info, err := LoadSnapshotInfo(snapshotDir)
	if err != nil {
		return nil, err
	}

	at := now().UTC()
	a := &acceptor{before: before, after: after, afterDir: afterDir, snapshotDir: snapshotDir,
		suffix: "-a" + strconv.FormatInt(at.UnixMilli(), 36), shotsDone: map[string]bool{}, siteDone: map[string]bool{}}
	var done []Accepted
	for _, t := range targets {
		entry := Accepted{ID: t.change.ID, Check: t.change.Check, Title: t.change.Title, Who: who, At: at}
		site := a.site(t.change)
		onPage := false
		for _, p := range t.pages {
			ok, err := a.page(t.change, p)
			if err != nil {
				a.discard()
				return nil, err
			}
			if ok {
				entry.Page, onPage = p, true
				done = append(done, entry)
			}
		}
		// A site issue (a broken link) is accepted even when the pages
		// that link to it are gone.
		if site && !onPage {
			entry.Page = ""
			done = append(done, entry)
		}
	}
	if len(done) == 0 {
		a.discard()
		return nil, errors.New("nothing to accept: the pages of those changes aren't in both the snapshot and the run")
	}
	if err := SaveRun(snapshotDir, before); err != nil {
		a.discard()
		return nil, err
	}
	info.Accepted = append(info.Accepted, done...)
	if err := cache.WriteJSON(filepath.Join(snapshotDir, SnapshotFile), info); err != nil {
		// run.json already holds the accepted baselines; only the record
		// of who accepted them is missing, which is worth saying.
		return done, fmt.Errorf("accepted, but couldn't record it in %s: %w", SnapshotFile, err)
	}
	a.removeReplaced()
	return done, nil
}

// pick resolves a selector to the changes and pages to accept.
func pick(c *model.Comparison, sel Selector) ([]target, error) {
	byID := len(sel.IDs) > 0
	switch {
	case byID && (sel.Page != "" || sel.Check != ""):
		return nil, errors.New("pick changes by id or by page and check, not both")
	case !byID && sel.Page == "" && sel.Check == "":
		return nil, errors.New("say which changes to accept: their ids, or a page or check")
	}
	var out []target
	if byID {
		for _, id := range sel.IDs {
			i := slices.IndexFunc(c.Changes, func(ch model.Change) bool { return ch.ID == id })
			if i < 0 {
				return nil, fmt.Errorf("no change %q in this comparison", id)
			}
			out = append(out, target{change: c.Changes[i], pages: c.Changes[i].Pages})
		}
		return out, nil
	}
	for _, ch := range c.Changes {
		if sel.Check != "" && !model.Glob(sel.Check, ch.Check) {
			continue
		}
		pages := ch.Pages
		if sel.Page != "" {
			pages = slices.DeleteFunc(slices.Clone(ch.Pages), func(p string) bool { return !model.Glob(sel.Page, p) })
			if len(pages) == 0 {
				continue
			}
		}
		out = append(out, target{change: ch, pages: pages})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no change matches %s", sel.describe())
	}
	return out, nil
}

func (sel Selector) describe() string {
	var parts []string
	if sel.Page != "" {
		parts = append(parts, fmt.Sprintf("page %q", sel.Page))
	}
	if sel.Check != "" {
		parts = append(parts, fmt.Sprintf("check %q", sel.Check))
	}
	return strings.Join(parts, " and ")
}

// acceptor applies accepted changes to the snapshot's run in memory and
// tracks the screenshot files that come and go.
type acceptor struct {
	before, after         *model.Run
	snapshotDir, afterDir string
	suffix                string          // makes new screenshot names unique to this accept
	shotsDone             map[string]bool // pages whose screenshots were copied
	siteDone              map[string]bool // site issue keys replaced
	written, replaced     []string        // screenshot files, relative to snapshotDir
}

// issueKey is the key of the issues a change comes from (see
// model.Issue.Identify); it matches nothing for module changes.
func issueKey(ch model.Change) string { return ch.Check + "|" + ch.Title + "|" + ch.Source }

func keyOf(i model.Issue) string {
	if i.Key == "" {
		i.Identify()
	}
	return i.Key
}

// withKey returns the issues in from with key k.
func withKey(from []model.Issue, k string) []model.Issue {
	var out []model.Issue
	for _, i := range from {
		if keyOf(i) == k {
			out = append(out, i)
		}
	}
	return out
}

// replaceKey returns in without its issues of key k, plus from's.
func replaceKey(in, from []model.Issue, k string) []model.Issue {
	in = slices.DeleteFunc(in, func(i model.Issue) bool { return keyOf(i) == k })
	return append(in, withKey(from, k)...)
}

// site replaces the snapshot's site-level issues with the change's key
// by the run's, when the change is about one, and reports whether it was.
func (a *acceptor) site(ch model.Change) bool {
	k := issueKey(ch)
	if a.siteDone[k] {
		return true
	}
	if len(withKey(a.before.Issues, k))+len(withKey(a.after.Issues, k)) == 0 {
		return false
	}
	a.before.Issues = replaceKey(a.before.Issues, a.after.Issues, k)
	a.siteDone[k] = true
	return true
}

// page accepts ch on one page. It reports false when the page isn't in
// both runs: there's nothing to take its new baseline from.
func (a *acceptor) page(ch model.Change, path string) (bool, error) {
	bp, ap := a.before.Page(path), a.after.Page(path)
	if bp == nil || ap == nil {
		return false, nil
	}
	bp.Issues = replaceKey(bp.Issues, ap.Issues, issueKey(ch))
	mod, rule, _ := strings.Cut(ch.Check, "/")
	switch mod {
	case "visual":
		if err := a.shots(bp, ap); err != nil {
			return false, err
		}
	case "speed":
		bp.Metrics, bp.Resources = ap.Metrics, slices.Clone(ap.Resources)
	case "cache":
		bp.Cache = maps.Clone(ap.Cache)
	case "echo":
		bp.Echo = slices.Clone(ap.Echo)
		// Flows are kept on the run, by the page they start on.
		a.before.Echo = slices.DeleteFunc(a.before.Echo, func(e model.EchoAction) bool { return e.Page == path })
		for _, e := range a.after.Echo {
			if e.Page == path {
				a.before.Echo = append(a.before.Echo, e)
			}
		}
	case "health":
		switch rule {
		case "title":
			bp.Title = ap.Title
		case "content-lost":
			bp.TextHash, bp.TextLength = ap.TextHash, ap.TextLength
		case "redirect":
			bp.FinalURL, bp.Redirects = ap.FinalURL, slices.Clone(ap.Redirects)
		}
	}
	return true, nil
}

// shots copies a page's screenshots from the run into the snapshot,
// once per page, under new names; see Accept.
func (a *acceptor) shots(bp, ap *model.Page) error {
	if a.shotsDone[bp.Path] {
		return nil
	}
	a.shotsDone[bp.Path] = true
	if len(ap.Shots) == 0 {
		return nil
	}
	shots := maps.Clone(bp.Shots)
	if shots == nil {
		shots = map[string]model.Shot{}
	}
	for _, vp := range slices.Sorted(maps.Keys(ap.Shots)) {
		s := ap.Shots[vp]
		old := shots[vp]
		var err error
		if s.File, err = a.copyShot(s.File); err != nil {
			return err
		}
		if s.Second, err = a.copyShot(s.Second); err != nil {
			return err
		}
		a.replaced = append(a.replaced, old.File, old.Second)
		shots[vp] = s
	}
	bp.Shots = shots
	return nil
}

// copyShot copies one screenshot from the run into the snapshot and
// returns its name there, or "" for none.
func (a *acceptor) copyShot(rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	if why := checkShot(a.afterDir, rel); why != "" {
		if why == "is outside the snapshot" {
			why = "is outside the run"
		}
		return "", fmt.Errorf("can't accept: the run's screenshot %s %s", rel, why)
	}
	src, err := Contained(a.afterDir, rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(src) //nolint:gosec // inside the run folder, checked above
	if err != nil {
		return "", err
	}
	base := strings.TrimSuffix(path.Base(rel), ".png")
	name := "shots/" + base + a.suffix + ".png"
	if err := Saver(a.snapshotDir)(name, data); err != nil {
		return "", err
	}
	a.written = append(a.written, name)
	return name, nil
}

// discard removes the screenshots an accept that failed wrote.
func (a *acceptor) discard() {
	for _, f := range a.written {
		if p, err := Contained(a.snapshotDir, f); err == nil {
			_ = os.Remove(p)
		}
	}
}

// removeReplaced removes the screenshots the snapshot no longer uses.
// It's best effort: a file left behind only takes space.
func (a *acceptor) removeReplaced() {
	used := map[string]bool{}
	for _, p := range a.before.Pages {
		for _, s := range p.Shots {
			used[s.File], used[s.Second] = true, true
		}
	}
	for _, f := range a.replaced {
		if f == "" || used[f] {
			continue
		}
		if p, err := Contained(a.snapshotDir, f); err == nil {
			_ = os.Remove(p)
		}
	}
}
