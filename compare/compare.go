// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package compare turns two runs into a ranked list of what changed: new
// errors are BROKE, other new problems NEW, problems that went away
// FIXED, plus whatever the modules' comparers report (visual changes,
// slowdowns, lost content).
package compare

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/model"
)

// Compare diffs cc.Before and cc.After.
//
// Pages whose visit was canceled (the run was stopped) are left out:
// they recorded nothing real and comparing them would report the site
// as broken. The comparison is then marked as interrupted.
func Compare(ctx context.Context, cc *check.CompareContext, modules []check.Module, version string) *model.Comparison {
	// Settings and pages missing from the run are judged on the runs as
	// given: a canceled page was reached, so it isn't counted as missing.
	warnings := mismatch(cc.Before, cc.After)
	before, droppedBefore := withoutCanceled(cc.Before)
	after, dropped := withoutCanceled(cc.After)
	c := *cc
	c.Before, c.After = before, after
	moduleWarn, moduleWarnings := collect()
	c.WarnFunc = moduleWarn
	cc = &c
	out := start(cc, version, len(cc.After.Pages), warnings, droppedBefore, dropped)
	changes, ignored, notes := IssuesWithGaps(cc.Before, cc.After)
	out.Changes = compared(changes, cc, modules)
	aside(out, compared(ignored, cc, modules), notes)
	warn := func(msg string) { out.Warnings = append(out.Warnings, msg) }
	for _, m := range modules {
		if c, ok := m.(check.Comparer); ok {
			safe(m.Name(), "Compare", warn, func() { out.Changes = append(out.Changes, c.Compare(ctx, cc)...) })
		}
	}
	out.Warnings = append(out.Warnings, moduleWarnings()...)
	return finish(out, cc, modules)
}

// collect gathers the warnings modules give through CompareContext.Warn,
// from any goroutine, for the comparison to add once they're done.
func collect() (warn func(string), all func() []string) {
	var mu sync.Mutex
	var got []string
	return func(msg string) {
			mu.Lock()
			got = append(got, msg)
			mu.Unlock()
		}, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(got)
		}
}

// start begins the comparison of cc's runs, of which the current one has
// pages pages that were checked: the warnings about how the runs differ
// and what was interrupted. droppedBefore and dropped count the canceled
// pages left out of each run.
func start(cc *check.CompareContext, version string, pages int, warnings []string, droppedBefore, dropped int) *model.Comparison {
	out := &model.Comparison{
		Schema: model.Schema, Tool: "rhythm-soundcheck", Version: version, Created: time.Now().UTC(),
		Before: ref(cc.Before, cc.BeforeDir, cc.After.Options.EAA), After: ref(cc.After, cc.AfterDir, cc.After.Options.EAA), Pages: pages,
	}
	// The run's own notes (a skipped leak test, unreadable flows) belong in
	// the comparison too, so JSON readers see them.
	out.Warnings = append(warnings, cc.After.Warnings...)
	var notes []string
	if out.Interrupted = cc.After.Interrupted || dropped > 0; out.Interrupted {
		note := "interrupted: the run was stopped before it finished; pages it didn't reach weren't checked"
		if dropped > 0 {
			note = "interrupted: " + pagesNotChecked(dropped)
		}
		notes = append(notes, note)
	}
	if droppedBefore > 0 {
		notes = append(notes, fmt.Sprintf("the snapshot was interrupted: %s in it weren't finished and aren't compared", plural(droppedBefore)))
	}
	out.Warnings = append(notes, out.Warnings...)
	return out
}

// aside puts the issue changes that can't count (an audit that didn't
// finish, an a11y rule one run didn't apply or an element past the
// per-rule cap) in the comparison's Ignored list and adds their
// warnings. Ignore rules add theirs to the list later (ApplyIgnore).
//
// A module's own warning about a page a not-checked note names is
// dropped: the note says it already (see dropCovered).
func aside(out *model.Comparison, ignored []model.Change, notes []string) {
	if len(ignored) > 0 {
		out.Ignored = append(out.Ignored, ignored...)
		Sort(out.Ignored)
	}
	out.Warnings = dropCovered(append(out.Warnings, notes...))
}

