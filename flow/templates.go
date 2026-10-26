// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package flow

import (
	"bytes"
	"embed"
	"path"
)

// templates are the key flow templates. Each file in the repository
// starts with the copyright header every source file carries; what a
// user gets starts with the comment that explains the flow, as Example
// does.
//
//go:embed templates/*.yaml
var templates embed.FS

// KeyboardTemplates returns the key flow templates `init --eaa` offers,
// by file name, such as keyboard-checkout.yaml: a WooCommerce checkout,
// a contact form, a menu and a dialog, all keyboard: true. Range over
// slices.Sorted(maps.Keys(...)) for a fixed order.
func KeyboardTemplates() map[string][]byte {
	files, _ := templates.ReadDir("templates")
	out := make(map[string][]byte, len(files))
	for _, f := range files {
		b, err := templates.ReadFile(path.Join("templates", f.Name()))
		if err != nil {
			continue // embedded at build time: can't fail
		}
		out[f.Name()] = withoutHeader(b)
	}
	return out
}

// withoutHeader drops the copyright and SPDX lines (and the blank line
// after them) a template starts with in the repository.
func withoutHeader(b []byte) []byte {
	rest := b
	for _, prefix := range []string{"# Copyright ", "# SPDX-License-Identifier:"} {
		line, after, _ := bytes.Cut(rest, []byte("\n"))
		if !bytes.HasPrefix(line, []byte(prefix)) {
			return b
		}
		rest = after
	}
	return bytes.TrimPrefix(rest, []byte("\n"))
}
