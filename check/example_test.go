// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package check_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/check/checktest"
	"github.com/RhythmShahriar/soundcheck/model"
)

// langScript reads the page's language.
const langScript = "() => document.documentElement.lang"

// lang is a sample module: its LiveTab hook reads each page's language
// and counts console errors; its PageChecker reports pages without a
// language. Nothing in it needs Chrome to test.
type lang struct {
	mu     sync.Mutex
	langs  map[string]string
	errors map[string]int
}

func (*lang) Name() string { return "lang" }

func (m *lang) Checks() []check.CheckInfo {
	return []check.CheckInfo{{ID: "lang/missing", Level: model.Notice, Title: "No page language",
		Doc: "Screen readers and translators need <html lang>."}}
}

func (m *lang) LiveTab(ctx context.Context, _ *check.Context, tab check.Tab, p *model.Page) {
	var l string
	if err := tab.Eval(ctx, langScript, &l); err != nil {
		return
	}
	errs := 0
	stop := tab.On(ctx, func(e check.Event) {
		if e.Kind == check.Console && e.Level == "error" {
			errs++
		}
	})
	stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.langs[p.ID], m.errors[p.ID] = l, errs
}

func (m *lang) CheckPage(_ *check.Context, p *model.Page) {
	m.mu.Lock()
	l, seen := m.langs[p.ID]
	m.mu.Unlock()
	if seen && l == "" {
		check.Page(p, model.Issue{Check: "lang/missing", Level: model.Notice, Title: "No page language",
			Fix: `Add lang="en" (or the page's language) to <html>.`})
	}
}

func newLang() *lang { return &lang{langs: map[string]string{}, errors: map[string]int{}} }

// A fake Tab drives a module's hooks the way the runner does, with no
// browser.
func Example() {
	m := newLang()
	cc, _ := checktest.Context("https://shop.test/")
	p := &model.Page{ID: "home", Path: "/"}
	tab := &checktest.Tab{
		Results: map[string]any{langScript: ""},
		Events:  []check.Event{{Kind: check.Console, Level: "error", Text: "Uncaught TypeError"}},
	}
	m.LiveTab(context.Background(), cc, tab, p)
	m.CheckPage(cc, p)
	for _, i := range p.Issues {
		fmt.Println(i.Key)
	}
	fmt.Println("console errors:", m.errors["home"])
	// Output:
	// lang/missing|No page language|
	// console errors: 1
}

// The negative case: a page with a language has no issue.
func TestLangPresent(t *testing.T) {
	m := newLang()
	cc, _ := checktest.Context("https://shop.test/")
	p := &model.Page{ID: "home", Path: "/"}
	tab := &checktest.Tab{Results: map[string]any{langScript: "en"}}
	m.LiveTab(context.Background(), cc, tab, p)
	m.CheckPage(cc, p)
	if len(p.Issues) != 0 {
		t.Errorf("issues = %+v", p.Issues)
	}
	if got := tab.Evals(); len(got) != 1 || got[0] != langScript {
		t.Errorf("evals = %q", got)
	}
}