// compared drops the issue changes of modules that didn't run in both
// runs and of modules that report their own (check.IssueOwner).
func compared(changes []model.Change, cc *check.CompareContext, modules []check.Module) []model.Change {
	// A module that ran only once has nothing to compare against: all its
	// issues would look new (or fixed).
	// The a11y and keyboard modules --eaa added are the exception: the
	// snapshot had none of their issues, so each one now is new.
	if b, a := cc.Before.Options.Modules, cc.After.Options.Modules; len(b) > 0 && len(a) > 0 {
		added := addedByEAA(cc.Before, cc.After)
		changes = slices.DeleteFunc(changes, func(c model.Change) bool {
			mod, _, _ := strings.Cut(c.Check, "/")
			return (!slices.Contains(b, mod) || !slices.Contains(a, mod)) && !added(mod)
		})
	}
	for _, m := range modules {
		if _, ok := m.(check.IssueOwner); ok {
			changes = slices.DeleteFunc(changes, func(c model.Change) bool {
				return strings.HasPrefix(c.Check, m.Name()+"/")
			})
		}
	}
	return changes
}

// finish runs the modules' change filters over every change, keeps one
// line per cause, sorts and completes the comparison.
func finish(out *model.Comparison, cc *check.CompareContext, modules []check.Module) *model.Comparison {
	warn := func(msg string) { out.Warnings = append(out.Warnings, msg) }
	for _, m := range modules {
		if f, ok := m.(check.ChangeFilter); ok {
			// The filter gets a copy: one that panics halfway through
			// filtering in place would otherwise leave a mangled list.
			safe(m.Name(), "FilterChanges", warn, func() { out.Changes = f.FilterChanges(slices.Clone(out.Changes)) })
		}
	}
	out.Changes = oneCause(out.Changes, siteIssue(cc.Before, cc.After))
	Sort(out.Changes)
	out.Complete() // IDs follow the sorted order, so they're deterministic too
	return out
}

// siteIssue reports whether a change comes from a site-level issue of
// either run (a broken link, found once with the pages that link to it)
// rather than from something on the pages it lists.
func siteIssue(runs ...*model.Run) func(model.Change) bool {
	keys := map[string]bool{}
	for _, r := range runs {
		for _, i := range r.Issues {
			keys[i.Check+"|"+i.Title+"|"+i.Source] = true
		}
	}
	return func(c model.Change) bool { return keys[c.Check+"|"+c.Title+"|"+c.Source] }
}

// withoutCanceled returns r without the pages whose visit was canceled
// and how many it left out. r itself is returned when there are none.
func withoutCanceled(r *model.Run) (*model.Run, int) {
	n := 0
	for i := range r.Pages {
		if r.Pages[i].Canceled() {
			n++
		}
	}
	if n == 0 {
		return r, 0
	}
	cp := *r
	cp.Pages = slices.DeleteFunc(slices.Clone(r.Pages), func(p model.Page) bool { return p.Canceled() })
	cp.Interrupted = true
	return &cp, n
}

func pagesNotChecked(n int) string {
	if n == 1 {
		return "1 page was not checked"
	}
	return fmt.Sprintf("%d pages were not checked", n)
}

func plural(n int) string {
	if n == 1 {
		return "1 page"
	}
	return fmt.Sprintf("%d pages", n)
}

// ref points at run r stored in dir. Under --eaa its start URL is kept
// as the reports show it (echolink.RedactTarget): comparison.json lands
// in CI artifacts like they do. Only the stored run needs the URL as
// typed, to visit the site again.
func ref(r *model.Run, dir string, eaa bool) model.RunRef {
	target := r.Target
	if eaa {
		target = echolink.RedactTarget(target)
	}
	return model.RunRef{Target: target, Name: r.Name, Dir: dir, Started: r.Started}
}

