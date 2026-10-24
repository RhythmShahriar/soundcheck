// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package checktest

import (
	"context"
	"testing"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

func TestTab(t *testing.T) {
	ctx := context.Background()
	tab := &Tab{
		Results: map[string]any{"() => [1, 2]": []int{1, 2}},
		Shot:    []byte("png"),
		Events:  []check.Event{{Kind: check.Response, URL: "https://shop.test/", Status: 200}},
	}
	var got []int
	if err := tab.Eval(ctx, "() => [1, 2]", &got); err != nil || len(got) != 2 || got[1] != 2 {
		t.Errorf("Eval = %v, %v", got, err)
	}
	if err := tab.Eval(ctx, "() => [1, 2]", nil); err != nil {
		t.Errorf("Eval into nil: %v", err)
	}
	if err := tab.Eval(ctx, "() => 3", &got); err == nil {
		t.Error("an unlisted script succeeded")
	}
	if shot, _ := tab.Screenshot(ctx, true); string(shot) != "png" {
		t.Errorf("Screenshot = %q", shot)
	}
	var events []check.Event
	tab.On(ctx, func(e check.Event) { events = append(events, e) })()
	if len(events) != 1 || events[0].Status != 200 {
		t.Errorf("events = %+v", events)
	}
	if tab.Rod() != nil {
		t.Error("a fake has a rod page")
	}
	if n := len(tab.Evals()); n != 3 {
		t.Errorf("%d evals recorded, want 3", n)
	}
}

func TestContext(t *testing.T) {
	cc, files := Context("https://shop.test/")
	if cc.Base.Host != "shop.test" || cc.Run == nil || cc.Config.Tabs == 0 {
		t.Fatalf("context = %+v", cc)
	}
	cc.AddIssue(model.Issue{Check: "x/y", Title: "T"})
	if len(cc.Run.Issues) != 1 {
		t.Error("AddIssue didn't record on the run")
	}
	if err := cc.Save("a.png", []byte("a")); err != nil || string(files.Get("a.png")) != "a" {
		t.Errorf("Save: %v", err)
	}
	if files.Get("b.png") != nil {
		t.Error("a file nobody saved is there")
	}
}
