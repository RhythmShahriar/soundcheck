// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"reflect"
	"slices"
	"sync"

	"gopkg.in/yaml.v3"
)

// A module can keep its settings to itself (check.Configurable): the
// config file then has a top-level key named after it, which Load leaves
// for the module to decode with DecodeBlock.
var (
	blockMu    sync.Mutex
	blockNames = map[string]bool{}
)

// RegisterBlock lets the config file have a top-level key name whose
// settings a module decodes itself. check.Register calls it for every
// check.Configurable module.
func RegisterBlock(name string) {
	blockMu.Lock()
	defer blockMu.Unlock()
	blockNames[name] = true
}

func isBlock(name string) bool {
	blockMu.Lock()
	defer blockMu.Unlock()
	return blockNames[name]
}

// Block returns what the config file sets under the top-level key name,
// or the zero Node when the file doesn't set it.
func (c Config) Block(name string) yaml.Node {
	if n, ok := c.blocks[name]; ok {
		return *n
	}
	return yaml.Node{}
}

// DecodeBlock decodes a module's block of the config file (see Block)
// into v, a pointer to a struct, the way Load decodes the file: a key v
// doesn't have or a value of the wrong type is a SettingError that names
// the setting ("speed.slowr") and its line. The zero Node leaves v as it
// is.
func DecodeBlock(name string, node yaml.Node, v any) error {
	if node.Kind == 0 {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return &SettingError{Key: name, Line: node.Line, Msg: "takes a group of settings, each on its own indented line"}
	}
	keys, values := map[string]int{}, map[int]string{}
	if err := checkKeys(&node, reflect.TypeOf(v).Elem(), name+".", keys, values); err != nil {
		return err
	}
	if err := node.Decode(v); err != nil {
		return typeError(err, values)
	}
	return nil
}

// KeyLine returns the line of key in a block, or the block's own line
// when it doesn't have the key: where an error about that setting
// points.
func KeyLine(node yaml.Node, key string) int {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i].Line
		}
	}
	return node.Line
}

// SetBlockValue sets key in the block name to value, as if the config
// file said `name: {key: value}`, for a flag that sets a module's own
// setting: --wcag sets a11y.wcag. The module's Configure and helpers such
// as a11y.Standard then read the flag's value. The rest of the block
// stays. Copies of c made before keep the block they had.
func (c *Config) SetBlockValue(name, key, value string) {
	blocks := make(map[string]*yaml.Node, len(c.blocks)+1)
	for k, v := range c.blocks {
		blocks[k] = v
	}
	var node yaml.Node
	if old, ok := c.blocks[name]; ok && old.Kind == yaml.MappingNode {
		node = *old
		node.Content = slices.Clone(old.Content)
	} else {
		node = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
	set := false
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			v.Line = node.Content[i+1].Line
			node.Content[i+1], set = v, true
		}
	}
	if !set {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	}
	blocks[name] = &node
	c.blocks = blocks
}
