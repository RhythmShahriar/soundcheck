// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package history keeps a short record of every snapshot, run and
// comparison of a site in .soundcheck/history.jsonl, one JSON line each,
// so slow drift across many updates shows up: page counts, issue
// counts, the verdict and median timings, plus timings for the first
// pages of each run.
//
// The file is append-only: each line is written with a single write to a
// file opened with O_APPEND, so two runs finishing at once can't
// interleave their lines.
package history

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/soundcheck/echolink"
	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/store"
)

// Metrics are the metrics Render and Value accept, as --metric names
// them: the timings, then the open accessibility (a11y) and keyboard
// problems of --eaa runs.
var Metrics = []string{"lcp", "tbt", "ttfb", "a11y", "keyboard"}

// File is the history's name inside the store folder.
const File = "history.jsonl"

// Schema identifies the layout of a history line.
const Schema = "rhythm-soundcheck/history/v1"

// MaxPages caps the per-page timings kept for each entry, so a big site
// doesn't make every line huge.
const MaxPages = 50

// Kinds of entries.
const (
	KindSnapshot   = "snapshot"
	KindRun        = "run"
	KindComparison = "comparison"
)

// Verdicts. A comparison is broke, changed or clean; a snapshot or run
// has errors, warnings or is clean.
const (
	Broke    = "broke"
	Changed  = "changed"
	Errors   = "errors"
	Warnings = "warnings"
	Clean    = "clean"
)

// Entry is one line of the history.
type Entry struct {
	Schema  string         `json:"schema"`
	At      time.Time      `json:"at"`             // when the run started
	Kind    string         `json:"kind"`           // snapshot, run or comparison
	Name    string         `json:"name,omitempty"` // the snapshot's name; for a comparison, the snapshot it was compared with
	Target  string         `json:"target"`
	Version string         `json:"version"` // Soundcheck's version
	Pages   int            `json:"pages"`
	Issues  map[string]int `json:"issues"`            // grouped issues by level
	Changes map[string]int `json:"changes,omitempty"` // a comparison's changes by class
	Verdict string         `json:"verdict"`
	LCP     float64        `json:"lcp_ms"` // medians over the pages that loaded
	TBT     float64        `json:"tbt_ms"`
	TTFB    float64        `json:"ttfb_ms"`
	PageSet []PageMetrics  `json:"page_metrics,omitempty"` // the first MaxPages pages that loaded
	// A11yOpen and KeyboardOpen count the distinct a11y/* and
	// keyboard/* problems of a run with the a11y or keyboard module on;
	// A11yByCriterion counts both by the WCAG criterion they fail. A
	// run without the modules leaves them out, so "not measured" reads
	// apart from 0: the counts are pointers for that reason.
	A11yOpen        *int           `json:"a11y_open,omitempty"`
	A11yByCriterion map[string]int `json:"a11y_by_criterion,omitempty"`
	KeyboardOpen    *int           `json:"keyboard_open,omitempty"`
}

// PageMetrics are one page's timings in an entry and, for a run with
// the a11y or keyboard module on, its open problems of each.
type PageMetrics struct {
	Path string  `json:"path"`
	LCP  float64 `json:"lcp_ms"`
	TBT  float64 `json:"tbt_ms"`
	TTFB float64 `json:"ttfb_ms"`
	CLS  float64 `json:"cls"`
	// A11yOpen and KeyboardOpen count the page's distinct a11y/* and
	// keyboard/* problems; nil when the module was off (or the line was
	// written before 1.3).
	A11yOpen     *int `json:"a11y_open,omitempty"`
	KeyboardOpen *int `json:"keyboard_open,omitempty"`
}

