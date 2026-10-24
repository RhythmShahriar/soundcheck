// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"strings"
	"testing"
)

// A module's block: the file may have the key once a module registered
// it, Block hands it over and DecodeBlock reads it the way Load reads
// the file, with the same errors.
func TestModuleBlocks(t *testing.T) {
	body := "rate: 2\nwidget:\n  level: 3\n  name: big\n"
	if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), `unknown setting "widget"`) {
		t.Fatalf("an unregistered block was accepted: %v", err)
	}
	RegisterBlock("widget")
	t.Cleanup(func() {
		blockMu.Lock()
		delete(blockNames, "widget")
		blockMu.Unlock()
	})
	c, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if !c.InFile("widget") || c.Rate != 2 {
		t.Errorf("InFile(widget) %v, rate %v", c.InFile("widget"), c.Rate)
	}
	var w struct {
		Level int    `yaml:"level"`
		Name  string `yaml:"name"`
	}
	node := c.Block("widget")
	if err := DecodeBlock("widget", node, &w); err != nil || w.Level != 3 || w.Name != "big" {
		t.Errorf("decoded %+v, %v", w, err)
	}
	if n := c.Block("nosuch"); n.Kind != 0 {
		t.Errorf("a block the file doesn't set: %+v", n)
	}
	if err := DecodeBlock("nosuch", c.Block("nosuch"), &w); err != nil || w.Level != 3 {
		t.Errorf("the zero Node changed %+v: %v", w, err)
	}
	if got := KeyLine(node, "name"); got != 4 {
		t.Errorf("KeyLine(name) = %d", got)
	}
	if got := KeyLine(node, "nosuch"); got != node.Line {
		t.Errorf("KeyLine(nosuch) = %d", got)
	}

	for body, want := range map[string]struct {
		line int
		msg  string
	}{
		"widget:\n  levl: 3\n":     {2, `unknown setting "widget.levl" (did you mean "widget.level"?)`},
		"widget:\n  level: high\n": {2, `widget.level: "high" isn't a number`},
		"widget: [a, b]\n":         {1, `widget: takes a group of settings, each on its own indented line`},
	} {
		c, err := Load(write(t, body))
		if err != nil {
			t.Fatal(err)
		}
		err = DecodeBlock("widget", c.Block("widget"), &w)
		var se *SettingError
		if !errors.As(err, &se) || se.Line != want.line || err.Error() != want.msg {
			t.Errorf("%q: %v, want line %d: %s", body, err, want.line, want.msg)
		}
	}
}