// Issues diffs the issues of two runs. Issues are matched by key on the
// same page (page issues) or by key alone (site issues). A page that only
// exists in one run doesn't make its issues new or fixed. Neither does a
// page whose a11y or keyboard audit didn't finish on one side (see
// IssuesWithGaps).
func Issues(before, after *model.Run) []model.Change {
	return diffIssues(before, after).changes()
}

// IssuesWithGaps is Issues that also returns the issue changes of pages
// whose audit didn't finish on one side (model.Audit), which aren't
// counted. They have the class they would have had and a Reason
// ("not checked on this page: a11y timed out"), for the comparison's
// Ignored list. notes are its warnings, one per page and module, sorted.
func IssuesWithGaps(before, after *model.Run) (changes, ignored []model.Change, notes []string) {
	d := diffIssues(before, after)
	return d.changes(), d.ignoredChanges(), d.sortedNotes()
}

func diffIssues(before, after *model.Run) *issueDiff {
	ran, listed := ranInBoth(before, after), func(m string) bool { return slices.Contains(after.Options.Modules, m) }
	d := newIssueDiff(rulesOf(before, after, ran), keptFrom(before))
	diffed := diffedIn(before, after)
	// Every page with a path counts, when a path is there twice.
	byPath := func(r *model.Run) map[string][]model.Issue {
		m := map[string][]model.Issue{}
		for _, p := range r.Pages {
			m[p.Path] = append(m[p.Path], p.Issues...)
		}
		return m
	}
	b, a := byPath(before), byPath(after)
	ba, aa := auditsByPath(before), auditsByPath(after)
	for path, ai := range a {
		if bi, ok := b[path]; ok {
			d.page(path, bi, ai, aa[path], keyboardOf(after, path), gaps(path, ba[path], aa[path], diffed, listed, d.kept), d.guard(bi, ba[path], aa[path]))
		} else {
			d.newPage(path, aa[path], diffed)
		}
	}
	d.site(before.Issues, after.Issues)
	return d
}

// keyboardOf is the keyboard summary of r's first page with path that
// has one, or nil.
func keyboardOf(r *model.Run, path string) *model.KeyboardSummary {
	for i := range r.Pages {
		if p := &r.Pages[i]; p.Path == path && p.Keyboard != nil {
			return p.Keyboard
		}
	}
	return nil
}

// auditsByPath is the audits of r's pages by path (see mergeAudits).
func auditsByPath(r *model.Run) map[string]map[string]model.Audit {
	m := map[string]map[string]model.Audit{}
	for _, p := range r.Pages {
		m[p.Path] = mergeAudits(m[p.Path], p.Audits)
	}
	return m
}

// issueDiff collects the changes Issues reports a path at a time, so
// Stream can build them without holding either run. One PHP warning on
// 14 pages is one change. The result doesn't depend on the order paths
// come in: pages are a set; the detail comes from the lowest page
// path (a page's issue before a site issue listing the same page).
//
// Issues of a module whose audit didn't finish on a page go to ignored
// instead, grouped by reason too, with a note for the page. So do the
// a11y issues the guards put aside (see pageGuard).
type issueDiff struct {
	groups  map[string]*issueGroup
	ignored map[string]*issueGroup // by reason, class and key
	notes   map[string]bool
	rules   ruleSets
	moved   map[string]bool // rules whose NEW changes were put aside as not in the baseline
	judges  judges          // modules that say which missing issues a page can't judge
	kept    string          // where kept issues come from, for the notes: see keptFrom
}

func newIssueDiff(rules ruleSets, kept string) *issueDiff {
	return &issueDiff{groups: map[string]*issueGroup{}, ignored: map[string]*issueGroup{}, notes: map[string]bool{},
		rules: rules, moved: map[string]bool{}, judges: unjudgers(), kept: kept}
}

// keptFrom names the earlier run of a comparison with before, for the
// notes about pages not checked: the snapshot, or the other site's run
// of an --against comparison, which has no snapshot (and no name).
func keptFrom(before *model.Run) string {
	if before == nil || before.Name == "" {
		return "the other site's run"
	}
	return "the snapshot"
}

