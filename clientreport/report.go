// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package clientreport

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"image"
	"image/jpeg"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"

	"golang.org/x/image/draw"

	"github.com/RhythmShahriar/soundcheck/model"
	"github.com/RhythmShahriar/soundcheck/report"
)

//go:embed client.html.tmpl
var pageSource string

var pageTemplate = template.Must(template.New("client").Parse(pageSource))

const (
	maxItems    = 8 // changes shown in full; the rest are counted
	maxPictures = 6 // changes with before and after pictures
	maxFixed    = 10
	shotWidth   = 900
	shotHeight  = 2400 // keep the top of long pages: what a client looks at
	shotQuality = 72
)

type pageData struct {
	Agency, Contact, Accent string
	Logo                    template.URL
	Site, Date, Title, Tone string
	Lead                    string
	Counts                  []count
	Checked                 []string
	Items                   []item
	More                    int
	Fixed                   []string
	MoreFixed               int
}

type count struct {
	Label, Tone string
	N           int
}

type item struct {
	Label, Tone, Title, Where, Change string
	Before, After                     template.URL
}

// classWords are change classes in plain words.
var classWords = map[string]string{
	model.Broke:   "Stopped working",
	model.New:     "New problem",
	model.Changed: "Looks different",
	model.Slower:  "Slower",
	model.Fixed:   "Fixed",
}

// levelWords are issue levels in plain words.
var levelWords = map[model.Level]string{model.Error: "Error", model.Warning: "Warning", model.Notice: "Note"}

// Write renders res as a client report branded with b. Load b with
// LoadBrand so its logo is embedded.
func Write(w io.Writer, res report.Result, b Brand) error {
	if err := b.Validate(); err != nil {
		return err
	}
	run := res.Run
	d := pageData{Agency: b.Name, Contact: strings.TrimSpace(b.Contact), Accent: b.Accent, Logo: b.logo,
		Site: siteName(run.Target), Date: run.Started.Local().Format("2 January 2006"), Checked: checked(run)}
	if res.Compared() {
		fillComparison(&d, res)
	} else {
		fillRun(&d, run)
	}
	return pageTemplate.Execute(w, d)
}

func fillComparison(d *pageData, res report.Result) {
	c := res.Comparison
	n := func(class string) int { return c.Count(class) }
	switch {
	case n(model.Broke) > 0:
		d.Title, d.Tone = "Something stopped working", "bad"
	case n(model.New) > 0:
		d.Title, d.Tone = "We found new problems", "warn"
	case n(model.Changed)+n(model.Slower) > 0:
		d.Title, d.Tone = "Everything works; some pages look or load differently", "note"
	default:
		d.Title, d.Tone = "Everything works as before", "good"
	}
	them, they := "them", "they"
	if c.Pages == 1 {
		them, they = "it", "it"
	}
	d.Lead = fmt.Sprintf("We checked %s on %s and compared %s with how %s worked", plural(c.Pages, "page", "pages"), d.Date, them, they)
	if !c.Before.Started.IsZero() {
		d.Lead += " on " + c.Before.Started.Local().Format("2 January 2006")
	} else {
		d.Lead += " before"
	}
	d.Lead += "."
	for _, class := range model.Classes {
		// UNSTABLE (seen once, gone on the second visit) isn't counted
		// anywhere, so a report for a client leaves it out.
		if words, ok := classWords[class]; ok {
			d.Counts = append(d.Counts, count{words, toneOf(class), n(class)})
		}
	}
	pictures := 0
	for _, class := range model.Classes[:4] {
		for _, ch := range c.Changes {
			if ch.Class != class {
				continue
			}
			if len(d.Items) == maxItems {
				d.More++
				continue
			}
			it := item{Label: classWords[class], Tone: toneOf(class), Title: ch.Title, Where: where(ch.Pages)}
			if ch.Before != "" && ch.After != "" {
				it.Change = ch.Before + " → " + ch.After
			}
			if ch.Files["before"] != "" && ch.Files["after"] != "" && pictures < maxPictures {
				it.Before, it.After = shot(res, ch.Files["before"]), shot(res, ch.Files["after"])
				if it.Before != "" && it.After != "" {
					pictures++
				} else {
					it.Before, it.After = "", ""
				}
			}
			d.Items = append(d.Items, it)
		}
	}
	for _, ch := range c.Changes {
		if ch.Class == model.Fixed {
			if len(d.Fixed) == maxFixed {
				d.MoreFixed++
				continue
			}
			d.Fixed = append(d.Fixed, ch.Title)
		}
	}
}