// New makes the entry for a run, compared with a snapshot when c isn't
// nil.
func New(run *model.Run, c *model.Comparison) Entry {
	e := Entry{Schema: Schema, At: run.Started.UTC(), Kind: KindRun, Name: run.Name, Target: run.Target,
		Version: run.Version, Pages: len(run.Pages), Issues: map[string]int{}}
	if run.Name != "" {
		e.Kind = KindSnapshot
	}
	for _, i := range groupIssues(run) {
		e.Issues[string(i)]++
	}
	var lcp, tbt, ttfb []float64
	for _, p := range run.Pages {
		if p.Error != "" {
			continue
		}
		m := p.Metrics
		// A zero LCP or TTFB means it wasn't measured; zero blocking time
		// is a real (and good) value.
		if m.LCP > 0 {
			lcp = append(lcp, m.LCP)
		}
		if m.TTFB > 0 {
			ttfb = append(ttfb, m.TTFB)
		}
		tbt = append(tbt, m.TBT)
		if len(e.PageSet) < MaxPages {
			e.PageSet = append(e.PageSet, PageMetrics{Path: pagePath(p.Path), LCP: m.LCP, TBT: m.TBT, TTFB: m.TTFB, CLS: m.CLS})
		}
	}
	e.LCP, e.TBT, e.TTFB = median(lcp), median(tbt), median(ttfb)
	unstable := c.Unstable()
	accessibility(&e, run, unstable)
	pageAccessibility(&e, run, unstable)
	e.Verdict = Clean
	switch {
	case e.Issues[string(model.Error)] > 0:
		e.Verdict = Errors
	case e.Issues[string(model.Warning)] > 0:
		e.Verdict = Warnings
	}
	if c != nil {
		e.Kind, e.Name = KindComparison, c.Before.Name
		e.Changes = map[string]int{}
		for _, ch := range c.Changes {
			e.Changes[ch.Class]++
		}
		e.Verdict = Clean
		switch {
		case e.Changes[model.Broke] > 0:
			e.Verdict = Broke
		case e.Changes[model.New]+e.Changes[model.Changed]+e.Changes[model.Slower] > 0:
			e.Verdict = Changed
		}
	}
	return e
}

// accessibility fills an entry's a11y and keyboard counts when the run
// had those modules on (--eaa turns both on): each distinct problem
// once, however many pages it is on, as the reports count them. A
// comparison's UNSTABLE findings aren't counted.
func accessibility(e *Entry, run *model.Run, unstable model.UnstableSet) {
	o := run.Options
	a11y := o.EAA || slices.Contains(o.Modules, "a11y")
	keyboard := o.EAA || slices.Contains(o.Modules, "keyboard")
	if !a11y && !keyboard {
		return
	}
	seen := map[string]bool{}
	var open, keys int
	byCriterion := map[string]int{}
	for _, i := range run.AllIssues() {
		isA11y, isKey := strings.HasPrefix(i.Check, "a11y/"), strings.HasPrefix(i.Check, "keyboard/")
		if !isA11y && !isKey {
			continue
		}
		path := ""
		if len(i.Pages) == 1 {
			path = i.Pages[0]
		}
		if unstable.Has(i, path) {
			continue
		}
		k := i.Key
		if k == "" {
			i.Identify()
			k = i.Key
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		if isA11y {
			open++
		} else {
			keys++
		}
		for _, id := range i.WCAG {
			byCriterion[id]++
		}
	}
	if a11y {
		e.A11yOpen = &open
	}
	if keyboard {
		e.KeyboardOpen = &keys
	}
	if len(byCriterion) > 0 {
		e.A11yByCriterion = byCriterion
	}
}

// pageAccessibility fills the a11y and keyboard counts of each page in
// the entry's PageSet, as accessibility does for the site: each distinct
// problem on the page once.
func pageAccessibility(e *Entry, run *model.Run, unstable model.UnstableSet) {
	if e.A11yOpen == nil && e.KeyboardOpen == nil {
		return
	}
	at := map[string]int{}
	for i, p := range e.PageSet {
		at[p.Path] = i
	}
	for _, p := range run.Pages {
		path := pagePath(p.Path)
		i, ok := at[path]
		if !ok || p.Error != "" {
			continue
		}
		seen := map[string]bool{}
		var open, keys int
		for _, is := range p.Issues {
			isA11y, isKey := strings.HasPrefix(is.Check, "a11y/"), strings.HasPrefix(is.Check, "keyboard/")
			if !isA11y && !isKey || unstable.Has(is, p.Path) {
				continue
			}
			if is.Key == "" {
				is.Identify()
			}
			if seen[is.Key] {
				continue
			}
			seen[is.Key] = true
			if isA11y {
				open++
			} else {
				keys++
			}
		}
		if e.A11yOpen != nil {
			e.PageSet[i].A11yOpen = &open
		}
		if e.KeyboardOpen != nil {
			e.PageSet[i].KeyboardOpen = &keys
		}
		delete(at, path) // a path listed twice counts once, as the timings do
	}
}

// pagePath is a page path as the history keeps it: a token in the path
// (/reset/<token>/) or a one-time code masked the way statement drafts
// and keyboard traces mask them, as history.jsonl ends up in CI caches.
func pagePath(p string) string { return echolink.RedactURL(p) }

// samePage reports whether a recorded path is the page asked for, which
// is given as the visitor sees it and masked the way the history keeps
// it. Lines written before paths were masked hold it as it was.
func samePage(recorded, page string) bool {
	return recorded == page || recorded == pagePath(page)
}

// groupIssues returns the level of each distinct issue in a run, the
// same problem on many pages counted once, as the reports count them.
func groupIssues(run *model.Run) []model.Level {
	seen := map[string]bool{}
	var out []model.Level
	for _, i := range run.AllIssues() {
		k := i.Key
		if k == "" {
			i.Identify()
			k = i.Key
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, i.Level)
		}
	}
	return out
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return math.Round((s[n/2-1]+s[n/2])/2*10) / 10
}

