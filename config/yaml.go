// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// decode reads a config document over c. Instead of yaml's own errors
// ("field pagez not found in type config.Config") it names the line and
// the setting: `line 3: unknown setting "pagez" (did you mean "pages"?)`.
// It also returns the line of each key two levels deep, for InFile and
// for pointing Validate's errors at the file.
func decode(data []byte, c *Config) (map[string]int, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, syntaxError(err)
	}
	if len(doc.Content) == 0 { // empty, or only comments
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, &SettingError{Line: root.Line, Msg: "the file must be a list of settings like \"pages:\" and \"rate: 5\""}
	}
	keys := map[string]int{}
	values := map[int]string{} // value line -> key, for type errors
	if err := checkKeys(root, reflect.TypeFor[Config](), "", keys, values); err != nil {
		return nil, err
	}
	if err := root.Decode(c); err != nil {
		return nil, typeError(err, values)
	}
	return keys, nil
}

// checkKeys walks a mapping alongside the struct it decodes into and
// reports the first key the struct doesn't have, recording where each key
// and value is on the way.
func checkKeys(n *yaml.Node, t reflect.Type, prefix string, keys map[string]int, values map[int]string) error {
	if n.Kind != yaml.MappingNode || t.Kind() != reflect.Struct {
		return nil
	}
	fields := map[string]reflect.Type{}
	var names []string
	for f := range fieldsOf(t) {
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		fields[name] = f.Type
		names = append(names, name)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		key := prefix + k.Value
		ft, ok := fields[k.Value]
		if !ok {
			msg := fmt.Sprintf("unknown setting %q", key)
			if m := Suggest(k.Value, names); m != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", prefix+m)
			}
			return &SettingError{Line: k.Line, Msg: msg}
		}
		if strings.Count(key, ".") < 2 {
			keys[key] = k.Line
		}
		if v.Kind != yaml.MappingNode { // a group's line belongs to its first key
			values[v.Line] = key
		}
		if err := checkKeys(v, ft, key+".", keys, values); err != nil {
			return err
		}
	}
	return nil
}

// fieldsOf yields a struct's exported fields.
func fieldsOf(t reflect.Type) func(func(reflect.StructField) bool) {
	return func(yield func(reflect.StructField) bool) {
		for i := range t.NumField() {
			if f := t.Field(i); f.IsExported() && !yield(f) {
				return
			}
		}
	}
}

var (
	syntaxRe    = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)
	lineMsgRe   = regexp.MustCompile(`^line (\d+): (.*)$`)
	unmarshalRe = regexp.MustCompile("^line (\\d+): cannot unmarshal !!(\\w+)(?: `(.*)`)? into (.+)$")
)

// syntaxError turns "yaml: line 3: mapping values are not allowed in this
// context" into a SettingError on line 3.
func syntaxError(err error) error {
	if m := syntaxRe.FindStringSubmatch(err.Error()); m != nil {
		line, _ := strconv.Atoi(m[1])
		return &SettingError{Line: line, Msg: "this isn't valid YAML: " + m[2]}
	}
	return &SettingError{Msg: "this isn't valid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}
}

// typeError turns yaml's "line 5: cannot unmarshal !!str `fast` into
// float64" into `line 5: rate: "fast" isn't a number`. Only the first
// error is kept: fix one line, run again.
func typeError(err error, values map[int]string) error {
	var se *SettingError
	if errors.As(err, &se) { // from an UnmarshalYAML, which knows the line but not the key
		se.Key = values[se.Line]
		return se
	}
	var te *yaml.TypeError
	if !errors.As(err, &te) || len(te.Errors) == 0 {
		return &SettingError{Msg: err.Error()}
	}
	msg := te.Errors[0]
	m := unmarshalRe.FindStringSubmatch(msg)
	if m == nil {
		if lm := lineMsgRe.FindStringSubmatch(msg); lm != nil {
			line, _ := strconv.Atoi(lm[1])
			return &SettingError{Key: values[line], Line: line, Msg: lm[2]}
		}
		return &SettingError{Msg: msg}
	}
	line, _ := strconv.Atoi(m[1])
	want := "a " + m[4]
	switch m[4] {
	case "config.Pages", "config.Visual", "config.Interactions", "config.Login", "config.Echo", "config.Speed":
		want = "a group of settings, each on its own indented line"
	case "int", "float64":
		want = "a number"
	case "bool":
		want = "true or false"
	case "string":
		want = "a single value"
	case "[]string":
		want = "a list like [a, b]"
	case "time.Duration":
		want = "a duration like 1500ms or 10s"
	case "map[string]string":
		want = "name: value pairs"
	}
	switch m[2] {
	case "seq":
		msg = "takes " + want + ", not a list"
	case "map":
		msg = "takes " + want + ", not a group of settings"
	default:
		msg = fmt.Sprintf("%q isn't %s", m[3], want)
	}
	return &SettingError{Key: values[line], Line: line, Msg: msg}
}

// Seconds is a per-page timeout in whole seconds. In the config file it
// is a number of seconds (30) or a duration like --timeout takes ("30s",
// "1m").
type Seconds int

// UnmarshalYAML reads a number of seconds or a duration.
func (s *Seconds) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return &SettingError{Line: n.Line, Msg: "takes a number of seconds or a duration like 30s"}
	}
	v, err := ParseSeconds(n.Value)
	if err != nil {
		return &SettingError{Line: n.Line, Msg: err.Error()}
	}
	*s = v
	return nil
}

// ParseSeconds reads "30", "30s" or "1m" as whole seconds. Anything over
// a day is refused before it can overflow; Validate sets the real limits.
func ParseSeconds(v string) (Seconds, error) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		d, derr := time.ParseDuration(v)
		if derr != nil {
			return 0, fmt.Errorf("%q isn't a number of seconds or a duration like 30s", v)
		}
		f = d.Seconds()
	}
	if math.IsNaN(f) || f < 0 || f > 24*60*60 {
		return 0, fmt.Errorf("%q isn't a number of seconds or a duration like 30s, up to a day", v)
	}
	return Seconds(math.Round(f)), nil
}