// guard is the a11y guard of a page pair: the snapshot's issues on the
// page and both sides' audits.
func (d *issueDiff) guard(before []model.Issue, b, a map[string]model.Audit) *pageGuard {
	return newPageGuard(&d.rules, before, b, a)
}

type issueGroup struct {
	change     model.Change
	pages      map[string]bool
	detailFrom string // the lowest page path seen, where the detail came from
	detailSite bool   // the detail came from a site issue
}

// issueAt is an issue found on pages, or on the site (a broken link,
// which re-visiting pages doesn't check again).
type issueAt struct {
	issue model.Issue
	pages []string
	site  bool
}

func (d *issueDiff) add(class string, x issueAt) {
	addTo(d.groups, class+"|"+x.issue.Key, class, x)
}

// addAside adds a change that doesn't count, with the reason it gives
// and the comparison's note for it ("" for none): one of a page whose
// audit didn't finish (see unaudited) or one a guard put aside.
func (d *issueDiff) addAside(class string, x issueAt, reason, note string) {
	g := addTo(d.ignored, reason+"|"+class+"|"+x.issue.Key, class, x)
	g.change.Reason = reason
	if note != "" {
		d.notes[note] = true
	}
}

func addTo(groups map[string]*issueGroup, k, class string, x issueAt) *issueGroup {
	g := groups[k]
	first := ""
	if len(x.pages) > 0 {
		first = slices.Min(x.pages)
	}
	if g == nil {
		// WCAG, EN301549, Impact and FocusPath follow from the check and
		// the element, which the key holds, so the first issue's do.
		g = &issueGroup{change: model.Change{Class: class, Check: x.issue.Check, Title: x.issue.Title, Detail: x.issue.Detail,
			Source: x.issue.Source, Fix: x.issue.Fix, FromPageIssue: !x.site && !x.issue.FromSite, Key: x.issue.Key,
			WCAG: slices.Clone(x.issue.WCAG), EN301549: slices.Clone(x.issue.EN301549), Impact: x.issue.Impact,
			FocusPath: slices.Clone(x.issue.FocusPath)}, pages: map[string]bool{}, detailFrom: first, detailSite: x.site}
		groups[k] = g
	} else if first < g.detailFrom || (first == g.detailFrom && g.detailSite && !x.site) {
		g.change.Detail, g.detailFrom, g.detailSite = x.issue.Detail, first, x.site
	}
	g.change.FromPageIssue = g.change.FromPageIssue && !x.site && !x.issue.FromSite
	for _, p := range x.pages {
		g.pages[p] = true
	}
	return g
}

// page diffs the issues of every page with path in each run. The first
// issue with a key is the one reported. The issues of the modules in
// gaps go aside. So do the a11y changes guard puts aside and the issues
// missing from the later page that their module can't judge there with
// its audits and its keyboard summary kb (see Unjudger).
func (d *issueDiff) page(path string, before, after []model.Issue, audits map[string]model.Audit, kb *model.KeyboardSummary, gaps unaudited, guard *pageGuard) {
	byKey := func(is []model.Issue) map[string]model.Issue {
		m := make(map[string]model.Issue, len(is))
		for _, i := range is {
			if _, ok := m[i.Key]; !ok {
				m[i.Key] = i
			}
		}
		return m
	}
	b, a := byKey(before), byKey(after)
	// Every module the run couldn't audit here gets its note, as the run
	// says it, not only those with issues set aside.
	for _, g := range gaps {
		if g.now {
			d.notes[g.note] = true
		}
	}
	add := func(class string, i model.Issue) {
		x := issueAt{issue: i, pages: []string{path}}
		if gap, ok := gaps.of(i.Check); ok {
			d.addAside(class, x, gap.reason, gap.note)
			return
		}
		// An issue missing from a walk that stopped at its cap, or from
		// a page whose widgets weren't tested, isn't fixed: the module
		// says the page can't judge it.
		if class == model.Fixed && d.judges.of(i.Key) != nil &&
			d.judges.cantJudge(&model.Page{Path: path, Issues: after, Audits: audits, Keyboard: kb}, i.Key) {
			reason, note := unjudged(path, i.Check, audits, d.kept)
			d.addAside(class, x, reason, note)
			return
		}
		if why := guard.reason(class, i); why != "" {
			if why == reasonNotInBaseline {
				r, _ := a11yRule(i.Check)
				d.moved[r] = true
			}
			d.addAside(class, x, why, "")
			return
		}
		d.add(class, x)
	}
	same := movedElements(b, a)
	if guard.counts() {
		// The cap guard judges a rule's FIXED changes on the page together.
		for k, i := range b {
			if _, ok := a[k]; !ok && !same[k] {
				guard.count(model.Fixed, i)
			}
		}
	}
	for k, i := range a {
		if _, ok := b[k]; !ok && !same[k] {
			add(appeared(i), i)
		}
	}
	for k, i := range b {
		if _, ok := a[k]; !ok && !same[k] {
			add(model.Fixed, i)
		}
	}
}

