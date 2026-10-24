// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package clientreport writes a report an agency can send to its client:
// one standalone HTML page in plain language, with the agency's name,
// logo and color, what was checked, how many things changed and the
// most important changes with before and after pictures. It leaves out
// what only a developer needs: sources, stack traces, selectors and
// check ids.
package clientreport

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"html/template"
	"image"
	_ "image/jpeg" // logos may be JPEG
	_ "image/png"  // or PNG
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/config"
)

// DefaultAccent is the Rhythm dot, used when agency.yaml sets no color.
const DefaultAccent = "#E8603A"

// maxLogo caps the logo file: it's embedded in every report.
const maxLogo = 1 << 20

// Brand is the agency a report is from, as agency.yaml describes it.
type Brand struct {
	Name string `yaml:"name"`
	// Logo is a PNG, JPEG or SVG file, relative to agency.yaml and inside
	// its folder.
	Logo string `yaml:"logo"`
	// Accent is a hex color such as "#0F766E" for headings and badges.
	Accent string `yaml:"accent"`
	// Contact is how the client reaches the agency: an email address, a
	// phone number or a web address, shown as text.
	Contact string `yaml:"contact"`

	logo template.URL // the logo as a data URL, after LoadLogo
}

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// LoadBrand reads agency.yaml and its logo.
func LoadBrand(path string) (Brand, error) {
	var b Brand
	data, err := os.ReadFile(path) //nolint:gosec // the user's own brand file
	if err != nil {
		return b, err
	}
	if err := decodeBrand(data, &b); err != nil {
		return b, fmt.Errorf("%s: %w", path, err)
	}
	if err := b.Validate(); err != nil {
		return b, fmt.Errorf("%s: %w", path, err)
	}
	if err := b.LoadLogo(filepath.Dir(path)); err != nil {
		return b, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// brandKeys are the keys agency.yaml takes.
var brandKeys = []string{"name", "logo", "accent", "contact"}

// decodeBrand reads agency.yaml into b. "color" (and "colour") is taken
// for accent, as help calls it the agency's color. An unknown key names
// its line and the keys there are, not yaml's Go type names.
func decodeBrand(data []byte, b *Brand) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 { // empty, or only comments
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: want keys like \"name: Acme Web\": %s", root.Line, keyList())
	}
	seen := map[string]int{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		if k.Value == "color" || k.Value == "colour" {
			k.Value = "accent"
		}
		if !slices.Contains(brandKeys, k.Value) {
			return fmt.Errorf("line %d: unknown key %q%s; agency.yaml takes %s", k.Line, root.Content[i].Value,
				config.DidYouMean(k.Value, brandKeys), keyList())
		}
		if line, ok := seen[k.Value]; ok {
			return fmt.Errorf("line %d: %s is already set on line %d (color is another name for accent)", k.Line, k.Value, line)
		}
		seen[k.Value] = k.Line
	}
	return root.Decode(b)
}

func keyList() string {
	return strings.Join(brandKeys[:len(brandKeys)-1], ", ") + " and " + brandKeys[len(brandKeys)-1]
}

// Validate checks the name and the accent color.
func (b *Brand) Validate() error {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return errors.New("name: the agency's name is missing")
	}
	if b.Accent == "" {
		b.Accent = DefaultAccent
	}
	if !hexColor.MatchString(b.Accent) {
		return fmt.Errorf("accent %q: want a hex color like #0F766E", b.Accent)
	}
	return nil
}

// LoadLogo reads the logo from dir and embeds it. The logo must be a
// relative path inside dir: a brand file shared between people must not
// be able to pull other files into a report that goes to a client.
func (b *Brand) LoadLogo(dir string) error {
	if b.Logo == "" {
		return nil
	}
	clean := filepath.Clean(filepath.FromSlash(b.Logo))
	if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("logo %q: give a path inside the folder of the brand file", b.Logo)
	}
	// os.Root also refuses symlinks that lead out of dir.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(clean)
	if err != nil {
		return fmt.Errorf("logo %q: %w", b.Logo, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxLogo+1))
	if err != nil {
		return fmt.Errorf("logo %q: %w", b.Logo, err)
	}
	if len(data) > maxLogo {
		return fmt.Errorf("logo %q is over 1 MB", b.Logo)
	}
	mime, err := logoType(data)
	if err != nil {
		return fmt.Errorf("logo %q: %w", b.Logo, err)
	}
	b.logo = template.URL("data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)) //nolint:gosec // checked PNG, JPEG or safe SVG
	return nil
}

// logoType tells a PNG, JPEG or SVG logo by its content rather than its
// name and refuses anything else.
func logoType(data []byte) (string, error) {
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		switch format {
		case "png":
			return "image/png", nil
		case "jpeg":
			return "image/jpeg", nil
		}
	}
	if looksLikeSVG(data) {
		if err := checkSVG(data); err != nil {
			return "", err
		}
		return "image/svg+xml", nil
	}
	return "", errors.New("want a PNG, JPEG or SVG image")
}

func looksLikeSVG(data []byte) bool {
	head := strings.ToLower(string(data[:min(len(data), 1024)]))
	return strings.Contains(head, "<svg")
}

// svgElements are the drawing elements a logo may use. Anything else
// (script, foreignObject, image, a, style, animate…) can run code, load
// files from elsewhere or carry links, so the logo is refused.
var svgElements = []string{
	"svg", "g", "path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "text", "tspan",
	"defs", "symbol", "use", "lineargradient", "radialgradient", "stop", "clippath", "mask", "pattern",
	"title", "desc", "metadata",
}

// checkSVG refuses an SVG logo with scripts, event handlers, external
// references or a DOCTYPE (entities). The logo is shown through an img
// element too, where browsers don't run scripts; this check keeps the
// file clean wherever else it ends up.
func checkSVG(data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("the SVG doesn't parse: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return errors.New("SVG logos can't have a DOCTYPE or entities")
		case xml.ProcInst:
			if t.Target != "xml" {
				return fmt.Errorf("SVG logos can't have <?%s ...?>", t.Target)
			}
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if !slices.Contains(svgElements, name) {
				return fmt.Errorf("SVG logos can't have <%s> elements", t.Name.Local)
			}
			for _, a := range t.Attr {
				if err := checkSVGAttr(t.Name.Local, a); err != nil {
					return err
				}
			}
		}
	}
}

func checkSVGAttr(elem string, a xml.Attr) error {
	name, v := strings.ToLower(a.Name.Local), strings.ToLower(strings.Join(strings.Fields(a.Value), ""))
	switch {
	case strings.HasPrefix(name, "on"):
		return fmt.Errorf("SVG logos can't have event handlers (%s on <%s>)", a.Name.Local, elem)
	case name == "href" && !strings.HasPrefix(v, "#"):
		return fmt.Errorf("SVG logos can only link inside the file (href on <%s>)", elem)
	case strings.Contains(v, "javascript:"), strings.Contains(v, "data:"):
		return fmt.Errorf("SVG logos can't have scripts or embedded files (%s on <%s>)", a.Name.Local, elem)
	}
	for _, m := range cssURL.FindAllStringSubmatch(v, -1) {
		if !strings.HasPrefix(m[1], "#") {
			return fmt.Errorf("SVG logos can only refer to their own parts (%s on <%s>)", a.Name.Local, elem)
		}
	}
	return nil
}

// cssURL finds url(...) references in an attribute value (spaces
// already removed).
var cssURL = regexp.MustCompile(`url\(['"]?([^'")]*)`)
