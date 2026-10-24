// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"html/template"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/soundcheck/model"
)

func writeSolidPNG(t *testing.T, path string, c color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := range 30 {
		for x := range 40 {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A received comparison.json can name any file. Only screenshots inside
// the result's folder may end up in the report.
func TestHTMLEmbedsOnlyContainedShots(t *testing.T) {
	base := t.TempDir()
	private := filepath.Join(base, "private.png")
	writeSolidPNG(t, private, color.RGBA{0, 200, 0, 255})
	dir := filepath.Join(base, "run")
	writeSolidPNG(t, filepath.Join(dir, "shots", "before.png"), color.White)
	writeSolidPNG(t, filepath.Join(dir, "shots", "after.png"), color.Black)
	files := []map[string]string{
		{"before": private, "after": "shots/after.png"},
		{"before": "shots/before.png", "after": "../private.png"},
		{"before": "shots/../../private.png", "after": "shots/after.png", "diff": private},
	}
	if runtime.GOOS != "windows" { // symlinks need privileges there
		if err := os.Symlink(private, filepath.Join(dir, "shots", "link.png")); err != nil {
			t.Fatal(err)
		}
		files = append(files, map[string]string{"before": "shots/before.png", "after": "shots/link.png"})
	}
	c := &model.Comparison{Schema: model.Schema, Pages: 1}
	for _, f := range files {
		c.Changes = append(c.Changes, model.Change{Class: model.Changed, Check: "visual/changed", Title: "Hostile", Pages: []string{"/"}, Files: f})
	}
	c.Changes = append(c.Changes, model.Change{Class: model.Changed, Check: "visual/changed", Title: "Normal", Pages: []string{"/a"},
		Files: map[string]string{"before": "shots/before.png", "after": "shots/after.png"}})

	var buf bytes.Buffer
	if err := HTML(&buf, Result{Run: sampleRun(), Comparison: c, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	leak, err := embedImage(private)
	if err != nil {
		t.Fatal(err)
	}
	// html/template writes + as &#43; in attributes.
	attr := func(u template.URL) string { return strings.ReplaceAll(string(u), "+", "&#43;") }
	if strings.Contains(out, string(leak)) || strings.Contains(out, attr(leak)) {
		t.Error("the report embeds the private image")
	}
	if strings.Contains(out, private) {
		t.Error("the report names the private file")
	}
	normal, err := embedImage(filepath.Join(dir, "shots", "after.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, attr(normal)) {
		t.Error("a normal screenshot isn't embedded")
	}
	refused := 0
	for _, f := range files {
		for _, v := range f {
			if !strings.HasPrefix(v, "shots/") || strings.Contains(v, "..") || v == "shots/link.png" {
				refused++
			}
		}
	}
	if n := strings.Count(out, "image not available"); n != refused {
		t.Errorf("'image not available' shows %d times, want %d (once per refused file)", n, refused)
	}
}

func TestPathRefusesOutside(t *testing.T) {
	r := Result{Dir: t.TempDir()}
	for _, bad := range []string{"", "/etc/passwd", "../x.png", "a/../../x.png", ".."} {
		if p, err := r.Path(bad); err == nil {
			t.Errorf("Path(%q) = %q, want an error", bad, p)
		}
	}
	for _, ok := range []string{"shots/a.png", "a/../b.png", "..x.png"} {
		p, err := r.Path(ok)
		if err != nil || !strings.HasPrefix(p, r.Dir) {
			t.Errorf("Path(%q) = %q, %v", ok, p, err)
		}
	}
}