// movedElements pairs the a11y issues of a page that only the later run
// has with those only the earlier one has, by rule and element snippet
// (the first line of the issue's Detail). It returns the keys of both
// sides of each pair. An a11y issue is keyed by its selector, which can
// change when nothing about the element did: removing one of two images
// turns "main > img:nth-of-type(1)" into "main > img". That is the same
// problem, not one NEW plus one FIXED. Keys pair in sorted order, each
// once, so with three identical images down to one the other two are
// still FIXED.
func movedElements(before, after map[string]model.Issue) map[string]bool {
	var gone []string
	for k, i := range before {
		if _, ok := after[k]; !ok && snippetOf(i) != "" {
			gone = append(gone, k)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	slices.Sort(gone)
	var same map[string]bool
	for _, k := range sortedKeys(after) {
		i := after[k]
		if _, ok := before[k]; ok || snippetOf(i) == "" {
			continue
		}
		for _, g := range gone {
			if j := before[g]; !same[g] && j.Check == i.Check && snippetOf(j) == snippetOf(i) {
				if same == nil {
					same = map[string]bool{}
				}
				same[g], same[k] = true, true
				break
			}
		}
	}
	return same
}

// snippetOf is the element snippet an a11y issue's Detail starts with,
// or "" for an issue of another module or one without a snippet.
func snippetOf(i model.Issue) string {
	if _, ok := a11yRule(i.Check); !ok {
		return ""
	}
	first, _, _ := strings.Cut(i.Detail, "\n")
	if first = strings.TrimSpace(first); !strings.HasPrefix(first, "<") {
		return ""
	}
	return first
}

// newPage notes the modules whose audit didn't finish on a page only
// the later run has (see newPageNotes).
func (d *issueDiff) newPage(path string, after map[string]model.Audit, ran func(string) bool) {
	for _, n := range newPageNotes(path, after, ran) {
		d.notes[n] = true
	}
}

// site diffs the site-level issues. The last issue with a key is the
// one reported.
func (d *issueDiff) site(before, after []model.Issue) {
	byKey := func(is []model.Issue) map[string]model.Issue {
		m := make(map[string]model.Issue, len(is))
		for _, i := range is {
			m[i.Key] = i
		}
		return m
	}
	b, a := byKey(before), byKey(after)
	for k, i := range a {
		if _, ok := b[k]; !ok {
			d.add(appeared(i), issueAt{issue: i, pages: i.Pages, site: true})
		}
	}
	for k, i := range b {
		if _, ok := a[k]; !ok {
			d.add(model.Fixed, issueAt{issue: i, pages: i.Pages, site: true})
		}
	}
}

// appeared is the class of an issue that wasn't there before: BROKE for
// an error, NEW for the rest.
func appeared(i model.Issue) string {
	if i.Level == model.Error {
		return model.Broke
	}
	return model.New
}

// changes returns the collected changes, in a fixed order.
func (d *issueDiff) changes() []model.Change { return grouped(d.groups) }

// ignoredChanges returns the changes put aside, in a fixed order.
func (d *issueDiff) ignoredChanges() []model.Change { return grouped(d.ignored) }

// sortedNotes returns the warnings for the pages whose changes were put
// aside, then the one naming the rules the snapshot didn't apply.
func (d *issueDiff) sortedNotes() []string {
	notes := sortedKeys(d.notes)
	if n := rulesNote(d.rules, d.moved); n != "" {
		notes = append(notes, n)
	}
	return notes
}

func grouped(groups map[string]*issueGroup) []model.Change {
	out := make([]model.Change, 0, len(groups))
	for _, k := range sortedKeys(groups) {
		g := groups[k]
		g.change.Pages = sortedKeys(g.pages)
		g.change.Score = float64(len(g.change.Pages))
		out = append(out, g.change)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// Sort orders changes by class, then by score (biggest first). Ties are
// broken by check, source, first page, title and detail, so the order
// never depends on the order the changes were found in.
func Sort(cs []model.Change) {
	firstPage := func(c model.Change) string {
		if len(c.Pages) == 0 {
			return ""
		}
		return c.Pages[0]
	}
	slices.SortStableFunc(cs, func(x, y model.Change) int {
		return cmp.Or(
			cmp.Compare(slices.Index(model.Classes, x.Class), slices.Index(model.Classes, y.Class)),
			cmp.Compare(y.Score, x.Score),
			strings.Compare(x.Check, y.Check),
			strings.Compare(x.Source, y.Source),
			strings.Compare(firstPage(x), firstPage(y)),
			strings.Compare(x.Title, y.Title),
			strings.Compare(x.Detail, y.Detail))
	})
}

// mismatch warns when two runs weren't made the same way, because their
// differences may then come from the settings, not the site.
func mismatch(before, after *model.Run) []string {
	missing := 0
	for _, p := range before.Pages {
		if after.Page(p.Path) == nil {
			missing++
		}
	}
	return mismatchWith(before, after, missing)
}

// mismatchWith is mismatch for runs whose pages aren't in memory: missing
// is how many pages of before have no page in after.
func mismatchWith(before, after *model.Run, missing int) []string {
	var w []string
	if note := versionNote(before.Version, after.Version); note != "" {
		w = append(w, note)
	}
	if b, a := before.Options.EngineName(), after.Options.EngineName(); b != a {
		w = append(w, fmt.Sprintf("browser engines differ (snapshot %s, now %s); screenshots and speed only compare in the same engine", b, a))
	}
	if !slices.Equal(before.Options.Viewports, after.Options.Viewports) {
		w = append(w, fmt.Sprintf("viewports differ (snapshot %v, now %v)", before.Options.Viewports, after.Options.Viewports))
	}
	if !slices.Equal(before.Options.Modules, after.Options.Modules) {
		var added []string
		for _, m := range eaaModules {
			if addedByEAA(before, after)(m) {
				added = append(added, m)
			}
		}
		switch len(added) {
		case 0:
			w = append(w, fmt.Sprintf("modules differ (snapshot %v, now %v); only modules in both are compared", before.Options.Modules, after.Options.Modules))
		default:
			w = append(w, fmt.Sprintf("modules differ (snapshot %v, now %v); only modules in both are compared, besides %s, which --eaa added: all their issues are new",
				before.Options.Modules, after.Options.Modules, strings.Join(added, " and ")))
		}
	}
	if !slices.Equal(before.Options.Levels, after.Options.Levels) {
		w = append(w, fmt.Sprintf("interaction levels differ (snapshot %v, now %v)", before.Options.Levels, after.Options.Levels))
	}
	if before.Options.Echo && after.Options.Echo && before.Options.EchoMode != after.Options.EchoMode {
		w = append(w, fmt.Sprintf("Echo's mode differs (snapshot %s, now %s); messages may differ only because of it", before.Options.EchoMode, after.Options.EchoMode))
	}
	if before.Options.LoggedIn != after.Options.LoggedIn {
		w = append(w, "one run was logged in and the other wasn't")
	}
	if missing > 0 {
		verb := "weren't"
		if missing == 1 {
			verb = "wasn't"
		}
		w = append(w, fmt.Sprintf("%s from the snapshot %s checked this time", plural(missing), verb))
	}
	return w
}