func fillRun(d *pageData, run *model.Run) {
	issues := report.Issues(run)
	counts := report.Counts(issues)
	switch {
	case counts[model.Error] > 0:
		d.Title, d.Tone = "We found errors on the site", "bad"
	case counts[model.Warning] > 0:
		d.Title, d.Tone = "The site works, with a few warnings", "warn"
	default:
		d.Title, d.Tone = "Everything works", "good"
	}
	d.Lead = fmt.Sprintf("We checked %s on %s.", plural(len(run.Pages), "page", "pages"), d.Date)
	tones := map[model.Level]string{model.Error: "bad", model.Warning: "warn", model.Notice: "note"}
	for _, l := range report.Levels {
		d.Counts = append(d.Counts, count{levelWords[l] + "s", tones[l], counts[l]})
	}
	for _, i := range issues {
		if i.Level == model.Notice {
			continue // too small to send to a client
		}
		if len(d.Items) == maxItems {
			d.More++
			continue
		}
		d.Items = append(d.Items, item{Label: levelWords[i.Level], Tone: tones[i.Level], Title: i.Title, Where: where(i.Pages)})
	}
}

func toneOf(class string) string {
	switch class {
	case model.Broke:
		return "bad"
	case model.New:
		return "warn"
	case model.Fixed:
		return "good"
	}
	return "note"
}

// checked says in plain words what the run looked at, from its options.
func checked(run *model.Run) []string {
	o := run.Options
	var out []string
	pages := plural(len(run.Pages), "page", "pages")
	screens := screenWords(o.Viewports)
	if screens != "" {
		out = append(out, fmt.Sprintf("%s of the site, on %s", capitalize(pages), screens))
	} else {
		out = append(out, capitalize(pages)+" of the site")
	}
	if o.LoggedIn {
		out = append(out, "Pages as a signed-in user, as well as a visitor")
	}
	words := map[string]string{
		"health":     "Every page loads, without errors or missing parts",
		"visual":     "How every page looks, compared picture by picture",
		"speed":      "How fast pages load",
		"cache":      "That caching works and never shows one visitor's page to another",
		"a11y":       "Accessibility basics",
		"seo":        "What search engines see",
		"echo":       "The emails and messages the site sends from its forms",
		"lighthouse": "Google Lighthouse scores",
	}
	for _, m := range o.Modules {
		if w := words[m]; w != "" {
			out = append(out, w)
		}
	}
	if slices.Contains(o.Modules, "interact") {
		levels := map[string]string{
			"inspect": "Forms are in place and complete",
			"click":   "Buttons and menus respond when clicked",
			"submit":  "Forms can be sent (with test details)",
			"flows":   "Key journeys, such as signing in or checking out",
		}
		for _, l := range []string{"inspect", "click", "submit", "flows"} {
			if slices.Contains(o.Levels, l) {
				out = append(out, levels[l])
			}
		}
	}
	return out
}

func screenWords(viewports []string) string {
	names := map[string]string{"desktop": "desktop", "mobile": "phone", "tablet": "tablet"}
	var ws []string
	for _, v := range viewports {
		if n := names[v]; n != "" {
			ws = append(ws, n)
		}
	}
	switch len(ws) {
	case 0:
		return ""
	case 1:
		return ws[0] + " screens"
	}
	return strings.Join(ws[:len(ws)-1], ", ") + " and " + ws[len(ws)-1] + " screens"
}

// where is "On /shop/", "On 12 pages" or "Across the site".
func where(pages []string) string {
	switch len(pages) {
	case 0:
		return "Across the site"
	case 1:
		return "On " + pages[0]
	}
	return fmt.Sprintf("On %d pages", len(pages))
}

// siteName is the target's host, as a client knows their site.
func siteName(target string) string {
	if u, err := url.Parse(target); err == nil && u.Host != "" {
		return strings.TrimPrefix(u.Host, "www.")
	}
	return target
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// shot returns the run's screenshot rel as a picture, or "" when the
// path leaves the result's folder: a result can come from someone else,
// and a client report must not embed a private image from outside it.
func shot(res report.Result, rel string) template.URL {
	path, err := res.Path(rel)
	if err != nil {
		return ""
	}
	return picture(path)
}

// picture returns a screenshot as a JPEG data URL, scaled to the report
// width and cut to the top of the page; "" when it can't be read.
func picture(path string) template.URL {
	f, err := os.Open(path) //nolint:gosec // a screenshot from the run folder
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	src, _, err := image.Decode(f)
	if err != nil {
		return ""
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > shotWidth {
		h = h * shotWidth / w
		w = shotWidth
	}
	crop := b
	if h > shotHeight {
		crop.Max.Y = b.Min.Y + shotHeight*b.Dx()/w
		h = shotHeight
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: shotQuality}); err != nil {
		return ""
	}
	return template.URL("data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())) //nolint:gosec // our own encoded image
}
