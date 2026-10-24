// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package importer turns a BackstopJS or Lost Pixel setup into a
// Soundcheck config, for `rhythm-soundcheck import`. It only reads: a
// backstop.json is decoded as data and a lostpixel.config.ts is scanned
// as text for its page paths, never run.
package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/soundcheck/config"
)

// mobileMax is the widest viewport imported as mobile: phones are up to
// about 430 px and small tablets in portrait start past 600.
const mobileMax = 600

// Result is what an import could map and what it couldn't.
type Result struct {
	Site      string   // the site the pages are on, "" when not known
	Pages     []string // paths for pages.extra
	Viewports []string // desktop, mobile, in config.Viewports order
	Ignore    []string // selectors for visual.ignore

	// Mapped and Unmapped explain the import, one line each, to print.
	Mapped   []string
	Unmapped []string
}

// YAML writes the config file: only the settings the import set, so
// everything else keeps its default.
func (r *Result) YAML(source string) ([]byte, error) {
	type pages struct {
		Extra []string `yaml:"extra,omitempty"`
	}
	type visual struct {
		Ignore []string `yaml:"ignore,omitempty"`
	}
	doc := struct {
		Site      string   `yaml:"site,omitempty"`
		Pages     pages    `yaml:"pages,omitempty"`
		Viewports []string `yaml:"viewports,omitempty"`
		Visual    visual   `yaml:"visual,omitempty"`
	}{r.Site, pages{r.Pages}, r.Viewports, visual{r.Ignore}}
	// A file name with a line break would end the comment and put a
	// setting of its own in the config, so such a name is quoted.
	if strings.IndexFunc(source, func(r rune) bool { return !strconv.IsPrint(r) }) >= 0 {
		source = strconv.Quote(source)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Imported from %s. Settings left out keep their defaults; the\n", source)
	b.WriteString("# init command writes a file that lists them all.\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("{}\n")), nil
}

// backstop is the part of a BackstopJS config the import reads.
type backstop struct {
	Viewports        []viewport                   `json:"viewports"`
	Scenarios        []map[string]json.RawMessage `json:"scenarios"`
	ScenarioDefaults map[string]json.RawMessage   `json:"scenarioDefaults"`
	OnBeforeScript   string                       `json:"onBeforeScript"`
	OnReadyScript    string                       `json:"onReadyScript"`
}

type viewport struct {
	Label  string  `json:"label"`
	Name   string  `json:"name"` // older configs
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (v viewport) label() string {
	if v.Label != "" {
		return v.Label
	}
	if v.Name != "" {
		return v.Name
	}
	return fmt.Sprintf("%gx%g", v.Width, v.Height)
}

// scenario is one decoded BackstopJS scenario, after scenarioDefaults.
type scenario struct {
	Label           string     `json:"label"`
	URL             string     `json:"url"`
	ReferenceURL    string     `json:"referenceUrl"`
	HideSelectors   []string   `json:"hideSelectors"`
	RemoveSelectors []string   `json:"removeSelectors"`
	Viewports       []viewport `json:"viewports"`
}

// unmappedKeys are scenario settings Soundcheck has no match for, with
// what to do instead. A key is listed only when a scenario sets it to
// something other than its empty default.
var unmappedKeys = []struct{ key, why string }{
	{"delay", "Soundcheck waits for each page to settle by itself"},
	{"readyEvent", "Soundcheck waits for each page to settle by itself"},
	{"readySelector", "Soundcheck waits for each page to settle by itself"},
	{"onBeforeScript", "scripts aren't run; a flow can log in or set things up (flows init writes one)"},
	{"onReadyScript", "scripts aren't run; a flow can click and type before checking (flows init writes one)"},
	{"hoverSelector", "the interact module hovers and clicks controls by itself"},
	{"hoverSelectors", "the interact module hovers and clicks controls by itself"},
	{"clickSelector", "the interact module hovers and clicks controls by itself"},
	{"clickSelectors", "the interact module hovers and clicks controls by itself"},
	{"keyPressSelectors", "a flow can type into fields (flows init writes one)"},
	{"scrollToSelector", "Soundcheck screenshots the whole page"},
	{"postInteractionWait", "Soundcheck waits for each page to settle by itself"},
	{"selectors", "Soundcheck screenshots and compares whole pages, not single elements"},
	{"cookiePath", "for a logged-in check set SOUNDCHECK_USER and SOUNDCHECK_PASSWORD; for other cookies pass --cookie"},
	{"misMatchThreshold", "visual.threshold is the same idea (% of pixels, default 0.5); set it by hand if you need to"},
	{"referenceUrl", "to compare with another site, run compare with --against <url>"},
}

// Backstop maps a backstop.json. site is the site to check; when it's ""
// the first scenario's site is used. Scenario URLs on another site are
// listed as unmapped. Crawling stays on either way.
func Backstop(data []byte, site string) (*Result, error) {
	var b backstop
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, jsonError(data, err)
	}
	if len(b.Scenarios) == 0 {
		return nil, errors.New("no scenarios: this doesn't look like a BackstopJS config")
	}
	r := &Result{}
	var all []scenario
	for i, raw := range b.Scenarios {
		for k, v := range b.ScenarioDefaults {
			if _, ok := raw[k]; !ok {
				raw[k] = v
			}
		}
		joined, _ := json.Marshal(raw) // a map of raw JSON always marshals
		var s scenario
		if err := json.Unmarshal(joined, &s); err != nil {
			return nil, fmt.Errorf("scenario %d: %w", i+1, err)
		}
		if s.Label == "" {
			s.Label = fmt.Sprintf("scenario %d", i+1)
		}
		all = append(all, s)
	}

	base, err := r.backstopSite(all, site)
	if err != nil {
		return nil, err
	}
	r.backstopPages(all, base)
	vps := b.Viewports
	for _, s := range all {
		vps = append(vps, s.Viewports...)
	}
	r.viewports(vps)
	r.backstopIgnore(all)

	// Settings without a match, top level and per scenario.
	if b.OnBeforeScript != "" {
		r.unmapped("onBeforeScript %s: %s", b.OnBeforeScript, why("onBeforeScript"))
	}
	if b.OnReadyScript != "" {
		r.unmapped("onReadyScript %s: %s", b.OnReadyScript, why("onReadyScript"))
	}
	for _, k := range unmappedKeys {
		var labels []string
		for i, raw := range b.Scenarios {
			if v, ok := raw[k.key]; ok && !emptyJSON(v) {
				labels = append(labels, all[i].Label)
			}
		}
		if len(labels) > 0 {
			r.unmapped("%s on %s: %s", k.key, scenarios(labels), k.why)
		}
	}
	return r, nil
}

// backstopSite picks the site the import is for and returns its URL.
func (r *Result) backstopSite(all []scenario, site string) (*url.URL, error) {
	if site != "" {
		u, err := siteURL(site)
		if err != nil {
			return nil, err
		}
		r.Site = site
		r.mapped("site: %s, from --site", site)
		return u, nil
	}
	for _, s := range all {
		if u, err := url.Parse(s.URL); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			r.Site = u.Scheme + "://" + u.Host
			r.mapped("site: %s, from scenario %q", r.Site, s.Label)
			return u, nil
		}
	}
	return nil, nil // no web URLs at all: every scenario is listed below
}

// backstopPages maps scenario URLs on base's site to pages.extra.
func (r *Result) backstopPages(all []scenario, base *url.URL) {
	for _, s := range all {
		u, err := url.Parse(s.URL)
		switch {
		case s.URL == "":
			r.unmapped("scenario %q has no url", s.Label)
			continue
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
			r.unmapped("scenario %q: %s isn't a web page Soundcheck can open", s.Label, s.URL)
			continue
		case base == nil || !sameSite(u, base):
			r.unmapped("scenario %q: %s isn't on %s, so it isn't checked", s.Label, s.URL, r.Site)
			continue
		}
		u.Fragment = ""
		if p := u.RequestURI(); !slices.Contains(r.Pages, p) {
			r.Pages = append(r.Pages, p)
		}
	}
	if len(r.Pages) > 0 {
		r.mapped("pages.extra: %s (%s; crawling still finds the rest of the site)", strings.Join(r.Pages, ", "), plural(len(r.Pages), "page"))
	}
}

// viewports maps widths to desktop and mobile.
func (r *Result) viewports(vps []viewport) {
	got := map[string][]string{}
	for _, v := range vps {
		if v.Width <= 0 {
			r.unmapped("viewport %q has no width", v.label())
			continue
		}
		kind := "desktop"
		if v.Width <= mobileMax {
			kind = "mobile"
		}
		desc := fmt.Sprintf("%s %g px", v.label(), v.Width)
		if !slices.Contains(got[kind], desc) {
			got[kind] = append(got[kind], desc)
		}
	}
	var how []string
	for _, kind := range config.Viewports {
		if len(got[kind]) > 0 {
			r.Viewports = append(r.Viewports, kind)
			how = append(how, strings.Join(got[kind], ", ")+" as "+kind)
		}
	}
	if len(how) > 0 {
		r.mapped("viewports: %s (%s; Soundcheck checks at its own desktop and phone sizes)", strings.Join(r.Viewports, ", "), strings.Join(how, "; "))
	}
}

// backstopIgnore maps hideSelectors and removeSelectors to visual.ignore.
func (r *Result) backstopIgnore(all []scenario) {
	removed, perScenario := false, false
	for _, s := range all {
		for _, sel := range append(slices.Clone(s.HideSelectors), s.RemoveSelectors...) {
			if sel = strings.TrimSpace(sel); sel != "" && !slices.Contains(r.Ignore, sel) {
				r.Ignore = append(r.Ignore, sel)
			}
		}
		if len(s.RemoveSelectors) > 0 {
			removed = true
		}
		if len(s.HideSelectors)+len(s.RemoveSelectors) > 0 && len(all) > 1 {
			perScenario = true
		}
	}
	if len(r.Ignore) == 0 {
		return
	}
	line := fmt.Sprintf("visual.ignore: %s, from hideSelectors", strings.Join(r.Ignore, ", "))
	if removed {
		line += " and removeSelectors (hidden before screenshots, not removed from the page)"
	}
	if perScenario {
		line += "; they now apply to every page"
	}
	r.mapped("%s", line)
}

// LostPixelPages reads the page paths from a Lost Pixel config's
// pageShots.pages. The file is TypeScript or JavaScript, so it's scanned
// as text: only paths written as plain strings are found. Nothing in
// the file is run.
func LostPixelPages(data []byte) (*Result, error) {
	src := string(data)
	at := keyIndex(src, "pageShots", 0)
	if at < 0 {
		return nil, errors.New("no pageShots in it: --pages-only reads the pages of a Lost Pixel pageShots config")
	}
	pages := keyIndex(src, "pages", at)
	if pages < 0 {
		return nil, errors.New("pageShots has no pages")
	}
	open := pages + len(src[pages:]) - len(strings.TrimLeft(src[pages:], " \t\r\n"))
	if open >= len(src) || src[open] != '[' {
		return nil, errors.New("pageShots.pages isn't a list written in the file; Soundcheck reads it as text and never runs it")
	}
	list, ok := balanced(src, open)
	if !ok {
		return nil, errors.New("pageShots.pages: the list never closes")
	}
	r := &Result{}
	for _, v := range stringsAfter(list, "path") {
		switch {
		case v.computed:
			r.unmapped("path %s is computed, not written out; add it to pages.extra by hand", v.text)
		case v.text == "":
		default:
			p := v.text
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			if !slices.Contains(r.Pages, p) {
				r.Pages = append(r.Pages, p)
			}
		}
	}
	if len(r.Pages) == 0 && len(r.Unmapped) == 0 {
		return nil, errors.New("no page paths in pageShots.pages; Soundcheck reads only paths written as plain strings and never runs the file")
	}
	if len(r.Pages) > 0 {
		r.mapped("pages.extra: %s (%s; crawling still finds the rest of the site)", strings.Join(r.Pages, ", "), plural(len(r.Pages), "page"))
	}
	r.unmapped("everything but the page paths (--pages-only): breakpoints, masks, thresholds and the rest of the file")
	return r, nil
}

func (r *Result) mapped(format string, a ...any) {
	r.Mapped = append(r.Mapped, fmt.Sprintf(format, a...))
}

func (r *Result) unmapped(format string, a ...any) {
	r.Unmapped = append(r.Unmapped, fmt.Sprintf(format, a...))
}

func why(key string) string {
	for _, k := range unmappedKeys {
		if k.key == key {
			return k.why
		}
	}
	return ""
}

// emptyJSON reports whether v is a setting's "not set" value: null,
// false, 0, "" or an empty list or object.
func emptyJSON(v json.RawMessage) bool {
	switch strings.TrimSpace(string(v)) {
	case "", "null", "false", "0", `""`, "[]", "{}":
		return true
	}
	return false
}

// siteURL reads --site: a host or a URL.
func siteURL(site string) (*url.URL, error) {
	raw := site
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%q isn't a site like staging.example.com", site)
	}
	return u, nil
}

// sameSite compares hosts as the production guard does: the www prefix,
// case and default ports don't matter.
func sameSite(a, b *url.URL) bool {
	return config.HostKey(a) == config.HostKey(b)
}

// jsonError adds the line to a JSON syntax error.
func jsonError(data []byte, err error) error {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		line := 1 + bytes.Count(data[:min(int(se.Offset), len(data))], []byte("\n"))
		return fmt.Errorf("line %d: %w", line, err)
	}
	return err
}

func scenarios(labels []string) string {
	if len(labels) == 1 {
		return fmt.Sprintf("scenario %q", labels[0])
	}
	if len(labels) > 3 {
		return fmt.Sprintf("%d scenarios", len(labels))
	}
	q := make([]string, len(labels))
	for i, l := range labels {
		q[i] = fmt.Sprintf("%q", l)
	}
	return "scenarios " + strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