// Append adds the entry for run (and its comparison, if any) to the
// history in storeRoot.
func Append(storeRoot string, run *model.Run, c *model.Comparison) error {
	line, err := json.Marshal(New(run, c))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(storeRoot, 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(storeRoot, File), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // inside the store folder
	if err != nil {
		return err
	}
	// One write per line: with O_APPEND the kernel places it at the end
	// as a whole, even when another process appends at the same time.
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Read returns the history in storeRoot, oldest first. A missing file is
// an empty history. Lines that don't parse (a run killed mid-write) are
// skipped.
func Read(storeRoot string) ([]Entry, error) {
	data, err := os.ReadFile(filepath.Join(storeRoot, File)) //nolint:gosec // inside the store folder
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Schema == Schema {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b Entry) int { return a.At.Compare(b.At) })
	return out, sc.Err()
}

// key identifies an entry, so Backfill doesn't add one twice.
func (e Entry) key() string {
	return e.Kind + "|" + e.At.UTC().Format(time.RFC3339Nano) + "|" + e.Name + "|" + e.Target
}

// Backfill adds entries for the snapshots, runs and comparisons already
// in storeRoot that the history doesn't have yet and returns how many
// it added. Entries for runs that were since pruned are kept. Runs made
// by an incompatible Soundcheck are skipped.
func Backfill(storeRoot string) (int, error) {
	have, err := Read(storeRoot)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for _, e := range have {
		seen[e.key()] = true
	}
	var found []Entry
	for _, kind := range []string{"snapshots", "runs"} {
		dirs, err := os.ReadDir(filepath.Join(storeRoot, kind))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			dir := filepath.Join(storeRoot, kind, d.Name())
			run, err := store.LoadRun(dir)
			if err != nil {
				continue
			}
			var c *model.Comparison
			if kind == "runs" {
				c, _ = store.LoadComparison(dir) // nil for a plain run
			}
			if e := New(run, c); !seen[e.key()] {
				seen[e.key()] = true
				found = append(found, e)
			}
		}
	}
	if len(found) == 0 {
		return 0, nil
	}
	all := append(have, found...)
	slices.SortStableFunc(all, func(a, b Entry) int { return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.Kind, b.Kind)) })
	var buf bytes.Buffer
	for _, e := range all {
		line, err := json.Marshal(e)
		if err != nil {
			return 0, err
		}
		buf.Write(append(line, '\n'))
	}
	// The whole file is rewritten (atomically) to keep it in time order.
	if err := cache.WriteFile(filepath.Join(storeRoot, File), buf.Bytes()); err != nil {
		return 0, fmt.Errorf("writing the history: %w", err)
	}
	return len(found), nil
}
