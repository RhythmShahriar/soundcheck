// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package model

import (
	"fmt"
	"slices"
	"sync"
	"testing"
)

func TestRunPage(t *testing.T) {
	r := &Run{}
	if r.Page("/") != nil {
		t.Fatal("empty run found a page")
	}
	r.Pages = []Page{{Path: "/", Title: "home"}, {Path: "/a"}, {Path: "/", Title: "dup"}}
	if p := r.Page("/"); p == nil || p.Title != "home" {
		t.Fatalf("Page(/) = %+v, want the first of two", p)
	}
	if r.Page("/missing") != nil {
		t.Error("found a missing page")
	}
	r.Pages = append(r.Pages, Page{Path: "/b"})
	if r.Page("/b") == nil {
		t.Error("an appended page wasn't found")
	}
	r.Pages = []Page{{Path: "/c"}, {Path: "/d"}, {Path: "/e"}, {Path: "/f"}}
	if r.Page("/a") != nil || r.Page("/f") == nil {
		t.Error("a replaced slice of the same length kept the old index")
	}
	slices.Reverse(r.Pages)
	if p := r.Page("/f"); p == nil || p.Path != "/f" {
		t.Errorf("after reordering in place Page(/f) = %+v", p)
	}
	r.Page("/c").Title = "edited"
	if r.Pages[slices.IndexFunc(r.Pages, func(p Page) bool { return p.Path == "/c" })].Title != "edited" {
		t.Error("Page must point into Pages")
	}

	cp := *r
	cp.Pages = []Page{{Path: "/copy"}}
	if cp.Page("/copy") == nil || r.Page("/copy") != nil || r.Page("/c") == nil {
		t.Error("a copied run must keep its own index")
	}
}

func TestRunPageConcurrentReaders(t *testing.T) {
	r := &Run{}
	for i := range 500 {
		r.Pages = append(r.Pages, Page{Path: fmt.Sprintf("/p%d", i)})
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 500 {
				if p := r.Page(fmt.Sprintf("/p%d", (i+g)%500)); p == nil {
					t.Error("page not found")
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestPageCanceled(t *testing.T) {
	for err, want := range map[string]bool{
		"": false, CanceledError: true, "navigate: " + CanceledError: true,
		"net::ERR_CONNECTION_REFUSED": false, "context canceled by the site": false,
	} {
		if got := (&Page{Error: err}).Canceled(); got != want {
			t.Errorf("Canceled(%q) = %v, want %v", err, got, want)
		}
	}
}
