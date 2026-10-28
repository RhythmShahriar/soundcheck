// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/crawl"
	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// planMod records the paths it was planned with.
type planMod struct {
	plain
	got *[][]string
}

func (m planMod) Plan(paths []string) { *m.got = append(*m.got, paths) }

func TestPlanPages(t *testing.T) {
	var got [][]string
	mods := []check.Module{plain("health"), planMod{"keyboard", &got}}
	done := []*model.Page{{Path: "/done/"}}
	pages := []crawl.Page{{URL: "https://example.test/b/"}, {URL: "https://example.test/a/?q=1"}}
	planPages(mods, done, pages)
	want := []string{"/done/", "/b/", "/a/?q=1"}
	if len(got) != 1 || !slices.Equal(got[0], want) {
		t.Fatalf("Plan got %q, want one call with %q", got, want)
	}

	// Without a planner nothing is built or called.
	got = nil
	planPages([]check.Module{plain("health")}, done, pages)
	if got != nil {
		t.Errorf("Plan called without a planner: %q", got)
	}
}

func TestStoredLinks(t *testing.T) {
	token := "abcdefghijklmnopqrstuvwxyz0123456789"
	got := storedLinks([]string{
		"https://example.test/a/?token=" + token,
		"https://example.test/a/#top",
		"https://example.test/b/",
		"https://example.test/password/reset/" + token,
	})
	want := []string{"https://example.test/a/", "https://example.test/b/", "https://example.test/password/reset/"}
	if len(got) != len(want) || !slices.Equal(got[:2], want[:2]) ||
		!strings.HasPrefix(got[2], want[2]) || strings.Contains(got[2], token) {
		t.Errorf("storedLinks = %q, want %q", got, want)
	}
	if storedLinks(nil) != nil {
		t.Error("nil links became a list")
	}
}

// An --eaa page keeps its subresources and failed requests without their
// query strings: a signed image URL is a credential like a link's token.
// The page it was copied from keeps its URLs.
func TestStorePage(t *testing.T) {
	sig := "sig" + strings.Repeat("a9f8e", 6)
	orig := model.Page{
		Links:     []string{"https://example.test/a/?x=" + sig},
		Resources: []model.Resource{{URL: "https://example.test/img.png?sig=" + sig, Type: "Image"}},
		Failed:    []model.Request{{URL: "https://example.test/api?sig=" + sig + "#f", Status: 500}},
	}
	p := orig
	storePage(&p)
	if p.Resources[0].URL != "https://example.test/img.png" || p.Failed[0].URL != "https://example.test/api" || p.Links[0] != "https://example.test/a/" {
		t.Errorf("stored page: %+v", p)
	}
	if !strings.Contains(orig.Resources[0].URL, sig) || !strings.Contains(orig.Failed[0].URL, sig) {
		t.Error("storePage changed the page it was copied from")
	}
}
